package main

import (
	"database/sql"
	"net/http"
)

type stockHistoryRecord struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	WorkDate        string `json:"work_date"`
	SourceID        string `json:"source_id"`
	DestinationID   string `json:"destination_id"`
	SourceName      string `json:"source_name"`
	DestinationName string `json:"destination_name"`
	Note            string `json:"note"`
	Handler         string `json:"handler"`
	CreatedBy       string `json:"created_by"`
	CreatedAt       string `json:"created_at"`
	TotalCents      *int64 `json:"total_cents"`
	Version         int64  `json:"version"`
	Voided          int64  `json:"voided"`
	VoidReason      string `json:"void_reason"`
}

type stockDocumentView struct {
	stockHistoryRecord
	LineItems *string `json:"line_items"`
}

type auditHistoryRecord struct {
	ID        int64  `json:"id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Action    string `json:"action"`
	TargetID  string `json:"target_id"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

const stockHistoryColumns = `d.id,d.kind,d.work_date,d.source_id,d.destination_id,
	COALESCE((SELECT name FROM warehouses WHERE id=d.source_id),'') AS source_name,
	COALESCE((SELECT name FROM warehouses WHERE id=d.destination_id),'') AS destination_name,
	d.note,d.handler,d.created_by,d.created_at,d.total_cents,d.version,d.voided,d.void_reason`

func scanStockHistory(q rowQuery, query string, args []any) ([]stockHistoryRecord, error) {
	return scanRows[stockHistoryRecord](q, query, args, func(r *sql.Rows, item *stockHistoryRecord) error {
		var total sql.NullInt64
		if err := r.Scan(&item.ID, &item.Kind, &item.WorkDate, &item.SourceID, &item.DestinationID,
			&item.SourceName, &item.DestinationName, &item.Note, &item.Handler, &item.CreatedBy,
			&item.CreatedAt, &total, &item.Version, &item.Voided, &item.VoidReason); err != nil {
			return err
		}
		if total.Valid {
			item.TotalCents = &total.Int64
		}
		return nil
	})
}

func (a *App) readStockDocument(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("warehouse") {
		return fail(403, "无仓储权限")
	}
	query := `SELECT ` + stockHistoryColumns + `,d.line_items FROM stock_documents d WHERE d.id=?`
	var detail stockDocumentView
	var total sql.NullInt64
	var lineItems sql.NullString
	err := a.db.QueryRow(query, r.URL.Query().Get("id")).Scan(&detail.ID, &detail.Kind, &detail.WorkDate,
		&detail.SourceID, &detail.DestinationID, &detail.SourceName, &detail.DestinationName,
		&detail.Note, &detail.Handler, &detail.CreatedBy, &detail.CreatedAt, &total,
		&detail.Version, &detail.Voided, &detail.VoidReason, &lineItems)
	if err == sql.ErrNoRows {
		return fail(404, "库存单不存在")
	}
	if err != nil {
		return err
	}
	if total.Valid {
		detail.TotalCents = &total.Int64
	}
	if lineItems.Valid {
		detail.LineItems = &lineItems.String
	}
	write(w, 200, detail)
	return nil
}
