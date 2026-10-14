package main

import (
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

func expenseMySQLCall(f priceMySQLFixture, key string, payload expenseActionRequest) (any, error) {
	return f.call(f.u, "/api/expense-action", "expense-"+key, payload)
}

func expenseMySQLMust(t *testing.T, f priceMySQLFixture, key string, payload expenseActionRequest) map[string]any {
	t.Helper()
	result, err := expenseMySQLCall(f, key, payload)
	if err != nil {
		t.Fatalf("expense action %s: %v", payload.Action, err)
	}
	return result.(map[string]any)
}

func expenseMySQLFail(t *testing.T, f priceMySQLFixture, key string, payload expenseActionRequest, code int) {
	t.Helper()
	_, err := expenseMySQLCall(f, key, payload)
	var failureValue failure
	if !errors.As(err, &failureValue) || failureValue.Code != code {
		t.Fatalf("expense action %s error = %v, want HTTP %d", payload.Action, err, code)
	}
}

func expenseID(t *testing.T, value any) string {
	t.Helper()
	id, ok := value.(string)
	if !ok || id == "" {
		t.Fatalf("expense result id = %#v, want non-empty string", value)
	}
	return id
}

func expenseInt(t *testing.T, value any) int64 {
	t.Helper()
	number, ok := value.(int64)
	if !ok {
		t.Fatalf("expense integer = %#v (%T), want int64", value, value)
	}
	return number
}

func expenseDBInt(t *testing.T, f priceMySQLFixture, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := f.a.db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func expenseListedRow(t *testing.T, rows any, id string) map[string]any {
	t.Helper()
	values, ok := rows.([]any)
	if !ok {
		t.Fatalf("expense list rows = %#v, want array", rows)
	}
	for _, value := range values {
		row, ok := value.(map[string]any)
		if ok && row["id"] == id {
			return row
		}
	}
	t.Fatalf("expense row %q not found in %#v", id, rows)
	return nil
}

func expenseHistoryListedRow(t *testing.T, rows any, entityID string) map[string]any {
	t.Helper()
	values, ok := rows.([]any)
	if !ok {
		t.Fatalf("expense history rows = %#v, want array", rows)
	}
	for _, value := range values {
		row, ok := value.(map[string]any)
		if ok && row["entity_id"] == entityID {
			return row
		}
	}
	t.Fatalf("expense history entity %q not found", entityID)
	return nil
}

func expenseSnapshotLedgers(t *testing.T, f priceMySQLFixture) map[string][]string {
	t.Helper()
	all := orderLocationsState(t, f.a.db)
	rows := map[string][]string{}
	for _, table := range []string{"wage_settlements", "order_worker_wages", "payroll_advances", "payroll_settlements", "payroll_payments", "payroll_history"} {
		rows[table] = all[table]
	}
	return rows
}

func TestExpenseLedgerExamplesPurchaseIsolationAndHistoryMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	payrollBefore := expenseSnapshotLedgers(t, f)

	claim := expenseMySQLMust(t, f, "claim-basic-create", expenseActionRequest{Action: "create-claim", PersonName: "合成张三", ExpenseDate: "2026-10-12", Category: "材料采购", AmountCents: 20000, Note: "合成先行垫付"})
	claimID := expenseID(t, claim["id"])
	expenseMySQLFail(t, f, "claim-before-confirm-payment", expenseActionRequest{Action: "pay-claim", ID: claimID, Version: 1, AmountCents: 20000, PaidDate: "2026-10-12"}, 409)
	confirmed := expenseMySQLMust(t, f, "claim-basic-confirm", expenseActionRequest{Action: "confirm-claim", ID: claimID, Version: 1})
	paid := expenseMySQLMust(t, f, "claim-basic-pay", expenseActionRequest{Action: "pay-claim", ID: claimID, Version: expenseInt(t, confirmed["version"]), AmountCents: 20000, PaidDate: "2026-10-12"})
	if expenseInt(t, paid["due_cents"]) != 0 || expenseInt(t, paid["paid_cents"]) != 20000 {
		t.Fatalf("200元实际付款余额错误: %#v", paid)
	}
	paidClaimList := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expenses?tab=claims&page=1", "", nil, http.StatusOK))
	if row := expenseListedRow(t, paidClaimList["rows"], claimID); row["status"] != "已付清" || row["paid_cents"] != float64(20000) || row["due_cents"] != float64(0) {
		t.Fatalf("200元垫付报销列表状态或现金余额错误: %#v", row)
	}
	paymentID := expenseID(t, paid["id"])
	reversed := expenseMySQLMust(t, f, "claim-basic-payment-reverse", expenseActionRequest{Action: "reverse-payment", PaymentID: paymentID, Version: 1, Reason: "合成测试纠正付款"})
	updated := expenseMySQLMust(t, f, "claim-basic-correct", expenseActionRequest{Action: "update-claim", ID: claimID, Version: expenseInt(t, reversed["version"]), PersonName: "合成张三", ExpenseDate: "2026-10-12", Category: "其他", AmountCents: 21000, Note: "合成更正后费用", Reason: "合成测试更正原始金额"})
	if expenseInt(t, updated["version"]) != 5 {
		t.Fatalf("更正后版本 = %#v, want 5", updated)
	}
	if expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_payments WHERE id=? AND status='reversed' AND reversal_reason<>''", paymentID) != 1 {
		t.Fatal("更正后没有保留原付款的反向历史")
	}

	advance := expenseMySQLMust(t, f, "advance-return-create", expenseActionRequest{Action: "create-advance", PersonName: "合成李四", AdvanceDate: "2026-10-12", AmountCents: 100000, Purpose: "合成采购备用金"})
	advanceID := expenseID(t, advance["id"])
	claim800 := expenseMySQLMust(t, f, "advance-return-claim", expenseActionRequest{Action: "create-claim", PersonName: "合成李四", ExpenseDate: "2026-10-12", Category: "材料采购", AmountCents: 80000})
	claim800ID := expenseID(t, claim800["id"])
	claim800Confirmed := expenseMySQLMust(t, f, "advance-return-confirm", expenseActionRequest{Action: "confirm-claim", ID: claim800ID, Version: 1})
	allocated800 := expenseMySQLMust(t, f, "advance-return-allocate", expenseActionRequest{Action: "allocate-advance", ClaimID: claim800ID, ClaimVersion: expenseInt(t, claim800Confirmed["version"]), AdvanceID: advanceID, AdvanceVersion: 1, AmountCents: 80000, AppliedDate: "2026-10-12"})
	if expenseInt(t, allocated800["due_cents"]) != 0 || expenseInt(t, allocated800["advance_remaining_cents"]) != 20000 {
		t.Fatalf("1000元预支核销800元后余额错误: %#v", allocated800)
	}
	returned := expenseMySQLMust(t, f, "advance-return-200", expenseActionRequest{Action: "return-advance", AdvanceID: advanceID, AdvanceVersion: expenseInt(t, allocated800["advance_version"]), AmountCents: 20000, ReturnedDate: "2026-10-12"})
	if expenseInt(t, returned["remaining_cents"]) != 0 || expenseInt(t, returned["version"]) != 3 {
		t.Fatalf("退回200元后预支未结清: %#v", returned)
	}
	if expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_payments WHERE claim_id=? AND status='active'", claim800ID) != 0 {
		t.Fatal("仅预支核销的800元错误生成了员工付款")
	}
	expenseMySQLFail(t, f, "advance-over-return", expenseActionRequest{Action: "return-advance", AdvanceID: advanceID, AdvanceVersion: expenseInt(t, returned["version"]), AmountCents: 1, ReturnedDate: "2026-10-12"}, 409)

	topupAdvance := expenseMySQLMust(t, f, "advance-topup-create", expenseActionRequest{Action: "create-advance", PersonName: "合成王五", AdvanceDate: "2026-10-12", AmountCents: 100000, Purpose: "合成材料采购"})
	topupClaim := expenseMySQLMust(t, f, "advance-topup-claim", expenseActionRequest{Action: "create-claim", PersonName: "合成王五", ExpenseDate: "2026-10-12", Category: "材料采购", AmountCents: 120000})
	topupClaimID := expenseID(t, topupClaim["id"])
	topupConfirmed := expenseMySQLMust(t, f, "advance-topup-confirm", expenseActionRequest{Action: "confirm-claim", ID: topupClaimID, Version: 1})
	topupAllocation := expenseMySQLMust(t, f, "advance-topup-allocate", expenseActionRequest{Action: "allocate-advance", ClaimID: topupClaimID, ClaimVersion: expenseInt(t, topupConfirmed["version"]), AdvanceID: expenseID(t, topupAdvance["id"]), AdvanceVersion: 1, AmountCents: 100000, AppliedDate: "2026-10-12"})
	topupPaid := expenseMySQLMust(t, f, "advance-topup-pay", expenseActionRequest{Action: "pay-claim", ID: topupClaimID, Version: expenseInt(t, topupAllocation["claim_version"]), AmountCents: 20000, PaidDate: "2026-10-12"})
	if expenseInt(t, topupPaid["due_cents"]) != 0 || expenseInt(t, topupPaid["paid_cents"]) != 20000 || topupPaid["status"] != "已付清" {
		t.Fatalf("补付200元后状态/余额错误: %#v", topupPaid)
	}
	if expenseInt(t, topupAllocation["advance_remaining_cents"]) != 0 {
		t.Fatalf("补付场景预支余额不为0: %#v", topupAllocation)
	}

	multiAdvance := expenseMySQLMust(t, f, "advance-multi-create", expenseActionRequest{Action: "create-advance", PersonName: "合成赵六", AdvanceDate: "2026-10-12", AmountCents: 100000, Purpose: "合成分批报账"})
	multiAdvanceID := expenseID(t, multiAdvance["id"])
	var multiClaimIDs []string
	var multiClaimVersions []int64
	for index, amount := range []int64{40000, 60000} {
		created := expenseMySQLMust(t, f, "advance-multi-claim-"+string(rune('a'+index)), expenseActionRequest{Action: "create-claim", PersonName: "合成赵六", ExpenseDate: "2026-10-12", Category: "其他", AmountCents: amount})
		id := expenseID(t, created["id"])
		multiClaimIDs = append(multiClaimIDs, id)
		confirmed := expenseMySQLMust(t, f, "advance-multi-confirm-"+string(rune('a'+index)), expenseActionRequest{Action: "confirm-claim", ID: id, Version: 1})
		multiClaimVersions = append(multiClaimVersions, expenseInt(t, confirmed["version"]))
	}
	firstAllocation := expenseMySQLMust(t, f, "advance-multi-allocate-a", expenseActionRequest{Action: "allocate-advance", ClaimID: multiClaimIDs[0], ClaimVersion: multiClaimVersions[0], AdvanceID: multiAdvanceID, AdvanceVersion: 1, AmountCents: 40000, AppliedDate: "2026-10-12"})
	secondAllocation := expenseMySQLMust(t, f, "advance-multi-allocate-b", expenseActionRequest{Action: "allocate-advance", ClaimID: multiClaimIDs[1], ClaimVersion: multiClaimVersions[1], AdvanceID: multiAdvanceID, AdvanceVersion: expenseInt(t, firstAllocation["advance_version"]), AmountCents: 60000, AppliedDate: "2026-10-12"})
	if expenseInt(t, secondAllocation["advance_remaining_cents"]) != 0 {
		t.Fatalf("一笔预支分次核销后余额错误: %#v", secondAllocation)
	}

	maintenanceFaultExec(t, f.a.db, `INSERT INTO stock_documents(id,kind,work_date,source_id,destination_id,note,created_by,total_cents,line_items,version,voided)
	 VALUES('expense-phase6-purchase','进货','2026-10-12','','loc-wh','合成关联进货','test',20000,JSON_ARRAY(),1,0)`)
	stockBefore := orderLocationsState(t, f.a.db)
	purchaseOptions := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expense-purchases?q=expense-phase6-purchase&page=1", "", nil, http.StatusOK))
	purchaseOption := expenseListedRow(t, purchaseOptions["rows"], "expense-phase6-purchase")
	if purchaseOption["available_cents"] != float64(20000) {
		t.Fatalf("unreserved purchase option balance = %#v, want 20000", purchaseOption)
	}
	linked := expenseMySQLMust(t, f, "purchase-linked-claim", expenseActionRequest{Action: "create-claim", PersonName: "合成采购人", ExpenseDate: "2026-10-12", Category: "材料采购", AmountCents: 20000, PurchaseDocumentID: "expense-phase6-purchase"})
	expenseMySQLFail(t, f, "purchase-link-over-reserve", expenseActionRequest{Action: "create-claim", PersonName: "合成另一采购人", ExpenseDate: "2026-10-12", Category: "材料采购", AmountCents: 1, PurchaseDocumentID: "expense-phase6-purchase"}, 409)
	linkedID := expenseID(t, linked["id"])
	linkedConfirmed := expenseMySQLMust(t, f, "purchase-linked-confirm", expenseActionRequest{Action: "confirm-claim", ID: linkedID, Version: 1})
	linkedPaid := expenseMySQLMust(t, f, "purchase-linked-pay", expenseActionRequest{Action: "pay-claim", ID: linkedID, Version: expenseInt(t, linkedConfirmed["version"]), AmountCents: 20000, PaidDate: "2026-10-12"})
	stockAfter := orderLocationsState(t, f.a.db)
	for _, table := range []string{"stock_balances", "stock_movements", "product_purchase_costs"} {
		if !reflect.DeepEqual(stockBefore[table], stockAfter[table]) {
			t.Fatalf("报销操作改动库存数据表 %s", table)
		}
	}
	if !reflect.DeepEqual(stockBefore["stock_documents"], stockAfter["stock_documents"]) {
		t.Fatal("报销操作改动进货单或其采购金额")
	}
	reservedOptions := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expense-purchases?q=expense-phase6-purchase&page=1", "", nil, http.StatusOK))
	reservedPurchase := expenseListedRow(t, reservedOptions["rows"], "expense-phase6-purchase")
	if reservedPurchase["available_cents"] != float64(0) {
		t.Fatalf("fully linked purchase remaining option balance = %#v, want 0", reservedPurchase)
	}
	claimList := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expenses?tab=claims&page=1", "", nil, http.StatusOK))
	if row := expenseListedRow(t, claimList["rows"], claim800ID); row["status"] != "预支已核销" || row["paid_cents"] != float64(0) || row["due_cents"] != float64(0) {
		t.Fatalf("800元预支核销报销列表状态或现金余额错误: %#v", row)
	}
	if row := expenseListedRow(t, claimList["rows"], topupClaimID); row["status"] != "已付清" || row["paid_cents"] != float64(20000) || row["due_cents"] != float64(0) {
		t.Fatalf("预支加补付款报销列表状态或余额错误: %#v", row)
	}
	advanceList := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expenses?tab=advances&page=1", "", nil, http.StatusOK))
	if row := expenseListedRow(t, advanceList["rows"], advanceID); row["status"] != "已结清" || row["applied_cents"] != float64(80000) || row["returned_cents"] != float64(20000) || row["remaining_cents"] != float64(0) {
		t.Fatalf("预支核销/退回列表余额错误: %#v", row)
	}
	historyList := priceTestJSON(t, f.request(t, f.admin, http.MethodGet, "/api/expenses?tab=history&page=1", "", nil, http.StatusOK))
	if row := expenseHistoryListedRow(t, historyList["rows"], paymentID); row["current_status"] != "reversed" || row["current_version"] != float64(2) || row["claim_id"] != claimID {
		t.Fatalf("付款更正历史投影错误: %#v", row)
	}
	if row := expenseHistoryListedRow(t, historyList["rows"], expenseID(t, allocated800["id"])); row["current_status"] != "active" || row["claim_id"] != claim800ID || row["advance_id"] != advanceID {
		t.Fatalf("预支核销历史关联错误: %#v", row)
	}
	if payrollAfter := expenseSnapshotLedgers(t, f); !reflect.DeepEqual(payrollBefore, payrollAfter) {
		t.Fatalf("费用操作改动工资账本: before=%v after=%v", payrollBefore, payrollAfter)
	}
	if expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_history WHERE entity_id IN (?,?,?,?,?,?,?)", claimID, paymentID, claim800ID,
		expenseID(t, allocated800["id"]), expenseID(t, returned["id"]), linkedID, expenseID(t, linkedPaid["id"])) != 12 {
		t.Fatal("确认、付款、撤销、更正、核销及退回操作历史未保留")
	}
}

func TestExpenseConcurrentPaymentAndAllocationCapsMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	claim := expenseMySQLMust(t, f, "race-pay-create", expenseActionRequest{Action: "create-claim", PersonName: "合成并发员工", ExpenseDate: "2026-10-12", Category: "油费", AmountCents: 10000})
	claimID := expenseID(t, claim["id"])
	confirmed := expenseMySQLMust(t, f, "race-pay-confirm", expenseActionRequest{Action: "confirm-claim", ID: claimID, Version: 1})
	confirmedVersion := expenseInt(t, confirmed["version"])
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := expenseMySQLCall(f, "race-pay-"+string(rune('a'+i)), expenseActionRequest{Action: "pay-claim", ID: claimID,
				Version: confirmedVersion, AmountCents: 7000, PaidDate: "2026-10-12"})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var value failure
		if errors.As(err, &value) && value.Code == 409 {
			conflicts++
			continue
		}
		t.Fatalf("concurrent payment error = %v, want one success and one conflict", err)
	}
	if successes != 1 || conflicts != 1 || expenseDBInt(t, f, "SELECT SUM(amount_cents) FROM expense_payments WHERE claim_id=? AND status='active'", claimID) != 7000 {
		t.Fatalf("concurrent payment exceeded cap: successes=%d conflicts=%d", successes, conflicts)
	}

	advance := expenseMySQLMust(t, f, "race-alloc-advance", expenseActionRequest{Action: "create-advance", PersonName: "合成并发核销", AdvanceDate: "2026-10-12", AmountCents: 10000, Purpose: "合成并发核销"})
	allocationClaim := expenseMySQLMust(t, f, "race-alloc-claim", expenseActionRequest{Action: "create-claim", PersonName: "合成并发核销", ExpenseDate: "2026-10-12", Category: "其他", AmountCents: 10000})
	allocationClaimID := expenseID(t, allocationClaim["id"])
	allocationConfirmed := expenseMySQLMust(t, f, "race-alloc-confirm", expenseActionRequest{Action: "confirm-claim", ID: allocationClaimID, Version: 1})
	allocationConfirmedVersion := expenseInt(t, allocationConfirmed["version"])
	advanceID := expenseID(t, advance["id"])
	allocResults := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := expenseMySQLCall(f, "race-alloc-"+string(rune('a'+i)), expenseActionRequest{Action: "allocate-advance", ClaimID: allocationClaimID,
				ClaimVersion: allocationConfirmedVersion, AdvanceID: advanceID, AdvanceVersion: 1,
				AmountCents: 7000, AppliedDate: "2026-10-12"})
			allocResults <- err
		}(i)
	}
	wg.Wait()
	close(allocResults)
	successes, conflicts = 0, 0
	for err := range allocResults {
		if err == nil {
			successes++
			continue
		}
		var value failure
		if errors.As(err, &value) && value.Code == 409 {
			conflicts++
			continue
		}
		t.Fatalf("concurrent allocation error = %v, want one success and one conflict", err)
	}
	if successes != 1 || conflicts != 1 || expenseDBInt(t, f, "SELECT SUM(amount_cents) FROM expense_advance_allocations WHERE advance_id=? AND status='active'", advanceID) != 7000 {
		t.Fatalf("concurrent allocation exceeded cap: successes=%d conflicts=%d", successes, conflicts)
	}

	returnAdvance := expenseMySQLMust(t, f, "race-return-advance", expenseActionRequest{Action: "create-advance", PersonName: "合成并发退回", AdvanceDate: "2026-10-12", AmountCents: 10000, Purpose: "合成并发退回"})
	returnAdvanceID := expenseID(t, returnAdvance["id"])
	returnResults := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := expenseMySQLCall(f, "race-return-"+string(rune('a'+i)), expenseActionRequest{Action: "return-advance", AdvanceID: returnAdvanceID,
				AdvanceVersion: 1, AmountCents: 7000, ReturnedDate: "2026-10-12"})
			returnResults <- err
		}(i)
	}
	wg.Wait()
	close(returnResults)
	successes, conflicts = 0, 0
	for err := range returnResults {
		if err == nil {
			successes++
			continue
		}
		var value failure
		if errors.As(err, &value) && value.Code == 409 {
			conflicts++
			continue
		}
		t.Fatalf("concurrent advance return error = %v, want one success and one conflict", err)
	}
	if successes != 1 || conflicts != 1 || expenseDBInt(t, f, "SELECT SUM(amount_cents) FROM expense_advance_returns WHERE advance_id=? AND status='active'", returnAdvanceID) != 7000 {
		t.Fatalf("concurrent return exceeded cap: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestExpensePermissionAndIdempotencyMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	unauthorized := f.employee(t, []string{"orders"})
	f.request(t, unauthorized, http.MethodGet, "/api/expenses?tab=claims", "", nil, 403)
	f.request(t, unauthorized, http.MethodGet, "/api/expense-purchases", "", nil, 403)
	for _, action := range []string{"create-claim", "update-claim", "confirm-claim", "void-claim", "pay-claim", "reverse-payment", "create-advance", "correct-advance", "reverse-advance", "allocate-advance", "reverse-allocation", "return-advance", "reverse-return"} {
		f.request(t, unauthorized, http.MethodPost, "/api/expense-action", "expense-permission-denied-"+action, expenseActionRequest{Action: action}, 403)
	}
	if expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_claims") != 0 || expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_advances") != 0 || expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_history") != 0 {
		t.Fatal("expense actions without permission wrote expense data")
	}

	payload := expenseActionRequest{Action: "create-claim", PersonName: "合成幂等", ExpenseDate: "2026-10-12", Category: "停车费", AmountCents: 100}
	first, err := expenseMySQLCall(f, "same-idempotency-key", payload)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := expenseMySQLCall(f, "same-idempotency-key", payload)
	if err != nil {
		t.Fatal(err)
	}
	if jsonText(first) != jsonText(replay) || expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_claims WHERE person_name='合成幂等'") != 1 {
		t.Fatal("same expense operation key duplicated a claim or changed its result")
	}
	changed := payload
	changed.AmountCents = 101
	if _, err = expenseMySQLCall(f, "same-idempotency-key", changed); err == nil {
		t.Fatal("same expense operation key accepted different content")
	}

	permitted := f.employee(t, []string{"expenses"})
	revokedPayload := expenseActionRequest{Action: "create-claim", PersonName: "合成撤权幂等", ExpenseDate: "2026-10-12", Category: "其他", AmountCents: 100}
	f.request(t, permitted, http.MethodPost, "/api/expense-action", "expense-revoked-key", revokedPayload, http.StatusOK)
	revoked := f.permissions(t, permitted, []string{"orders"})
	f.request(t, revoked, http.MethodPost, "/api/expense-action", "expense-revoked-key", revokedPayload, http.StatusForbidden)
	if expenseDBInt(t, f, "SELECT COUNT(*) FROM expense_claims WHERE person_name='合成撤权幂等'") != 1 {
		t.Fatal("revoked expense permission replayed or duplicated the prior claim")
	}
}
