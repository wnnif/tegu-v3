package main

import "database/sql"

type archiveCompany struct {
	ID   string
	Name string
}

type archiveLine struct {
	WorkerID       string
	WorkerName     string
	ID             string
	CraftID        string
	Number         string
	Address        string
	WorkDate       string
	Company        string
	Category       string
	Name           string
	Spec           string
	Region         string
	SubitemName    string
	ActualLocation string
	Description    string
	Mode           string
	Segments       string
	QtyMicro       int64
	PriceCents     int64
	TotalCents     int64
	WageCents      int64
	LaborCents     int64
	ChargeMode     string
	ChargeNote     string
	IncludedPrice  bool
	SortOrder      int
}

type archiveOrder struct {
	ID               string
	Number           string
	Address          string
	WorkDate         string
	Company          string
	Worker           string
	TotalCents       int64
	WageCents        int64
	Status           string
	WagePaid         bool
	ReceivedCents    int64
	OutstandingCents int64
	TicketNo         string
	DeliveredAt      string
	Recipient        string
	TicketAttachment string
	ReturnNote       string
}

type archiveReceipt struct {
	ID          string
	AmountCents int64
	ReceivedAt  string
	Method      string
	VoucherNo   string
	Attachment  string
	Note        string
	Voided      bool
	Number      string
	WorkDate    string
	Address     string
	OrderStatus string
	Company     string
}

func archiveCompanies(tx *sql.Tx, receipts bool, where string, args []any) ([]archiveCompany, error) {
	from := "orders o "
	if receipts {
		from = "payment_receipts r JOIN orders o ON o.id=r.order_id "
	}
	query := "SELECT o.company_id,MAX(COALESCE(NULLIF(o.company_name,''),c.name)) FROM " + from +
		"JOIN companies c ON c.id=o.company_id WHERE " + where + " GROUP BY o.company_id"
	return scanRows[archiveCompany](tx, query, args, func(r *sql.Rows, item *archiveCompany) error {
		return r.Scan(&item.ID, &item.Name)
	})
}

func archiveLines(tx *sql.Tx, where string, args []any) ([]archiveLine, error) {
	query := `SELECT l.id,o.number,o.address,o.work_date,COALESCE(NULLIF(o.company_name,''),c.name),
		l.category,l.name,l.spec,l.region,l.description,l.mode,l.segments,l.qty_micro,l.price_cents,l.total_cents,
		l.craft_id,COALESCE(l.subitem_name,''),COALESCE(l.actual_location,''),COALESCE(l.charge_mode,'normal'),COALESCE(l.charge_note,''),COALESCE(l.use_included_price,0)
		FROM order_lines l JOIN orders o ON o.id=l.order_id JOIN companies c ON c.id=o.company_id
		WHERE ` + where + ` AND o.company_id=? AND l.id>? ORDER BY l.id LIMIT 500`
	return scanRows[archiveLine](tx, query, args, func(r *sql.Rows, item *archiveLine) error {
		return r.Scan(&item.ID, &item.Number, &item.Address, &item.WorkDate, &item.Company,
			&item.Category, &item.Name, &item.Spec, &item.Region, &item.Description,
			&item.Mode, &item.Segments, &item.QtyMicro, &item.PriceCents, &item.TotalCents,
			&item.CraftID, &item.SubitemName, &item.ActualLocation, &item.ChargeMode, &item.ChargeNote, &item.IncludedPrice)
	})
}

func archiveOrders(tx *sql.Tx, where string, args []any) ([]archiveOrder, error) {
	query := `SELECT o.id,o.number,o.address,o.work_date,COALESCE(NULLIF(o.company_name,''),c.name),
		COALESCE(NULLIF(o.worker_name,''),w.name,''),o.total_cents,o.wage_cents,o.status,o.wage_paid,
		` + orderReceivedCentsSQL + `,` + orderOutstandingCentsSQL + `,o.ticket_no,o.delivered_at,o.recipient,o.ticket_attachment,o.return_note
		FROM orders o JOIN companies c ON c.id=o.company_id LEFT JOIN workers w ON w.id=o.worker_id` + orderReceiptTotalsJoinSQL + `WHERE ` + where + ` AND o.company_id=? AND o.id>? ORDER BY o.id LIMIT 500`
	return scanRows[archiveOrder](tx, query, args, func(r *sql.Rows, item *archiveOrder) error {
		var delivered sql.NullString
		err := r.Scan(&item.ID, &item.Number, &item.Address, &item.WorkDate, &item.Company,
			&item.Worker, &item.TotalCents, &item.WageCents, &item.Status, &item.WagePaid,
			&item.ReceivedCents, &item.OutstandingCents, &item.TicketNo, &delivered,
			&item.Recipient, &item.TicketAttachment, &item.ReturnNote)
		if delivered.Valid {
			item.DeliveredAt = delivered.String
		}
		return err
	})
}

func archiveWorkerWages(tx *sql.Tx, where string, args []any) ([]archiveOrder, error) {
	if err := validateWorkerAccountScope(tx, where+" AND o.company_id=?", args[:len(args)-1]); err != nil {
		return nil, err
	}
	query := `SELECT ww.id,o.number,o.address,o.work_date,COALESCE(NULLIF(o.company_name,''),c.name),ww.worker_name,ww.wage_cents,ww.wage_paid FROM orders o JOIN ` + workerWageSource + ` ww ON ww.order_id=o.id JOIN companies c ON c.id=o.company_id WHERE ` + where + ` AND o.company_id=? AND ww.id>? ORDER BY ww.id LIMIT 500`
	return scanRows[archiveOrder](tx, query, args, func(r *sql.Rows, v *archiveOrder) error {
		return r.Scan(&v.ID, &v.Number, &v.Address, &v.WorkDate, &v.Company, &v.Worker, &v.WageCents, &v.WagePaid)
	})
}

func archiveReceipts(tx *sql.Tx, where string, args []any) ([]archiveReceipt, error) {
	query := `SELECT r.id,r.amount_cents,r.received_at,r.method,r.voucher_no,r.attachment,r.note,r.voided,
		o.number,o.work_date,o.address,o.status,COALESCE(NULLIF(o.company_name,''),c.name)
		FROM payment_receipts r JOIN orders o ON o.id=r.order_id JOIN companies c ON c.id=o.company_id
		WHERE ` + where + ` AND o.company_id=? AND r.id>? ORDER BY r.id LIMIT 500`
	return scanRows[archiveReceipt](tx, query, args, func(r *sql.Rows, item *archiveReceipt) error {
		return r.Scan(&item.ID, &item.AmountCents, &item.ReceivedAt, &item.Method,
			&item.VoucherNo, &item.Attachment, &item.Note, &item.Voided, &item.Number,
			&item.WorkDate, &item.Address, &item.OrderStatus, &item.Company)
	})
}
