package main

import "database/sql"

type ticketSnapshot struct {
	TicketNo         string  `json:"ticket_no"`
	DeliveredAt      *string `json:"delivered_at"`
	Recipient        string  `json:"recipient"`
	TicketAttachment string  `json:"ticket_attachment"`
	ReturnNote       string  `json:"return_note"`
}

type receiptSnapshot struct {
	ID          string `json:"id"`
	AmountCents int64  `json:"amount_cents"`
	ReceivedAt  string `json:"received_at"`
	Method      string `json:"method"`
	VoucherNo   string `json:"voucher_no"`
	Attachment  string `json:"attachment"`
	Note        string `json:"note"`
}

func readTicketSnapshot(tx *sql.Tx, orderID string) (ticketSnapshot, error) {
	var item ticketSnapshot
	var delivered sql.NullString
	err := tx.QueryRow("SELECT ticket_no,delivered_at,recipient,ticket_attachment,return_note FROM orders WHERE id=?", orderID).Scan(
		&item.TicketNo, &delivered, &item.Recipient, &item.TicketAttachment, &item.ReturnNote)
	if delivered.Valid {
		item.DeliveredAt = &delivered.String
	}
	return item, err
}

func readActiveReceiptSnapshots(tx *sql.Tx, orderID string) ([]receiptSnapshot, error) {
	return scanRows[receiptSnapshot](tx, "SELECT id,amount_cents,received_at,method,voucher_no,attachment,note FROM payment_receipts WHERE order_id=? AND voided=0 ORDER BY id", []any{orderID}, func(r *sql.Rows, item *receiptSnapshot) error {
		return r.Scan(&item.ID, &item.AmountCents, &item.ReceivedAt, &item.Method,
			&item.VoucherNo, &item.Attachment, &item.Note)
	})
}
