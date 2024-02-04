package datastorer

import (
	"context"
	"fmt"
	"github.com/mkruczek/datastorer/inventory"
	"time"

	log "github.com/sirupsen/logrus"
)

const delay = 5 * time.Second

var limit = 1000

type headerRepo interface {
	CreateInventoryHeader(userID string, warehouseID string, productType []string, inventoryType string) (inventory.Header, error)
}

type detailRepo interface {
	CreateInventoryDetailInBach(data []inventory.Detail) error
}

// globalServer - interface for global microservice where we have some data
type globalServer interface {
	InitSnapshot(ctx context.Context, predicate predicate) error
	SnapshotIsReady(ctx context.Context, predicate predicate) (bool, error)
	GetSnapshotData(ctx context.Context, predicate predicate, offset, limit int) ([]inventory.Snapshot, error)
}

type reportGenerator interface {
	Generate(inventoryDocumentID, lang string) error //prepare data for excel
}

// Manager - aggregates the actions needed for the inventory process
type Manager struct {
	header headerRepo
	detail detailRepo
	global globalServer
	report reportGenerator

	snapshotReady      chan bool
	snapshotData       chan []inventory.Snapshot
	dataReadyForReport chan bool

	snapshotError chan error
}

// NewManager - create new instance of Manager
func NewManager(header headerRepo, detail detailRepo, provider globalServer, generator reportGenerator) *Manager {
	return &Manager{
		header: header,
		detail: detail,
		global: provider,
		report: generator,
	}
}

func (s *Manager) Process(ctx context.Context, userID, warehouseID, lang string, productTypes []string, inventoryType string) (string, error) {
	/*
	   1) Create a header with inventory document number
	   2) Initialize product inventory snapshot from global service
	   3) Check every x seconds if the snapshot is ready
	   4) Copy/download the first 1000 records and send via channel to the function saving to the local details database
	   5) Save the data to the local details database in batches of 1000 records -> back to 4
	   6) send command to generate an Excel report
	*/

	//open/reopen channel if it was closed
	s.snapshotReady = make(chan bool)
	s.snapshotData = make(chan []inventory.Snapshot)
	s.dataReadyForReport = make(chan bool)
	s.snapshotError = make(chan error)

	header, err := s.header.CreateInventoryHeader(userID, warehouseID, productTypes, inventoryType)
	if err != nil {
		return "", fmt.Errorf("error creating inventory header : %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	go s.errorMonitor(ctx, cancel)

	err = s.initializeRackInventorySnapshotFromGlobalService(ctx, header)
	if err != nil {
		return "", err
	}

	go s.monitoringSnapshotProcess(ctx, header)

	go s.pullDataInBatch(ctx, header)

	go s.saveSnapshotToDetailsDB(ctx, header)

	go s.prepareReport(ctx, header.InventoryNumber, lang)

	return header.InventoryNumber, nil
}

// errorMonitor - monitor if any error occurred during snapshot creation process and cancel context if so
func (s *Manager) errorMonitor(ctx context.Context, cancel context.CancelFunc) {

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-s.snapshotError:
			if err == nil {
				continue
			}
			cancel()
			log.Errorf("Snapshot creation procces end with error: %s", err)
			return
		}
	}
}

// initializeRackInventorySnapshotFromGlobalService - make call to global to  initialize snapshot from main product table.
func (s *Manager) initializeRackInventorySnapshotFromGlobalService(ctx context.Context, header inventory.Header) error {

	inventoryPredicate := predicate{
		WarehouseID:     header.WarehouseID,
		InventoryNumber: header.InventoryNumber,
		InventoryType:   header.InventoryType,
		ProductTypes:    []string{"PT100, PT200"},
	}

	return s.global.InitSnapshot(ctx, inventoryPredicate)
}

// function will be calling global microservice to checking if snapshot is ready
func (s *Manager) monitoringSnapshotProcess(ctx context.Context, header inventory.Header) {

	defer close(s.snapshotReady)

	for {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled, exiting monitoringSnapshotProcess")
			return
		default:

			inventoryPredicate := predicate{
				WarehouseID:     header.WarehouseID,
				InventoryNumber: header.InventoryNumber,
			}

			ready, err := s.global.SnapshotIsReady(ctx, inventoryPredicate)
			if err != nil {
				s.snapshotError <- err
				return
			}

			if ready {
				s.snapshotReady <- true
				log.Infof("Snapshot is ready for inventory number %s", header.InventoryNumber)
				return
			}

			time.Sleep(delay)
		}
	}
}

func (s *Manager) pullDataInBatch(ctx context.Context, header inventory.Header) {

	defer close(s.snapshotData)

	inventoryPredicate := predicate{
		WarehouseID:     header.WarehouseID,
		InventoryNumber: header.InventoryNumber,
	}

	offset := 0

	for {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled, exiting pullDataInBatch")
			return
		case <-s.snapshotReady:
			data, err := s.global.GetSnapshotData(ctx, inventoryPredicate, offset, limit)
			if err != nil {
				s.snapshotError <- err
				return
			}

			s.snapshotData <- data

			if len(data) < limit {
				// if  we get less data than limit, it means that we have all data
				log.Debugf("All data was pulled from global service for inventory number %s", header.InventoryType)
				return
			}

			// else increase offset by limit to get next batch of data
			offset += limit
		}
	}
}

func (s *Manager) saveSnapshotToDetailsDB(ctx context.Context, header inventory.Header) {

	defer close(s.dataReadyForReport)

	//hasDataToSave is used to check if we have any data at local/facade details database
	//variable is used to send message via channel to prepare report
	//where we check if we have data(true) to save and we can prepare report or not(false)
	hasDataToSave := false

	for {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled, exiting saveSnapshotToDetailsDB")
			return
		case data, ok := <-s.snapshotData:
			log.Infof("TUTAJ_7: ok " + fmt.Sprint(ok) + " data: " + fmt.Sprint(data))
			log.Infof("TUTAJ_7: hasDataToSave " + fmt.Sprint(hasDataToSave))
			if !ok {
				log.Infof("No more data to save for inventory document number %s", header.InventoryNumber)
				select {
				case s.dataReadyForReport <- hasDataToSave:
				default:
					return
				}
			}

			dataToSave := make([]inventory.Detail, len(data))
			for i, v := range data {
				dataToSave[i] = inventory.Detail{
					HeaderID:        header.ID,
					InventoryNumber: v.InventoryNumber,
					//...
				}
			}

			if err := s.detail.CreateInventoryDetailInBach(dataToSave); err != nil {
				log.Infof("TUTAJ_8")
				s.snapshotError <- err
				return
			}

			hasDataToSave = true
		}
	}
}

func (s *Manager) prepareReport(ctx context.Context, inventoryNumber string, lang string) {

	defer close(s.snapshotError)

	for {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled, exiting prepareReport")
			return
		case hasData, ok := <-s.dataReadyForReport:
			log.Infof("TUTAJ_9: hasData " + fmt.Sprint(hasData) + " ok: " + fmt.Sprint(ok))
			if !hasData || !ok {
				log.Infof("No data to prepare report for inventory document number %s", inventoryNumber)
				return
			}

			err := s.report.Generate(inventoryNumber, lang)
			if err != nil {
				s.snapshotError <- err
			}

			return
		}
	}

}
