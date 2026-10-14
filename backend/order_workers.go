package main

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

type workerSnapshot struct {
	Name   string
	Crafts []string
}

// Lock all participating workers in one stable order. Header warehouse ownership
// remains independent of the workers selected for individual construction rows.
func prepareLineWorkers(tx *sql.Tx, u User, o *Order, previous map[string]Line) error {
	ids := map[string]bool{}
	if o.Worker != "" {
		ids[o.Worker] = true
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		if old, ok := previous[l.ID]; ok && (!lineFillAllowed(u, old) || (old.ticketBatchID != "" && (l.Worker == "" || l.Worker == old.Worker))) {
			continue
		}
		if l.Worker == "" {
			if old, ok := previous[l.ID]; ok && old.Worker != "" {
				l.Worker = old.Worker
			} else {
				l.Worker = o.Worker
			}
		}
		ids[l.Worker] = true
	}
	keys := make([]string, 0, len(ids))
	for key := range ids {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	workers := map[string]workerSnapshot{}
	for _, key := range keys {
		var w workerSnapshot
		var raw string
		if key == "" {
			return fail(400, "请选择各施工小项的施工人")
		}
		if err := tx.QueryRow("SELECT name,crafts FROM workers WHERE id=? FOR SHARE", key).Scan(&w.Name, &raw); err != nil {
			if err == sql.ErrNoRows {
				return fail(400, "施工人不存在，请重新选择")
			}
			return err
		}
		if err := json.Unmarshal([]byte(raw), &w.Crafts); err != nil {
			return err
		}
		workers[key] = w
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		if old, ok := previous[l.ID]; ok && (!lineFillAllowed(u, old) || (old.ticketBatchID != "" && (l.Worker == "" || l.Worker == old.Worker))) {
			continue
		}
		w := workers[l.Worker]
		old, saved := previous[l.ID]
		if !contains(w.Crafts, l.Category) {
			return fail(403, "所选施工人未获准施工："+l.Category)
		}
		if saved && old.Worker == l.Worker && old.WorkerName != "" {
			l.WorkerName = old.WorkerName
		} else {
			l.WorkerName = w.Name
		}
	}
	return nil
}

type workerWageRecord struct {
	ID           string `json:"id"`
	OrderID      string `json:"order_id"`
	WorkerID     string `json:"worker_id"`
	WorkerName   string `json:"worker_name"`
	WageCents    int64  `json:"wage_cents"`
	WagePaid     bool   `json:"wage_paid"`
	Version      int64  `json:"version"`
	DeliveryWage int64  `json:"delivery_wage_cents"`
	Number       string `json:"number"`
	WorkDate     string `json:"work_date"`
}

// A single compatibility source is used by lists, exports, archives and detail.
// Fixtures and pre-feature restored records without accounts retain the exact
// original whole-order amount/state; no current prices or names are inferred.
const workerWageSource = `(SELECT id,order_id,worker_id,worker_name,wage_cents,wage_paid,version,delivery_wage_cents FROM order_worker_wages
	UNION ALL SELECT MD5(CONCAT('legacy-wage:',o.id)),o.id,o.worker_id,o.worker_name,o.wage_cents,o.wage_paid,o.version,o.delivery_wage_cents
	FROM orders o WHERE NOT EXISTS(SELECT 1 FROM order_worker_wages x WHERE x.order_id=o.id))`

func lockOrderWorkerPayroll(tx *sql.Tx, orderID string) (paid, settled bool, err error) {
	rows, err := tx.Query("SELECT id,wage_paid FROM order_worker_wages WHERE order_id=? ORDER BY worker_id,id FOR UPDATE", orderID)
	if err != nil {
		return false, false, err
	}
	wageIDs := []string{}
	for rows.Next() {
		var wageID string
		var wagePaid bool
		if err = rows.Scan(&wageID, &wagePaid); err != nil {
			rows.Close()
			return false, false, err
		}
		wageIDs = append(wageIDs, wageID)
		paid = paid || wagePaid
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return false, false, err
	}
	if err = rows.Close(); err != nil {
		return false, false, err
	}
	for _, wageID := range wageIDs {
		allocated, allocationErr := payrollSourceAllocated(tx, "wage", wageID)
		if allocationErr != nil {
			return false, false, allocationErr
		}
		if allocated {
			settled = true
		}
	}
	return paid, settled, nil
}

func readWorkerWages(tx *sql.Tx, orderID string) ([]workerWageRecord, error) {
	return readWorkerWagesFrom(tx, workerWageSource, orderID)
}

func readWorkerWagesFrom(tx *sql.Tx, source, orderID string) ([]workerWageRecord, error) {
	return scanRows[workerWageRecord](tx, "SELECT id,order_id,worker_id,worker_name,wage_cents,wage_paid,version,delivery_wage_cents FROM "+source+" ww WHERE order_id=? ORDER BY worker_id,id", []any{orderID}, func(r *sql.Rows, v *workerWageRecord) error {
		return r.Scan(&v.ID, &v.OrderID, &v.WorkerID, &v.WorkerName, &v.WageCents, &v.WagePaid, &v.Version, &v.DeliveryWage)
	})
}

func persistWorkerWages(tx *sql.Tx, o Order, headerName string) error {
	old, err := readWorkerWagesFrom(tx, "order_worker_wages", o.ID)
	if err != nil {
		return err
	}
	previous := map[string]workerWageRecord{}
	for _, v := range old {
		if v.WagePaid {
			return fail(409, "已有工人工资发放，请先撤销发放再修改")
		}
		previous[v.WorkerID] = v
	}
	current := map[string]workerWageRecord{}
	for _, l := range o.Lines {
		v := current[l.Worker]
		v.WorkerID, v.WorkerName = l.Worker, l.WorkerName
		v.WageCents += l.Labor
		current[l.Worker] = v
	}
	if len(o.Sales) > 0 || o.DeliveryWage > 0 {
		v := current[o.Worker]
		v.WorkerID = o.Worker
		if v.WorkerName == "" {
			v.WorkerName = headerName
		}
		v.WageCents += o.DeliveryWage
		v.DeliveryWage = o.DeliveryWage
		current[o.Worker] = v
	}
	keys := make([]string, 0, len(current))
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := current[key]
		if p, ok := previous[key]; ok {
			v.ID, v.Version = p.ID, p.Version+1
			if p.WorkerName != "" {
				v.WorkerName = p.WorkerName
			}
		} else {
			v.ID, v.Version = id(), 1
		}
		if _, err = tx.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version,legacy) VALUES(?,?,?,?,?,?,0,?,0) ON DUPLICATE KEY UPDATE worker_name=VALUES(worker_name),wage_cents=VALUES(wage_cents),delivery_wage_cents=VALUES(delivery_wage_cents),version=VALUES(version),legacy=0`, v.ID, o.ID, key, v.WorkerName, v.WageCents, v.DeliveryWage, v.Version); err != nil {
			return err
		}
	}
	for key, v := range previous {
		if _, ok := current[key]; !ok {
			if _, err = tx.Exec("DELETE FROM order_worker_wages WHERE id=?", v.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinWorkerNames(names []string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return strings.Join(out, "、")
}

const orderWorkerNamesSQL = `(SELECT JSON_ARRAYAGG(ww.worker_name) FROM ` + workerWageSource + ` ww WHERE ww.order_id=o.id)`

func decodeWorkerNames(raw []byte) (string, error) {
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return "", err
	}
	return joinWorkerNames(names), nil
}
