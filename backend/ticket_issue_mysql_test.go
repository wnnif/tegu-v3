package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The external runner must pin the isolated MySQL identity and restricted
// grants before opting in. Existing fixtures create and clean disposable schemas.
func ticketIssueMySQLRead(t *testing.T, f priceMySQLFixture) ticketIssueResponse {
	t.Helper()
	w := f.request(t, f.admin, "GET", "/api/ticket-issue?company_id=loc-co", "", nil, 200)
	var out ticketIssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ticketIssueMySQLSelection(t *testing.T, f priceMySQLFixture, ids ...string) []ticketSelection {
	t.Helper()
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []ticketSelection{}
	for _, row := range ticketIssueMySQLRead(t, f).Rows {
		if wanted[row.ItemID] {
			if row.Status != "pending" {
				t.Fatal("actual row is not selectable", row)
			}
			selected = append(selected, ticketSelection{ItemID: row.ItemID, ItemKind: row.ItemKind, OrderVersion: row.OrderVersion})
		}
	}
	if len(selected) != len(wanted) {
		t.Fatal("actual row missing from candidates", ids, selected)
	}
	return selected
}

func ticketIssueMySQLCreate(t *testing.T, f priceMySQLFixture, key string, selected []ticketSelection) ticketBatchResponse {
	t.Helper()
	w := f.request(t, f.admin, "POST", "/api/ticket-batch", key, ticketBatchCreateRequest{CompanyID: "loc-co", DeliveredAt: "2026-10-12", Recipient: "合成收票人", Items: selected}, 200)
	var out ticketBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ticketIssueMySQLTablesUnchanged(t *testing.T, db *sql.DB, before map[string][]string, tables ...string) {
	t.Helper()
	after := orderLocationsState(t, db)
	for _, table := range tables {
		if !reflect.DeepEqual(before[table], after[table]) {
			t.Fatal("ticket operation changed unrelated ledger", table)
		}
	}
}

func TestTicketIssueWorkflowMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	first := Order{Company: "loc-co", Address: "4A合成甲工地", Date: "2026-10-12", Worker: "loc-a",
		Lines: []Line{
			{ClientRef: "correct", Temporary: true, Category: "开工", Name: "合成待更正施工", Mode: "金额", Price: 100000, Wage: 3000, Worker: "loc-a", ActualLocation: "主卫"},
			{CraftID: "loc-amount", Worker: "loc-b", Qty: 1000000, ActualLocation: "厨房"},
			{Temporary: true, Category: "开工", Name: "合成免费补做", Mode: "金额", Price: 9000, Wage: 700, Worker: "loc-a", ChargeMode: "free"},
		},
		Materials: []Material{{LineID: "correct", Product: "loc-product", InputQty: "1", InputUnit: "base"}},
		Sales:     []Sale{{Product: "loc-product", InputQty: "2", InputUnit: "base", Price: 500}},
	}
	firstID := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", first, 200))["id"].(string)
	secondID := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", Order{Company: "loc-co", Address: "4A合成乙工地", Date: "2026-10-12", Lines: []Line{{Temporary: true, Category: "开工", Name: "合成乙施工", Mode: "金额", Price: 20000, Wage: 2000, Worker: "loc-a"}}}, 200))["id"].(string)
	d := orderLocationsRead(t, f.a, firstID, f.u)
	second := orderLocationsRead(t, f.a, secondID, f.u)
	var correct, protected, free string
	for _, l := range d.Lines {
		switch l.Name {
		case "合成待更正施工":
			correct = l.ID
		case "合成整项金额":
			protected = l.ID
		case "合成免费补做":
			free = l.ID
		}
	}
	if correct == "" || protected == "" || free == "" || len(d.Sales) != 1 || len(second.Lines) != 1 || d.TotalCents != 105567 {
		t.Fatal("unexpected synthetic construction", d)
	}
	// Seed a historical actual receipt without changing the draft source status.
	// This fixture proves 4A preserves money; receipt entry itself belongs to 4B.
	if _, err := f.a.db.Exec(`INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,method,note,created_by) SELECT '4a-existing-receipt',ticket_id,40000,'2026-10-12','合成转账','合成历史实际到账','test' FROM project_ticket_items WHERE item_id=? AND item_kind='line'`, correct); err != nil {
		t.Fatal(err)
	}
	beforePreview := orderLocationsState(t, f.a.db)
	selected := ticketIssueMySQLSelection(t, f, correct, free, d.Sales[0].ID, second.Lines[0].ID)
	ticketIssueMySQLRead(t, f)
	orderLocationsUnchanged(t, f.a.db, beforePreview)
	key := id()
	batch := ticketIssueMySQLCreate(t, f, key, selected)
	if len(batch.Items) != 4 || batch.Batch.TotalCents != 121000 || batch.Batch.Status != "delivered" {
		t.Fatal("cross-worksite batch total", batch)
	}
	projects, sawFree := map[string]bool{}, false
	for _, item := range batch.Items {
		projects[item.ProjectID] = true
		if item.ItemID == free {
			sawFree = item.TotalCents == 0 && item.Quantity == 1
		}
	}
	if len(projects) != 2 || !sawFree {
		t.Fatal("cross-site or zero-price work lost", batch)
	}
	replay := ticketIssueMySQLCreate(t, f, key, selected)
	if replay.Batch.ID != batch.Batch.ID {
		t.Fatal("idempotency created a second paper ticket")
	}
	beforeDuplicate := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "POST", "/api/ticket-batch", "", ticketBatchCreateRequest{CompanyID: "loc-co", DeliveredAt: "2026-10-12", Recipient: "合成收票人", Items: selected}, 409)
	orderLocationsUnchanged(t, f.a.db, beforeDuplicate)
	other := ticketIssueMySQLCreate(t, f, "", ticketIssueMySQLSelection(t, f, protected))
	beforeRead := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "GET", "/api/ticket-batch?id="+batch.Batch.ID, "", nil, 200)
	ticketIssueMySQLRead(t, f)
	orderLocationsUnchanged(t, f.a.db, beforeRead)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, beforePreview, "orders", "order_worker_wages", "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements", "project_tickets", "project_ticket_items", "project_ticket_receipts")
	edit := orderLocationsEdit(t, d)
	for i := range edit.Lines {
		if edit.Lines[i].ID == correct {
			edit.Lines[i].Price = 50000
			edit.Lines[i].Segments = []Segment{{Mode: "金额", Amount: 50000}}
		}
	}
	f.request(t, f.admin, "POST", "/api/order", "", edit, 409)
	orderLocationsUnchanged(t, f.a.db, beforeRead)
	void := ticketBatchActionRequest{ID: batch.Batch.ID, Version: batch.Batch.Version, Action: "void", Reason: "合成收费更正"}
	f.request(t, f.admin, "POST", "/api/ticket-batch-action", "", void, 200)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, beforePreview, "orders", "order_worker_wages", "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements", "project_tickets", "project_ticket_items", "project_ticket_receipts")
	f.request(t, f.admin, "POST", "/api/order", "", edit, 200)
	corrected := orderLocationsRead(t, f.a, firstID, f.u)
	if corrected.TotalCents != 55567 || corrected.WageCents == nil || d.WageCents == nil || *corrected.WageCents != *d.WageCents {
		t.Fatal("real order correction money/wage", corrected)
	}
	if corrected.WageRecords == nil || d.WageRecords == nil {
		t.Fatal("missing actual worker wage accounts")
	}
	oldAccounts := append([]workerWageRecord(nil), (*d.WageRecords)...)
	newAccounts := append([]workerWageRecord(nil), (*corrected.WageRecords)...)
	// An ordinary order save advances account versions. It must retain exact
	// account IDs, people, wage amounts and payment states rather than add wages.
	for i := range oldAccounts {
		oldAccounts[i].Version = 0
	}
	for i := range newAccounts {
		newAccounts[i].Version = 0
	}
	if !reflect.DeepEqual(oldAccounts, newAccounts) {
		t.Fatal("price correction duplicated or changed worker wages", oldAccounts, newAccounts)
	}
	var active string
	if err := f.a.db.QueryRow("SELECT ticket_batch_id FROM order_lines WHERE id=?", protected).Scan(&active); err != nil || active != other.Batch.ID {
		t.Fatal("other batch lost its protected row", active, err)
	}
	var untouched ticketBatchResponse
	w := f.request(t, f.admin, "GET", "/api/ticket-batch?id="+other.Batch.ID, "", nil, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &untouched); err != nil || !reflect.DeepEqual(untouched, other) {
		t.Fatal("other batch snapshot changed", untouched, err)
	}
	beforeReissue := orderLocationsState(t, f.a.db)
	reissued := ticketIssueMySQLCreate(t, f, "", ticketIssueMySQLSelection(t, f, correct, free, d.Sales[0].ID, second.Lines[0].ID))
	if reissued.Batch.ID == batch.Batch.ID || reissued.Batch.TotalCents != 71000 {
		t.Fatal("reissue did not use corrected source", reissued)
	}
	ticketIssueMySQLTablesUnchanged(t, f.a.db, beforeReissue, "orders", "order_worker_wages", "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements", "project_tickets", "project_ticket_items", "project_ticket_receipts")
	ticketIssueMySQLTablesUnchanged(t, f.a.db, beforePreview, "wage_settlements", "order_materials", "stock_balances", "stock_movements", "settlements", "project_ticket_receipts")
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batches", 3)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batch_versions", 4)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM project_ticket_receipts WHERE id='4a-existing-receipt' AND amount_cents=40000 AND voided=0", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batch_items", 9)
	for _, permissions := range [][]string{{"orders", "customer_prices"}, {"finance"}} {
		actor := f.employee(t, permissions)
		beforeDenied := orderLocationsState(t, f.a.db)
		f.request(t, actor, "GET", "/api/ticket-issue?company_id=loc-co", "", nil, 403)
		f.request(t, actor, "GET", "/api/ticket-batch?id="+other.Batch.ID, "", nil, 403)
		f.request(t, actor, "POST", "/api/ticket-batch", "", ticketBatchCreateRequest{CompanyID: "loc-co", DeliveredAt: "2026-10-12", Recipient: "无权", Items: selected}, 403)
		f.request(t, actor, "POST", "/api/ticket-batch-action", "", ticketBatchActionRequest{ID: other.Batch.ID, Version: 1, Action: "void", Reason: "无权"}, 403)
		orderLocationsUnchanged(t, f.a.db, beforeDenied)
	}
}

func TestTicketIssueConcurrentClaimMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	orderID := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", Order{Company: "loc-co", Address: "4A合成并发工地", Date: "2026-10-12", Lines: []Line{{CraftID: "loc-amount", Worker: "loc-a", Qty: 1000000}}}, 200))["id"].(string)
	d := orderLocationsRead(t, f.a, orderID, f.u)
	selected := ticketIssueMySQLSelection(t, f, d.Lines[0].ID)
	before := orderLocationsState(t, f.a.db)
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			r := httptest.NewRequest("POST", "/api/ticket-batch", strings.NewReader(jsonText(ticketBatchCreateRequest{CompanyID: "loc-co", DeliveredAt: "2026-10-12", Recipient: "合成并发收票人", Items: selected})))
			r.Header.Set("Idempotency-Key", id())
			r.AddCookie(&http.Cookie{Name: "tegu_session", Value: f.admin.token})
			w := httptest.NewRecorder()
			f.a.route(w, r)
			responses <- w
		}()
	}
	close(start)
	workers.Wait()
	close(responses)
	counts := map[int]int{}
	for response := range responses {
		counts[response.Code]++
		if response.Code != 200 && response.Code != 409 {
			t.Fatal("concurrent claim unexpected response", response.Code, response.Body.String())
		}
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("same actual row claimed twice", counts)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batches", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batch_items", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM ticket_batch_versions", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM audit WHERE action='公司批次交票'", 1)
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "orders", "order_worker_wages", "stock_balances", "stock_movements", "project_tickets", "project_ticket_items", "project_ticket_receipts", "settlements")
}

func TestTicketIssueMigrationUpgradeMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=444", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('ticket_batches','ticket_batch_items','ticket_batch_versions')", 3)
	f.must(t, Order{Company: "loc-co", Address: "4A合成升级工地", Date: "2026-10-12", Lines: []Line{{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-a", Segments: []Segment{{Length: 2, Width: 3}}}}})
	before := orderLocationsState(t, f.a.db)
	// Only this newly created disposable schema is reduced to its pre-444
	// shape. No real database, existing ticket batch or business data is touched.
	for _, query := range []string{
		"DROP TABLE ticket_batch_versions", "DROP TABLE ticket_batch_items", "DROP TABLE ticket_batches",
		"ALTER TABLE order_lines DROP INDEX idx_order_lines_ticket_batch, DROP COLUMN ticket_batch_id",
		"ALTER TABLE order_sales DROP INDEX idx_order_sales_ticket_batch, DROP COLUMN ticket_batch_id",
		"DELETE FROM schema_migrations WHERE version=444",
	} {
		if _, err := f.a.db.Exec(query); err != nil {
			t.Fatal("synthetic pre-444 fixture", err)
		}
	}
	if err := initSchema(f.a.db); err != nil {
		t.Fatal("upgrade existing construction schema", err)
	}
	orderLocationsUnchanged(t, f.a.db, before)
	if err := initSchema(f.a.db); err != nil {
		t.Fatal("idempotent schema rerun", err)
	}
	orderLocationsUnchanged(t, f.a.db, before)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=444", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND COLUMN_NAME='ticket_batch_id' AND TABLE_NAME IN ('order_lines','order_sales') AND IS_NULLABLE='YES'", 2)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND INDEX_NAME IN ('idx_order_lines_ticket_batch','idx_order_sales_ticket_batch')", 2)
}
