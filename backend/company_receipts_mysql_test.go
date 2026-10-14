package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func companyReceiptTestDetail(t *testing.T, w *httptest.ResponseRecorder) companyReceiptDetail {
	t.Helper()
	var out companyReceiptDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}
func companyReceiptTestCreate(t *testing.T, f priceMySQLFixture, amount int64) companyReceiptDetail {
	t.Helper()
	return companyReceiptTestDetail(t, f.request(t, f.admin, "POST", "/api/company-receipt", "", companyReceiptRequest{CompanyID: "loc-co", Amount: amount, ReceivedAt: "2026-10-12", Method: "合成转账"}, 200))
}
func companyReceiptTestAllocate(t *testing.T, f priceMySQLFixture, r companyReceiptDetail, allocations ...receiptAllocationInput) companyReceiptDetail {
	t.Helper()
	return companyReceiptTestDetail(t, f.request(t, f.admin, "POST", "/api/receipt-allocation", "", receiptAllocationRequest{ReceiptID: r.Receipt.ID, Version: r.Receipt.Version, Reason: "合成收款分配", Allocations: allocations}, 200))
}
func companyReceiptTestOrder(t *testing.T, f priceMySQLFixture, address string, amount int64) orderDetailView {
	t.Helper()
	body := Order{Company: "loc-co", Address: address, Date: "2026-10-12", Worker: "loc-a", Lines: []Line{{ClientRef: "work", Temporary: true, Category: "开工", Name: "合成收款施工", Mode: "金额", Price: amount, Wage: 1000, Worker: "loc-a"}}, Materials: []Material{{LineID: "work", Product: "loc-product", InputQty: "1", InputUnit: "base"}}}
	w := f.request(t, f.admin, "POST", "/api/order", "", body, 200)
	orderID := priceTestJSON(t, w)["id"].(string)
	return orderLocationsRead(t, f.a, orderID, f.u)
}
func companyReceiptTestTicket(t *testing.T, f priceMySQLFixture, projectID string) string {
	t.Helper()
	var ticket string
	if err := f.a.db.QueryRow("SELECT id FROM project_tickets WHERE project_id=?", projectID).Scan(&ticket); err != nil {
		t.Fatal(err)
	}
	return ticket
}
func companyReceiptTestScalar(t *testing.T, db *sql.DB, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := db.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("query %s got=%d want=%d err=%v", query, got, want, err)
	}
}

func TestCompanyReceiptPrepaymentMultiWorksiteAndInstallmentsMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	// No worksite and no delivery is required to preserve an actual bank payment.
	r := companyReceiptTestCreate(t, f, 100000)
	if r.Receipt.Allocated != 0 || r.Receipt.Unallocated != 100000 || len(r.Allocations) != 0 {
		t.Fatal("prepayment was not preserved", r)
	}
	first := companyReceiptTestOrder(t, f, "收款合成甲工地", 100000)
	second := companyReceiptTestOrder(t, f, "收款合成乙工地", 50000)
	t1, t2 := companyReceiptTestTicket(t, f, first.ProjectID), companyReceiptTestTicket(t, f, second.ProjectID)
	before := orderLocationsState(t, f.a.db)
	// A single actual payment can clear parts of two worksites before any paper.
	r = companyReceiptTestAllocate(t, f, r, receiptAllocationInput{TicketID: t1, Amount: 50000}, receiptAllocationInput{TicketID: t2, Amount: 20000})
	if r.Receipt.Amount != 100000 || r.Receipt.Allocated != 70000 || r.Receipt.Unallocated != 30000 || len(r.Allocations) != 2 {
		t.Fatal("1500/1000/700 balances wrong", r)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents) FROM project_tickets", 150000)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='草稿'", 2)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents)-(SELECT SUM(amount_cents) FROM project_ticket_receipts WHERE voided=0) FROM project_tickets", 80000)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents)-(SELECT SUM(amount_cents) FROM company_receipts WHERE status='active') FROM project_tickets", 50000)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "orders", "order_lines", "order_worker_wages", "order_materials", "stock_balances", "stock_movements", "settlements", "wage_settlements")
	// Unallocated funds can be allocated later without adding another receipt.
	r = companyReceiptTestAllocate(t, f, r, receiptAllocationInput{TicketID: t1, Amount: 80000}, receiptAllocationInput{TicketID: t2, Amount: 20000})
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 1)
	if r.Receipt.Unallocated != 0 {
		t.Fatal("later allocation lost balance", r)
	}
	part := companyReceiptTestCreate(t, f, 20000)
	part = companyReceiptTestAllocate(t, f, part, receiptAllocationInput{TicketID: t1, Amount: 20000})
	final := companyReceiptTestCreate(t, f, 30000)
	final = companyReceiptTestAllocate(t, f, final, receiptAllocationInput{TicketID: t2, Amount: 30000})
	if part.Receipt.Unallocated != 0 || final.Receipt.Unallocated != 0 {
		t.Fatal("installments not allocated")
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM company_receipts WHERE status='active'", 150000)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM project_ticket_receipts WHERE voided=0", 150000)
	// Nothing is delivered merely because it has been fully paid.
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='草稿'", 2)
	f.request(t, f.admin, "POST", "/api/receipt-allocation", "", receiptAllocationRequest{ReceiptID: final.Receipt.ID, Version: final.Receipt.Version, Reason: "合成超额", Allocations: []receiptAllocationInput{{TicketID: t2, Amount: 30001}}}, 400)
	maintenanceFaultExec(t, f.a.db, "INSERT INTO companies(id,name) VALUES('cash-other','合成另一公司')")
	f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "cash-other", "ticket_id": t1, "amount_cents": 1, "reason": "不允许跨公司"}, 400)
}

func TestCompanyReceiptDiscountAndCorrectionMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "减免合成工地", 1000000)
	ticket := companyReceiptTestTicket(t, f, o.ProjectID)
	r := companyReceiptTestCreate(t, f, 900000)
	r = companyReceiptTestAllocate(t, f, r, receiptAllocationInput{TicketID: ticket, Amount: 900000})
	companyReceiptTestScalar(t, f.a.db, "SELECT total_cents-(SELECT SUM(amount_cents) FROM project_ticket_receipts WHERE ticket_id=? AND voided=0) FROM project_tickets WHERE id=?", 100000, ticket, ticket)
	// Until the boss explicitly agrees, the short payment remains due.
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM customer_discounts", 0)
	w := f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": ticket, "amount_cents": 100000, "reason": "老板同意合成客户减免"}, 200)
	var d customerDiscountDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM company_receipts WHERE status='active'", 900000)
	tx, err := f.a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := readProjectTicketSummary(tx, ticket)
	tx.Rollback()
	if err != nil || summary.OutstandingCents != 0 || summary.DiscountCents != 100000 {
		t.Fatal("discount did not clear without increasing cash", summary, err)
	}
	f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": ticket, "amount_cents": 1, "reason": "重复减免"}, 400)
	f.request(t, f.admin, "POST", "/api/customer-discount-action", "", map[string]any{"id": d.Discount.ID, "version": d.Discount.Version, "action": "void", "reason": "老板撤回合成减免"}, 200)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM customer_discount_versions", 2)
	// Corrected amounts shrink allocation, and a wrong company releases it all.
	w = f.request(t, f.admin, "POST", "/api/company-receipt-action", "", map[string]any{"id": r.Receipt.ID, "version": r.Receipt.Version, "action": "correct", "reason": "合成金额登记错误", "company_id": "loc-co", "received_at": "2026-10-12", "amount_cents": 800000, "method": "合成现金"}, 200)
	r = companyReceiptTestDetail(t, w)
	if r.Receipt.Amount != 800000 || r.Receipt.Allocated != 800000 || r.Receipt.Unallocated != 0 {
		t.Fatal("amount correction did not reconcile", r)
	}
	maintenanceFaultExec(t, f.a.db, "INSERT INTO companies(id,name) VALUES('cash-correct-co','合成更正公司')")
	w = f.request(t, f.admin, "POST", "/api/company-receipt-action", "", map[string]any{"id": r.Receipt.ID, "version": r.Receipt.Version, "action": "correct", "reason": "合成付款公司登记错误", "company_id": "cash-correct-co", "received_at": "2026-10-12", "amount_cents": 800000, "method": "合成现金"}, 200)
	r = companyReceiptTestDetail(t, w)
	if r.Receipt.Allocated != 0 || r.Receipt.Unallocated != 800000 || r.Receipt.CompanyID != "cash-correct-co" {
		t.Fatal("company correction kept wrong allocation", r)
	}
	f.request(t, f.admin, "POST", "/api/receipt-allocation", "", receiptAllocationRequest{ReceiptID: r.Receipt.ID, Version: r.Receipt.Version, Reason: "合成跨公司", Allocations: []receiptAllocationInput{{TicketID: ticket, Amount: 1}}}, 400)
	w = f.request(t, f.admin, "POST", "/api/company-receipt-action", "", map[string]any{"id": r.Receipt.ID, "version": r.Receipt.Version, "action": "void", "reason": "合成重复登记"}, 200)
	r = companyReceiptTestDetail(t, w)
	if r.Receipt.Status != "voided" || r.Receipt.Amount != 800000 || r.Receipt.Unallocated != 0 {
		t.Fatal("void physically deleted or retained effective cash", r)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 1)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipt_versions", 5)
}

func TestCompanyReceiptVoidReissueReturnsExcessMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "作废更正合成工地", 100000)
	other := companyReceiptTestOrder(t, f, "保持正常合成工地", 30000)
	first := ticketIssueMySQLCreate(t, f, id(), ticketIssueMySQLSelection(t, f, o.Lines[0].ID))
	unrelated := ticketIssueMySQLCreate(t, f, id(), ticketIssueMySQLSelection(t, f, other.Lines[0].ID))
	ticket := companyReceiptTestTicket(t, f, o.ProjectID)
	r := companyReceiptTestCreate(t, f, 60000)
	r = companyReceiptTestAllocate(t, f, r, receiptAllocationInput{TicketID: ticket, BatchID: first.Batch.ID, Amount: 60000})
	before := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "POST", "/api/ticket-batch-action", "", ticketBatchActionRequest{ID: first.Batch.ID, Version: first.Batch.Version, Action: "void", Reason: "合成原收费错误"}, 200)
	r = companyReceiptTestDetail(t, f.request(t, f.admin, "GET", "/api/company-receipt?id="+r.Receipt.ID, "", nil, 200))
	if r.Receipt.Amount != 60000 || r.Receipt.Allocated != 60000 || r.Allocations[0].BatchID != "" {
		t.Fatal("paper void changed money", r)
	}
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "orders", "order_worker_wages", "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements")
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM order_lines", 2)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents) FROM order_lines", 130000)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(labor_cents) FROM order_lines", 2000)
	edited := orderLocationsEdit(t, orderLocationsRead(t, f.a, o.ID, f.u))
	edited.Lines[0].Price = 50000
	edited.Lines[0].Segments = []Segment{{Mode: "金额", Amount: 50000}}
	f.request(t, f.admin, "POST", "/api/order", "", edited, 200)
	r = companyReceiptTestDetail(t, f.request(t, f.admin, "GET", "/api/company-receipt?id="+r.Receipt.ID, "", nil, 200))
	if r.Receipt.Amount != 60000 || r.Receipt.Allocated != 50000 || r.Receipt.Unallocated != 10000 {
		t.Fatal("1000 to 500 with 600 cash reconciliation wrong", r)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT wage_cents FROM orders WHERE id=?", 1000, o.ID)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(wage_cents) FROM order_worker_wages WHERE order_id=?", 1000, o.ID)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE order_id=? AND wage_paid=1", 0, o.ID)
	beforeReissue := orderLocationsState(t, f.a.db)
	newBatch := ticketIssueMySQLCreate(t, f, id(), ticketIssueMySQLSelection(t, f, o.Lines[0].ID))
	if newBatch.Batch.TotalCents != 50000 {
		t.Fatal("reissue charged again", newBatch)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 1)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents) FROM project_tickets", 80000)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM ticket_batches WHERE id=? AND status='delivered' AND version=?", 1, unrelated.Batch.ID, unrelated.Batch.Version)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements")
	ticketIssueMySQLTablesUnchanged(t, f.a.db, beforeReissue, "orders", "order_worker_wages", "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements")
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM order_lines", 2)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(total_cents) FROM order_lines", 80000)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(labor_cents) FROM order_lines", 2000)
	if len(r.History) != 4 {
		t.Fatal("missing receipt/void/correction history", r.History)
	}
	// One payment can carry labels for separate batches and worksites, with no
	// new actual money generated by the reallocation or paper reissue.
	r = companyReceiptTestAllocate(t, f, r, receiptAllocationInput{TicketID: ticket, BatchID: newBatch.Batch.ID, Amount: 50000}, receiptAllocationInput{TicketID: companyReceiptTestTicket(t, f, other.ProjectID), BatchID: unrelated.Batch.ID, Amount: 10000})
	if r.Receipt.Amount != 60000 || r.Receipt.Allocated != 60000 || len(r.Allocations) != 2 {
		t.Fatal("multi batch allocation wrong", r)
	}
}

func TestCompanyReceiptPermissionsAndConcurrentLimitsMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "并发合成工地", 100000)
	ticket := companyReceiptTestTicket(t, f, o.ProjectID)
	noFinance := f.employee(t, []string{"orders", "customer_prices"})
	onlyFinance := f.employee(t, []string{"finance"})
	finance := f.employee(t, []string{"finance", "customer_prices"})
	p := companyReceiptRequest{CompanyID: "loc-co", ReceivedAt: "2026-10-12", Amount: 80000, Method: "合成转账"}
	for _, actor := range []priceTestActor{noFinance, onlyFinance} {
		f.request(t, actor, "POST", "/api/company-receipt", "", p, 403)
		f.request(t, actor, "GET", "/api/company-receipts", "", nil, 403)
		f.request(t, actor, "GET", "/api/receipt-targets?company_id=loc-co", "", nil, 403)
	}
	key := id()
	r := companyReceiptTestDetail(t, f.request(t, finance, "POST", "/api/company-receipt", key, p, 200))
	f.request(t, finance, "POST", "/api/company-receipt", key, p, 200)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 1)
	p.Amount++
	f.request(t, finance, "POST", "/api/company-receipt", key, p, 409)
	p.Amount--
	f.request(t, finance, "POST", "/api/company-receipt-action", "", map[string]any{"id": r.Receipt.ID, "version": r.Receipt.Version, "action": "void", "reason": "无权限作废"}, 403)
	f.request(t, finance, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": ticket, "amount_cents": 1, "reason": "无权限减免"}, 403)
	finance = f.permissions(t, finance, []string{"orders"})
	f.request(t, finance, "POST", "/api/company-receipt", key, p, 403)
	other := companyReceiptTestCreate(t, f, 80000)
	before := orderLocationsState(t, f.a.db)
	requests := []receiptAllocationRequest{{ReceiptID: r.Receipt.ID, Version: 1, Reason: "并发甲分配", Allocations: []receiptAllocationInput{{TicketID: ticket, Amount: 80000}}}, {ReceiptID: other.Receipt.ID, Version: 1, Reason: "并发乙分配", Allocations: []receiptAllocationInput{{TicketID: ticket, Amount: 80000}}}}
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, input := range requests {
		wg.Add(1)
		go func(input receiptAllocationRequest) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest("POST", "/api/receipt-allocation", strings.NewReader(jsonText(input)))
			req.AddCookie(&http.Cookie{Name: "tegu_session", Value: f.admin.token})
			req.Header.Set("Idempotency-Key", id())
			w := httptest.NewRecorder()
			f.a.route(w, req)
			results <- w.Code
		}(input)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for status := range results {
		if status == 200 {
			success++
		} else if status == 400 || status == 409 {
			conflict++
		} else {
			t.Fatal("unexpected concurrent status", status)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent allocation exceeded charge", success, conflict)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM project_ticket_receipts WHERE voided=0", 80000)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "orders", "order_lines", "order_worker_wages", "order_materials", "stock_balances", "stock_movements", "settlements", "wage_settlements")
	// Simultaneous repeats of one bank receipt create one head only.
	key = id()
	start = make(chan struct{})
	results = make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest("POST", "/api/company-receipt", strings.NewReader(jsonText(p)))
			req.AddCookie(&http.Cookie{Name: "tegu_session", Value: f.admin.token})
			req.Header.Set("Idempotency-Key", key)
			w := httptest.NewRecorder()
			f.a.route(w, req)
			results <- w.Code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for status := range results {
		if status != 200 {
			t.Fatal("simultaneous duplicate registration", status)
		}
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 3)
}

func TestCompanyReceiptLegacyMigrationMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "历史收款迁移合成工地", 100000)
	ticket := companyReceiptTestTicket(t, f, o.ProjectID)
	// Recreate just the Phase 444 financial structure in this disposable schema.
	// The migration must add its columns/tables and convert old cash atomically.
	for _, table := range []string{"company_receipt_versions", "customer_discount_versions", "customer_discounts", "company_receipts"} {
		if _, err := f.a.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"ALTER TABLE project_ticket_receipts DROP INDEX idx_ticket_receipt_head,DROP INDEX idx_ticket_receipt_batch,DROP COLUMN receipt_id,DROP COLUMN batch_id", "DELETE FROM schema_migrations WHERE version=445"} {
		if _, err := f.a.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.a.db.Exec(`INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,method,created_by,voided) VALUES('legacy-cash-one',?,30000,'2026-10-12','合成转账','old',0),('legacy-cash-two',?,30000,'2026-10-12','合成转账','old',0),('legacy-cash-void',?,10000,'2026-10-12','合成转账','old',1)`, ticket, ticket, ticket); err != nil {
		t.Fatal(err)
	}
	if err := migrateCompanyReceipts(f.a.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateCompanyReceipts(f.a.db); err != nil {
		t.Fatal(err)
	}
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 3)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM company_receipts WHERE status='active'", 60000)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM project_ticket_receipts WHERE receipt_id=id", 3)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipt_versions", 3)
	companyReceiptTestScalar(t, f.a.db, "SELECT MAX(version) FROM schema_migrations", 445)
}
