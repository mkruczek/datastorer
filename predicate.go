package datastorer

type predicate struct {
	WarehouseID     string   `json:"warehouseID"`
	InventoryNumber string   `json:"inventoryNumber"`
	InventoryType   string   `json:"inventoryType"`
	ProductTypes    []string `json:"productTypes"`
}
