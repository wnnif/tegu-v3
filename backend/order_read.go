package main

import (
	"database/sql"
	"net/http"
)

type orderLineView struct {
	customerPricesHidden bool
	WorkerID             string               `json:"worker_id"`
	WorkerName           string               `json:"worker_name"`
	ID                   string               `json:"id"`
	OrderID              string               `json:"order_id"`
	CraftID              string               `json:"craft_id"`
	Category             string               `json:"category"`
	Name                 string               `json:"name"`
	Spec                 string               `json:"spec"`
	Region               string               `json:"region"`
	SubitemName          string               `json:"subitem_name"`
	ActualLocation       string               `json:"actual_location"`
	Description          string               `json:"description"`
	Mode                 string               `json:"mode"`
	Segments             []Segment            `json:"segments"`
	Qty                  int64                `json:"qty_micro"`
	Price                int64                `json:"price_cents"`
	Total                int64                `json:"total_cents"`
	Temporary            bool                 `json:"temporary"`
	WageConfigured       bool                 `json:"wage_configured"`
	Wage                 *int64               `json:"wage_cents,omitempty"`
	Labor                *int64               `json:"labor_cents,omitempty"`
	SubitemID            string               `json:"subitem_id"`
	SortOrder            int                  `json:"sort_order"`
	Editable             bool                 `json:"editable"`
	TicketClaimed        bool                 `json:"ticket_claimed"`
	WaivedQty            int64                `json:"waived_qty_micro"`
	ChargeableQty        int64                `json:"chargeable_qty_micro"`
	UseIncludedPrice     bool                 `json:"use_included_price"`
	IncludedByCraftID    string               `json:"included_by_craft_id"`
	ChargeNote           string               `json:"charge_note"`
	ChargeMode           string               `json:"charge_mode"`
	PackageIncluded      bool                 `json:"package_included"`
	WageOverrideCents    *int64               `json:"wage_override_cents,omitempty"`
	WageAdjustmentReason string               `json:"wage_adjustment_reason,omitempty"`
	WageAdjustments      []wageAdjustmentView `json:"wage_adjustments,omitempty"`
}

type wageAdjustmentView struct {
	ID          string `json:"id"`
	LineID      string `json:"line_id"`
	WorkerName  string `json:"worker_name"`
	LineDeleted bool   `json:"line_deleted"`
	Before      int64  `json:"before_cents"`
	After       int64  `json:"after_cents"`
	Reason      string `json:"reason"`
	ActorName   string `json:"actor_name"`
	CreatedAt   string `json:"created_at"`
}

func readWageAdjustments(tx *sql.Tx, orderID string) ([]wageAdjustmentView, error) {
	rows, err := tx.Query("SELECT a.id,a.line_id,a.worker_name,(l.id IS NULL),a.before_cents,a.after_cents,a.reason,a.actor_name,a.created_at FROM order_line_wage_adjustments a LEFT JOIN order_lines l ON l.id=a.line_id AND l.order_id=a.order_id WHERE a.order_id=? ORDER BY a.created_at,a.id", orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []wageAdjustmentView{}
	for rows.Next() {
		var v wageAdjustmentView
		if err := rows.Scan(&v.ID, &v.LineID, &v.WorkerName, &v.LineDeleted, &v.Before, &v.After, &v.Reason, &v.ActorName, &v.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

type paymentReceiptView struct {
	ID         string `json:"id"`
	Amount     int64  `json:"amount_cents"`
	ReceivedAt string `json:"received_at"`
	Method     string `json:"method"`
	VoucherNo  string `json:"voucher_no"`
	Attachment string `json:"attachment"`
	Note       string `json:"note"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	Voided     bool   `json:"voided"`
}

type orderMaterialView struct {
	LineID          string `json:"line_id"`
	ProductID       string `json:"product_id"`
	WarehouseID     string `json:"warehouse_id"`
	Qty             int64  `json:"qty_micro"`
	QtyQuantum      string `json:"qty_quantum"`
	InputQty        string `json:"input_qty"`
	InputUnit       string `json:"input_unit"`
	ConversionRatio string `json:"conversion_ratio"`
	ProductName     string `json:"product_name"`
	Unit            string `json:"unit"`
	AuxUnit         string `json:"aux_unit"`
	WarehouseName   string `json:"warehouse_name"`
}

type orderDetailView struct {
	customerPricesHidden bool
	financeHidden        bool
	WorkerNames          string                `json:"worker_names"`
	WageRecords          *[]workerWageRecord   `json:"wage_records,omitempty"`
	WageAdjustments      []wageAdjustmentView  `json:"wage_adjustments,omitempty"`
	ID                   string                `json:"id"`
	Number               string                `json:"number"`
	CompanyID            string                `json:"company_id"`
	CompanyName          string                `json:"company_name"`
	ProjectID            string                `json:"project_id"`
	ProjectName          string                `json:"project_name"`
	ProjectNumber        string                `json:"project_number"`
	Address              string                `json:"address"`
	WorkDate             string                `json:"work_date"`
	WorkerID             string                `json:"worker_id"`
	WorkerName           string                `json:"worker_name"`
	WarehouseID          string                `json:"warehouse_id"`
	Contact              string                `json:"contact"`
	Phone                string                `json:"phone"`
	Note                 string                `json:"note"`
	Status               string                `json:"status"`
	TotalCents           int64                 `json:"total_cents"`
	Version              int64                 `json:"version"`
	CreatedBy            string                `json:"created_by"`
	UpdatedAt            string                `json:"updated_at"`
	ReceivedCents        int64                 `json:"received_cents"`
	OutstandingCents     int64                 `json:"outstanding_cents"`
	Editable             bool                  `json:"editable"`
	Combinations         []ProjectCombination  `json:"combinations"`
	CombinationVersion   int64                 `json:"combination_version"`
	Lines                []orderLineView       `json:"lines"`
	Materials            []orderMaterialView   `json:"materials"`
	Sales                []Sale                `json:"sales"`
	DeliveryWage         *int64                `json:"delivery_wage_cents,omitempty"`
	TicketManaged        bool                  `json:"ticket_managed"`
	SalesTicketSlot      int                   `json:"sales_ticket_slot"`
	WageCents            *int64                `json:"wage_cents,omitempty"`
	WagePaid             *bool                 `json:"wage_paid,omitempty"`
	TicketNo             *string               `json:"ticket_no,omitempty"`
	DeliveredAt          *string               `json:"delivered_at,omitempty"`
	Recipient            *string               `json:"recipient,omitempty"`
	TicketAttachment     *string               `json:"ticket_attachment,omitempty"`
	ReturnNote           *string               `json:"return_note,omitempty"`
	PaymentReceipts      *[]paymentReceiptView `json:"payment_receipts,omitempty"`

	CombinationDetails []orderCombinationDetail `json:"combination_details"`
}

func (a *App) readOrder(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") && !u.can("finance") && !u.can("wages") {
		return fail(403, "无单据权限")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	detail, err := readOrderDetail(tx, r.URL.Query().Get("id"), u)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, detail)
	return nil
}

func readOrderDetail(tx *sql.Tx, orderID string, u User) (orderDetailView, error) {
	d := orderDetailView{customerPricesHidden: !u.can(customerPricePermission), financeHidden: !u.can("finance")}
	var wage, delivery int64
	var paid bool
	var ticketNo, recipient, attachment, returnNote string
	var delivered sql.NullString
	err := tx.QueryRow(`SELECT o.id,o.number,o.company_id,o.company_name,o.project_id,COALESCE(NULLIF(o.project_name,''),p.name,''),COALESCE(NULLIF(o.project_number,''),p.number,''),o.address,DATE_FORMAT(o.work_date,'%Y-%m-%d'),o.worker_id,o.worker_name,o.warehouse_id,o.contact,o.phone,o.note,o.status,o.total_cents,o.wage_cents,o.wage_paid,o.version,o.created_by,o.updated_at,o.ticket_no,DATE_FORMAT(o.delivered_at,'%Y-%m-%d'),o.recipient,o.ticket_attachment,o.return_note,o.delivery_wage_cents,o.ticket_managed,o.sales_ticket_slot FROM orders o LEFT JOIN projects p ON p.id=o.project_id WHERE o.id=?`, orderID).Scan(&d.ID, &d.Number, &d.CompanyID, &d.CompanyName, &d.ProjectID, &d.ProjectName, &d.ProjectNumber, &d.Address, &d.WorkDate, &d.WorkerID, &d.WorkerName, &d.WarehouseID, &d.Contact, &d.Phone, &d.Note, &d.Status, &d.TotalCents, &wage, &paid, &d.Version, &d.CreatedBy, &d.UpdatedAt, &ticketNo, &delivered, &recipient, &attachment, &returnNote, &delivery, &d.TicketManaged, &d.SalesTicketSlot)
	if err == sql.ErrNoRows {
		return d, fail(404, "施工记录不存在")
	}
	if err != nil {
		return d, err
	}
	u, err = withCompanyCraftSources(tx, u, d.CompanyID)
	if err != nil {
		return d, err
	}
	if err = validateOrderWorkerAccount(tx, d.ID); err != nil {
		return d, err
	}
	records, err := readWorkerWages(tx, d.ID)
	if err != nil {
		return d, err
	}
	names := []string{}
	for i := range records {
		records[i].Number, records[i].WorkDate = d.Number, d.WorkDate
		names = append(names, records[i].WorkerName)
	}
	d.WorkerNames = joinWorkerNames(names)
	if u.can("wages") {
		d.WageRecords = &records
	}
	for _, v := range records {
		paid = paid || v.WagePaid
	}
	d.Editable = u.can("orders") && d.Status == "进行中" && !paid
	if d.Editable && d.TicketManaged {
		var locked int
		if err = tx.QueryRow("SELECT COUNT(*) FROM project_ticket_items i JOIN project_tickets t ON t.id=i.ticket_id WHERE i.order_id=? AND t.status<>'草稿'", d.ID).Scan(&locked); err != nil {
			return d, err
		}
		d.Editable = locked == 0
	}
	lines, err := readStoredOrderLines(tx, d.ID)
	if err != nil {
		return d, err
	}
	d.Combinations, err = readOrderCombinations(tx, d.ID)
	if err != nil {
		return d, err
	}
	d.CombinationDetails, err = readOrderCombinationDetails(tx, d.ID, u)
	if err != nil {
		return d, err
	}
	if d.ProjectID != "" {
		if err = tx.QueryRow("SELECT combination_version FROM projects WHERE id=?", d.ProjectID).Scan(&d.CombinationVersion); err != nil {
			return d, err
		}
	}
	adjustments := map[string][]wageAdjustmentView{}
	if u.can("wages") {
		d.WageAdjustments, err = readWageAdjustments(tx, d.ID)
		if err != nil {
			return d, err
		}
		for _, adjustment := range d.WageAdjustments {
			adjustments[adjustment.LineID] = append(adjustments[adjustment.LineID], adjustment)
		}
	}
	d.Lines = []orderLineView{}
	for _, l := range lines {
		if l.Worker == "" {
			l.Worker, l.WorkerName = d.WorkerID, d.WorkerName
		}
		v := orderLineView{customerPricesHidden: !u.can(customerPricePermission), WorkerID: l.Worker, WorkerName: l.WorkerName, ID: l.ID, OrderID: d.ID, CraftID: l.CraftID, Category: l.Category, Name: l.Name, Spec: l.Spec, Region: l.Region, SubitemName: l.SubitemName, ActualLocation: l.ActualLocation, Description: l.Description, Mode: l.Mode, Segments: l.Segments, Qty: l.Qty, Price: l.Price, Total: l.Total, Temporary: l.Temporary, WageConfigured: !u.can("wages") || l.WageConfigured, SubitemID: l.SubitemID, SortOrder: l.SortOrder, Editable: d.Editable && lineFillAllowed(u, l) && l.ticketBatchID == "", TicketClaimed: l.ticketBatchID != "", WaivedQty: l.WaivedQty, ChargeableQty: l.Qty - l.WaivedQty, UseIncludedPrice: l.UseIncludedPrice, IncludedByCraftID: l.IncludedByCraftID, ChargeNote: l.ChargeNote, ChargeMode: normalizeChargeMode(l.ChargeMode), PackageIncluded: l.PackageIncluded}
		if u.can("wages") {
			w, lab := l.Wage, l.Labor
			if l.WageConfigured {
				v.Wage = &w
			}
			if l.WageConfigured || l.WageOverrideCents != nil {
				v.Labor = &lab
			}
			v.WageOverrideCents, v.WageAdjustmentReason = l.WageOverrideCents, l.WageAdjustmentReason
			v.WageAdjustments = adjustments[l.ID]
		}
		d.Lines = append(d.Lines, v)
	}
	materials, err := readStoredMaterials(tx, d.ID)
	if err != nil {
		return d, err
	}
	d.Materials = []orderMaterialView{}
	for _, m := range materials {
		d.Materials = append(d.Materials, orderMaterialView{LineID: m.LineID, ProductID: m.Product, WarehouseID: m.Warehouse, Qty: m.Qty, QtyQuantum: m.QtyQuantum, InputQty: m.InputQty, InputUnit: m.InputUnit, ConversionRatio: m.ConversionRatio, ProductName: m.ProductName, Unit: m.Unit, AuxUnit: m.AuxUnit, WarehouseName: m.WarehouseName})
	}
	d.Sales, err = readOrderSales(tx, d.ID)
	if err != nil {
		return d, err
	}
	for i := range d.Sales {
		editable := d.Editable && d.Sales[i].ticketBatchID == ""
		d.Sales[i].Editable = &editable
	}
	if u.can("finance") {
		d.TicketNo, d.Recipient, d.TicketAttachment, d.ReturnNote = &ticketNo, &recipient, &attachment, &returnNote
		if delivered.Valid {
			d.DeliveredAt = &delivered.String
		}
		receipts := []paymentReceiptView{}
		rows, e := tx.Query(`SELECT id,amount_cents,received_at,method,voucher_no,attachment,note,created_by,created_at,voided FROM payment_receipts WHERE order_id=? ORDER BY received_at,id`, d.ID)
		if e != nil {
			return d, e
		}
		for rows.Next() {
			var p paymentReceiptView
			if err = rows.Scan(&p.ID, &p.Amount, &p.ReceivedAt, &p.Method, &p.VoucherNo, &p.Attachment, &p.Note, &p.CreatedBy, &p.CreatedAt, &p.Voided); err != nil {
				break
			}
			receipts = append(receipts, p)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return d, err
		}
		d.PaymentReceipts = &receipts
	}
	if !d.TicketManaged {
		if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payment_receipts WHERE order_id=? AND voided=0", d.ID).Scan(&d.ReceivedCents); err != nil {
			return d, err
		}
		// Only legacy records may predate receipts. Managed project tickets always
		// calculate their received balance from actual non-voided cash records.
		if d.Status == "已收款" {
			d.ReceivedCents = d.TotalCents
		}
		d.OutstandingCents = d.TotalCents - d.ReceivedCents
	}
	if d.Status == "已取消" {
		d.ReceivedCents, d.OutstandingCents = 0, 0
	}
	if u.can("wages") {
		d.WageCents, d.WagePaid, d.DeliveryWage = &wage, &paid, &delivery
	}
	return d, nil
}
