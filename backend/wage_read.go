package main

import (
	"database/sql"
	"net/http"
	"net/url"
)

func workerWageFilter(q url.Values) (string, []any, error) {
	copy := url.Values{}
	for k, v := range q {
		copy[k] = v
	}
	copy.Del("worker_id")
	copy.Del("wage_paid")
	copy.Set("view", "wages")
	where, args, err := orderFilter(copy)
	if err != nil {
		return "", nil, err
	}
	if v := q.Get("worker_id"); v != "" {
		where += " AND ww.worker_id=?"
		args = append(args, v)
	}
	if v := q.Get("wage_paid"); v != "" {
		where += " AND ww.wage_paid=?"
		args = append(args, v == "1")
	}
	return where, args, nil
}

func workerWageOrderFilter(q url.Values) (string, []any, error) {
	copy := url.Values{}
	for k, v := range q {
		copy[k] = v
	}
	copy.Del("wage_paid")
	copy.Set("view", "wages")
	return orderFilter(copy)
}

func (a *App) listWorkerWages(w http.ResponseWriter, r *http.Request, u User) error {
	where, args, err := workerWageFilter(r.URL.Query())
	if err != nil {
		return err
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
	scope, scopeArgs, err := workerWageOrderFilter(r.URL.Query())
	if err != nil {
		return err
	}
	if err = validateWorkerAccountScope(tx, scope, scopeArgs); err != nil {
		return err
	}
	var count, total, wagePendingCount int64
	if err = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(IF(ww.wage_configured=1,ww.wage_cents,0)),0),COALESCE(SUM(IF(ww.wage_configured=0,1,0)),0) FROM "+workerWageSource+" ww JOIN orders o ON o.id=ww.order_id WHERE "+where, args...).Scan(&count, &total, &wagePendingCount); err != nil {
		return err
	}
	items, err := readOrderListMode(tx, where, args, page, u, true)
	if err != nil {
		return err
	}
	writeOrderList(w, items, count, 0, total, wagePendingCount, page, u)
	return nil
}

// Missing old accounts are safe only when their original header owns all rows.
// A partially restored mixed-worker record must fail visibly, never misattribute
// its whole wage to the default/material worker.
func validateWorkerAccounts(tx *sql.Tx) error {
	return validateWorkerAccountScope(tx, "1=1", nil)
}

func validateOrderWorkerAccount(tx *sql.Tx, orderID string) error {
	return validateWorkerAccountScope(tx, "o.id=?", []any{orderID})
}

// Correlated checks must use the existing order_id indexes even immediately
// after a large restore, before InnoDB has refreshed its index statistics.
// Otherwise MySQL can freeze a per-order full-scan plan and make validation
// quadratic. Legacy accounts retain their saved amount when line totals differ.
const invalidWorkerAccountsSQL = `(
 (NOT EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id)
  AND EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND l.worker_id<>'' AND l.worker_id<>o.worker_id))
 OR (EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id) AND (
  o.wage_paid<>EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.wage_paid=1)
  OR o.wage_cents<>(SELECT COALESCE(SUM(w.wage_cents),0) FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id)
  OR o.delivery_wage_cents<>(SELECT COALESCE(SUM(w.delivery_wage_cents),0) FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id)
  OR (EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.legacy=1) AND (
   (SELECT COUNT(*) FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id)<>1
   OR EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.worker_id<>o.worker_id)
   OR EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND l.worker_id<>'' AND l.worker_id<>o.worker_id)))
  OR (NOT EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.legacy=1) AND (
   EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND NOT EXISTS(
    SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.worker_id=COALESCE(NULLIF(l.worker_id,''),o.worker_id)))
   OR ((o.delivery_wage_cents>0 OR EXISTS(SELECT 1 FROM order_sales s WHERE s.order_id=o.id))
    AND NOT EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND w.worker_id=o.worker_id))
   OR EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id AND (
    w.wage_cents<>(SELECT COALESCE(SUM(l.labor_cents),0) FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND COALESCE(NULLIF(l.worker_id,''),o.worker_id)=w.worker_id)+IF(w.worker_id=o.worker_id,o.delivery_wage_cents,0)
    OR w.delivery_wage_cents<>IF(w.worker_id=o.worker_id,o.delivery_wage_cents,0)
    OR w.wage_configured<>(NOT EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND COALESCE(NULLIF(l.worker_id,''),o.worker_id)=w.worker_id AND l.wage_configured=0 AND l.wage_override_cents IS NULL))
    OR (NOT EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND COALESCE(NULLIF(l.worker_id,''),o.worker_id)=w.worker_id)
     AND NOT(w.worker_id=o.worker_id AND (o.delivery_wage_cents>0 OR EXISTS(SELECT 1 FROM order_sales s WHERE s.order_id=o.id))))))
  ))
 )))`

type workerAccountReader interface {
	QueryRow(string, ...any) *sql.Row
}

func validateWorkerAccountScope(tx workerAccountReader, where string, args []any) error {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM orders o WHERE ("+where+") AND "+invalidWorkerAccountsSQL, args...).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fail(422, "施工记录的分人工资归属不完整，请先核对恢复数据")
	}
	return nil
}
