package main

import "database/sql"

type stockDocumentSnapshot struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	WorkDate      string  `json:"work_date"`
	SourceID      string  `json:"source_id"`
	DestinationID string  `json:"destination_id"`
	Note          string  `json:"note"`
	CreatedBy     string  `json:"created_by"`
	CreatedAt     string  `json:"created_at"`
	TotalCents    *int64  `json:"total_cents"`
	LineItems     *string `json:"line_items"`
	Handler       string  `json:"handler"`
	Version       int64   `json:"version"`
	Voided        bool    `json:"voided"`
	VoidReason    string  `json:"void_reason"`
}

type stockMovementTotal struct {
	WarehouseID string
	ProductID   string
	Quantity    int64
}

func loadStockDocumentForUpdate(tx *sql.Tx, id string) (stockDocumentSnapshot, error) {
	var item stockDocumentSnapshot
	var amount sql.NullInt64
	var lines sql.NullString
	err := tx.QueryRow("SELECT id,kind,work_date,source_id,destination_id,note,created_by,created_at,total_cents,line_items,handler,version,voided,void_reason FROM stock_documents WHERE id=? FOR UPDATE", id).Scan(
		&item.ID, &item.Kind, &item.WorkDate, &item.SourceID, &item.DestinationID, &item.Note,
		&item.CreatedBy, &item.CreatedAt, &amount, &lines, &item.Handler, &item.Version, &item.Voided, &item.VoidReason)
	if amount.Valid {
		item.TotalCents = &amount.Int64
	}
	if lines.Valid {
		item.LineItems = &lines.String
	}
	return item, err
}

func stockMovementTotals(tx *sql.Tx, source string) ([]stockMovementTotal, error) {
	return scanRows[stockMovementTotal](tx, "SELECT warehouse_id,product_id,SUM(delta_quantum) FROM stock_movements WHERE source_id=? GROUP BY warehouse_id,product_id", []any{source}, func(r *sql.Rows, item *stockMovementTotal) error {
		return r.Scan(&item.WarehouseID, &item.ProductID, &item.Quantity)
	})
}
