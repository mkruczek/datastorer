package inventory

import "github.com/google/uuid"

type Snapshot struct {
	ID              uuid.UUID
	InventoryNumber string
	//...
}
