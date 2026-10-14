package main

import "database/sql"

// CombinationSavedLine contains only saved ordinary construction snapshots.
// Customer money is optional; this view never contains wage data or current
// craft/worker configuration, even for a reader who has wage permissions.
type CombinationSavedLine struct {
	ID             string `json:"id"`
	OrderID        string `json:"order_id"`
	Number         string `json:"number"`
	WorkDate       string `json:"work_date"`
	CraftID        string `json:"craft_id"`
	Category       string `json:"category"`
	Name           string `json:"name"`
	SubitemID      string `json:"subitem_id"`
	SubitemName    string `json:"subitem_name"`
	ActualLocation string `json:"actual_location"`
	WorkerID       string `json:"worker_id"`
	WorkerName     string `json:"worker_name"`
	Mode           string `json:"mode"`
	Qty            int64  `json:"qty_micro"`
	WaivedQty      int64  `json:"waived_qty_micro"`
	ChargeableQty  int64  `json:"chargeable_qty_micro"`
	Price          *int64 `json:"price_cents,omitempty"`
	Total          *int64 `json:"total_cents,omitempty"`
}

type orderCombinationDetail struct {
	ID            string               `json:"id"`
	Waterproof    CombinationSavedLine `json:"waterproof"`
	Polypropylene CombinationSavedLine `json:"polypropylene"`
}

// The caller owns the same repeatable-read, read-only transaction as the order
// detail. Both actual sides and the relationship must be within that order's
// company/project; a malformed foreign relationship must not expose snapshots.
func readOrderCombinationDetails(tx *sql.Tx, orderID string, u User) ([]orderCombinationDetail, error) {
	rows, err := tx.Query(`SELECT c.id,
        w.id,w.order_id,wo.number,DATE_FORMAT(wo.work_date,'%Y-%m-%d'),w.craft_id,w.category,w.name,w.subitem_id,
        COALESCE(w.subitem_name,''),COALESCE(w.actual_location,''),
        COALESCE(NULLIF(w.worker_id,''),wo.worker_id),CASE WHEN w.worker_id='' THEN wo.worker_name ELSE w.worker_name END,
        w.mode,w.qty_micro,
        COALESCE((SELECT SUM(wc.waived_qty_micro) FROM project_combinations wc WHERE
            (wc.waive_direction='waterproof' AND wc.waterproof_line_id=w.id) OR
            (wc.waive_direction='polypropylene' AND wc.polypropylene_line_id=w.id)),0),w.price_cents,w.total_cents,
        p.id,p.order_id,po.number,DATE_FORMAT(po.work_date,'%Y-%m-%d'),p.craft_id,p.category,p.name,p.subitem_id,
        COALESCE(p.subitem_name,''),COALESCE(p.actual_location,''),
        COALESCE(NULLIF(p.worker_id,''),po.worker_id),CASE WHEN p.worker_id='' THEN po.worker_name ELSE p.worker_name END,
        p.mode,p.qty_micro,
        COALESCE((SELECT SUM(pc.waived_qty_micro) FROM project_combinations pc WHERE
            (pc.waive_direction='waterproof' AND pc.waterproof_line_id=p.id) OR
            (pc.waive_direction='polypropylene' AND pc.polypropylene_line_id=p.id)),0),p.price_cents,p.total_cents
        FROM project_combinations c
        JOIN order_lines w ON w.id=c.waterproof_line_id JOIN orders wo ON wo.id=w.order_id
        JOIN order_lines p ON p.id=c.polypropylene_line_id JOIN orders po ON po.id=p.order_id
        JOIN orders requested ON requested.id=?
        JOIN projects project_scope ON project_scope.id=requested.project_id AND project_scope.company_id=requested.company_id
        WHERE c.project_id=project_scope.id
          AND wo.project_id=project_scope.id AND po.project_id=project_scope.id
          AND wo.company_id=requested.company_id AND po.company_id=requested.company_id
          AND (wo.id=requested.id OR po.id=requested.id)
        ORDER BY c.id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []orderCombinationDetail{}
	canSeePrices := u.can(customerPricePermission)
	for rows.Next() {
		var d orderCombinationDetail
		var waterPrice, waterTotal, polyPrice, polyTotal int64
		w, p := &d.Waterproof, &d.Polypropylene
		if err = rows.Scan(&d.ID,
			&w.ID, &w.OrderID, &w.Number, &w.WorkDate, &w.CraftID, &w.Category, &w.Name, &w.SubitemID,
			&w.SubitemName, &w.ActualLocation, &w.WorkerID, &w.WorkerName, &w.Mode, &w.Qty, &w.WaivedQty, &waterPrice, &waterTotal,
			&p.ID, &p.OrderID, &p.Number, &p.WorkDate, &p.CraftID, &p.Category, &p.Name, &p.SubitemID,
			&p.SubitemName, &p.ActualLocation, &p.WorkerID, &p.WorkerName, &p.Mode, &p.Qty, &p.WaivedQty, &polyPrice, &polyTotal); err != nil {
			return nil, err
		}
		// Use the same actual waiver sum as readStoredOrderLines, not this one
		// relationship's area. Amounts remain the stored values, without repricing.
		w.ChargeableQty, p.ChargeableQty = w.Qty-w.WaivedQty, p.Qty-p.WaivedQty
		if canSeePrices {
			w.Price, w.Total, p.Price, p.Total = &waterPrice, &waterTotal, &polyPrice, &polyTotal
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
