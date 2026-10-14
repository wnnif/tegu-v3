package main

import (
	"database/sql"
	"net/http"
)

type orderListItem struct {
	customerPricesHidden bool
	financeHidden        bool
	OrderID              string  `json:"order_id"`
	WorkerNames          string  `json:"worker_names"`
	ID                   string  `json:"id"`
	Number               string  `json:"number"`
	CompanyID            string  `json:"company_id"`
	CompanyName          string  `json:"company_name"`
	ProjectID            string  `json:"project_id"`
	ProjectName          string  `json:"project_name"`
	ProjectNumber        string  `json:"project_number"`
	Address              string  `json:"address"`
	WorkDate             string  `json:"work_date"`
	WorkerID             string  `json:"worker_id"`
	WorkerName           string  `json:"worker_name"`
	WarehouseID          string  `json:"warehouse_id"`
	Contact              string  `json:"contact"`
	Phone                string  `json:"phone"`
	Note                 string  `json:"note"`
	Status               string  `json:"status"`
	TotalCents           int64   `json:"total_cents"`
	ReceivedCents        int64   `json:"received_cents"`
	OutstandingCents     int64   `json:"outstanding_cents"`
	Version              int64   `json:"version"`
	CreatedBy            string  `json:"created_by"`
	UpdatedAt            string  `json:"updated_at"`
	WageCents            *int64  `json:"wage_cents,omitempty"`
	WageConfigured       *bool   `json:"wage_configured,omitempty"`
	WagePaid             *bool   `json:"wage_paid,omitempty"`
	TicketNo             *string `json:"ticket_no,omitempty"`
	DeliveredAt          *string `json:"delivered_at,omitempty"`
	Recipient            *string `json:"recipient,omitempty"`
	TicketAttachment     *string `json:"ticket_attachment,omitempty"`
	ReturnNote           *string `json:"return_note,omitempty"`
}

type orderListPage struct {
	customerPricesHidden bool
	Rows                 []orderListItem `json:"rows"`
	Total                int64           `json:"total"`
	TotalCents           int64           `json:"total_cents"`
	WageCents            *int64          `json:"wage_cents,omitempty"`
	WagePendingCount     *int64          `json:"wage_pending_count,omitempty"`
	Page                 int             `json:"page"`
}

func readOrderList(tx *sql.Tx, where string, args []any, page int, u User) ([]orderListItem, error) {
	return readOrderListMode(tx, where, args, page, u, false)
}

func readOrderListMode(tx *sql.Tx, where string, args []any, page int, u User, wages bool) ([]orderListItem, error) {
	id, workerID, workerName, wage, paid, version, wageConfigured, join := "o.id", "o.worker_id", "o.worker_name", "o.wage_cents", "o.wage_paid", "o.version", "NOT EXISTS(SELECT 1 FROM order_lines l WHERE l.order_id=o.id AND l.wage_configured=0 AND l.wage_override_cents IS NULL)", ""
	if wages {
		id, workerID, workerName, wage, paid, version, wageConfigured, join = "ww.id", "ww.worker_id", "ww.worker_name", "ww.wage_cents", "ww.wage_paid", "ww.version", "ww.wage_configured", " JOIN "+workerWageSource+" ww ON ww.order_id=o.id"
	}
	orderTail := ""
	if wages {
		orderTail = ",ww.id DESC"
	}
	query := `SELECT ` + id + `,o.id,o.number,o.company_id,o.company_name,o.project_id,o.project_name,o.project_number,
		o.address,o.work_date,` + workerID + `,` + workerName + `,o.warehouse_id,o.contact,o.phone,o.note,o.status,
		o.total_cents,` + wage + `,` + paid + `,` + wageConfigured + `,` + version + `,o.created_by,o.updated_at,o.ticket_no,o.delivered_at,
		o.recipient,o.ticket_attachment,o.return_note,` + orderReceivedCentsSQL + ` AS received_cents,` + orderOutstandingCentsSQL + ` AS outstanding_cents,` + orderWorkerNamesSQL + `
		FROM orders o` + join + ` LEFT JOIN (SELECT order_id,SUM(amount_cents) received_cents FROM payment_receipts WHERE voided=0 GROUP BY order_id) r ON r.order_id=o.id
		WHERE ` + where + ` ORDER BY o.work_date DESC,o.id DESC` + orderTail + ` LIMIT 100 OFFSET ?`
	rows, err := tx.Query(query, append(args, (page-1)*100)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []orderListItem{}
	for rows.Next() {
		item := orderListItem{customerPricesHidden: !u.can(customerPricePermission), financeHidden: !u.can("finance")}
		var wage int64
		var namesRaw []byte
		var wagePaid, wageConfiguredValue bool
		var ticketNo, recipient, ticketAttachment, returnNote string
		var deliveredAt sql.NullString
		if err = rows.Scan(&item.ID, &item.OrderID, &item.Number, &item.CompanyID, &item.CompanyName,
			&item.ProjectID, &item.ProjectName, &item.ProjectNumber, &item.Address, &item.WorkDate,
			&item.WorkerID, &item.WorkerName, &item.WarehouseID, &item.Contact, &item.Phone,
			&item.Note, &item.Status, &item.TotalCents, &wage, &wagePaid, &wageConfiguredValue, &item.Version,
			&item.CreatedBy, &item.UpdatedAt, &ticketNo, &deliveredAt, &recipient,
			&ticketAttachment, &returnNote, &item.ReceivedCents, &item.OutstandingCents, &namesRaw); err != nil {
			return nil, err
		}
		item.WorkerNames, err = decodeWorkerNames(namesRaw)
		if err != nil {
			return nil, err
		}
		if u.can("wages") {
			item.WageConfigured, item.WagePaid = &wageConfiguredValue, &wagePaid
			if wageConfiguredValue {
				item.WageCents = &wage
			}
		}
		if u.can("finance") {
			item.TicketNo, item.Recipient = &ticketNo, &recipient
			item.TicketAttachment, item.ReturnNote = &ticketAttachment, &returnNote
			if deliveredAt.Valid {
				item.DeliveredAt = &deliveredAt.String
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func writeOrderList(w http.ResponseWriter, items []orderListItem, total, amount, wages, wagePendingCount int64, page int, u User) {
	result := orderListPage{customerPricesHidden: !u.can(customerPricePermission), Rows: items, Total: total, TotalCents: amount, Page: page}
	if u.can("wages") {
		result.WageCents = &wages
		result.WagePendingCount = &wagePendingCount
	}
	write(w, 200, result)
}
