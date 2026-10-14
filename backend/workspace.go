package main

import (
	"database/sql"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
)

func (a *App) changePassword(tx *sql.Tx, u User, b []byte) (any, error) {
	var p struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	if p.Password == "" || len(p.Password) > 72 {
		return nil, fail(400, "新密码不能为空且不能超过72字节")
	}
	var old string
	if e := tx.QueryRow("SELECT password_hash FROM users WHERE id=? FOR UPDATE", u.ID).Scan(&old); e != nil {
		return nil, e
	}
	if bcrypt.CompareHashAndPassword([]byte(old), []byte(p.Current)) != nil {
		return nil, fail(400, "原密码不正确")
	}
	encrypted, e := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE users SET password_hash=?,version=version+1 WHERE id=?", string(encrypted), u.ID); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", u.ID); e != nil {
		return nil, e
	}
	if e = audit(tx, u, "修改本人密码", u.ID, map[string]bool{"changed": true}); e != nil {
		return nil, e
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) deleteWarehouse(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("warehouse") {
		return nil, fail(403, "无仓库管理权限")
	}
	var p struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	v, e := readWarehouseForDelete(tx, p.ID)
	if e == sql.ErrNoRows {
		return nil, fail(409, "仓库已变化，请刷新")
	}
	if e != nil {
		return nil, e
	}
	if v.Version != p.Version {
		return nil, fail(409, "仓库已变化，请刷新")
	}
	var n int
	for _, query := range []string{"SELECT COUNT(*) FROM stock_movements WHERE warehouse_id=?", "SELECT COUNT(*) FROM stock_balances WHERE warehouse_id=?", "SELECT COUNT(*) FROM order_materials WHERE warehouse_id=?", "SELECT COUNT(*) FROM orders WHERE warehouse_id=?", "SELECT COUNT(*) FROM stock_documents WHERE source_id=? OR destination_id=?"} {
		args := []any{p.ID}
		if strings.Count(query, "?") == 2 {
			args = append(args, p.ID)
		}
		if e = tx.QueryRow(query, args...).Scan(&n); e != nil {
			return nil, e
		}
		if n > 0 {
			return nil, fail(400, "仓库已被单据使用，只能停用，不能删除")
		}
	}
	if _, e = tx.Exec("DELETE FROM warehouses WHERE id=?", p.ID); e != nil {
		return nil, e
	}
	if e = audit(tx, u, "删除未使用仓库", p.ID, v); e != nil {
		return nil, e
	}
	return map[string]bool{"ok": true}, nil
}

func dashboardOrderStatsSQL(u User) string {
	receivableSQL, receiptJoin := "0", ""
	if u.can("finance") && u.can("customer_prices") {
		receivableSQL, receiptJoin = orderOutstandingCentsSQL, orderReceiptTotalsJoinSQL
	}
	return "SELECT COUNT(*),COALESCE(SUM(o.status='进行中'),0),COALESCE(SUM(o.status='待交单'),0),COALESCE(SUM(" + receivableSQL + "),0) FROM orders o" + receiptJoin + " WHERE o.work_date>=? AND o.work_date<?"
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request, u User) error {
	from, to, e := monthRange(r.URL.Query().Get("month"))
	if e != nil {
		return e
	}
	type dashboardStats struct {
		Orders          *int64 `json:"orders,omitempty"`
		Ongoing         *int64 `json:"ongoing,omitempty"`
		Pending         *int64 `json:"pending,omitempty"`
		ReceivableCents *int64 `json:"receivable_cents,omitempty"`
		UnpaidWageCents *int64 `json:"unpaid_wage_cents,omitempty"`
		Warehouses      *int64 `json:"warehouses,omitempty"`
	}
	result := dashboardStats{}
	if u.can("orders") || u.can("finance") || u.can("wages") {
		var orders, ongoing, pending, receivable int64
		e := a.db.QueryRow(dashboardOrderStatsSQL(u), from, to).Scan(&orders, &ongoing, &pending, &receivable)
		if e != nil {
			return e
		}
		result.Orders, result.Ongoing, result.Pending = &orders, &ongoing, &pending
		if u.can("finance") && u.can("customer_prices") {
			var ticketOutstanding int64
			if e = a.db.QueryRow("SELECT COALESCE(SUM(t.total_cents-COALESCE(tr.received,0)-COALESCE(td.discount,0)),0)"+ticketFinanceFrom+"WHERE t.updated_at>=? AND t.updated_at<?", from, to).Scan(&ticketOutstanding); e != nil {
				return e
			}
			receivable += ticketOutstanding
			result.ReceivableCents = &receivable
		}
		if u.can("wages") {
			var unpaid int64
			if e = a.db.QueryRow("SELECT COALESCE(SUM(unpaid_cents),0) FROM ("+payrollWorkerSummarySQL+") payroll_summary").Scan(&unpaid); e != nil {
				return e
			}
			result.UnpaidWageCents = &unpaid
		}
	}
	if u.can("warehouse") {
		var n int64
		if e = a.db.QueryRow("SELECT COUNT(*) FROM warehouses WHERE active=1").Scan(&n); e != nil {
			return e
		}
		result.Warehouses = &n
	}
	write(w, 200, result)
	return nil
}
