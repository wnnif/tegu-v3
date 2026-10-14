package main

import (
	"database/sql"
	"sort"
	"time"
)

type projectTicketView struct {
	customerPricesHidden bool
	financeHidden        bool
	TicketMode           string `json:"ticket_mode_snapshot"`
	ID                   string `json:"id"`
	ProjectID            string `json:"project_id"`
	CompanyID            string `json:"company_id"`
	Number               string `json:"number"`
	Kind                 string `json:"kind"`
	Slot                 *int   `json:"slot"`
	Status               string `json:"status"`
	Version              int64  `json:"version"`
	TotalCents           int64  `json:"total_cents"`
	PackageChargeCents   int64  `json:"package_charge_cents,omitempty"`
	PackageCharge        *projectPackageChargeView `json:"package_charge,omitempty"`
	ReceivedCents        int64  `json:"received_cents"`
	DiscountCents        int64  `json:"discount_cents"`
	OutstandingCents     int64  `json:"outstanding_cents"`
	TicketNo             string `json:"ticket_no"`
	DeliveredAt          string `json:"delivered_at"`
	Recipient            string `json:"recipient"`
	CreatedAt            string `json:"created_at"`
	CompanyName          string `json:"company_name"`
	ProjectName          string `json:"project_name"`
	Address              string `json:"address"`
}

func ensureProjectTicketSnapshot(tx *sql.Tx, projectID, companyID string) (string, error) {
	var mode string
	if err := tx.QueryRow("SELECT ticket_mode_snapshot FROM projects WHERE id=? AND company_id=? FOR UPDATE", projectID, companyID).Scan(&mode); err != nil {
		return "", err
	}
	if mode != "" {
		return mode, nil
	}
	if err := tx.QueryRow("SELECT ticket_mode FROM companies WHERE id=?", companyID).Scan(&mode); err != nil {
		return "", err
	}
	if mode != "split" {
		mode = "single"
	}
	if _, err := tx.Exec("UPDATE projects SET ticket_mode_snapshot=? WHERE id=?", mode, projectID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO project_craft_slots(project_id,craft_id,slot,included_by_craft_id) SELECT ?,id,IF(?='single',1,ticket_slot),included_by_craft_id FROM crafts WHERE company_id=?`, projectID, mode, companyID); err != nil {
		return "", err
	}
	return mode, nil
}

func readProjectTicketSummary(tx *sql.Tx, ticketID string) (projectTicketView, error) {
	var t projectTicketView
	var slot sql.NullInt64
	var packageID, packageName, packageType string
	var packageRooms sql.NullInt64
	err := tx.QueryRow(`SELECT t.id,t.project_id,t.company_id,t.number,t.kind,t.slot,t.status,t.version,t.total_cents,t.ticket_no,COALESCE(DATE_FORMAT(t.delivered_at,'%Y-%m-%d'),''),t.recipient,t.created_at,COALESCE((SELECT SUM(r.amount_cents) FROM project_ticket_receipts r WHERE r.ticket_id=t.id AND r.voided=0),0),t.company_name,t.project_name,t.address,p.ticket_mode_snapshot,COALESCE(pp.package_id,''),COALESCE(pp.name,''),COALESCE(pp.package_type,''),pp.room_count,COALESCE(pp.price_cents,0) FROM project_tickets t JOIN projects p ON p.id=t.project_id LEFT JOIN project_packages pp ON pp.charged_ticket_id=t.id WHERE t.id=?`, ticketID).Scan(&t.ID, &t.ProjectID, &t.CompanyID, &t.Number, &t.Kind, &slot, &t.Status, &t.Version, &t.TotalCents, &t.TicketNo, &t.DeliveredAt, &t.Recipient, &t.CreatedAt, &t.ReceivedCents, &t.CompanyName, &t.ProjectName, &t.Address, &t.TicketMode, &packageID, &packageName, &packageType, &packageRooms, &t.PackageChargeCents)
	if err == sql.ErrNoRows {
		return t, fail(404, "工地票据不存在")
	}
	if err != nil {
		return t, err
	}
	if slot.Valid {
		v := int(slot.Int64)
		t.Slot = &v
	}
	if packageID != "" {
		rooms := 0
		if packageRooms.Valid {
			rooms = int(packageRooms.Int64)
		}
		t.PackageCharge = &projectPackageChargeView{ID: packageID, Name: packageName, PackageType: packageType, RoomCount: rooms, Amount: t.PackageChargeCents}
	}
	if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM customer_discounts WHERE ticket_id=? AND voided=0", ticketID).Scan(&t.DiscountCents); err != nil {
		return t, err
	}
	t.OutstandingCents = t.TotalCents - t.ReceivedCents - t.DiscountCents
	return t, nil
}

func newProjectTicket(tx *sql.Tx, o Order, kind string, slot *int) (string, error) {
	ticketID := id()
	number := "PJ-" + time.Now().Format("20060102") + "-" + ticketID[:12]
	var sqlSlot any
	if slot != nil {
		sqlSlot = *slot
	}
	_, err := tx.Exec(`INSERT INTO project_tickets(id,project_id,company_id,number,kind,slot,company_name,project_name,address) SELECT ?,?,?,?, ?,?,company_name,project_name,address FROM orders WHERE id=?`, ticketID, o.Project, o.Company, number, kind, sqlSlot, o.ID)
	return ticketID, err
}

func normalProjectTicket(tx *sql.Tx, o Order, slot int) (string, error) {
	var ticketID, status string
	err := tx.QueryRow("SELECT id,status FROM project_tickets WHERE project_id=? AND slot=? FOR UPDATE", o.Project, slot).Scan(&ticketID, &status)
	if err == sql.ErrNoRows {
		return newProjectTicket(tx, o, "normal", &slot)
	}
	if err != nil {
		return "", err
	}
	if status != "草稿" {
		return "", fail(409, "本次工艺所属票已交票，请选择更正原票或单独开追加票")
	}
	return ticketID, nil
}

type projectCraftConfiguration struct {
	Slot       int
	IncludedBy string
}

func projectCraftConfigurationFor(tx *sql.Tx, o Order, craftID, mode string) (projectCraftConfiguration, error) {
	var configuration projectCraftConfiguration
	err := tx.QueryRow("SELECT slot,included_by_craft_id FROM project_craft_slots WHERE project_id=? AND craft_id=?", o.Project, craftID).Scan(&configuration.Slot, &configuration.IncludedBy)
	if err == sql.ErrNoRows {
		// Crafts added after project creation acquire their configuration on first use.
		if err = tx.QueryRow("SELECT ticket_slot,included_by_craft_id FROM crafts WHERE id=? AND company_id=?", craftID, o.Company).Scan(&configuration.Slot, &configuration.IncludedBy); err != nil {
			return configuration, fail(400, "工艺归票配置无效")
		}
		if mode == "single" {
			configuration.Slot = 1
		}
		if _, err = tx.Exec("INSERT INTO project_craft_slots(project_id,craft_id,slot,included_by_craft_id) VALUES(?,?,?,?)", o.Project, craftID, configuration.Slot, configuration.IncludedBy); err != nil {
			return configuration, err
		}
	} else if err != nil {
		return configuration, err
	}
	if configuration.Slot != 1 && configuration.Slot != 2 {
		return configuration, fail(400, "工艺归票位置无效")
	}
	return configuration, nil
}

func projectCraftSlot(tx *sql.Tx, o Order, l Line, mode string) (int, error) {
	if l.Temporary {
		return o.SalesTicketSlot, nil
	}
	craftID := l.CraftID
	if l.UseIncludedPrice {
		craftID = l.IncludedByCraftID
	}
	configuration, err := projectCraftConfigurationFor(tx, o, craftID, mode)
	return configuration.Slot, err
}

func orderTicketIDs(tx *sql.Tx, orderID string) ([]string, error) {
	rows, err := tx.Query("SELECT DISTINCT ticket_id FROM project_ticket_items WHERE order_id=? ORDER BY ticket_id", orderID)
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

func removeOrderTicketItems(tx *sql.Tx, u User, orderID string) error {
	ids, err := orderTicketIDs(tx, orderID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM project_ticket_items WHERE order_id=?", orderID); err != nil {
		return err
	}
	for _, ticketID := range ids {
		if err = refreshProjectTicket(tx, u, ticketID, "取消施工记录"); err != nil {
			return err
		}
	}
	return nil
}

func assignOrderTickets(tx *sql.Tx, u User, o Order, mode string, fresh bool) ([]string, error) {
	oldIDs, err := orderTicketIDs(tx, o.ID)
	if err != nil {
		return nil, err
	}
	if !fresh {
		if _, err = tx.Exec("DELETE FROM project_ticket_items WHERE order_id=?", o.ID); err != nil {
			return nil, err
		}
	}
	ids := map[string]bool{}
	for _, v := range oldIDs {
		ids[v] = true
	}
	var chosen string
	if !fresh && len(oldIDs) == 1 {
		var kind string
		if err = tx.QueryRow("SELECT kind FROM project_tickets WHERE id=?", oldIDs[0]).Scan(&kind); err != nil {
			return nil, err
		}
		// Editing an undelivered append ticket must retain its original number and
		// scope. A normal assignment here could move its items into the old bill.
		if kind == "append" {
			chosen = oldIDs[0]
		}
	}
	if o.TicketAction == "correct" {
		if !u.can("finance") {
			return nil, fail(403, "更正已交票据需要财务权限")
		}
		if o.TicketID == "" {
			return nil, fail(400, "请选择需要更正的原票")
		}
		var project, status string
		var version int64
		if err = tx.QueryRow("SELECT project_id,status,version FROM project_tickets WHERE id=? FOR UPDATE", o.TicketID).Scan(&project, &status, &version); err != nil || project != o.Project {
			return nil, fail(400, "更正目标票不属于本工地")
		}
		if o.TicketVersion < 1 || o.TicketVersion != version {
			return nil, fail(409, "需要更正的原票已变化，请刷新工地票据后重新核对")
		}
		if status == "草稿" {
			return nil, fail(400, "未交票内容请按正常归票继续填写")
		}
		chosen = o.TicketID
	} else if o.TicketAction == "append" {
		chosen, err = newProjectTicket(tx, o, "append", nil)
		if err != nil {
			return nil, err
		}
	}
	slots := map[int]string{}
	resolve := func(slot int) (string, error) {
		if chosen != "" {
			return chosen, nil
		}
		if ticket, ok := slots[slot]; ok {
			return ticket, nil
		}
		ticket, err := normalProjectTicket(tx, o, slot)
		if err == nil {
			slots[slot] = ticket
		}
		return ticket, err
	}
	for _, l := range o.Lines {
		slot, err := projectCraftSlot(tx, o, l, mode)
		if err != nil {
			return nil, err
		}
		ticket, err := resolve(slot)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO project_ticket_items(item_id,ticket_id,order_id,item_kind) VALUES(?,?,?,'line')", l.ID, ticket, o.ID); err != nil {
			return nil, err
		}
		ids[ticket] = true
	}
	for _, s := range o.Sales {
		ticket, err := resolve(o.SalesTicketSlot)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO project_ticket_items(item_id,ticket_id,order_id,item_kind) VALUES(?,?,?,'sale')", s.ID, ticket, o.ID); err != nil {
			return nil, err
		}
		ids[ticket] = true
	}
	packageTicket, chargePackage, err := projectPackageChargeTicket(tx, o)
	if err != nil {		return nil, err
	}
	if chargePackage {
		if err = attachProjectPackageCharge(tx, u, o.Project, packageTicket); err != nil {
			return nil, err
		}
		ids[packageTicket] = true
	}
	result := []string{}
	for ticket := range ids {
		result = append(result, ticket)
	}
	sort.Strings(result)
	event := "保存施工记录"
	if o.TicketAction == "correct" {
		event = "更正原票"
	}
	if o.TicketAction == "append" {
		event = "新增追加票"
	}
	for _, ticket := range result {
		if err = refreshProjectTicket(tx, u, ticket, event); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func ticketStatus(total, received int64, delivered bool) string {
	if !delivered {
		return "草稿"
	}
	if received == total {
		return "已收款"
	}
	if received > 0 {
		return "部分收款"
	}
	return "已交票"
}

func refreshProjectTicket(tx *sql.Tx, u User, ticketID, event string) error {
	var currentVersion, total, received int64
	var status string
	if err := tx.QueryRow("SELECT version,status FROM project_tickets WHERE id=? FOR UPDATE", ticketID).Scan(&currentVersion, &status); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COALESCE(SUM(CASE i.item_kind WHEN 'line' THEN l.total_cents ELSE s.total_cents END),0)+COALESCE((SELECT pp.price_cents FROM project_packages pp WHERE pp.charged_ticket_id=?),0) FROM project_ticket_items i LEFT JOIN order_lines l ON i.item_kind='line' AND l.id=i.item_id LEFT JOIN order_sales s ON i.item_kind='sale' AND s.id=i.item_id JOIN orders o ON o.id=i.order_id AND o.status<>'已取消' WHERE i.ticket_id=?`, ticketID, ticketID).Scan(&total); err != nil {
		return err
	}
	if err := tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM project_ticket_receipts WHERE ticket_id=? AND voided=0", ticketID).Scan(&received); err != nil {
		return err
	}
	if err := reconcileTicketCash(tx, u, ticketID, total, event); err != nil {
		return err
	}
	var discount int64
	if err := tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM project_ticket_receipts WHERE ticket_id=? AND voided=0", ticketID).Scan(&received); err != nil {
		return err
	}
	if err := tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM customer_discounts WHERE ticket_id=? AND voided=0", ticketID).Scan(&discount); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE project_tickets SET total_cents=?,status=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", total, ticketStatus(total, received+discount, status != "草稿"), ticketID); err != nil {
		return err
	}
	return saveProjectTicketVersion(tx, u, ticketID, event)
}

func saveProjectTicketVersion(tx *sql.Tx, u User, ticketID, event string) error {
	// Store the complete immutable financial version; every public read redacts
	// wage fields unless the current requester is permitted to view wages.
	snapshot, err := buildProjectTicketDetail(tx, ticketID, "", User{Role: "admin"})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO project_ticket_versions(ticket_id,version,event,snapshot,created_by) VALUES(?,?,?,?,?)", ticketID, snapshot.Ticket.Version, event, jsonText(snapshot), u.ID)
	return err
}

type ProjectTicketActionRequest struct {
	ID          string `json:"id"`
	Version     int64  `json:"version"`
	Action      string `json:"action"`
	DeliveredAt string `json:"delivered_at"`
	Recipient   string `json:"recipient"`
	TicketNo    string `json:"ticket_no"`
	Amount      int64  `json:"amount_cents"`
	ReceivedAt  string `json:"received_at"`
	Method      string `json:"method"`
	VoucherNo   string `json:"voucher_no"`
	Note        string `json:"note"`
	ReceiptID   string `json:"receipt_id"`
}

func (a *App) projectTicketAction(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if !u.can("finance") {
		return nil, fail(403, "无交票或收款权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return nil, err
	}
	var p ProjectTicketActionRequest
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	if p.Action == "deliver" {
		return nil, fail(409, "请在交票管理中选择实际明细并确认纸票交付")
	}
	if p.Action == "receive" || p.Action == "void_receipt" {
		return nil, fail(409, "请在收款管理中登记、分配或纠错实际收款")
	}
	return nil, fail(400, "未知的票据操作；收款请使用收款管理")
}
