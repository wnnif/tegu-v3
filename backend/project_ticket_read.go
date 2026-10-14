package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

type projectWorkPage struct {
	Project    projectWorkProject  `json:"project"`
	Orders     []orderDetailView   `json:"orders"`
	Tickets    []projectTicketView `json:"tickets"`
	Total      int64               `json:"total"`
	TotalCents *int64              `json:"total_cents,omitempty"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
}

func (p *projectWorkPage) applyCustomerPricePermissions(u User) {
	if !u.can("customer_prices") {
		p.TotalCents = nil
		p.Project.Package = nil
	}
	for i := range p.Tickets {
		p.Tickets[i] = projectTicketForUser(p.Tickets[i], u)
	}
}

func projectTicketForUser(t projectTicketView, u User) projectTicketView {
	t.customerPricesHidden, t.financeHidden = !u.can("customer_prices"), !u.can("finance")
	return t
}

type projectWorkProject struct {
	ID         string `json:"id"`
	Number     string `json:"number"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	CompanyID  string `json:"company_id"`
	TicketMode string `json:"ticket_mode"`
	Package    *projectPackageView `json:"package,omitempty"`
}
type projectTicketVersionView struct {
	Version   int64  `json:"version"`
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
}
type projectTicketDetail struct {
	Ticket          projectTicketView          `json:"ticket"`
	Orders          []orderDetailView          `json:"orders"`
	Versions        []projectTicketVersionView `json:"versions"`
	PaymentReceipts *[]paymentReceiptView      `json:"payment_receipts,omitempty"`
}

func applyProjectCraftConfiguration(q rowQuery, projectID, companyID string, crafts []craftsRecord) error {
	rows, err := q.Query(`SELECT IF(p.ticket_mode_snapshot='',c.ticket_mode,p.ticket_mode_snapshot),s.craft_id,s.slot,s.included_by_craft_id FROM projects p JOIN companies c ON c.id=p.company_id LEFT JOIN project_craft_slots s ON s.project_id=p.id WHERE p.id=? AND p.company_id=?`, projectID, companyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	settings := map[string]projectCraftConfiguration{}
	found, mode := false, ""
	for rows.Next() {
		var craftID, parent sql.NullString
		var slot sql.NullInt64
		if err = rows.Scan(&mode, &craftID, &slot, &parent); err != nil {
			return err
		}
		found = true
		if craftID.Valid {
			settings[craftID.String] = projectCraftConfiguration{Slot: int(slot.Int64), IncludedBy: parent.String}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !found {
		return fail(400, "工地不存在或不属于所选公司")
	}
	for i := range crafts {
		if mode == "single" {
			crafts[i].TicketSlot = 1
		}
		if setting, ok := settings[crafts[i].ID]; ok {
			crafts[i].TicketSlot, crafts[i].IncludedByCraftID = setting.Slot, setting.IncludedBy
		}
	}
	return nil
}

func readProjectWorkProject(tx *sql.Tx, projectID string) (projectWorkProject, error) {
	var p projectWorkProject
	err := tx.QueryRow(`SELECT p.id,p.number,p.name,p.address,p.company_id,IF(p.ticket_mode_snapshot='',c.ticket_mode,p.ticket_mode_snapshot) FROM projects p JOIN companies c ON c.id=p.company_id WHERE p.id=?`, projectID).Scan(&p.ID, &p.Number, &p.Name, &p.Address, &p.CompanyID, &p.TicketMode)
	if err == sql.ErrNoRows {
		return p, fail(404, "工地不存在")
	}
	if err != nil {
		return p, err
	}
	p.Package, err = readProjectPackageView(tx, p.ID)
	return p, err
}

func (a *App) readProjectWork(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") && !u.can("finance") && !u.can("wages") {
		return fail(403, "无工地施工查看权限")
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	size := 20
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > 100 {
			return fail(400, "每页条数须为1至100")
		}
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readProjectWorkProject(tx, r.URL.Query().Get("id"))
	if err != nil {
		return err
	}
	var count, total int64
	if err = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(total_cents),0) FROM orders WHERE project_id=? AND status<>'已取消'", p.ID).Scan(&count, &total); err != nil {
		return err
	}
	var packageTotal int64
	if err = tx.QueryRow("SELECT COALESCE(SUM(price_cents),0) FROM project_packages WHERE project_id=?", p.ID).Scan(&packageTotal); err != nil {
		return err
	}
	total += packageTotal
	rows, err := tx.Query("SELECT id FROM orders WHERE project_id=? AND status<>'已取消' ORDER BY work_date DESC,id DESC LIMIT ? OFFSET ?", p.ID, size, (page-1)*size)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			break
		}
		ids = append(ids, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	orders := []orderDetailView{}
	for _, v := range ids {
		d, e := readOrderDetail(tx, v, u)
		if e != nil {
			return e
		}
		orders = append(orders, d)
	}
	ticketIDs, err := projectTicketIDs(tx, p.ID)
	if err != nil {
		return err
	}
	tickets := []projectTicketView{}
	for _, v := range ticketIDs {
		ticket, e := readProjectTicketSummary(tx, v)
		if e != nil {
			return e
		}
		tickets = append(tickets, ticket)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	result := projectWorkPage{Project: p, Orders: orders, Tickets: tickets, Total: count, TotalCents: &total, Page: page, PageSize: size}
	result.applyCustomerPricePermissions(u)
	write(w, 200, result)
	return nil
}

func projectTicketIDs(tx *sql.Tx, projectID string) ([]string, error) {
	rows, err := tx.Query("SELECT id FROM project_tickets WHERE project_id=? ORDER BY slot IS NULL,slot,created_at,id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	return ids, rows.Err()
}

func buildProjectTicketDetail(tx *sql.Tx, ticketID, projectID string, u User) (projectTicketDetail, error) {
	d := projectTicketDetail{Orders: []orderDetailView{}, Versions: []projectTicketVersionView{}}
	var err error
	itemIDs := map[string]bool{}
	orderIDs := []string{}
	if ticketID != "" {
		d.Ticket, err = readProjectTicketSummary(tx, ticketID)
		if err != nil {
			return d, err
		}
		rows, e := tx.Query("SELECT item_id,order_id FROM project_ticket_items WHERE ticket_id=? ORDER BY order_id,item_id", ticketID)
		if e != nil {
			return d, e
		}
		seen := map[string]bool{}
		for rows.Next() {
			var item, order string
			if err = rows.Scan(&item, &order); err != nil {
				break
			}
			itemIDs[item] = true
			if !seen[order] {
				orderIDs = append(orderIDs, order)
				seen[order] = true
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return d, err
		}
	} else {
		p, e := readProjectWorkProject(tx, projectID)
		if e != nil {
			return d, e
		}
		d.Ticket = projectTicketView{ProjectID: p.ID, CompanyID: p.CompanyID, Number: p.Number, Kind: "summary", Status: "工地累计汇总"}
		d.Ticket.PackageCharge, e = readProjectPackageCharge(tx, "", p.ID)
		if e != nil {
			return d, e
		}
		if d.Ticket.PackageCharge != nil {
			d.Ticket.PackageChargeCents = d.Ticket.PackageCharge.Amount
			d.Ticket.TotalCents += d.Ticket.PackageCharge.Amount
		}
		rows, e := tx.Query("SELECT id FROM orders WHERE project_id=? AND status<>'已取消' ORDER BY work_date,id", projectID)
		if e != nil {
			return d, e
		}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				break
			}
			orderIDs = append(orderIDs, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return d, err
		}
	}
	for _, orderID := range orderIDs {
		o, e := readOrderDetail(tx, orderID, u)
		if e != nil {
			return d, e
		}
		if ticketID != "" {
			filterOrderForTicket(&o, itemIDs)
		} else {
			d.Ticket.TotalCents += o.TotalCents
		}
		o.Editable = false
		for i := range o.Lines {
			o.Lines[i].Editable = false
		}
		d.Orders = append(d.Orders, o)
	}
	if ticketID == "" {
		if err = tx.QueryRow(`SELECT COALESCE(SUM(r.amount_cents),0) FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id WHERE t.project_id=? AND r.voided=0`, projectID).Scan(&d.Ticket.ReceivedCents); err != nil {
			return d, err
		}
		// Explicitly associated legacy records retain their own cash history.
		var legacyReceived int64
		if err = tx.QueryRow(`SELECT COALESCE(SUM(CASE WHEN o.status='已收款' THEN o.total_cents ELSE COALESCE(r.received,0) END),0) FROM orders o LEFT JOIN (SELECT order_id,SUM(amount_cents) received FROM payment_receipts WHERE voided=0 GROUP BY order_id) r ON r.order_id=o.id WHERE o.project_id=? AND o.ticket_managed=0 AND o.status<>'已取消'`, projectID).Scan(&legacyReceived); err != nil {
			return d, err
		}
		d.Ticket.ReceivedCents += legacyReceived
		if err = tx.QueryRow(`SELECT COALESCE(SUM(d.amount_cents),0) FROM customer_discounts d JOIN project_tickets t ON t.id=d.ticket_id WHERE t.project_id=? AND d.voided=0`, projectID).Scan(&d.Ticket.DiscountCents); err != nil {
			return d, err
		}
		d.Ticket.OutstandingCents = d.Ticket.TotalCents - d.Ticket.ReceivedCents - d.Ticket.DiscountCents
	}
	if ticketID != "" && u.can("finance") {
		payments := []paymentReceiptView{}
		rows, e := tx.Query("SELECT id,amount_cents,DATE_FORMAT(received_at,'%Y-%m-%d'),method,voucher_no,note,created_by,created_at,voided FROM project_ticket_receipts WHERE ticket_id=? ORDER BY received_at,id", ticketID)
		if e != nil {
			return d, e
		}
		for rows.Next() {
			var p paymentReceiptView
			if err = rows.Scan(&p.ID, &p.Amount, &p.ReceivedAt, &p.Method, &p.VoucherNo, &p.Note, &p.CreatedBy, &p.CreatedAt, &p.Voided); err != nil {
				break
			}
			payments = append(payments, p)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return d, err
		}
		d.PaymentReceipts = &payments
	}
	redactProjectTicket(&d, u)
	return d, nil
}

// A construction record may contribute to both normal tickets. Only its lines
// assigned to this ticket count; delivery wages follow its sales exactly once.
func filterOrderForTicket(o *orderDetailView, itemIDs map[string]bool) {
	lines := []orderLineView{}
	sales := []Sale{}
	var total, labor int64
	for _, l := range o.Lines {
		if itemIDs[l.ID] {
			lines = append(lines, l)
			total += l.Total
			if l.Labor != nil {
				labor += *l.Labor
			}
		}
	}
	for _, s := range o.Sales {
		if itemIDs[s.ID] {
			sales = append(sales, s)
			total += s.Total
		}
	}
	o.Lines, o.Sales, o.TotalCents = lines, sales, total
	names := []string{}
	for _, l := range lines {
		names = append(names, l.WorkerName)
	}
	if len(sales) > 0 {
		names = append(names, o.WorkerName)
	}
	o.WorkerNames = joinWorkerNames(names)
	if len(sales) == 0 && o.DeliveryWage != nil {
		v := int64(0)
		o.DeliveryWage = &v
	}
	if o.DeliveryWage != nil {
		labor += *o.DeliveryWage
	}
	if o.WageCents != nil {
		o.WageCents = &labor
	}
	if o.WageRecords != nil {
		previous := map[string]workerWageRecord{}
		for _, v := range *o.WageRecords {
			previous[v.WorkerID] = v
		}
		current := map[string]workerWageRecord{}
		for _, l := range lines {
			v, ok := current[l.WorkerID]
			if !ok {
				v = previous[l.WorkerID]
				v.WageCents, v.DeliveryWage = 0, 0
			}
			v.WorkerID, v.WorkerName = l.WorkerID, l.WorkerName
			if l.Labor != nil {
				v.WageCents += *l.Labor
			}
			current[l.WorkerID] = v
		}
		if len(sales) > 0 && o.DeliveryWage != nil {
			v, ok := current[o.WorkerID]
			if !ok {
				v = previous[o.WorkerID]
				v.WageCents, v.DeliveryWage = 0, 0
			}
			v.WorkerID = o.WorkerID
			if v.WorkerName == "" {
				v.WorkerName = o.WorkerName
			}
			v.WageCents += *o.DeliveryWage
			v.DeliveryWage = *o.DeliveryWage
			current[o.WorkerID] = v
		}
		values := []workerWageRecord{}
		for _, v := range current {
			values = append(values, v)
		}
		sort.Slice(values, func(i, j int) bool { return values[i].WorkerID < values[j].WorkerID })
		o.WageRecords = &values
	}
	o.ReceivedCents, o.OutstandingCents = 0, 0
	o.PaymentReceipts = nil
}

func redactProjectTicket(d *projectTicketDetail, u User) {
	d.Ticket = projectTicketForUser(d.Ticket, u)
	financial := u.can("finance") && u.can("customer_prices")
	if !financial {
		d.PaymentReceipts = nil
	}
	for i := range d.Orders {
		o := &d.Orders[i]
		o.customerPricesHidden, o.financeHidden = !u.can("customer_prices"), !u.can("finance")
		o.Editable = false
		if !u.can("wages") {
			o.WageCents, o.WagePaid, o.DeliveryWage = nil, nil, nil
			o.WageRecords = nil
			o.WageAdjustments = nil
		}
		if !financial {
			o.PaymentReceipts = nil
			o.TicketNo, o.DeliveredAt, o.Recipient, o.TicketAttachment, o.ReturnNote = nil, nil, nil, nil, nil
		}
		for j := range o.Lines {
			if o.Lines[j].WorkerID == "" {
				o.Lines[j].WorkerID, o.Lines[j].WorkerName = o.WorkerID, o.WorkerName
			}
			o.Lines[j].customerPricesHidden = !u.can("customer_prices")
			o.Lines[j].Editable = false
			if !u.can("wages") {
				o.Lines[j].Wage, o.Lines[j].Labor = nil, nil
				o.Lines[j].WageOverrideCents = nil
				o.Lines[j].WageAdjustmentReason = ""
				o.Lines[j].WageAdjustments = nil
			}
		}
	}
}

func (a *App) readProjectTicket(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") && !u.can("finance") && !u.can("wages") {
		return fail(403, "无票据查看权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ticketID, projectID := r.URL.Query().Get("id"), r.URL.Query().Get("project_id")
	if ticketID == "" && projectID == "" {
		return fail(400, "请选择票据或工地")
	}
	var d projectTicketDetail
	if raw := r.URL.Query().Get("version"); raw != "" {
		version, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || version < 1 || ticketID == "" {
			return fail(400, "票据版本无效")
		}
		var snapshot []byte
		if err = tx.QueryRow("SELECT snapshot FROM project_ticket_versions WHERE ticket_id=? AND version=?", ticketID, version).Scan(&snapshot); err != nil {
			return fail(404, "历史票据版本不存在")
		}
		if err = json.Unmarshal(snapshot, &d); err != nil {
			return err
		}
		// Versions saved before the display field was introduced keep their
		// contents; only fill the name from the immutable project mode snapshot.
		if d.Ticket.TicketMode == "" {
			if err = tx.QueryRow("SELECT p.ticket_mode_snapshot FROM projects p JOIN project_tickets t ON t.project_id=p.id WHERE t.id=?", ticketID).Scan(&d.Ticket.TicketMode); err != nil {
				return err
			}
		}
		redactProjectTicket(&d, u)
	} else {
		d, err = buildProjectTicketDetail(tx, ticketID, projectID, u)
		if err != nil {
			return err
		}
	}
	if ticketID != "" {
		rows, e := tx.Query("SELECT version,event,created_at FROM project_ticket_versions WHERE ticket_id=? ORDER BY version DESC", ticketID)
		if e != nil {
			return e
		}
		d.Versions = []projectTicketVersionView{}
		for rows.Next() {
			var v projectTicketVersionView
			if err = rows.Scan(&v.Version, &v.Event, &v.CreatedAt); err != nil {
				break
			}
			d.Versions = append(d.Versions, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, d)
	return nil
}
