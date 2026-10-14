package main

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func payrollMySQLExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func payrollMySQLCall(f orderLocationsFixture, key string, payload payrollActionRequest) (any, error) {
	return f.call(f.u, "/api/payroll-action", "payroll-"+key, payload)
}

func payrollMySQLFailureCode(err error) int {
	var f failure
	if errors.As(err, &f) {
		return f.Code
	}
	return 0
}

func payrollMySQLRequest(t *testing.T, f priceMySQLFixture, actor priceTestActor, key string, payload payrollActionRequest, status int) map[string]any {
	t.Helper()
	w := f.request(t, actor, "POST", "/api/payroll-action", key, payload, status)
	if status != 200 {
		return nil
	}
	return priceTestJSON(t, w)
}

func payrollMySQLTablesUnchanged(t *testing.T, db *sql.DB, before map[string][]string, tables ...string) {
	t.Helper()
	after := orderLocationsState(t, db)
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], after[table]) {
			t.Fatalf("payroll action changed unrelated ledger table %s", table)
		}
	}
}

func payrollMySQLAssertCount(t *testing.T, db *sql.DB, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatal("read payroll assertion count", err)
	}
	if got != want {
		t.Fatalf("payroll assertion count = %d, want %d", got, want)
	}
}

func TestPayrollSettlementProjectSnapshotSurvivesLaterOrderAssociationMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	orders := []struct {
		id        string
		projectID string
	}{}
	names := []string{"合成未关联工资甲", "合成未关联工资乙"}
	for i, address := range []string{"未关联结算工地甲", "未关联结算工地乙"} {
		created := f.must(t, Order{
			Company: "loc-co", Address: address, Date: "2026-10-12", Worker: "loc-a",
			Lines: []Line{{Temporary: true, Category: "开工", Name: names[i], Mode: "金额", Wage: 70000}},
		})
		orderID := created["id"].(string)
		var projectID string
		if err := f.a.db.QueryRow("SELECT project_id FROM orders WHERE id=?", orderID).Scan(&projectID); err != nil || projectID == "" {
			t.Fatalf("read original project for order %s: project=%q err=%v", orderID, projectID, err)
		}
		orders = append(orders, struct {
			id        string
			projectID string
		}{orderID, projectID})
	}
	if _, err := f.a.db.Exec("UPDATE orders SET project_id='' WHERE id IN (?,?)", orders[0].id, orders[1].id); err != nil {
		t.Fatal("make wage sources initially unlinked", err)
	}

	wageRows, err := f.a.db.Query("SELECT order_id,id,version FROM order_worker_wages WHERE order_id IN (?,?) AND worker_id='loc-a' ORDER BY order_id", orders[0].id, orders[1].id)
	if err != nil {
		t.Fatal("read wage sources", err)
	}
	wageIDs := []string{}
	wageVersions := []payrollSourceVersion{}
	wageIDByOrder := map[string]string{}
	for wageRows.Next() {
		var orderID string
		var wageID string
		var version int64
		if err = wageRows.Scan(&orderID, &wageID, &version); err != nil {
			wageRows.Close()
			t.Fatal("scan wage source", err)
		}
		wageIDByOrder[orderID] = wageID
		wageIDs = append(wageIDs, wageID)
		wageVersions = append(wageVersions, payrollSourceVersion{ID: wageID, Version: version})
	}
	if err = wageRows.Err(); err != nil {
		wageRows.Close()
		t.Fatal("iterate wage sources", err)
	}
	wageRows.Close()
	if len(wageIDs) != 2 {
		t.Fatalf("got %d wage sources, want 2", len(wageIDs))
	}

	settled, err := payrollMySQLCall(f, "snapshot-settlement", payrollActionRequest{
		Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12", WageIDs: wageIDs, WageVersions: wageVersions,
	})
	if err != nil {
		t.Fatal("settle initially unlinked wages", err)
	}
	settlementID := settled.(map[string]any)["id"].(string)
	var emptySnapshots, nullSnapshots int
	if err = f.a.db.QueryRow("SELECT SUM(project_id=''),SUM(project_id IS NULL) FROM payroll_settlement_sources WHERE settlement_id=?", settlementID).Scan(&emptySnapshots, &nullSnapshots); err != nil {
		t.Fatal("read settlement project snapshots", err)
	}
	if emptySnapshots != 2 || nullSnapshots != 0 {
		t.Fatalf("new settlement snapshots: empty=%d null=%d, want 2 and 0", emptySnapshots, nullSnapshots)
	}
	if _, err = payrollMySQLCall(f, "snapshot-payment", payrollActionRequest{
		Action: "create-payment", SettlementID: settlementID, SettlementVersion: 1, AmountCents: 10000, PaidDate: "2026-10-12",
	}); err != nil {
		t.Fatal("record settlement payment", err)
	}
	var currentOrderVersion int64
	if err = f.a.db.QueryRow("SELECT version FROM orders WHERE id=?", orders[0].id).Scan(&currentOrderVersion); err != nil {
		t.Fatal("read paid order version before association", err)
	}
	if _, err = f.call(f.u, "/api/project-associate", "associate-after-settlement-one", map[string]any{
		"order_id": orders[0].id, "project_id": orders[0].projectID, "version": currentOrderVersion,
	}); err != nil {
		t.Fatal("associate previously unlinked order through the business API", err)
	}

	tx, err := f.a.db.Begin()
	if err != nil {
		t.Fatal("begin settlement project-filter read", err)
	}
	rows, total, err := readPayrollSettlements(tx, httptest.NewRequest("GET", "/api/payroll?tab=settlements", nil), []any{"loc-a", "", "", orders[0].projectID}, 1)
	if err != nil {
		tx.Rollback()
		t.Fatal("filter settlements by newly associated project", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal("close settlement project-filter read", err)
	}
	settlementRows, ok := rows.([]payrollSettlementRow)
	if !ok || total != 0 || len(settlementRows) != 0 {
		t.Fatalf("new project unexpectedly includes old unlinked settlement: total=%d rows=%#v", total, rows)
	}

	where, args := payrollHistoryQuery("loc-a", "", "", orders[0].projectID)
	var historyCount int
	if err = f.a.db.QueryRow("SELECT COUNT(*) FROM payroll_history h WHERE "+where, args...).Scan(&historyCount); err != nil {
		t.Fatal("filter settlement/payment history by newly associated project", err)
	}
	if historyCount != 0 {
		t.Fatalf("new project unexpectedly includes %d historical settlement/payment events", historyCount)
	}

	if err = f.a.db.QueryRow("SELECT version FROM orders WHERE id=?", orders[1].id).Scan(&currentOrderVersion); err != nil {
		t.Fatal("read second paid order version before association", err)
	}
	if _, err = f.call(f.u, "/api/project-associate", "associate-after-settlement-two", map[string]any{
		"order_id": orders[1].id, "project_id": orders[1].projectID, "version": currentOrderVersion,
	}); err != nil {
		t.Fatal("associate second previously unlinked order through the business API", err)
	}
	if _, err = f.a.db.Exec("UPDATE payroll_settlement_sources SET project_id=NULL WHERE settlement_id=? AND source_id=?", settlementID, wageIDByOrder[orders[0].id]); err != nil {
		t.Fatal("simulate an unbackfilled legacy snapshot", err)
	}
	if err = backfillPayrollSettlementProjectSnapshots(f.a.db); err != nil {
		t.Fatal("backfill null settlement project snapshots", err)
	}
	var firstSnapshot, secondSnapshot string
	if err = f.a.db.QueryRow("SELECT project_id FROM payroll_settlement_sources WHERE settlement_id=? AND source_id=?", settlementID, wageIDByOrder[orders[0].id]).Scan(&firstSnapshot); err != nil {
		t.Fatal("read backfilled legacy snapshot", err)
	}
	if err = f.a.db.QueryRow("SELECT project_id FROM payroll_settlement_sources WHERE settlement_id=? AND source_id=?", settlementID, wageIDByOrder[orders[1].id]).Scan(&secondSnapshot); err != nil {
		t.Fatal("read preserved unlinked snapshot", err)
	}
	if firstSnapshot != orders[0].projectID || secondSnapshot != "" {
		t.Fatalf("NULL-only backfill snapshots = %q, %q; want %q and empty string", firstSnapshot, secondSnapshot, orders[0].projectID)
	}
}

func TestPayrollLegacyBackfillUsesLatestPayByTimestampMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	created := f.must(t, Order{
		Company: "loc-co", Address: "工资迁移回填工地", Date: "2026-10-12", Worker: "loc-a",
		Lines: []Line{{Temporary: true, Category: "开工", Name: "合成回填工资项", Mode: "金额", Wage: 70000}},
	})
	orderID := created["id"].(string)
	var wageID string
	if err := f.a.db.QueryRow("SELECT id FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", orderID).Scan(&wageID); err != nil {
		t.Fatal("read legacy wage source", err)
	}
	payrollMySQLExec(t, f.a.db, "UPDATE order_worker_wages SET wage_paid=1 WHERE id=?", wageID)
	payrollMySQLExec(t, f.a.db, `INSERT INTO wage_settlements(wage_id,order_id,worker_id,worker_name,kind,amount_cents,actor_id,operation_id,created_at)
	 VALUES(?,?,?,'合成甲师傅','pay',?,'legacy-later','backfill-later-pay-first','2026-10-11 09:00:00')`, wageID, orderID, "loc-a", int64(70000))
	payrollMySQLExec(t, f.a.db, `INSERT INTO wage_settlements(wage_id,order_id,worker_id,worker_name,kind,amount_cents,actor_id,operation_id,created_at)
	 VALUES(?,?,?,'合成甲师傅','pay',?,'legacy-older','backfill-older-pay-second','2026-10-02 09:00:00')`, wageID, orderID, "loc-a", int64(50000))
	for i := 0; i < 2; i++ {
		if err := migratePayroll(f.a.db); err != nil {
			t.Fatal("rerun payroll migration", i, err)
		}
	}
	var settlementID string
	if err := f.a.db.QueryRow("SELECT settlement_id FROM payroll_settlement_sources WHERE source_type='wage' AND source_id=?", wageID).Scan(&settlementID); err != nil {
		t.Fatal("read migrated settlement source", err)
	}
	var amount int64
	var paidDate, actorID string
	if err := f.a.db.QueryRow(`SELECT amount_cents,DATE_FORMAT(paid_date,'%Y-%m-%d'),actor_id FROM payroll_payments
	 WHERE settlement_id=?`, settlementID).Scan(&amount, &paidDate, &actorID); err != nil {
		t.Fatal("read migrated legacy payment", err)
	}
	if amount != 70000 || paidDate != "2026-10-11" || actorID != "legacy-later" {
		t.Fatalf("migrated payment = amount %d, date %s, actor %s; want latest pay 70000, 2026-10-11, legacy-later", amount, paidDate, actorID)
	}
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlements WHERE id=?", 1, settlementID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_payments WHERE settlement_id=?", 1, settlementID)
}

func TestPayrollEndToEndLifecycleAndTicketHistoryMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=446", 1)

	noWageAdvance := payrollMySQLRequest(t, f, f.admin, "payroll-advance-before-wage", payrollActionRequest{
		Action: "create-advance", WorkerID: "loc-b", AmountCents: 20000, AdvanceDate: "2026-10-12", Note: "合成无施工工资预支",
	}, 200)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='loc-b'", 0)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_advances WHERE id=? AND amount_cents=20000", 1, noWageAdvance["id"])
	adjustment := payrollMySQLRequest(t, f, f.admin, "payroll-adjustment-create", payrollActionRequest{
		Action: "create-adjustment", WorkerID: "loc-a", AmountCents: 1000, EffectiveDate: "2026-10-12", Reason: "合成补差更正",
	}, 200)
	adjustmentID := adjustment["id"].(string)
	reversal := payrollMySQLRequest(t, f, f.admin, "payroll-adjustment-reverse", payrollActionRequest{
		Action: "reverse-adjustment", ID: adjustmentID, Version: 1, Reason: "合成补差反向更正",
	}, 200)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_adjustments WHERE id=? AND amount_cents=1000 AND reversal_of IS NULL", 1, adjustmentID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_adjustments WHERE id=? AND amount_cents=-1000 AND reversal_of=?", 1, reversal["id"], adjustmentID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_history WHERE entity_type='adjustment' AND entity_id=? AND event IN ('create-adjustment','reverse-adjustment')", 2, adjustmentID)

	order := Order{Company: "loc-co", Address: "工资端到端合成工地", Date: "2026-10-12", Worker: "loc-a", Lines: []Line{{
		Temporary: true, Category: "开工", Name: "合成工资1000元", Mode: "金额", Price: 100000, Wage: 100000, Worker: "loc-a",
		Segments: []Segment{{Mode: "金额", Amount: 100000}},
	}}}
	orderID := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", order, 200))["id"].(string)
	detail := orderLocationsRead(t, f.a, orderID, f.u)
	advance := payrollMySQLRequest(t, f, f.admin, "payroll-advance-before-settlement", payrollActionRequest{
		Action: "create-advance", WorkerID: "loc-a", AmountCents: 20000, AdvanceDate: "2026-10-12", ProjectID: detail.ProjectID, Note: "合成工资预支",
	}, 200)
	advanceID := advance["id"].(string)
	var wageID string
	var wageVersion, wageAmount int64
	if err := f.a.db.QueryRow("SELECT id,version,wage_cents FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", orderID).Scan(&wageID, &wageVersion, &wageAmount); err != nil {
		t.Fatal("read saved wage source", err)
	}
	if wageAmount != 100000 {
		t.Fatalf("saved wage = %d cents, want 100000", wageAmount)
	}
	deduction := payrollMySQLRequest(t, f, f.admin, "payroll-deduction-create", payrollActionRequest{
		Action: "create-deduction", WorkerID: "loc-a", AmountCents: 5000, DeductionDate: "2026-10-12", ProjectID: detail.ProjectID, Reason: "合成确认扣款",
	}, 200)
	deductionID := deduction["id"].(string)
	payrollMySQLRequest(t, f, f.admin, "payroll-deduction-confirm", payrollActionRequest{
		Action: "confirm-deduction", ID: deductionID, Version: 1, LegalBasis: "合成双方书面约定依据",
	}, 200)
	batch := ticketIssueMySQLCreate(t, f, "payroll-ticket-before-payment", ticketIssueMySQLSelection(t, f, detail.Lines[0].ID))

	beforeOtherLedgers := orderLocationsState(t, f.a.db)
	settlementRequest := payrollActionRequest{
		Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12",
		WageIDs: []string{wageID}, WageVersions: []payrollSourceVersion{{ID: wageID, Version: wageVersion}},
		DeductionIDs: []string{deductionID}, DeductionVersions: []payrollSourceVersion{{ID: deductionID, Version: 2}},
		AdvanceAllocations: []payrollAdvanceAllocation{{ID: advanceID, Version: 1, AmountCents: 20000}},
	}
	settlement := payrollMySQLRequest(t, f, f.admin, "payroll-settlement-once", settlementRequest, 200)
	settlementID := settlement["id"].(string)
	if settlement["payable_cents"] != float64(75000) {
		t.Fatalf("settlement payable = %v cents, want 75000", settlement["payable_cents"])
	}
	replaySettlement := payrollMySQLRequest(t, f, f.admin, "payroll-settlement-once", settlementRequest, 200)
	if replaySettlement["id"] != settlementID {
		t.Fatal("same-key settlement replay created a different settlement", replaySettlement)
	}
	f.request(t, f.admin, "POST", "/api/payroll-action", "payroll-settlement-new-key", settlementRequest, 409)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlements WHERE worker_id='loc-a' AND legacy=0", 1)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlement_sources WHERE settlement_id=? AND source_type='advance' AND source_id=? AND amount_cents=20000", 1, settlementID, advanceID)

	firstPaymentRequest := payrollActionRequest{Action: "create-payment", SettlementID: settlementID, SettlementVersion: 1, AmountCents: 50000, PaidDate: "2026-10-12", Method: "合成转账"}
	firstPayment := payrollMySQLRequest(t, f, f.admin, "payroll-payment-first", firstPaymentRequest, 200)
	if firstPayment["paid_cents"] != float64(50000) || firstPayment["remaining_cents"] != float64(25000) {
		t.Fatalf("after 500 yuan payment paid/remaining = %v/%v, want 50000/25000 cents", firstPayment["paid_cents"], firstPayment["remaining_cents"])
	}
	replayPayment := payrollMySQLRequest(t, f, f.admin, "payroll-payment-first", firstPaymentRequest, 200)
	if replayPayment["id"] != firstPayment["id"] {
		t.Fatal("same-key payment replay created a duplicate payment", replayPayment)
	}
	secondPayment := payrollMySQLRequest(t, f, f.admin, "payroll-payment-second", payrollActionRequest{
		Action: "create-payment", SettlementID: settlementID, SettlementVersion: 2, AmountCents: 25000, PaidDate: "2026-10-12", Method: "合成转账",
	}, 200)
	if secondPayment["paid_cents"] != float64(75000) || secondPayment["remaining_cents"] != float64(0) {
		t.Fatalf("final payment balance = %v/%v, want 75000/0 cents", secondPayment["paid_cents"], secondPayment["remaining_cents"])
	}
	f.request(t, f.admin, "POST", "/api/payroll-action", "payroll-payment-overpay", payrollActionRequest{
		Action: "create-payment", SettlementID: settlementID, SettlementVersion: 3, AmountCents: 1, PaidDate: "2026-10-12", Method: "合成转账",
	}, 409)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_payments WHERE settlement_id=? AND status='active'", 2, settlementID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=? AND status='active'", 75000, settlementID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlement_sources WHERE settlement_id=? AND source_type='advance'", 1, settlementID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT amount_cents-COALESCE((SELECT SUM(src.amount_cents) FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND src.source_id=a.id AND s.status='confirmed'),0) FROM payroll_advances a WHERE a.id=?", 0, advanceID)
	payrollMySQLTablesUnchanged(t, f.a.db, beforeOtherLedgers,
		"order_lines", "order_materials", "order_sales", "stock_balances", "stock_movements", "settlements", "wage_settlements",
		"project_tickets", "project_ticket_items", "project_ticket_receipts", "company_receipts", "company_receipt_versions", "customer_discounts", "customer_discount_versions")

	secondOrder := Order{Company: "loc-co", Project: detail.ProjectID, Address: "工资端到端合成工地", Date: "2026-10-12", Worker: "loc-a", Lines: []Line{{Temporary: true, Category: "开工", Name: "合成第二笔工资", Mode: "金额", Price: 10000, Wage: 10000, Worker: "loc-a"}}}
	secondOrderID := f.orderLocationsFixture.must(t, secondOrder)["id"].(string)
	var secondWageID string
	var secondWageVersion int64
	if err := f.a.db.QueryRow("SELECT id,version FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", secondOrderID).Scan(&secondWageID, &secondWageVersion); err != nil {
		t.Fatal("read second wage source", err)
	}
	f.request(t, f.admin, "POST", "/api/payroll-action", "payroll-advance-reuse", payrollActionRequest{
		Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12",
		WageIDs: []string{secondWageID}, WageVersions: []payrollSourceVersion{{ID: secondWageID, Version: secondWageVersion}},
		AdvanceAllocations: []payrollAdvanceAllocation{{ID: advanceID, Version: 2, AmountCents: 20000}},
	}, 409)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlements WHERE worker_id='loc-a' AND legacy=0", 1)

	var orderTotal, orderWage int64
	var orderPaid bool
	if err := f.a.db.QueryRow("SELECT total_cents,wage_cents,wage_paid FROM orders WHERE id=?", orderID).Scan(&orderTotal, &orderWage, &orderPaid); err != nil || orderTotal != 100000 || orderWage != 100000 || !orderPaid {
		t.Fatalf("payroll changed the order charge/wage facts or failed to mark payment: total=%d wage=%d paid=%v err=%v", orderTotal, orderWage, orderPaid, err)
	}
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE order_id=? AND wage_cents=100000 AND wage_paid=1", 1, orderID)

	payrollBeforeTicketVoid := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "POST", "/api/ticket-batch-action", "", ticketBatchActionRequest{ID: batch.Batch.ID, Version: batch.Batch.Version, Action: "void", Reason: "合成工资已发后作废交票"}, 200)
	detailAfterVoid := orderLocationsRead(t, f.a, orderID, f.u)
	edit := orderLocationsEdit(t, detailAfterVoid)
	edit.Note = "工资已发后尝试修改"
	_, err := f.orderLocationsFixture.call(f.u, "/api/order", "payroll-edit-after-ticket-void", edit)
	if payrollMySQLFailureCode(err) != 409 {
		t.Fatalf("paid wage order edit after ticket void = %v, want 409", err)
	}
	edit = orderLocationsEdit(t, detailAfterVoid)
	edit.Delete = true
	_, err = f.orderLocationsFixture.call(f.u, "/api/order", "payroll-cancel-after-ticket-void", edit)
	if payrollMySQLFailureCode(err) != 409 {
		t.Fatalf("paid wage order cancellation after ticket void = %v, want 409", err)
	}
	reissued := ticketIssueMySQLCreate(t, f, "payroll-ticket-reissue", ticketIssueMySQLSelection(t, f, detailAfterVoid.Lines[0].ID))
	if reissued.Batch.ID == batch.Batch.ID {
		t.Fatal("ticket reissue reused the voided batch")
	}
	payrollMySQLTablesUnchanged(t, f.a.db, payrollBeforeTicketVoid,
		"orders", "order_worker_wages", "payroll_adjustments", "payroll_deductions", "payroll_advances", "payroll_settlements",
		"payroll_settlement_sources", "payroll_payments", "payroll_history")
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_payments WHERE settlement_id=? AND status='active'", 2, settlementID)
}

func TestPayrollPermissionsAndRevokedReplayMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	payload := payrollActionRequest{Action: "create-advance", WorkerID: "loc-a", AmountCents: 1000, AdvanceDate: "2026-10-12", Note: "合成权限测试"}
	noWages := f.employee(t, []string{"orders"})
	f.request(t, noWages, "GET", "/api/payroll?tab=workers", "", nil, 403)
	f.request(t, noWages, "GET", "/api/payroll-workers", "", nil, 403)
	f.request(t, noWages, "GET", "/api/payroll-history?worker_id=loc-a", "", nil, 403)
	f.request(t, noWages, "POST", "/api/payroll-action", "payroll-no-wages", payload, 403)

	wages := f.employee(t, []string{"wages"})
	key := "payroll-revoke-after-success"
	created := payrollMySQLRequest(t, f, wages, key, payload, 200)
	if created["id"] == nil {
		t.Fatal("authorized advance was not created", created)
	}
	wages = f.permissions(t, wages, []string{"orders"})
	f.request(t, wages, "GET", "/api/payroll?tab=workers", "", nil, 403)
	f.request(t, wages, "GET", "/api/payroll-workers", "", nil, 403)
	f.request(t, wages, "POST", "/api/payroll-action", key, payload, 403)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_advances WHERE worker_id='loc-a' AND amount_cents=1000", 1)
}

func TestPayrollConcurrentSettlementAndPaymentLimitsMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	orderID := f.must(t, Order{Company: "loc-co", Address: "工资并发限制合成工地", Date: "2026-10-12", Worker: "loc-a", Lines: []Line{{
		Temporary: true, Category: "开工", Name: "合成并发工资", Mode: "金额", Price: 100000, Wage: 100000, Worker: "loc-a",
	}}})["id"].(string)
	var wageID string
	var wageVersion int64
	if err := f.a.db.QueryRow("SELECT id,version FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", orderID).Scan(&wageID, &wageVersion); err != nil {
		t.Fatal("read concurrent wage source", err)
	}
	settlementRequest := payrollActionRequest{Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12", WageIDs: []string{wageID}, WageVersions: []payrollSourceVersion{{ID: wageID, Version: wageVersion}}}
	type result struct {
		value any
		err   error
	}
	start := make(chan struct{})
	settlements := make(chan result, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"payroll-race-settle-a", "payroll-race-settle-b"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := payrollMySQLCall(f, key, settlementRequest)
			settlements <- result{value: value, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(settlements)
	success, conflict := 0, 0
	var settlementID string
	for result := range settlements {
		if result.err == nil {
			success++
			settlementID = result.value.(map[string]any)["id"].(string)
		} else if payrollMySQLFailureCode(result.err) == 409 {
			conflict++
		} else {
			t.Fatalf("concurrent settlement returned unexpected error: %v", result.err)
		}
	}
	if success != 1 || conflict != 1 || settlementID == "" {
		t.Fatalf("concurrent settlement results: success=%d conflict=%d", success, conflict)
	}
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_settlements WHERE worker_id='loc-a' AND legacy=0", 1)

	start = make(chan struct{})
	payments := make(chan result, 2)
	for _, key := range []string{"payroll-race-pay-a", "payroll-race-pay-b"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := payrollMySQLCall(f, key, payrollActionRequest{Action: "create-payment", SettlementID: settlementID, SettlementVersion: 1, AmountCents: 60000, PaidDate: "2026-10-12", Method: "合成转账"})
			payments <- result{value: value, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(payments)
	success, conflict = 0, 0
	for result := range payments {
		if result.err == nil {
			success++
		} else if payrollMySQLFailureCode(result.err) == 409 {
			conflict++
		} else {
			t.Fatalf("concurrent payment returned unexpected error: %v", result.err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent payment results: success=%d conflict=%d", success, conflict)
	}
	payrollMySQLAssertCount(t, f.a.db, "SELECT COUNT(*) FROM payroll_payments WHERE settlement_id=? AND status='active'", 1, settlementID)
	payrollMySQLAssertCount(t, f.a.db, "SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=? AND status='active'", 60000, settlementID)
	if _, err := payrollMySQLCall(f, "payroll-race-pay-rest", payrollActionRequest{Action: "create-payment", SettlementID: settlementID, SettlementVersion: 2, AmountCents: 40000, PaidDate: "2026-10-12", Method: "合成转账"}); err != nil {
		t.Fatal("pay remaining balance after concurrent payment", err)
	}
	payrollMySQLAssertCount(t, f.a.db, "SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=? AND status='active'", 100000, settlementID)
}

func TestPayrollSettlementLocksOriginalWorkAndAllowsZeroPayableMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	created := f.must(t, Order{
		Company: "loc-co", Address: "工资结算保护工地", Date: "2026-10-12", Worker: "loc-a",
		Lines: []Line{{Temporary: true, Category: "开工", Name: "合成工资项", Mode: "金额", Wage: 50000}},
	})
	orderID := created["id"].(string)
	var wageID string
	var wageVersion int64
	if err := f.a.db.QueryRow("SELECT id,version FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", orderID).Scan(&wageID, &wageVersion); err != nil {
		t.Fatal("read saved wage source", err)
	}
	settled, err := payrollMySQLCall(f, "settle-source", payrollActionRequest{
		Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12",
		WageIDs: []string{wageID}, WageVersions: []payrollSourceVersion{{ID: wageID, Version: wageVersion}},
	})
	if err != nil {
		t.Fatal("confirm wage settlement", err)
	}
	settlementID := settled.(map[string]any)["id"].(string)

	detail := orderLocationsRead(t, f.a, orderID, f.u)
	edit := orderLocationsEdit(t, detail)
	edit.Note = "结算后尝试修改"
	if _, err = f.call(f.u, "/api/order", "payroll-edit-block", edit); payrollMySQLFailureCode(err) != 409 {
		t.Fatalf("active settlement allowed wage source edit: %v", err)
	}
	edit = orderLocationsEdit(t, detail)
	edit.Delete = true
	if _, err = f.call(f.u, "/api/order", "payroll-cancel-block", edit); payrollMySQLFailureCode(err) != 409 {
		t.Fatalf("active settlement allowed order cancellation: %v", err)
	}
	if _, err = payrollMySQLCall(f, "reverse-unpaid-settlement", payrollActionRequest{Action: "reverse-settlement", ID: settlementID, Version: 1, Reason: "合成测试撤销未付款结算"}); err != nil {
		t.Fatal("reverse unpaid settlement", err)
	}
	detail = orderLocationsRead(t, f.a, orderID, f.u)
	edit = orderLocationsEdit(t, detail)
	edit.Note = "撤销结算后允许修改"
	if _, err = f.call(f.u, "/api/order", "payroll-edit-after-reversal", edit); err != nil {
		t.Fatal("edit after reversing uncollected settlement", err)
	}
	if err = f.a.db.QueryRow("SELECT id,version FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", orderID).Scan(&wageID, &wageVersion); err != nil {
		t.Fatal("read updated wage source", err)
	}
	var projectID string
	if err = f.a.db.QueryRow("SELECT project_id FROM orders WHERE id=?", orderID).Scan(&projectID); err != nil {
		t.Fatal("read wage order project", err)
	}
	deduction, err := payrollMySQLCall(f, "create-zero-deduction", payrollActionRequest{
		Action: "create-deduction", WorkerID: "loc-a", AmountCents: 50000, DeductionDate: "2026-10-12", ProjectID: projectID, Reason: "合成全额扣款测试",
	})
	if err != nil {
		t.Fatal("create pending deduction", err)
	}
	deductionID := deduction.(map[string]any)["id"].(string)
	if _, err = payrollMySQLCall(f, "confirm-zero-deduction", payrollActionRequest{
		Action: "confirm-deduction", ID: deductionID, Version: 1, ProjectID: projectID, LegalBasis: "合成双方书面约定依据",
	}); err != nil {
		t.Fatal("confirm deduction with legal basis", err)
	}
	zero, err := payrollMySQLCall(f, "zero-payable-settlement", payrollActionRequest{
		Action: "create-settlement", WorkerID: "loc-a", SettlementDate: "2026-10-12",
		WageIDs: []string{wageID}, WageVersions: []payrollSourceVersion{{ID: wageID, Version: wageVersion}},
		DeductionIDs: []string{deductionID}, DeductionVersions: []payrollSourceVersion{{ID: deductionID, Version: 2}},
	})
	if err != nil {
		t.Fatal("create zero-payable settlement", err)
	}
	zeroResult := zero.(map[string]any)
	if zeroResult["payable_cents"] != int64(0) {
		t.Fatalf("zero settlement amount = %v", zeroResult["payable_cents"])
	}
	zeroID := zeroResult["id"].(string)
	if _, err = payrollMySQLCall(f, "zero-payment-rejected", payrollActionRequest{
		Action: "create-payment", SettlementID: zeroID, AmountCents: 0, PaidDate: "2026-10-12",
	}); payrollMySQLFailureCode(err) != 400 {
		t.Fatalf("zero-value payment accepted: %v", err)
	}
	var paid int64
	if err = f.a.db.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=?", zeroID).Scan(&paid); err != nil || paid != 0 {
		t.Fatalf("zero settlement created payment: paid=%d err=%v", paid, err)
	}
}
