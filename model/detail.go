package model

import "github.com/google/uuid"

type Detail struct {
	ID              uuid.UUID
	HeaderID        uuid.UUID
	InventoryNumber string
	//...
}
