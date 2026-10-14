package main

import "database/sql"

// Called before the idempotency response is read, in the same transaction.
// Submitted rows need current public grants. Omitted readonly rows remain the
// save handler's responsibility, so normal edits do not require every craft.
func publicCraftMutationAllowed(tx *sql.Tx, path string, u User, data []byte) error {
	if u.Role != "employee" || !contains([]string{"/api/order", "/api/project-combination", "/api/project-associate"}, path) {
		return nil
	}
	if !u.can("orders") {
		return fail(403, "无制单权限")
	}
	switch path {
	case "/api/order":
		var o Order
		if err := decode(data, &o); err != nil {
			return err
		}
		company := o.Company
		if o.ID != "" {
			if err := tx.QueryRow("SELECT company_id FROM orders WHERE id=?", o.ID).Scan(&company); err != nil {
				return fail(404, "施工记录不存在")
			}
		}
		var err error
		u, err = withCompanyCraftSources(tx, u, company)
		if err != nil {
			return err
		}
		for _, l := range o.Lines {
			if !lineFillAllowed(u, l) {
				return fail(403, "没有对应公共工艺的填写权限")
			}
			if !u.can("wages") && (l.WageOverrideCents != nil || l.WageAdjustmentReason != "") {
				return fail(403, "没有工资调整权限")
			}
			if !u.can(customerPricePermission) && l.ChargeMode != "" {
				mode := "normal"
				if l.ID != "" {
					if err := tx.QueryRow("SELECT charge_mode FROM order_lines WHERE id=? AND order_id=?", l.ID, o.ID).Scan(&mode); err != nil {
						return fail(400, "工艺明细不存在于本次记录")
					}
				}
				if normalizeChargeMode(l.ChargeMode) != normalizeChargeMode(mode) {
					return fail(403, "调整免费或补做收费方式需要客户价格权限")
				}
			}
		}
		if o.Delete {
			if err = requireStoredCraftGrants(tx, u, o.ID); err != nil {
				return err
			}
		}
		if o.Combinations != nil {
			return requireCombinationCraftGrants(tx, u, company, o.ID, *o.Combinations, o.Lines)
		}
	case "/api/project-combination":
		var p struct {
			Order        string               `json:"order_id"`
			Combinations []ProjectCombination `json:"combinations"`
		}
		if err := decode(data, &p); err != nil {
			return err
		}
		var company string
		if err := tx.QueryRow("SELECT company_id FROM orders WHERE id=?", p.Order).Scan(&company); err != nil {
			return fail(404, "施工记录不存在")
		}
		var err error
		u, err = withCompanyCraftSources(tx, u, company)
		if err != nil {
			return err
		}
		return requireCombinationCraftGrants(tx, u, company, p.Order, p.Combinations, nil)
	case "/api/project-associate":
		var p struct {
			Order string `json:"order_id"`
		}
		if err := decode(data, &p); err != nil {
			return err
		}
		var company string
		if err := tx.QueryRow("SELECT company_id FROM orders WHERE id=?", p.Order).Scan(&company); err != nil {
			return fail(404, "施工记录不存在")
		}
		var err error
		u, err = withCompanyCraftSources(tx, u, company)
		if err != nil {
			return err
		}
		return requireStoredCraftGrants(tx, u, p.Order)
	}
	return nil
}

func requireStoredCraftGrants(tx *sql.Tx, u User, orderID string) error {
	lines, err := readStoredOrderLines(tx, orderID)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if !lineFillAllowed(u, l) {
			return fail(403, "操作需要施工记录全部公共工艺的填写权限")
		}
	}
	return nil
}

func requireCombinationCraftGrants(tx *sql.Tx, u User, company, orderID string, desired []ProjectCombination, submitted []Line) error {
	old, err := readOrderCombinations(tx, orderID)
	if err != nil {
		return err
	}
	clientRefs := map[string]Line{}
	for _, l := range submitted {
		if l.ClientRef != "" {
			clientRefs[l.ClientRef] = l
		}
	}
	for _, c := range append(old, desired...) {
		for _, ref := range []string{c.WaterproofLine, c.PolypropyleneLine} {
			if l, ok := clientRefs[ref]; ok {
				if !lineFillAllowed(u, l) {
					return fail(403, "组合需要双方公共工艺的填写权限")
				}
				continue
			}
			line, err := readCombinationLine(tx, ref)
			if err != nil {
				return err
			}
			if line.Company != company || !lineFillAllowed(u, line.Line) {
				return fail(403, "组合需要同公司双方公共工艺的填写权限")
			}
		}
	}
	return nil
}
