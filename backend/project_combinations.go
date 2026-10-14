package main

import (
	"database/sql"
	"sort"
)

const combinationPermission = "combo_pricing"

type ProjectCombination struct {
	ID                   string `json:"id"`
	Version              int64  `json:"version"`
	WaterproofLine       string `json:"waterproof_line_id"`
	PolypropyleneLine    string `json:"polypropylene_line_id"`
	Direction            string `json:"waive_direction"`
	WaivedQty            int64  `json:"waived_qty_micro"`
	WaterproofOrder      string `json:"waterproof_order_id,omitempty"`
	PolypropyleneOrder   string `json:"polypropylene_order_id,omitempty"`
	WaterproofVersion    int64  `json:"waterproof_order_version"`
	PolypropyleneVersion int64  `json:"polypropylene_order_version"`
}

func requireCombinationOperation(u User) error {
	if !u.can("orders") {
		return fail(403, "无制单权限")
	}
	if !u.can(combinationPermission) {
		return fail(403, "无组合免收权限；须由系统管理员明确授权")
	}
	return nil
}

func comboLineKind(l Line) string {
	if l.Temporary || l.UseIncludedPrice || l.Mode != "平方" || l.CraftID == "" || l.Qty <= 0 {
		return ""
	}
	switch l.Category {
	case "防水涂料", "室外防水":
		return "waterproof"
	case "丙纶":
		return "polypropylene"
	}
	return ""
}

func (c ProjectCombination) target() string {
	if c.Direction == "waterproof" {
		return c.WaterproofLine
	}
	return c.PolypropyleneLine
}
func (c ProjectCombination) pair() string { return c.WaterproofLine + ":" + c.PolypropyleneLine }

func orderContainsLine(lines []Line, lineID string) bool {
	for _, l := range lines {
		if l.ID == lineID {
			return true
		}
	}
	return false
}

// Only the customer projection changes. Labor uses full actual quantity and is
// deliberately not touched here (nor by the combination persistence service).
func applyComboWaiver(l *Line, waived int64) error {
	if waived < 0 || waived > l.Qty {
		return fail(409, "免收面积超过该施工明细的剩余可用面积，请重新核对")
	}
	if waived > 0 && comboLineKind(*l) == "" {
		return fail(400, "组合仅支持明确按平方米计量的固定防水、丙纶明细")
	}
	l.WaivedQty = waived
	// A combination changes only the normal customer charge. Free/redo work
	// stays free when a relation is added, changed, or released; its actual
	// quantity and labor snapshots must remain intact.
	if l.ChargeMode == "free" || l.ChargeMode == "redo" {
		l.Total = 0
	} else if waived > 0 {
		l.Total = scaledAmount(l.Qty-waived, l.Price)
	}
	return nil
}

func storedComboWaiver(tx *sql.Tx, lineID string) (int64, error) {
	var q int64
	err := tx.QueryRow(`SELECT COALESCE(SUM(waived_qty_micro),0) FROM project_combinations WHERE
        (waive_direction='waterproof' AND waterproof_line_id=?) OR
        (waive_direction='polypropylene' AND polypropylene_line_id=?)`, lineID, lineID).Scan(&q)
	return q, err
}
func applyStoredComboWaiver(tx *sql.Tx, l *Line) error {
	q, err := storedComboWaiver(tx, l.ID)
	if err != nil {
		return err
	}
	return applyComboWaiver(l, q)
}

func readOrderCombinations(tx *sql.Tx, orderID string) ([]ProjectCombination, error) {
	rows, err := tx.Query(`SELECT c.id,c.version,c.waterproof_line_id,c.polypropylene_line_id,c.waive_direction,c.waived_qty_micro,
        w.order_id,p.order_id,wo.version,po.version FROM project_combinations c
        JOIN order_lines w ON w.id=c.waterproof_line_id JOIN orders wo ON wo.id=w.order_id
        JOIN order_lines p ON p.id=c.polypropylene_line_id JOIN orders po ON po.id=p.order_id
        WHERE w.order_id=? OR p.order_id=? ORDER BY c.id`, orderID, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ProjectCombination{}
	for rows.Next() {
		var c ProjectCombination
		if err = rows.Scan(&c.ID, &c.Version, &c.WaterproofLine, &c.PolypropyleneLine, &c.Direction, &c.WaivedQty, &c.WaterproofOrder, &c.PolypropyleneOrder, &c.WaterproofVersion, &c.PolypropyleneVersion); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// All order writes already take the project lock. Finance takes that same lock;
// payroll takes the order lock. Lock orders in a stable order before testing any
// wages/receipts so a simultaneous settlement cannot slip past the check.
func protectCombinationOrders(tx *sql.Tx, orderIDs map[string]bool) error {
	ids := sortedKeys(orderIDs)
	for _, orderID := range ids {
		var status string
		var paid bool
		if err := tx.QueryRow("SELECT status,wage_paid FROM orders WHERE id=? FOR UPDATE", orderID).Scan(&status, &paid); err != nil {
			return err
		}
		if status != "进行中" || paid {
			return fail(409, "关联施工记录已交票、已收款、已取消或已结算，不能直接修改组合；请先处理原账务")
		}
		var protected bool
		if err := tx.QueryRow(`SELECT
            EXISTS(SELECT 1 FROM order_worker_wages WHERE order_id=? AND wage_paid=1) OR
            EXISTS(SELECT 1 FROM payment_receipts WHERE order_id=? AND voided=0) OR
            EXISTS(SELECT 1 FROM project_ticket_items i JOIN project_tickets t ON t.id=i.ticket_id
                WHERE i.order_id=? AND (t.status<>'草稿' OR EXISTS(SELECT 1 FROM project_ticket_receipts r WHERE r.ticket_id=t.id AND r.voided=0 AND r.receipt_id='')))`, orderID, orderID, orderID).Scan(&protected); err != nil {
			return err
		}
		if protected {
			return fail(409, "关联施工记录已有交票、有效收款或工资结算，拒绝追溯修改组合；请先处理原账务")
		}
	}
	return nil
}

func protectStoredCombinationOrder(tx *sql.Tx, orderID string) error {
	links, err := readOrderCombinations(tx, orderID)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}
	ids := map[string]bool{orderID: true}
	for _, c := range links {
		ids[c.WaterproofOrder] = true
		ids[c.PolypropyleneOrder] = true
	}
	return protectCombinationOrders(tx, ids)
}

func sortedKeys(set map[string]bool) []string {
	result := make([]string, 0, len(set))
	for s := range set {
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}
