package main

import "database/sql"

type combinationStoredLine struct {
	Line
	OrderID      string
	OrderVersion int64
	Company      string
	Project      string
}

func readCombinationLine(tx *sql.Tx, lineID string) (combinationStoredLine, error) {
	var v combinationStoredLine
	err := tx.QueryRow(`SELECT l.id,l.order_id,o.version,o.company_id,o.project_id,l.craft_id,l.category,l.mode,l.qty_micro,l.price_cents,l.total_cents,l.labor_cents,l.temporary,l.use_included_price,l.charge_mode
        FROM order_lines l JOIN orders o ON o.id=l.order_id WHERE l.id=?`, lineID).Scan(&v.ID, &v.OrderID, &v.OrderVersion, &v.Company, &v.Project, &v.CraftID, &v.Category, &v.Mode, &v.Qty, &v.Price, &v.Total, &v.Labor, &v.Temporary, &v.UseIncludedPrice, &v.ChargeMode)
	if err == sql.ErrNoRows {
		return v, fail(400, "组合引用的实际施工明细不存在，请重新选择")
	}
	return v, err
}

// Replace only the relations touching this order, never unrelated project rows.
// Caller holds the project lock; relation version protects simultaneous editors.
// Existing/new order save and the dedicated API use this exact transaction path.
func reconcileOrderCombinations(tx *sql.Tx, u User, projectID, companyID, orderID string, expected int64, desired []ProjectCombination, currentOrderAlreadySaved bool) (int64, error) {
	if err := requireCombinationOperation(u); err != nil {
		return 0, err
	}
	if projectID == "" || len(desired) > 200 {
		return 0, fail(400, "组合必须关联明确工地，且一次不超过200组")
	}
	var actual int64
	var company, status string
	if err := tx.QueryRow("SELECT company_id,status,combination_version FROM projects WHERE id=? FOR UPDATE", projectID).Scan(&company, &status, &actual); err != nil {
		return 0, fail(404, "工地不存在")
	}
	if company != companyID {
		return 0, fail(400, "组合必须属于同一公司、同一工地")
	}
	var grantErr error
	u, grantErr = withCompanyCraftSources(tx, u, companyID)
	if grantErr != nil {
		return 0, grantErr
	}
	if status != "进行中" {
		return 0, fail(409, "工地已停用或结项，不能修改组合")
	}
	if actual != expected {
		return 0, fail(409, "工地组合已被其他人修改，请重新打开核对；当前输入未保存")
	}
	old, err := readOrderCombinations(tx, orderID)
	if err != nil {
		return 0, err
	}
	oldPairs := map[string]ProjectCombination{}
	affectedLines := map[string]bool{}
	changedLines := map[string]bool{}
	for _, c := range old {
		oldPairs[c.pair()] = c
		affectedLines[c.target()] = true
	}
	seen := map[string]bool{}
	ids := map[string]bool{orderID: true}
	cache := map[string]combinationStoredLine{}
	getLine := func(ref string) (combinationStoredLine, error) {
		if v, ok := cache[ref]; ok {
			return v, nil
		}
		v, e := readCombinationLine(tx, ref)
		if e == nil {
			cache[ref] = v
		}
		return v, e
	}
	changed := false
	for i := range desired {
		c := &desired[i]
		if c.WaterproofLine == "" || c.PolypropyleneLine == "" || c.WaterproofLine == c.PolypropyleneLine || seen[c.pair()] {
			return 0, fail(400, "请选择不同的防水、丙纶施工明细，同一配对不能重复免收")
		}
		seen[c.pair()] = true
		if !contains([]string{"waterproof", "polypropylene"}, c.Direction) || c.WaivedQty <= 0 || c.WaivedQty > 1000000000000 {
			return 0, fail(400, "请明确选择免收方向，手动填写大于0且有效的免收平方米数")
		}
		w, e := getLine(c.WaterproofLine)
		if e != nil {
			return 0, e
		}
		p, e := getLine(c.PolypropyleneLine)
		if e != nil {
			return 0, e
		}
		if w.Project != projectID || p.Project != projectID || w.Company != companyID || p.Company != companyID || (w.OrderID != orderID && p.OrderID != orderID) {
			return 0, fail(400, "只能关联本次施工与同公司、同工地的实际明细")
		}
		if comboLineKind(w.Line) != "waterproof" || comboLineKind(p.Line) != "polypropylene" {
			return 0, fail(400, "首期组合仅支持公司固定的平方米防水、丙纶施工明细，历史含价记录不能转换")
		}
		if !lineFillAllowed(u, w.Line) || !lineFillAllowed(u, p.Line) {
			return 0, fail(403, "组合权限不能代替关联双方固定工艺的填写权限")
		}
		if (w.OrderID != orderID && c.WaterproofVersion != w.OrderVersion) || (p.OrderID != orderID && c.PolypropyleneVersion != p.OrderVersion) {
			return 0, fail(409, "关联施工记录已发生变化，请重新选择实际明细")
		}
		if !currentOrderAlreadySaved && ((w.OrderID == orderID && c.WaterproofVersion != w.OrderVersion) || (p.OrderID == orderID && c.PolypropyleneVersion != p.OrderVersion)) {
			return 0, fail(409, "本次施工版本已变化，请重新打开")
		}
		c.WaterproofOrder = w.OrderID
		c.PolypropyleneOrder = p.OrderID
		ids[w.OrderID] = true
		ids[p.OrderID] = true
		affectedLines[c.target()] = true
		if previous, ok := oldPairs[c.pair()]; ok {
			if c.ID != "" && c.ID != previous.ID {
				return 0, fail(400, "组合关系编号与实际明细不匹配")
			}
			c.ID = previous.ID
			if previous.Direction != c.Direction || previous.WaivedQty != c.WaivedQty {
				changed = true
				changedLines[previous.target()] = true
				changedLines[c.target()] = true
			}
		} else {
			if c.ID != "" {
				return 0, fail(400, "不能引用其他施工记录的组合编号")
			}
			c.ID = id()
			changed = true
			changedLines[c.target()] = true
		}
	}
	for _, c := range old {
		// Removing a relation changes its customer's charge too. The explicit
		// combination grant must not bypass either fixed-craft fill boundary.
		for _, lineID := range []string{c.WaterproofLine, c.PolypropyleneLine} {
			line, e := getLine(lineID)
			if e != nil {
				return 0, e
			}
			if !lineFillAllowed(u, line.Line) {
				return 0, fail(403, "解除组合也需要关联双方固定工艺的填写权限")
			}
		}
		ids[c.WaterproofOrder] = true
		ids[c.PolypropyleneOrder] = true
		if !seen[c.pair()] {
			changed = true
			changedLines[c.target()] = true
		}
	}
	if len(old) == 0 && len(desired) == 0 {
		return actual, nil
	}
	if err = protectCombinationOrders(tx, ids); err != nil {
		return 0, err
	}
	for _, lineID := range sortedKeys(changedLines) {
		var ticketBatchID string
		if err = tx.QueryRow("SELECT COALESCE(ticket_batch_id,'') FROM order_lines WHERE id=? FOR UPDATE", lineID).Scan(&ticketBatchID); err != nil {
			return 0, err
		}
		if ticketBatchID != "" {
			return 0, fail(409, "组合会改动已交票施工明细，请先作废该明细所属批次")
		}
	}
	// No price or wage snapshots in old included orders are converted by this API.
	for _, oid := range sortedKeys(ids) {
		var legacy bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM order_lines WHERE order_id=? AND use_included_price=1)", oid).Scan(&legacy); err != nil {
			return 0, err
		}
		if legacy {
			return 0, fail(409, "关联记录含旧含价快照，须保留原收费与工资；请新增实际施工记录")
		}
	}
	for _, c := range old {
		if !seen[c.pair()] {
			if _, err = tx.Exec("DELETE FROM project_combinations WHERE id=?", c.ID); err != nil {
				return 0, err
			}
		}
	}
	for _, c := range desired {
		previous, exists := oldPairs[c.pair()]
		if !exists {
			_, err = tx.Exec(`INSERT INTO project_combinations(id,project_id,waterproof_line_id,polypropylene_line_id,waive_direction,waived_qty_micro,created_by,updated_by) VALUES(?,?,?,?,?,?,?,?)`, c.ID, projectID, c.WaterproofLine, c.PolypropyleneLine, c.Direction, c.WaivedQty, u.ID, u.ID)
		} else if previous.Direction != c.Direction || previous.WaivedQty != c.WaivedQty {
			_, err = tx.Exec("UPDATE project_combinations SET waive_direction=?,waived_qty_micro=?,version=version+1,updated_by=?,updated_at=UTC_TIMESTAMP() WHERE id=?", c.Direction, c.WaivedQty, u.ID, c.ID)
		}
		if err != nil {
			return 0, err
		}
	}
	// Aggregate all reservations, including other dates/relations, before changing
	// any customer total. A failure rolls back lines, relations, tickets and audit.
	changedOrders := map[string]bool{}
	for _, lineID := range sortedKeys(affectedLines) {
		l, e := readCombinationLine(tx, lineID)
		if e != nil {
			return 0, e
		}
		q, e := storedComboWaiver(tx, lineID)
		if e != nil {
			return 0, e
		}
		// Releasing a normal waiver restores its full charge. applyComboWaiver
		// keeps explicit free/redo work at zero even when no waiver remains.
		l.Total = scaledAmount(l.Qty, l.Price)
		if e = applyComboWaiver(&l.Line, q); e != nil {
			return 0, e
		}
		var saved int64
		if err = tx.QueryRow("SELECT total_cents FROM order_lines WHERE id=?", lineID).Scan(&saved); err != nil {
			return 0, err
		}
		if saved != l.Total {
			var ticketBatchID string
			if err = tx.QueryRow("SELECT COALESCE(ticket_batch_id,'') FROM order_lines WHERE id=? FOR UPDATE", lineID).Scan(&ticketBatchID); err != nil {
				return 0, err
			}
			if err = validateCombinationTotalChange(saved, l.Total, ticketBatchID); err != nil {
				return 0, err
			}
			if _, err = tx.Exec("UPDATE order_lines SET total_cents=? WHERE id=?", l.Total, lineID); err != nil {
				return 0, err
			}
			changedOrders[l.OrderID] = true
		}
	}
	tickets := map[string]bool{}
	for _, oid := range sortedKeys(changedOrders) {
		var total int64
		if err = tx.QueryRow(`SELECT COALESCE((SELECT SUM(total_cents) FROM order_lines WHERE order_id=?),0)+COALESCE((SELECT SUM(total_cents) FROM order_sales WHERE order_id=?),0)`, oid, oid).Scan(&total); err != nil {
			return 0, err
		}
		if total > 100000000000000 {
			return 0, fail(400, "施工记录客户合计超出上限")
		}
		increment := 1
		if currentOrderAlreadySaved && oid == orderID {
			increment = 0
		}
		if _, err = tx.Exec("UPDATE orders SET total_cents=?,version=version+?,updated_at=UTC_TIMESTAMP() WHERE id=?", total, increment, oid); err != nil {
			return 0, err
		}
		ticketIDs, e := orderTicketIDs(tx, oid)
		if e != nil {
			return 0, e
		}
		for _, tid := range ticketIDs {
			tickets[tid] = true
		}
	}
	for _, ticketID := range sortedKeys(tickets) {
		if err = refreshProjectTicket(tx, u, ticketID, "修改防水丙纶组合免收"); err != nil {
			return 0, err
		}
	}
	if changed {
		if _, err = tx.Exec("UPDATE projects SET combination_version=combination_version+1 WHERE id=?", projectID); err != nil {
			return 0, err
		}
		actual++
		if err = audit(tx, u, "修改防水丙纶组合免收", projectID, map[string]any{"order_id": orderID, "before": old, "after": desired, "combination_version": actual}); err != nil {
			return 0, err
		}
	}
	return actual, nil
}

func validateCombinationTotalChange(saved, projected int64, ticketBatchID string) error {
	if saved != projected && ticketBatchID != "" {
		return fail(409, "组合会改动已交票施工明细，请先作废该明细所属批次")
	}
	return nil
}

func (a *App) saveProjectCombination(tx *sql.Tx, u User, b []byte) (any, error) {
	if err := requireCombinationOperation(u); err != nil {
		return nil, err
	}
	var p struct {
		Project            string                `json:"project_id"`
		Order              string                `json:"order_id"`
		Version            int64                 `json:"order_version"`
		CombinationVersion int64                 `json:"combination_version"`
		Combinations       *[]ProjectCombination `json:"combinations"`
	}
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	if p.Combinations == nil || p.Order == "" || p.Project == "" {
		return nil, fail(400, "请指定工地、施工记录及完整组合清单；空清单表示关闭组合")
	}
	var company string
	if err := tx.QueryRow("SELECT company_id FROM projects WHERE id=? FOR UPDATE", p.Project).Scan(&company); err != nil {
		return nil, fail(404, "工地不存在")
	}
	var project string
	var version int64
	if err := tx.QueryRow("SELECT project_id,version FROM orders WHERE id=? FOR UPDATE", p.Order).Scan(&project, &version); err != nil {
		return nil, fail(404, "施工记录不存在")
	}
	if project != p.Project || version != p.Version {
		return nil, fail(409, "施工记录工地或版本已变化，请重新打开")
	}
	v, err := reconcileOrderCombinations(tx, u, p.Project, company, p.Order, p.CombinationVersion, *p.Combinations, false)
	if err != nil {
		return nil, err
	}
	var total int64
	if err = tx.QueryRow("SELECT version,total_cents FROM orders WHERE id=?", p.Order).Scan(&version, &total); err != nil {
		return nil, err
	}
	result := map[string]any{"id": p.Order, "version": version, "combination_version": v}
	if u.can(customerPricePermission) {
		result["total_cents"] = total
	}
	return result, nil
}
