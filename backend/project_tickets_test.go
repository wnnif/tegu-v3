package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestCountPricingAndMixedUnits(t *testing.T) {
	l := Line{Category: "开工", Name: "按个收费", Mode: "个", Price: 10000, Wage: 3000, Segments: []Segment{{Count: 2}}}
	if err := calcLine(&l); err != nil || l.Total != 20000 || l.Labor != 6000 || l.Qty != 2000000 {
		t.Fatalf("quantity pricing: %+v %v", l, err)
	}
	l.Segments[0].Count = 0
	if calcLine(&l) == nil {
		t.Fatal("zero count accepted")
	}
	l.Segments = []Segment{{Count: 1000000}, {Count: 1}}
	if calcLine(&l) == nil {
		t.Fatal("count overflow accepted")
	}
	qty, saleQty, err := orderInputQuantity("6", 0, "aux", "袋", "6")
	if err != nil || qty != 1000000 || saleQty != 6000000 {
		t.Fatal(qty, saleQty, err)
	}
	for _, value := range []string{"0", "-1", "0.00000001", "1000001", "invalid"} {
		if _, _, err = orderInputQuantity(value, 0, "base", "", ""); err == nil {
			t.Fatal("invalid stock quantity accepted", value)
		}
	}
}

func TestTicketPrintAllocatesDeliveryWageOnce(t *testing.T) {
	wage, delivery, one, two := int64(9000), int64(2000), int64(3000), int64(4000)
	o := orderDetailView{WageCents: &wage, DeliveryWage: &delivery, Lines: []orderLineView{{ID: "first", Total: 10000, Labor: &one}, {ID: "second", Total: 20000, Labor: &two}}, Sales: []Sale{{ID: "sale", Total: 5000}}}
	first, second := o, o
	filterOrderForTicket(&first, map[string]bool{"first": true})
	filterOrderForTicket(&second, map[string]bool{"second": true, "sale": true})
	if *first.WageCents != 3000 || *first.DeliveryWage != 0 || first.TotalCents != 10000 || *second.WageCents != 6000 || second.TotalCents != 25000 {
		t.Fatal("wage counted in incorrect ticket", first, second)
	}
}

// This is a historical-state fixture for isolated MySQL tests, not a replacement
// delivery API. New paper deliveries must use the company row-selection flow.
// Preserve a version snapshot so legacy receipts and historical reads are still
// exercised without using the retired whole-project-ticket delivery operation.
func fixtureLegacyDeliveredProjectTicket(t *testing.T, db *sql.DB, actor User, ticketID string, version int64, date string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE project_tickets SET status='已交票',delivered_at=?,recipient='合成历史收票人',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='草稿'`, date, ticketID, version)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("historical delivered fixture changed %d rows: %v", count, err)
	}
	if err = saveProjectTicketVersion(tx, actor, ticketID, "合成历史已交票夹具"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredProjectTicketDeliveryRejectsBeforeSQL(t *testing.T) {
	// A nil transaction makes any SQL access fail immediately. The retired batch
	// action must be rejected before acquiring project/ticket locks.
	payload := map[string]any{"company_id": "offline-company", "action": "deliver", "delivered_at": "2026-10-12", "recipient": "合成收票人", "tickets": []SettlementOrder{{ID: "offline-ticket", Version: 1}}}
	_, err := (&App{}).projectTicketBatch(nil, User{Role: "admin"}, []byte(jsonText(payload)), "offline-retired-delivery")
	maintenanceFaultError(t, err, 409)
	_, err = (&App{}).projectTicketAction(nil, User{Role: "admin"}, []byte(jsonText(ProjectTicketActionRequest{ID: "offline-ticket", Version: 1, Action: "deliver", DeliveredAt: "2026-10-12", Recipient: "合成收票人"})), "offline-retired-single-delivery")
	maintenanceFaultError(t, err, 409)
}

func TestProjectTicketsWorkflowMySQL(t *testing.T) {
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("requires explicit isolated MySQL")
	}
	if !strings.HasPrefix(os.Getenv("MYSQL_DATABASE"), "tegu_go_test_") {
		t.Fatal("requires isolated test database")
	}
	cfg := mysql.Config{User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"), DBName: os.Getenv("MYSQL_DATABASE"), AllowNativePasswords: true}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	name := "tegu_go_test_project_tickets_" + id()[:12]
	if _, err = control.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer control.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db}
	admin := User{ID: "ticket-test-admin", Name: "合成测试", Role: "admin"}
	for _, q := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('ca','合成分两票公司','split'),('cb','合成统一公司','single')`,
		`INSERT INTO workers(id,name,crafts) VALUES('w','合成师傅',JSON_ARRAY('开工','防水涂料','丙纶'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('wh','合成仓库','w')`,
		`INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('p','合成材料','桶','袋',6)`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('wh','p',20000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('pub-count','','开工','计个工艺','个',0,3000,JSON_ARRAY(JSON_OBJECT('id','room','name','合成小项'))),('pub-water','','防水涂料','防水','平方',0,300,JSON_ARRAY()),('pub-fiber','','丙纶','丙纶','平方',0,200,JSON_ARRAY())`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,ticket_slot,included_by_craft_id) VALUES('ca-count','ca','pub-count','开工','计个工艺','个',10000,0,1,''),('ca-water','ca','pub-water','防水涂料','防水','平方',2000,0,2,''),('ca-fiber','ca','pub-fiber','丙纶','丙纶','平方',1000,0,2,'ca-water'),('cb-count','cb','pub-count','开工','计个工艺','个',8000,0,1,'')`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
		stockFixSyncLegacyBalances(t, db)
	}
	call := func(u User, path, key string, input any) (any, error) {
		body := []byte(jsonText(input))
		req := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		req.Header.Set("Idempotency-Key", key)
		return a.mutate(req, u, body, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, path, u, body, key) })
	}
	must := func(path string, input any) any {
		t.Helper()
		v, e := call(admin, path, id(), input)
		if e != nil {
			t.Fatalf("%s: %v", path, e)
		}
		return v
	}
	scalar := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if e := db.QueryRow(q, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	read := func(orderID string, u User) Order {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		d, e := readOrderDetail(tx, orderID, u)
		if e != nil {
			t.Fatal(e)
		}
		var o Order
		if e = json.Unmarshal([]byte(jsonText(d)), &o); e != nil {
			t.Fatal(e)
		}
		o.Combinations = nil // Ordinary edits do not submit a pricing operation.
		return o
	}
	ticket := func(ticketID string) projectTicketView {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		v, e := readProjectTicketSummary(tx, ticketID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	pa := must("/api/project", Project{Company: "ca", Name: "合成甲工地", Address: "合成地址"}).(map[string]any)["id"].(string)
	pb := must("/api/project", Project{Company: "cb", Name: "合成乙工地", Address: "合成地址"}).(map[string]any)["id"].(string)
	order := Order{Company: "ca", Project: pa, Address: "合成地址", Worker: "w", Date: "2026-10-01", Lines: []Line{{CraftID: "ca-count", SubitemID: "room", Segments: []Segment{{Mode: "个", Count: 2}}}}, Materials: []Material{{Product: "p", Qty: 1000000}}, Sales: []Sale{{Product: "p", InputQty: "6", InputUnit: "aux", Price: 1000}}, DeliveryWage: 2000}
	key := id()
	v, e := call(admin, "/api/order", key, order)
	if e != nil {
		t.Fatal(e)
	}
	oid := v.(map[string]any)["id"].(string)
	if _, e = call(admin, "/api/order", key, order); e != nil {
		t.Fatal("retry", e)
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE project_id=?", pa) != 1 || scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p'") != 18000000 {
		t.Fatal("sale and consumption must debit inventory once")
	}
	if scalar("SELECT total_cents FROM orders WHERE id=?", oid) != 26000 || scalar("SELECT wage_cents FROM orders WHERE id=?", oid) != 8000 {
		t.Fatal("sale price or flexible delivery wage calculation")
	}
	stored := read(oid, admin)
	if stored.Lines[0].SubitemName != "合成小项" || stored.Lines[0].Region != "" || stored.Lines[0].ActualLocation != "" {
		t.Fatal("subitem configuration missing")
	}
	stable := stored.Lines[0].ID
	stored.Lines[0].Segments[0].Count = 3
	must("/api/order", stored)
	stored = read(oid, admin)
	if stored.Lines[0].ID != stable {
		t.Fatal("line ID must survive edit")
	}
	if _, e = call(admin, "/api/order", id(), Order{ID: oid, Version: 1, Company: "ca", Project: pa, Address: "合成地址", Worker: "w", Date: "2026-10-01"}); e == nil {
		t.Fatal("stale edit accepted")
	}
	// A changed product conversion cannot be silently applied to an existing
	// material snapshot. Restore the definition before the historical edit so
	// the saved quantum remains stable and the stock delta stays zero.
	if _, err = db.Exec("UPDATE products SET aux_ratio=12 WHERE id='p'"); err != nil {
		t.Fatal(err)
	}
	if _, err = call(admin, "/api/order", id(), stored); err == nil {
		t.Fatal("historical conversion mismatch accepted")
	}
	if _, err = db.Exec("UPDATE products SET aux_ratio=6 WHERE id='p'"); err != nil {
		t.Fatal(err)
	}
	must("/api/order", stored)
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p'") != 18000000 {
		t.Fatal("historical unit conversion must remain stable")
	}
	other := Order{Company: "cb", Project: pb, Address: "合成地址", Worker: "w", Date: "2026-10-01", Lines: []Line{{CraftID: "cb-count", Segments: []Segment{{Count: 2}}}}}
	otherID := must("/api/order", other).(map[string]any)["id"].(string)
	if scalar("SELECT total_cents FROM orders WHERE id=?", otherID) != 16000 || scalar("SELECT wage_cents FROM orders WHERE id=?", otherID) != 6000 {
		t.Fatal("same public wage must serve different company pricing")
	}
	water := Order{Company: "ca", Project: pa, Address: "合成地址", Worker: "w", Date: "2026-12-01", Lines: []Line{{CraftID: "ca-water", Price: 1, Wage: 999999, Segments: []Segment{{Length: 2, Width: 3}}}, {CraftID: "ca-fiber", Price: 999999, Wage: 999999, Segments: []Segment{{Length: 2, Width: 4}}}}}
	waterID := must("/api/order", water).(map[string]any)["id"].(string)
	if scalar("SELECT total_cents FROM orders WHERE id=?", waterID) != 20000 || scalar("SELECT wage_cents FROM orders WHERE id=?", waterID) != 3400 {
		t.Fatal("new uncombined orders must charge and pay both crafts and ignore client prices")
	}
	if scalar("SELECT COUNT(*) FROM order_lines WHERE order_id=? AND craft_id='ca-water' AND total_cents=12000 AND labor_cents=1800", waterID) != 1 || scalar("SELECT COUNT(*) FROM order_lines WHERE order_id=? AND craft_id='ca-fiber' AND total_cents=8000 AND labor_cents=1600 AND use_included_price=0", waterID) != 1 {
		t.Fatal("stored waterproof/polypropylene charge and wage rows do not match")
	}
	if scalar("SELECT COUNT(*) FROM project_tickets WHERE project_id=?", pa) != 2 {
		t.Fatal("two normal slots required")
	}
	employee := User{ID: "fill-test", Name: "合成员工", Role: "employee", Permissions: []string{"orders", "customer_prices"}, Crafts: []string{"pub-water"}}
	loaded := read(waterID, employee)
	raw := jsonText(loaded)
	if len(loaded.Lines) != 2 || strings.Contains(raw, "3400") {
		t.Fatal("all crafts visible with wage isolation", raw)
	}
	loaded.Lines = loaded.Lines[:1]
	loaded.Lines[0].Segments[0].Length = 3
	if _, e = call(employee, "/api/order", id(), loaded); e != nil {
		t.Fatal("preserve readonly line", e)
	}
	if scalar("SELECT COUNT(*) FROM order_lines WHERE order_id=?", waterID) != 2 {
		t.Fatal("readonly omitted line lost")
	}
	forged := other
	forged.Company = "ca"
	forged.Project = pa
	if _, e = call(employee, "/api/order", id(), forged); e == nil {
		t.Fatal("cross company craft authorization bypass")
	}
	forgeID := read(waterID, admin)
	forgeID.Lines[0].ID = stable
	if _, e = call(admin, "/api/order", id(), forgeID); e == nil {
		t.Fatal("line ID from another record accepted")
	}
	temp := water
	temp.Lines = []Line{{Temporary: true, Category: "防水涂料", Name: "临时绕过", Mode: "金额", Price: 100}}
	if _, e = call(employee, "/api/order", id(), temp); e == nil {
		t.Fatal("temporary permission bypass")
	}
	var first string
	if err = db.QueryRow("SELECT id FROM project_tickets WHERE project_id=? AND slot=1", pa).Scan(&first); err != nil {
		t.Fatal(err)
	}
	current := ticket(first)
	_, e = call(admin, "/api/project-ticket-action", id(), ProjectTicketActionRequest{ID: first, Version: current.Version, Action: "deliver", DeliveredAt: "2026-10-01", Recipient: "合成收票人"})
	maintenanceFaultError(t, e, 409)
	if after := ticket(first); after.Status != "草稿" || after.Version != current.Version {
		t.Fatal("retired delivery changed a draft ticket", after)
	}
	fixtureLegacyDeliveredProjectTicket(t, db, admin, first, current.Version, "2026-10-01")
	current = ticket(first)
	_, e = call(admin, "/api/project-ticket-action", id(), ProjectTicketActionRequest{ID: first, Version: current.Version, Action: "receive", Amount: current.TotalCents, ReceivedAt: "2026-10-01"})
	maintenanceFaultError(t, e, 409)
	receipt := must("/api/company-receipt", companyReceiptRequest{CompanyID: "ca", Amount: current.TotalCents, ReceivedAt: "2026-10-01", Method: "合成转账"}).(companyReceiptDetail)
	must("/api/receipt-allocation", receiptAllocationRequest{ReceiptID: receipt.Receipt.ID, Version: receipt.Receipt.Version, Reason: "合成工地全额分配", Allocations: []receiptAllocationInput{{TicketID: first, Amount: current.TotalCents}}})
	old := ticket(first)
	newWork := Order{Company: "ca", Project: pa, Address: "合成地址", Worker: "w", Date: "2026-12-02", Lines: []Line{{CraftID: "ca-count", Segments: []Segment{{Count: 1}}}}}
	if _, e = call(admin, "/api/order", id(), newWork); e == nil {
		t.Fatal("delivered normal ticket silently changed")
	}
	newWork.TicketAction, newWork.TicketID = "correct", first
	newWork.TicketVersion = old.Version
	must("/api/order", newWork)
	corrected := ticket(first)
	if corrected.Number != old.Number || corrected.ReceivedCents != old.TotalCents || corrected.OutstandingCents != 10000 || corrected.TotalCents != old.TotalCents+10000 {
		t.Fatal("correction must preserve received money", old, corrected)
	}
	if scalar("SELECT COUNT(*) FROM company_receipts WHERE company_id='ca' AND status='active'") != 1 || scalar("SELECT amount_cents FROM company_receipts WHERE id=?", receipt.Receipt.ID) != old.TotalCents {
		t.Fatal("ticket correction changed or duplicated actual cash")
	}
	if scalar("SELECT COUNT(*) FROM project_ticket_versions WHERE ticket_id=?", first) < 3 {
		t.Fatal("historical versions missing")
	}
	historyResponse := httptest.NewRecorder()
	if e = a.readProjectTicket(historyResponse, httptest.NewRequest("GET", "/api/project-ticket?id="+first+"&version="+strconv.FormatInt(old.Version, 10), nil), employee); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"\"wage_cents\"", "\"labor_cents\"", "\"delivery_wage_cents\"", "\"payment_receipts\""} {
		if strings.Contains(historyResponse.Body.String(), field) {
			t.Fatal("historical ticket leaked restricted fields", field)
		}
	}
	if _, e = call(admin, "/api/order", id(), newWork); e == nil {
		t.Fatal("stale corrected ticket version accepted")
	}
	newWork.TicketAction, newWork.TicketID = "append", ""
	appended := must("/api/order", newWork).(map[string]any)
	appendID := appended["ticket_ids"].([]string)[0]
	if ticket(appendID).TotalCents != 10000 || ticket(first).TotalCents != corrected.TotalCents {
		t.Fatal("append must only include new work")
	}
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p'") != 18000000 {
		t.Fatal("ticket actions must not debit inventory again")
	}
	current = ticket(appendID)
	payInput := ProjectTicketActionRequest{ID: appendID, Version: current.Version, Action: "deliver", DeliveredAt: "2026-12-02", Recipient: "合成收票人"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := call(admin, "/api/project-ticket-action", id(), payInput); errs <- e }()
	}
	wg.Wait()
	close(errs)
	rejected := 0
	for e := range errs {
		maintenanceFaultError(t, e, 409)
		rejected++
	}
	if after := ticket(appendID); rejected != 2 || after.Status != "草稿" || after.Version != current.Version {
		t.Fatal("retired concurrent delivery changed ticket state", rejected, after)
	}
	// A paid record is frozen, while later records remain independent wages.
	stored = read(oid, admin)
	must("/api/settle", SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: oid, Version: stored.Version}}})
	stored = read(oid, admin)
	if _, e = call(admin, "/api/order", id(), stored); e == nil {
		t.Fatal("paid construction record editable")
	}
	pure := Order{Company: "cb", Project: pb, Address: "合成地址", Worker: "w", Date: "2026-12-02", Sales: []Sale{{Product: "p", InputQty: "1", Price: 5000}}, DeliveryWage: 3000}
	pureID := must("/api/order", pure).(map[string]any)["id"].(string)
	if scalar("SELECT wage_cents FROM orders WHERE id=?", pureID) != 3000 {
		t.Fatal("pure sale flexible wage")
	}
	toCancel := read(pureID, admin)
	toCancel.Delete = true
	must("/api/order", toCancel)
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p'") != 18000000 {
		t.Fatal("sale cancellation must return stock")
	}
	pure.Note = "并发同编号送货"
	sharedKey := id()
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := call(admin, "/api/order", sharedKey, pure); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal("concurrent order retry", e)
		}
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE note='并发同编号送货'") != 1 || scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p'") != 17000000 {
		t.Fatal("concurrent record retry counted sale twice")
	}
	bad := pure
	bad.Sales[0].InputQty = "99"
	if _, e = call(admin, "/api/order", id(), bad); e == nil {
		t.Fatal("negative stock accepted")
	}
	// All-project print is complete and its balance uses actual receipts.
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	summary, e := buildProjectTicketDetail(tx, "", pa, employee)
	tx.Rollback()
	if e != nil {
		t.Fatal(e)
	}
	if summary.Ticket.ReceivedCents != old.TotalCents || summary.Ticket.OutstandingCents != summary.Ticket.TotalCents-old.TotalCents {
		t.Fatal("project summary money", summary.Ticket)
	}
	if _, err = db.Exec("UPDATE companies SET name='更名公司' WHERE id='ca'"); err != nil {
		t.Fatal(err)
	}
	var nameSnapshot string
	if err = db.QueryRow("SELECT company_name FROM project_tickets WHERE id=?", first).Scan(&nameSnapshot); err != nil || nameSnapshot != "合成分两票公司" {
		t.Fatal("ticket snapshot changed", nameSnapshot, err)
	}
}
