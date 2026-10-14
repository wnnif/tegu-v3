package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func lineFillAllowed(u User, l Line) bool {
	if l.Temporary {
		return u.can("orders_temporary")
	}
	return u.canFillCompanyCraft(l.CraftID)
}

func readStoredOrderLines(tx *sql.Tx, orderID string) ([]Line, error) {
	rows, err := tx.Query(`SELECT id,craft_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,wage_configured,temporary,spec,region,description,subitem_id,sort_order,use_included_price,included_by_craft_id,charge_note,worker_id,worker_name,COALESCE(subitem_name,''),COALESCE(actual_location,''),COALESCE(charge_mode,'normal'),wage_override_cents,COALESCE(wage_adjustment_reason,''),COALESCE((SELECT SUM(c.waived_qty_micro) FROM project_combinations c WHERE (c.waive_direction='waterproof' AND c.waterproof_line_id=order_lines.id) OR (c.waive_direction='polypropylene' AND c.polypropylene_line_id=order_lines.id)),0),COALESCE(ticket_batch_id,''),package_included FROM order_lines WHERE order_id=? ORDER BY sort_order,id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []Line{}
	for rows.Next() {
		var l Line
		var override sql.NullInt64
		var raw []byte
		if err = rows.Scan(&l.ID, &l.CraftID, &l.Category, &l.Name, &l.Mode, &raw, &l.Qty, &l.Price, &l.Wage, &l.Total, &l.Labor, &l.WageConfigured, &l.Temporary, &l.Spec, &l.Region, &l.Description, &l.SubitemID, &l.SortOrder, &l.UseIncludedPrice, &l.IncludedByCraftID, &l.ChargeNote, &l.Worker, &l.WorkerName, &l.SubitemName, &l.ActualLocation, &l.ChargeMode, &override, &l.WageAdjustmentReason, &l.WaivedQty, &l.ticketBatchID, &l.PackageIncluded); err != nil {
			return nil, err
		}
		if override.Valid {
			v := override.Int64
			l.WageOverrideCents = &v
		}
		l.ChargeMode = normalizeChargeMode(l.ChargeMode)
		l.standardLaborCents = l.Labor
		if err = json.Unmarshal(raw, &l.Segments); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func readStoredMaterials(tx *sql.Tx, orderID string) ([]Material, error) {
	rows, err := tx.Query("SELECT m.line_id,m.product_id,m.warehouse_id,m.qty_micro,m.qty_quantum,m.input_qty,m.input_unit,m.conversion_ratio,m.product_name,m.unit,p.aux_unit,m.warehouse_name FROM order_materials m JOIN products p ON p.id=m.product_id WHERE m.order_id=? ORDER BY m.line_id,m.product_id,m.warehouse_id", orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	materials := []Material{}
	for rows.Next() {
		var m Material
		if err = rows.Scan(&m.LineID, &m.Product, &m.Warehouse, &m.Qty, &m.QtyQuantum, &m.InputQty, &m.InputUnit, &m.ConversionRatio, &m.ProductName, &m.Unit, &m.AuxUnit, &m.WarehouseName); err != nil {
			return nil, err
		}
		materials = append(materials, m)
	}
	return materials, rows.Err()
}

func prepareOrderLine(tx *sql.Tx, u User, o Order, l *Line, previous *Line) error {
	if len([]rune(l.Name)) > 200 || len([]rune(l.Spec)) > 200 || len([]rune(l.Region)) > 200 || len([]rune(l.SubitemName)) > 200 || len([]rune(l.ActualLocation)) > 200 || len([]rune(l.Description)) > 1000 || !validSortOrder(l.SortOrder) {
		return fail(400, "工艺文字或排序超出限制")
	}
	if !lineFillAllowed(u, *l) {
		return fail(403, "没有对应公共工艺的填写权限")
	}
	if previous != nil {
		if l.CraftID != previous.CraftID || l.Temporary != previous.Temporary || l.Category != previous.Category || l.Mode != previous.Mode || l.SubitemID != previous.SubitemID || l.UseIncludedPrice != previous.UseIncludedPrice {
			return fail(400, "已保存明细不能更换工艺、小项、单位或组合，请删除后另增")
		}
		// Legacy text and configured names are immutable saved snapshots, not locations.
		l.Region, l.SubitemName = previous.Region, previous.SubitemName
		if l.actualLocationOmitted {
			l.ActualLocation = previous.ActualLocation
		}
		if !l.Temporary {
			l.Name, l.Spec, l.Price, l.Wage, l.WageConfigured = previous.Name, previous.Spec, previous.Price, previous.Wage, previous.WageConfigured
			l.IncludedByCraftID, l.ChargeNote = previous.IncludedByCraftID, previous.ChargeNote
		}
		if l.Temporary && !u.can("wages") {
			l.Wage = previous.Wage
		}
	} else if !l.Temporary {
		if l.UseIncludedPrice {
			return fail(400, "新施工不能沿用旧含价设置，请在保存后明确选择施工明细并设置组合免收")
		}
		var company, source, subitems string
		var active bool
		var sourceWageConfigured bool
		var companyWage, legacyCompanyWage sql.NullInt64
		if err := tx.QueryRow(`SELECT c.company_id,s.category,s.name,s.mode,c.price_cents,s.wage_cents,s.wage_configured,wo.wage_cents,c.wage_override_cents,s.spec,(c.active AND s.active),c.source_id,COALESCE(s.subitems,JSON_ARRAY()) FROM crafts c JOIN crafts s ON s.id=c.source_id AND s.company_id=''
			LEFT JOIN company_wage_overrides wo ON wo.company_id=c.company_id AND wo.craft_id=c.source_id WHERE c.id=?`, l.CraftID).Scan(&company, &l.Category, &l.Name, &l.Mode, &l.Price, &l.Wage, &sourceWageConfigured, &companyWage, &legacyCompanyWage, &l.Spec, &active, &source, &subitems); err != nil {
			return fail(400, "固定工艺不存在或尚未关联公共工艺")
		}
		if company != o.Company || !active {
			return fail(400, "固定工艺与公司不匹配或已停用")
		}
		if source == "" {
			return fail(400, "公司固定工艺尚未关联公共工艺，请先在公司设置中核对")
		}
		l.SubitemName = ""
		var items []CraftSubitem
		if err := json.Unmarshal([]byte(subitems), &items); err != nil {
			return err
		}
		var override *int64
		if companyWage.Valid {
			value := companyWage.Int64
			override = &value
		}
		var legacyOverride *int64
		if legacyCompanyWage.Valid {
			value := legacyCompanyWage.Int64
			legacyOverride = &value
		}
		name, wage, configured, err := effectiveOrderLineWageStatus(l.SubitemID, l.Wage, sourceWageConfigured, override, legacyOverride, items)
		if err != nil {
			return err
		}
		l.SubitemName, l.Wage, l.WageConfigured = name, wage, configured
		l.IncludedByCraftID, l.ChargeNote = "", ""
		l.UseIncludedPrice = false
	} else {
		if l.CraftID != "" || l.UseIncludedPrice {
			return fail(400, "临时项不能引用固定工艺或冒用含价组合")
		}
		l.SubitemName = ""
		l.IncludedByCraftID, l.ChargeNote = "", ""
		l.WageConfigured = true
		if !u.can("wages") {
			l.Wage = 0
		}
	}
	if l.Temporary && !u.can("customer_prices") {
		if previous == nil {
			return fail(403, "新增临时收费项需要查看客户价格权限")
		}
		// Hidden/forged prices must never replace an existing customer price.
		l.Price = previous.Price
	}
	if l.Temporary && l.Mode == "金额" && len(l.Segments) > 0 {
		if len(l.Segments) != 1 {
			return fail(400, "金额模式只填写一个整项金额")
		}
		if u.can(customerPricePermission) {
			l.Price = l.Segments[0].Amount
		}
	}
	if l.Mode == "金额" {
		l.Segments = []Segment{{Mode: "金额", Amount: l.Price}}
	}
	l.actualLocationOmitted = false
	if err := protectLineChargeAndWage(u, l, previous); err != nil {
		return err
	}
	if err := calcLine(l); err != nil {
		return err
	}
	l.standardLaborCents = l.Labor
	if previous != nil && !l.Temporary {
		if l.Qty == previous.Qty {
			// Never repair historical snapshots as a side effect of note/location edits.
			l.Total, l.Labor = previous.Total, previous.Labor
		} else if previous.UseIncludedPrice || o.legacyIncluded {
			return fail(409, "历史含价施工须保留原收费及工资快照，数量变更请新增施工记录")
		}
	}
	return applyLineChargeAndWage(l, previous)
}

func sameWageOverride(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// Missing fields from older clients preserve saved snapshots. Money permissions
// are independent of permission to fill the underlying construction line.
func protectLineChargeAndWage(u User, l *Line, previous *Line) error {
	if l.ChargeMode == "" && previous != nil {
		l.ChargeMode = previous.ChargeMode
	}
	l.ChargeMode = normalizeChargeMode(l.ChargeMode)
	if !validChargeMode(l.ChargeMode) {
		return fail(400, "施工收费方式无效")
	}
	oldMode := "normal"
	if previous != nil {
		oldMode = normalizeChargeMode(previous.ChargeMode)
	}
	if oldMode != l.ChargeMode && !u.can(customerPricePermission) {
		return fail(403, "调整免费或补做收费方式需要客户价格权限")
	}
	l.WageAdjustmentReason = strings.TrimSpace(l.WageAdjustmentReason)
	if len([]rune(l.WageAdjustmentReason)) > 1000 {
		return fail(400, "工资调整原因超出长度限制")
	}
	if !u.can("wages") {
		if l.WageOverrideCents != nil || l.WageAdjustmentReason != "" {
			return fail(403, "没有工资调整权限")
		}
		if previous != nil {
			l.WageOverrideCents, l.WageAdjustmentReason = previous.WageOverrideCents, previous.WageAdjustmentReason
		}
	} else if previous != nil && l.wageOverrideOmitted {
		l.WageOverrideCents = previous.WageOverrideCents
		if l.WageAdjustmentReason == "" {
			l.WageAdjustmentReason = previous.WageAdjustmentReason
		}
	}
	if l.WageOverrideCents != nil && (*l.WageOverrideCents < 0 || *l.WageOverrideCents > 100000000000000) {
		return fail(400, "实际施工工资超出范围")
	}
	if previous != nil && sameWageOverride(l.WageOverrideCents, previous.WageOverrideCents) && (!l.Temporary || l.Wage == previous.Wage) {
		// An ordinary note edit must not silently rewrite the latest adjustment reason.
		l.WageAdjustmentReason = previous.WageAdjustmentReason
	}
	return nil
}

func applyLineChargeAndWage(l *Line, previous *Line) error {
	oldMode := "normal"
	if previous != nil {
		oldMode = normalizeChargeMode(previous.ChargeMode)
	}
	if l.ChargeMode != "normal" {
		l.Total = 0
	} else if oldMode != "normal" {
		l.Total = scaledAmount(l.Qty, l.Price)
	}
	oldOverride := (*int64)(nil)
	if previous != nil {
		oldOverride = previous.WageOverrideCents
	}
	overrideChanged := !sameWageOverride(l.WageOverrideCents, oldOverride)
	if l.WageOverrideCents != nil {
		l.Labor = *l.WageOverrideCents
	} else if overrideChanged {
		l.Labor = l.standardLaborCents
	}
	if (overrideChanged || (previous != nil && l.Temporary && l.Wage != previous.Wage)) && l.WageAdjustmentReason == "" {
		return fail(400, "调整实际施工工资须填写原因")
	}
	return nil
}

func lineWageAdjustmentChanged(l Line, previous *Line) bool {
	if previous == nil {
		return l.WageOverrideCents != nil
	}
	return !sameWageOverride(l.WageOverrideCents, previous.WageOverrideCents) || (l.Temporary && l.Wage != previous.Wage)
}

func persistLineWageAdjustments(tx *sql.Tx, u User, o Order, previous map[string]Line, key string) error {
	for _, l := range o.Lines {
		var old *Line
		before := l.standardLaborCents
		if p, found := previous[l.ID]; found {
			old, before = &p, p.Labor
		}
		if !lineWageAdjustmentChanged(l, old) {
			continue
		}
		if !u.can("wages") || strings.TrimSpace(l.WageAdjustmentReason) == "" {
			return fail(403, "调整实际施工工资需要工资权限及原因")
		}
		if _, err := tx.Exec(`INSERT INTO order_line_wage_adjustments(id,order_id,line_id,before_cents,after_cents,standard_cents,worker_id,worker_name,reason,actor_id,actor_name,operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP())`, id(), o.ID, l.ID, before, l.Labor, l.standardLaborCents, l.Worker, l.WorkerName, l.WageAdjustmentReason, u.ID, u.Name, stockOperationID(u.ID, key)); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) saveOrder(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if !u.can("orders") {
		return nil, fail(403, "无制单权限")
	}
	var o Order
	if err := decode(b, &o); err != nil {
		return nil, err
	}
	if o.ID == "" {
		o.Company = strings.TrimSpace(o.Company)
		o.Address = strings.TrimSpace(o.Address)
	}
	if o.Delete && !u.can("order_delete") {
		return nil, fail(403, "无删除施工记录权限")
	}
	if o.Combinations != nil {
		if err := requireCombinationOperation(u); err != nil {
			return nil, err
		}
		if o.Delete {
			return nil, fail(400, "取消记录前请先解除并保存组合")
		}
	}
	if len(o.Lines) > 200 || len(o.Materials) > 200 || len(o.Sales) > 200 {
		return nil, fail(400, "单次施工明细过多")
	}
	if len([]rune(o.Phone)) > 60 || len([]rune(o.Address)) > 300 || len([]rune(o.Contact)) > 100 || len([]rune(o.Note)) > 5000 {
		return nil, fail(400, "施工记录文字超出长度限制")
	}
	fresh := o.ID == ""
	var grantErr error
	u, grantErr = withCompanyCraftSources(tx, u, o.Company)
	if grantErr != nil {
		return nil, grantErr
	}
	if fresh {
		if o.Delete {
			return nil, fail(400, "未保存记录不能取消")
		}
		o.ID = id()
		o.Number = "SG-" + time.Now().Format("20060102") + "-" + o.ID[:12]
	}
	if o.Company == "" || strings.TrimSpace(o.Address) == "" {
		return nil, fail(400, "请选择公司并填写工地")
	}
	if _, err := time.Parse("2006-01-02", o.Date); err != nil {
		return nil, fail(400, "施工日期无效")
	}
	if err := checkRef(tx, "companies", o.Company); err != nil {
		return nil, err
	}
	projectName, projectNumber, ticketMode := "", "", ""
	if fresh && o.Project == "" {
		if (o.TicketAction != "" && o.TicketAction != "normal") || o.TicketID != "" || o.TicketVersion != 0 {
			return nil, fail(400, "首次施工地址请按正常归票保存；更正或追加票须先选择原地址")
		}
		p, err := projectFromAddress(o.Company, o.Address)
		if err != nil {
			return nil, err
		}
		p.PackageID = strings.TrimSpace(o.PackageID)
		if p.PackageID != "" {
			if err = requireCustomerPrices(u); err != nil {
				return nil, err
			}
		}
		created, err := createProject(tx, u, p)
		if err != nil {
			return nil, err
		}
		o.Project = created.ID
		projectName, projectNumber, ticketMode = created.Name, created.Number, created.TicketMode
	} else if o.Project != "" {
		if strings.TrimSpace(o.PackageID) != "" {
			return nil, fail(400, "已建工地套餐不能通过施工记录更换")
		}
		var owner, status string
		if err := tx.QueryRow("SELECT company_id,status,name,number FROM projects WHERE id=? FOR UPDATE", o.Project).Scan(&owner, &status, &projectName, &projectNumber); err != nil || owner != o.Company {
			return nil, fail(400, "工地不存在或不属于所选公司")
		}
		if fresh && status != "进行中" {
			return nil, fail(400, "工地已停用或结项，不能继续新增施工")
		}
		var err error
		ticketMode, err = ensureProjectTicketSnapshot(tx, o.Project, o.Company)
		if err != nil {
			return nil, err
		}
	}
	packageSnapshot, err := readProjectPackage(tx, o.Project, false)
	if err != nil {
		return nil, err
	}
	if packageSnapshot != nil && packageSnapshot.CompanyID != o.Company {
		return nil, fail(409, "工地套餐公司快照不匹配，请联系管理员核对")
	}
	if o.SalesTicketSlot == 0 {
		o.SalesTicketSlot = 1
	}
	if o.SalesTicketSlot != 1 && o.SalesTicketSlot != 2 {
		return nil, fail(400, "售料归票位置无效")
	}
	if ticketMode == "single" {
		o.SalesTicketSlot = 1
	}
	if o.TicketAction == "" {
		o.TicketAction = "normal"
	}
	if !contains([]string{"normal", "correct", "append"}, o.TicketAction) {
		return nil, fail(400, "归票方式无效")
	}
	if !fresh && o.TicketAction != "normal" {
		return nil, fail(400, "更正或追加票请新增一次施工记录，不重复修改原施工")
	}
	oldLines := []Line{}
	oldMaterials := []Material{}
	oldSales := []Sale{}
	previous := map[string]Line{}
	protected := []Line{}
	companyName, workerName, oldCompany, oldWorker := "", "", "", ""
	oldDelivery := int64(0)
	oldSalesTicketSlot := 0
	managed := fresh
	if !fresh {
		var version int64
		var status, oldProject, oldDate, oldAddress, oldContact, oldPhone, oldNote string
		var paid bool
		if err := tx.QueryRow(`SELECT version,status,wage_paid,company_id,worker_id,company_name,worker_name,project_id,DATE_FORMAT(work_date,'%Y-%m-%d'),address,contact,phone,note,delivery_wage_cents,ticket_managed,sales_ticket_slot FROM orders WHERE id=? FOR UPDATE`, o.ID).Scan(&version, &status, &paid, &oldCompany, &oldWorker, &companyName, &workerName, &oldProject, &oldDate, &oldAddress, &oldContact, &oldPhone, &oldNote, &oldDelivery, &managed, &oldSalesTicketSlot); err != nil {
			return nil, fail(404, "施工记录不存在")
		}
		if version != o.Version {
			return nil, fail(409, "记录已被其他人修改，请重新打开；当前输入尚未保存")
		}
		if oldProject != o.Project || oldCompany != o.Company {
			return nil, fail(409, "已保存记录不能更换公司或工地")
		}
		accountPaid, payrollSettled, err := lockOrderWorkerPayroll(tx, o.ID)
		if err != nil {
			return nil, err
		}
		if payrollSettled {
			return nil, fail(409, "施工工资已纳入确认结算，请先撤销未付款结算；已实际发放的工资请用工资调整处理差额")
		}
		paid = paid || accountPaid
		if status != "进行中" || paid {
			return nil, fail(400, "该次记录已经交票或发工资，请新增施工记录；修改原记录需先撤销相关结算")
		}
		var locked int
		if err := tx.QueryRow("SELECT COUNT(*) FROM project_ticket_items i JOIN project_tickets t ON t.id=i.ticket_id WHERE i.order_id=? AND t.status<>'草稿'", o.ID).Scan(&locked); err != nil {
			return nil, err
		}
		if locked > 0 {
			return nil, fail(400, "该次记录已交票，新增施工请选择更正原票或单独追加票")
		}
		oldLines, err = readStoredOrderLines(tx, o.ID)
		if err != nil {
			return nil, err
		}
		oldMaterials, err = readStoredMaterials(tx, o.ID)
		if err != nil {
			return nil, err
		}
		oldSales, err = readOrderSales(tx, o.ID)
		if err != nil {
			return nil, err
		}
		for _, l := range oldLines {
			o.legacyIncluded = o.legacyIncluded || l.UseIncludedPrice
			previous[l.ID] = l
			if !lineFillAllowed(u, l) {
				protected = append(protected, l)
			}
		}
		activeTicketClaims := false
		for _, line := range oldLines {
			activeTicketClaims = activeTicketClaims || line.ticketBatchID != ""
		}
		for _, sale := range oldSales {
			activeTicketClaims = activeTicketClaims || sale.ticketBatchID != ""
		}
		if o.Delete && activeTicketClaims {
			return nil, fail(409, "施工记录含有效交票批次，请先逐批次作废后再取消")
		}
		if activeTicketClaims && (oldDate != o.Date || oldAddress != o.Address || oldWorker != o.Worker || oldContact != o.Contact || oldPhone != o.Phone || oldNote != o.Note) {
			return nil, fail(409, "施工记录含有效交票批次，不能修改共同资料；请先作废受影响批次")
		}
		if activeTicketClaims && oldSalesTicketSlot != o.SalesTicketSlot {
			return nil, fail(409, "施工记录含有效交票批次，不能修改售料归票位置；请先作废受影响批次")
		}
		if len(protected) > 0 && (o.Delete || oldWorker != o.Worker || oldDate != o.Date || oldAddress != o.Address || oldContact != o.Contact || oldPhone != o.Phone || oldNote != o.Note) {
			return nil, fail(403, "记录含只读工艺，不能删除或改变共同的日期、施工人和工地资料")
		}
	}
	if !fresh {
		if err := protectStoredCombinationOrder(tx, o.ID); err != nil {
			return nil, err
		}
		if o.Delete {
			var linked bool
			if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM project_combinations c JOIN order_lines l ON l.id=c.waterproof_line_id OR l.id=c.polypropylene_line_id WHERE l.order_id=?)", o.ID).Scan(&linked); err != nil {
				return nil, err
			}
			if linked {
				return nil, fail(409, "本次施工已参与组合，请先解除并保存后再取消")
			}
		}
	}
	if !u.can("wages") {
		if o.DeliveryWage != 0 && o.DeliveryWage != oldDelivery {
			return nil, fail(403, "无带货工资填写权限")
		}
		o.DeliveryWage = oldDelivery
	}
	if o.DeliveryWage < 0 || o.DeliveryWage > 100000000 {
		return nil, fail(400, "本次带货工资须在0至100万元之间")
	}
	if !o.Delete && o.Worker == "" && (len(o.Materials) > 0 || len(o.Sales) > 0 || o.DeliveryWage > 0) {
		return nil, fail(400, "请选择本次材料/送货人")
	}
	if o.Worker != "" {
		if err := checkRef(tx, "workers", o.Worker); err != nil {
			return nil, err
		}
	}
	o.Warehouse = ""
	err = tx.QueryRow("SELECT id FROM warehouses WHERE worker_id=? AND active=1", o.Worker).Scan(&o.Warehouse)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if !o.Delete && o.Warehouse == "" && !u.can("warehouse") && (len(o.Materials) > 0 || len(o.Sales) > 0) {
		return nil, fail(400, "施工/送货人尚未关联材料结余位置，请在仓库管理设置")
	}
	var total, wages int64
	if !o.Delete {
		seen := map[string]bool{}
		clientRefs := map[string]bool{}
		lines := []Line{}
		claimedLines := map[string]Line{}
		for _, stored := range oldLines {
			if stored.ticketBatchID != "" {
				if stored.Worker == "" {
					stored.Worker, stored.WorkerName = oldWorker, workerName
				}
				claimedLines[stored.ID] = stored
			}
		}
		for i := range o.Lines {
			l := o.Lines[i]
			if l.ClientRef != "" {
				if len(l.ClientRef) > 64 || clientRefs[l.ClientRef] {
					return nil, fail(400, "本次施工行引用重复或过长")
				}
				clientRefs[l.ClientRef] = true
			}
			if l.ID == "" && l.SortOrder == 0 {
				l.SortOrder = i
			}
			var old *Line
			if l.ID != "" {
				stored, ok := previous[l.ID]
				if !ok || seen[l.ID] {
					return nil, fail(400, "工艺明细不存在于本次记录或重复提交")
				}
				seen[l.ID] = true
				if !lineFillAllowed(u, stored) {
					return nil, fail(403, "只读工艺不能提交修改，请保留页面只读内容")
				}
				old = &stored
				l.ticketBatchID = stored.ticketBatchID
			} else {
				l.ID = id()
			}
			if err = prepareOrderLine(tx, u, o, &l, old); err != nil {
				return nil, err
			}
			l.PackageIncluded = packageCoversLine(packageSnapshot, l.CraftID, l.SubitemID)
			if l.PackageIncluded {
				l.Total = 0
			}
			lines = append(lines, l)
		}
		for lineID, stored := range claimedLines {
			if !seen[lineID] && !orderContainsLine(protected, lineID) {
				lines = append(lines, stored)
			}
		}
		o.Lines = append(lines, protected...)
		for lineID := range previous {
			if !orderContainsLine(o.Lines, lineID) {
				var linked int
				if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM project_combinations WHERE waterproof_line_id=? OR polypropylene_line_id=?)", lineID, lineID).Scan(&linked); err != nil {
					return nil, err
				}
				if linked != 0 {
					return nil, fail(409, "该施工明细已参与组合免收，不能删除；请先解除相关组合")
				}
			}
		}
		if err = prepareLineWorkers(tx, u, &o, previous); err != nil {
			return nil, err
		}
		comboTargets := map[string]bool{}
		if o.Combinations != nil {
			links, e := readOrderCombinations(tx, o.ID)
			if e != nil {
				return nil, e
			}
			for _, link := range links {
				comboTargets[link.target()] = true
			}
			for _, link := range *o.Combinations {
				comboTargets[link.target()] = true
			}
		}
		for i := range o.Lines {
			l := &o.Lines[i]
			// Only actual old/new waiver targets are projected. An empty list or
			// the non-waived partner must never normalize unrelated saved totals.
			if o.Combinations != nil && l.ticketBatchID == "" && (comboTargets[l.ID] || (l.ClientRef != "" && comboTargets[l.ClientRef])) && lineFillAllowed(u, *l) && !o.legacyIncluded {
				l.Total = scaledAmount(l.Qty, l.Price) // desired waivers are validated after all IDs exist
			} else if err = applyStoredComboWaiver(tx, l); err != nil {
				return nil, err
			}
			if normalizeChargeMode(l.ChargeMode) != "normal" {
				l.Total = 0
			}
			total += l.Total
			wages += l.Labor
		}
		for lineID, stored := range claimedLines {
			if !orderContainsLine(o.Lines, lineID) {
				return nil, fail(409, "施工明细含有效交票批次，请先作废所属批次后再修改")
			}
			if !sameTicketClaimedLine(o.Lines, stored) {
				return nil, fail(409, "施工明细含有效交票批次，请先作废所属批次后再修改")
			}
		}
		if len(o.Lines) == 0 && len(o.Sales) == 0 {
			return nil, fail(400, "至少填写一项施工工艺或一条售料")
		}
		if len(o.Sales) == 0 && o.DeliveryWage > 0 {
			return nil, fail(400, "填写本次带货工资时须有售料明细")
		}
	}
	claimedSales := map[string]Sale{}
	for _, stored := range oldSales {
		if stored.ticketBatchID != "" {
			claimedSales[stored.ID] = stored
		}
	}
	if len(claimedSales) > 0 {
		seenSales := map[string]bool{}
		for i := range o.Sales {
			if stored, ok := claimedSales[o.Sales[i].ID]; ok {
				o.Sales[i].ticketBatchID = stored.ticketBatchID
				seenSales[stored.ID] = true
			}
		}
		for id := range claimedSales {
			if !seenSales[id] {
				return nil, fail(409, "售料明细含有效交票批次，请先作废所属批次后再删除")
			}
		}
	}
	if err := protectOrderSalesPrices(u, &o, oldSales); err != nil {
		return nil, err
	}
	_, salesTotal, err := prepareOrderStock(tx, u, &o, oldMaterials, oldSales)
	if err != nil {
		return nil, err
	}
	for _, stored := range oldLines {
		if stored.ticketBatchID != "" && !sameTicketClaimedMaterials(oldMaterials, o.Materials, stored.ID) {
			return nil, fail(409, "已交票施工明细的耗材不能更改，请先作废所属批次")
		}
	}
	for id, stored := range claimedSales {
		var current *Sale
		for i := range o.Sales {
			if o.Sales[i].ID == id {
				current = &o.Sales[i]
				break
			}
		}
		if current == nil || !sameTicketClaimedSale(*current, stored) {
			return nil, fail(409, "售料明细含有效交票批次，请先作废所属批次后再修改")
		}
	}
	if len(protected) > 0 {
		if !sameOrderStock(oldMaterials, o.Materials, oldSales, o.Sales) || o.DeliveryWage != oldDelivery || (len(oldSales) > 0 && o.SalesTicketSlot != oldSalesTicketSlot) {
			return nil, fail(403, "记录含只读工艺，不能更改共同耗材、售料、归票位置或带货工资；请另增施工记录")
		}
	}
	total += salesTotal
	wages += o.DeliveryWage
	if total > 100000000000000 || wages > 100000000000000 {
		return nil, fail(400, "本次记录总额超出上限")
	}
	materialDelta, saleDelta, deltaErr := orderStockDeltas(oldMaterials, o.Materials, oldSales, o.Sales)
	if deltaErr != nil {
		return nil, deltaErr
	}
	postings := make([]stockPosting, 0, 2)
	if len(materialDelta) > 0 {
		postings = append(postings, stockPosting{Kind: "施工耗材", KeySuffix: "materials", Delta: materialDelta})
	}
	if len(saleDelta) > 0 {
		postings = append(postings, stockPosting{Kind: "商品销售", KeySuffix: "sales", Delta: saleDelta})
	}
	if err = applyStockWithPostings(tx, u, key, o.ID, postings); err != nil {
		return nil, err
	}
	if o.Delete {
		if err = removeOrderTicketItems(tx, u, o.ID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE orders SET status='已取消',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", o.ID); err != nil {
			return nil, err
		}
		if err = syncOrderBonusEntries(tx, u, o.ID, key); err != nil {
			return nil, err
		}
		if err = audit(tx, u, "取消施工记录", o.ID, map[string]any{"version": o.Version + 1, "returned_materials": oldMaterials, "returned_sales": oldSales}); err != nil {
			return nil, err
		}
		return map[string]any{"id": o.ID, "version": o.Version + 1}, nil
	}
	if oldCompany != o.Company || companyName == "" {
		if err = tx.QueryRow("SELECT name FROM companies WHERE id=?", o.Company).Scan(&companyName); err != nil {
			return nil, err
		}
	}
	if o.Worker != "" && (oldWorker != o.Worker || workerName == "") {
		if err = tx.QueryRow("SELECT name FROM workers WHERE id=?", o.Worker).Scan(&workerName); err != nil {
			return nil, err
		}
	}
	if o.Worker == "" {
		workerName = ""
	}
	if fresh {
		_, err = tx.Exec(`INSERT INTO orders(id,number,company_id,address,work_date,worker_id,warehouse_id,contact,note,total_cents,wage_cents,created_by,phone,company_name,worker_name,project_id,project_name,project_number,delivery_wage_cents,ticket_managed,sales_ticket_slot) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, o.Number, o.Company, o.Address, o.Date, o.Worker, o.Warehouse, o.Contact, o.Note, total, wages, u.ID, o.Phone, companyName, workerName, o.Project, projectName, projectNumber, o.DeliveryWage, true, o.SalesTicketSlot)
	} else {
		_, err = tx.Exec(`UPDATE orders SET address=?,work_date=?,worker_id=?,warehouse_id=?,contact=?,note=?,total_cents=?,wage_cents=?,phone=?,worker_name=?,delivery_wage_cents=?,sales_ticket_slot=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?`, o.Address, o.Date, o.Worker, o.Warehouse, o.Contact, o.Note, total, wages, o.Phone, workerName, o.DeliveryWage, o.SalesTicketSlot, o.ID)
	}
	if err != nil {
		return nil, err
	}
	lineIDs := make(map[string]bool, len(o.Lines))
	for _, l := range o.Lines {
		lineIDs[l.ID] = true
		segments := l.Segments
		if segments == nil {
			segments = []Segment{}
		}
		if _, saved := previous[l.ID]; saved {
			_, err = tx.Exec(`UPDATE order_lines SET craft_id=?,category=?,name=?,mode=?,segments=?,qty_micro=?,price_cents=?,wage_cents=?,total_cents=?,labor_cents=?,wage_configured=?,temporary=?,spec=?,region=?,description=?,subitem_id=?,sort_order=?,use_included_price=?,included_by_craft_id=?,charge_note=?,worker_id=?,worker_name=?,subitem_name=?,actual_location=?,charge_mode=?,wage_override_cents=?,wage_adjustment_reason=?,package_included=? WHERE id=? AND order_id=?`, l.CraftID, l.Category, l.Name, l.Mode, jsonText(segments), l.Qty, l.Price, l.Wage, l.Total, l.Labor, l.WageConfigured, l.Temporary, l.Spec, l.Region, l.Description, l.SubitemID, l.SortOrder, l.UseIncludedPrice, l.IncludedByCraftID, l.ChargeNote, l.Worker, l.WorkerName, l.SubitemName, l.ActualLocation, l.ChargeMode, l.WageOverrideCents, l.WageAdjustmentReason, l.PackageIncluded, l.ID, o.ID)
		} else {
			_, err = tx.Exec(`INSERT INTO order_lines(id,order_id,craft_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,wage_configured,temporary,spec,region,description,subitem_id,sort_order,use_included_price,included_by_craft_id,charge_note,worker_id,worker_name,subitem_name,actual_location,charge_mode,wage_override_cents,wage_adjustment_reason,package_included) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, l.ID, o.ID, l.CraftID, l.Category, l.Name, l.Mode, jsonText(segments), l.Qty, l.Price, l.Wage, l.Total, l.Labor, l.WageConfigured, l.Temporary, l.Spec, l.Region, l.Description, l.SubitemID, l.SortOrder, l.UseIncludedPrice, l.IncludedByCraftID, l.ChargeNote, l.Worker, l.WorkerName, l.SubitemName, l.ActualLocation, l.ChargeMode, l.WageOverrideCents, l.WageAdjustmentReason, l.PackageIncluded)
		}
		if err != nil {
			return nil, err
		}
	}
	for oldID := range previous {
		if !lineIDs[oldID] {
			if _, err = tx.Exec("DELETE FROM order_lines WHERE id=? AND order_id=?", oldID, o.ID); err != nil {
				return nil, err
			}
		}
	}
	if err = persistLineWageAdjustments(tx, u, o, previous, key); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("DELETE FROM order_materials WHERE order_id=?", o.ID); err != nil {
		return nil, err
	}
	for _, m := range o.Materials {
		if _, err = tx.Exec("INSERT INTO order_materials(order_id,line_id,product_id,warehouse_id,qty_micro,qty_quantum,input_qty,input_unit,conversion_ratio,product_name,unit,warehouse_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", o.ID, m.LineID, m.Product, m.Warehouse, m.Qty, m.QtyQuantum, m.InputQty, m.InputUnit, m.ConversionRatio, m.ProductName, m.Unit, m.WarehouseName); err != nil {
			return nil, err
		}
	}
	if err = persistWorkerWages(tx, o, workerName); err != nil {
		return nil, err
	}
	if err = syncOrderBonusEntries(tx, u, o.ID, key); err != nil {
		return nil, err
	}
	if err = persistOrderSales(tx, o); err != nil {
		return nil, err
	}
	ticketIDs := []string{}
	if managed {
		ticketIDs, err = assignOrderTickets(tx, u, o, ticketMode, fresh)
		if err != nil {
			return nil, err
		}
	}
	combinationVersion := o.CombinationVersion
	if o.Combinations != nil {
		refs := map[string]string{}
		for _, l := range o.Lines {
			refs[l.ID] = l.ID
			if l.ClientRef != "" {
				refs[l.ClientRef] = l.ID
			}
		}
		desired := *o.Combinations
		for i := range desired {
			if v, ok := refs[desired[i].WaterproofLine]; ok {
				desired[i].WaterproofLine = v
			}
			if v, ok := refs[desired[i].PolypropyleneLine]; ok {
				desired[i].PolypropyleneLine = v
			}
		}
		combinationVersion, err = reconcileOrderCombinations(tx, u, o.Project, o.Company, o.ID, o.CombinationVersion, desired, true)
		if err != nil {
			return nil, err
		}
		if err = tx.QueryRow("SELECT total_cents FROM orders WHERE id=?", o.ID).Scan(&total); err != nil {
			return nil, err
		}
		o.Lines, err = readStoredOrderLines(tx, o.ID)
		if err != nil {
			return nil, err
		}
	}
	if err = audit(tx, u, "保存施工记录", o.ID, map[string]any{"version": o.Version + 1, "project_id": o.Project, "total_cents": total, "lines": o.Lines, "materials": o.Materials, "sales": o.Sales, "delivery_wage_cents": o.DeliveryWage, "tickets": ticketIDs}); err != nil {
		return nil, err
	}
	result := map[string]any{"id": o.ID, "version": o.Version + 1, "ticket_ids": ticketIDs, "project_id": o.Project, "combination_version": combinationVersion}
	if u.can("customer_prices") {
		result["total_cents"] = total
	}
	if fresh {
		result["project_name"], result["project_number"] = projectName, projectNumber
	}
	return result, nil
}

func sameOrderStock(a, b []Material, c, d []Sale) bool {
	if len(a) != len(b) || len(c) != len(d) {
		return false
	}
	m := map[string]string{}
	for _, v := range a {
		m[materialSnapshotKey(v)] = v.QtyQuantum
	}
	for _, v := range b {
		if m[materialSnapshotKey(v)] != v.QtyQuantum {
			return false
		}
	}
	s := map[string]Sale{}
	for _, v := range c {
		s[v.ID] = v
	}
	for _, v := range d {
		old, ok := s[v.ID]
		if !ok || old.Product != v.Product || old.Warehouse != v.Warehouse || old.QtyQuantum != v.QtyQuantum || old.Price != v.Price || old.InputUnit != v.InputUnit || old.Total != v.Total || old.SortOrder != v.SortOrder {
			return false
		}
	}
	return true
}
