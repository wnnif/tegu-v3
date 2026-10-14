package main

import "database/sql"

func (a *App) settleWorkerWages(_ *sql.Tx, u User, _ []byte, _ string) (any, error) {
	if !u.can("wages") {
		return nil, fail(403, "无工资结算权限")
	}
	return nil, fail(409, "旧工资发放入口已停用，请在工资管理登记结算和实际付款")
}

func materializeLegacyWage(tx *sql.Tx, orderID string) error {
	_, err := tx.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version,legacy) SELECT MD5(CONCAT('legacy-wage:',o.id)),o.id,o.worker_id,o.worker_name,o.wage_cents,o.delivery_wage_cents,o.wage_paid,o.version,1 FROM orders o WHERE o.id=? AND NOT EXISTS(SELECT 1 FROM order_worker_wages w WHERE w.order_id=o.id)`, orderID)
	return err
}

func recordWorkerWageSettlement(tx *sql.Tx, u User, key, action, note string, x workerWageRecord) error {
	if _, err := tx.Exec("UPDATE order_worker_wages SET wage_paid=?,version=version+1 WHERE id=?", action == "pay", x.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO wage_settlements(wage_id,order_id,worker_id,worker_name,kind,amount_cents,actor_id,operation_id,note) VALUES(?,?,?,?,?,?,?,?,?)`, x.ID, x.OrderID, x.WorkerID, x.WorkerName, action, x.WageCents, u.ID, key, note); err != nil {
		return err
	}
	return audit(tx, u, action, x.OrderID, map[string]any{"wage_id": x.ID, "worker_id": x.WorkerID, "worker_name": x.WorkerName, "amount_cents": x.WageCents, "before_wage_paid": x.WagePaid, "after_wage_paid": action == "pay", "before_version": x.Version, "after_version": x.Version + 1, "note": note})
}

func settleLegacyWorkerWage(tx *sql.Tx, u User, key string, p SettlementRequest, orderID string) error {
	if err := validateOrderWorkerAccount(tx, orderID); err != nil {
		return err
	}
	if err := materializeLegacyWage(tx, orderID); err != nil {
		return err
	}
	values, err := readWorkerWages(tx, orderID)
	if err != nil {
		return err
	}
	if len(values) != 1 {
		return fail(400, "该施工记录有多位施工人，请在工资明细中分别发放")
	}
	x := values[0]
	if x.WorkerID == "" {
		return fail(400, "单据未分配施工人")
	}
	if x.WagePaid == (p.Action == "pay") {
		return fail(409, "工资发放状态已变化")
	}
	return recordWorkerWageSettlement(tx, u, key, p.Action, p.Note, x)
}
