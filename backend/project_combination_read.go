package main

import (
	"database/sql"
	"net/http"
	"strings"
)

type combinationCandidate struct {
	ID            string `json:"id"`
	OrderID       string `json:"order_id"`
	OrderVersion  int64  `json:"order_version"`
	Number        string `json:"number"`
	Date          string `json:"work_date"`
	CraftID       string `json:"craft_id"`
	Kind          string `json:"kind"`
	Category      string `json:"category"`
	Name          string `json:"name"`
	SubitemName   string `json:"subitem_name"`
	Location      string `json:"actual_location"`
	Region        string `json:"region"`
	WorkerID      string `json:"worker_id"`
	WorkerName    string `json:"worker_name"`
	Qty           int64  `json:"qty_micro"`
	WaivedQty     int64  `json:"waived_qty_micro"`
	AvailableQty  int64  `json:"available_qty_micro"`
	Price         *int64 `json:"price_cents,omitempty"`
	Total         *int64 `json:"total_cents,omitempty"`
	Editable      bool   `json:"editable"`
	BlockedReason string `json:"blocked_reason"`
}

func (a *App) readProjectCombinations(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") {
		return fail(403, "无制单权限")
	}
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		return fail(400, "请选择明确工地")
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var company, projectStatus string
	var version int64
	if err = tx.QueryRow("SELECT company_id,status,combination_version FROM projects WHERE id=?", projectID).Scan(&company, &projectStatus, &version); err != nil {
		return fail(404, "工地不存在")
	}
	u, err = withCompanyCraftSources(tx, u, company)
	if err != nil {
		return err
	}
	condition := ` FROM order_lines l JOIN orders o ON o.id=l.order_id WHERE o.project_id=? AND o.company_id=? AND o.status<>'已取消' AND l.temporary=0 AND l.mode='平方' AND l.use_included_price=0 AND l.qty_micro>0 AND l.category IN ('防水涂料','室外防水','丙纶')`
	args := []any{projectID, company}
	// Reopening a relation must work even when its other date is beyond page 1.
	// Targeted reads retain exactly the same project and price-permission scope.
	if raw := r.URL.Query().Get("line_ids"); raw != "" {
		refs := strings.Split(raw, ",")
		if len(refs) > 50 {
			return fail(400, "一次最多读取50条明确施工明细")
		}
		placeholders := []string{}
		seen := map[string]bool{}
		for _, ref := range refs {
			if ref == "" || len(ref) > 64 || seen[ref] {
				return fail(400, "施工明细编号为空、过长或重复")
			}
			seen[ref] = true
			placeholders = append(placeholders, "?")
			args = append(args, ref)
		}
		condition += " AND l.id IN (" + strings.Join(placeholders, ",") + ")"
	}
	var total int
	if err = tx.QueryRow("SELECT COUNT(*)"+condition, args...).Scan(&total); err != nil {
		return err
	}
	rowArgs := append(append([]any{}, args...), (page-1)*50)
	rows, err := tx.Query(`SELECT l.id,l.order_id,o.version,o.number,DATE_FORMAT(o.work_date,'%Y-%m-%d'),l.craft_id,l.category,l.name,COALESCE(l.subitem_name,''),COALESCE(l.actual_location,''),l.region,COALESCE(NULLIF(l.worker_id,''),o.worker_id),COALESCE(NULLIF(l.worker_name,''),o.worker_name),l.qty_micro,l.price_cents,l.total_cents,
        COALESCE((SELECT SUM(c.waived_qty_micro) FROM project_combinations c WHERE (c.waive_direction='waterproof' AND c.waterproof_line_id=l.id) OR (c.waive_direction='polypropylene' AND c.polypropylene_line_id=l.id)),0),
        (o.status<>'进行中' OR o.wage_paid=1 OR EXISTS(SELECT 1 FROM order_worker_wages ww WHERE ww.order_id=o.id AND ww.wage_paid=1) OR
          EXISTS(SELECT 1 FROM payment_receipts pr WHERE pr.order_id=o.id AND pr.voided=0) OR
          EXISTS(SELECT 1 FROM project_ticket_items ti JOIN project_tickets t ON t.id=ti.ticket_id WHERE ti.order_id=o.id AND (t.status<>'草稿' OR EXISTS(SELECT 1 FROM project_ticket_receipts tr WHERE tr.ticket_id=t.id AND tr.voided=0 AND tr.receipt_id=''))) OR
          EXISTS(SELECT 1 FROM order_lines legacy WHERE legacy.order_id=o.id AND legacy.use_included_price=1))`+condition+" ORDER BY o.work_date DESC,o.id,l.sort_order,l.id LIMIT 50 OFFSET ?", rowArgs...)
	if err != nil {
		return err
	}
	candidates := []combinationCandidate{}
	for rows.Next() {
		var c combinationCandidate
		var price, amount int64
		var blocked bool
		if err = rows.Scan(&c.ID, &c.OrderID, &c.OrderVersion, &c.Number, &c.Date, &c.CraftID, &c.Category, &c.Name, &c.SubitemName, &c.Location, &c.Region, &c.WorkerID, &c.WorkerName, &c.Qty, &price, &amount, &c.WaivedQty, &blocked); err != nil {
			rows.Close()
			return err
		}
		c.Kind = "waterproof"
		if c.Category == "丙纶" {
			c.Kind = "polypropylene"
		}
		c.AvailableQty = c.Qty - c.WaivedQty
		c.Editable = u.can(combinationPermission) && u.canFillCompanyCraft(c.CraftID) && !blocked && projectStatus == "进行中"
		if projectStatus != "进行中" {
			c.BlockedReason = "工地已停用或结项，不能修改组合"
		} else if blocked {
			c.BlockedReason = "已交票、有效收款、工资结算或旧含价快照，不能直接组合"
		} else if !u.canFillCompanyCraft(c.CraftID) {
			c.BlockedReason = "没有此固定工艺的填写权限"
		} else if !u.can(combinationPermission) {
			c.BlockedReason = "没有组合免收权限"
		}
		if u.can(customerPricePermission) {
			c.Price = &price
			c.Total = &amount
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, map[string]any{"rows": candidates, "total": total, "page": page, "page_size": 50, "project_id": projectID, "combination_version": version})
	return nil
}
