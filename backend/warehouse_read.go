package main

import "database/sql"

type warehouseDeleteSnapshot struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	WorkerID      *string `json:"worker_id"`
	Active        bool    `json:"active"`
	Version       int64   `json:"version"`
	Category      string  `json:"category"`
	WarehouseType string  `json:"warehouse_type"`
	Note          string  `json:"note"`
}

func readWarehouseForDelete(tx *sql.Tx, id string) (warehouseDeleteSnapshot, error) {
	var item warehouseDeleteSnapshot
	var worker sql.NullString
	err := tx.QueryRow("SELECT id,name,worker_id,active,version,category,warehouse_type,note FROM warehouses WHERE id=? FOR UPDATE", id).Scan(
		&item.ID, &item.Name, &worker, &item.Active, &item.Version, &item.Category, &item.WarehouseType, &item.Note)
	if worker.Valid {
		item.WorkerID = &worker.String
	}
	return item, err
}
