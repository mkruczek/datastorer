package inventory

import "github.com/google/uuid"

type Header struct {
	ID              uuid.UUID
	InventoryNumber string
	WarehouseID     string
	InventoryType   string
	//...
}
