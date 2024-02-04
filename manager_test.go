package datastorer

import (
	"context"
	"fmt"
	"github.com/mkruczek/datastorer/inventory"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/mock"
)

type MockHeaderService struct{}

func (m *MockHeaderService) CreateInventoryHeader(userID string, warehouseID string, productType []string, inventoryType string) (inventory.Header, error) {
	return inventory.Header{
		ID:              uuid.New(),
		InventoryNumber: "newInventoryNumber",
		WarehouseID:     warehouseID,
		InventoryType:   inventoryType,
	}, nil
}

type MockDetailService struct {
}

func (m *MockDetailService) CreateInventoryDetailInBach(data []inventory.Detail) error {
	return nil
}

type MockGlobalConnector struct {
	mock.Mock
}

func (m *MockGlobalConnector) InitSnapshot(ctx context.Context, predicate predicate) error {
	args := m.Called(ctx, predicate)
	return args.Error(0)
}

func (m *MockGlobalConnector) SnapshotIsReady(ctx context.Context, predicate predicate) (bool, error) {
	args := m.Called(ctx, predicate)
	return args.Bool(0), args.Error(1)
}

func (m *MockGlobalConnector) GetSnapshotData(ctx context.Context, predicate predicate, limit, offset int) ([]inventory.Snapshot, error) {

	args := m.Called(ctx, predicate, offset, limit)
	if args.Error(1) != nil {
		return nil, args.Error(1)
	}

	//otherwise "simulate" long running process, as this operation will be most heavy
	time.Sleep(3 * time.Second)

	return args.Get(0).([]inventory.Snapshot), nil
}

type mockReportGenerator struct {
}

func (m *mockReportGenerator) Generate(inventoryDocumentID, lang string) error {
	return nil
}

func TestManager_Process_HappyPath(t *testing.T) {

	header := new(MockHeaderService)             //mock database connection
	detail := new(MockDetailService)             //mock database connection
	mGlobalConnector := new(MockGlobalConnector) //mock global connector
	mReportGenerator := new(mockReportGenerator) //mock report generator

	manager := NewManager(header, detail, mGlobalConnector, mReportGenerator)

	getSnapshotData := []inventory.Snapshot{
		{ID: uuid.New(), InventoryNumber: "fakeInventDocNum"},
		{ID: uuid.New(), InventoryNumber: "fakeInventDocNum"},
	}

	mGlobalConnector.On("InitSnapshot", mock.Anything, mock.Anything).Return(nil)
	mGlobalConnector.On("SnapshotIsReady", mock.Anything, mock.Anything).Return(true, nil)
	mGlobalConnector.On("GetSnapshotData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(getSnapshotData, nil)

	invDocNumber, err := manager.Process(context.Background(), "U100", "P300", "en", []string{"RT100"}, "all")
	if err != nil {
		t.Fatalf("Error processing empty rack inventory: %v", err)
	}

	//todo -> figure out how to test this without time.Sleep
	time.Sleep(5 * time.Second)

	mGlobalConnector.AssertExpectations(t)

	if invDocNumber == "" {
		t.Fatal("Inventory document number is empty")
	}

	//channels should be closed after success to prevent goroutine leak
	areChannsClosed(t, manager)
}

func TestManager_Process_EndWithError(t *testing.T) {

	plantCode := "P303"

	header := new(MockHeaderService)             //mock database connection
	detail := new(MockDetailService)             //mock database connection
	mGlobalConnector := new(MockGlobalConnector) //mock global connector
	mReportGenerator := new(mockReportGenerator) //mock report generator

	manager := NewManager(header, detail, mGlobalConnector, mReportGenerator)

	mGlobalConnector.On("InitSnapshot", mock.Anything, mock.Anything).Return(nil)
	mGlobalConnector.On("SnapshotIsReady", mock.Anything, mock.Anything).Return(true, nil)

	//force error and we expect that context will be cancelled and that all channels will be closed
	mGlobalConnector.On("GetSnapshotData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]inventory.Snapshot{}, fmt.Errorf("test error"))

	_, err := manager.Process(context.Background(), "U100", plantCode, "en", []string{"RT100"}, "all")
	if err != nil {
		//for this test we simulate error for background process
		t.Fatal("Expected error, got nil")
	}

	//todo -> figure out how to test this without time.Sleep
	time.Sleep(5 * time.Second)

	mGlobalConnector.AssertExpectations(t)

	//channels should be closed after error
	areChannsClosed(t, manager)
}

func TestManager_Process_ContextCancellation(t *testing.T) {

	header := new(MockHeaderService)             //mock database connection
	detail := new(MockDetailService)             //mock database connection
	mGlobalConnector := new(MockGlobalConnector) //mock global connector
	mReportGenerator := new(mockReportGenerator) //mock report generator

	manager := NewManager(header, detail, mGlobalConnector, mReportGenerator)

	mGlobalConnector.On("InitSnapshot", mock.Anything, mock.Anything).Return(nil)
	mGlobalConnector.On("SnapshotIsReady", mock.Anything, mock.Anything).Return(true, nil)
	mGlobalConnector.On("GetSnapshotData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]inventory.Snapshot{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	startTime := time.Now()
	_, err := manager.Process(ctx, "U100", "SDK100", "en", []string{"RT100"}, "all")
	if err != nil {
		t.Fatal("Expected no error, got ", err)
	}
	//cancel context and wait for 3 seconds for all goroutines to receive cancel signal
	cancel()
	//todo -> figure out how to test this without time.Sleep
	time.Sleep(10 * time.Second)

	if time.Since(startTime) > 11*time.Second {
		t.Errorf("Context was not cancelled properly")
	}

	//channels should be closed after error
	areChannsClosed(t, manager)
}

func areChannsClosed(t *testing.T, manager *Manager) {
	t.Helper()

	if !isChannelClosed[bool](t, manager.snapshotReady) {
		t.Errorf("The channel snapshotReady is not closed")
	}

	if !isChannelClosed[[]inventory.Snapshot](t, manager.snapshotData) {
		t.Errorf("The channel snapshotData is not closed")
	}

	if !isChannelClosed[bool](t, manager.dataReadyForReport) {
		t.Errorf("The channel dataReadyForReport is not closed")
	}

	if !isChannelClosed[error](t, manager.snapshotError) {
		t.Errorf("The channel snapshotError is not closed")
	}
}

func isChannelClosed[V any](t *testing.T, ch chan V) (isClosed bool) {
	t.Helper()
	defer func() {
		if recover() != nil {
			isClosed = false
		}
	}()

	select {
	case _, ok := <-ch:
		if !ok {
			isClosed = true
		}
	default:
	}

	return isClosed
}
