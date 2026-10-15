package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSubitemWorkersWorkflowMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	admin := User{ID: "subitem-admin", Name: "合成管理员", Role: "admin"}
	for _, q := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('co','合成小项公司','split')`,
		`INSERT INTO workers(id,name,crafts) VALUES('a','合成主卫师傅',JSON_ARRAY('防水涂料')),('b','合成厨房师傅',JSON_ARRAY('防水涂料','丙纶')),('c','合成送货人',JSON_ARRAY())`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('wh','合成送货仓库','c')`,
		`INSERT INTO products(id,name,unit) VALUES('p','合成材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('wh','p',20000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('water-src','','防水涂料','合成防水','平方',0,300,JSON_ARRAY(JSON_OBJECT('id','master','name','合成主卫'),JSON_OBJECT('id','kitchen','name','合成厨房'))),('fiber-src','','丙纶','合成丙纶','平方',0,200,JSON_ARRAY())`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,ticket_slot,included_by_craft_id) VALUES('water','co','water-src','防水涂料','合成防水','平方',2000,0,2,''),('fiber','co','fiber-src','丙纶','合成丙纶','平方',1000,0,2,'water')`,
	} {
		maintenanceFaultExec(t, a.db, q)
	}
	call := func(user User, path, key string, v any) (any, error) {
		raw := []byte(jsonText(v))
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		req.Header.Set("Idempotency-Key", "subitem-workers-"+key)
		return a.mutate(req, user, raw, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, path, user, raw, key) })
	}
	must := func(path string, v any) any {
		t.Helper()
		out, err := call(admin, path, id(), v)
		if err != nil {
			t.Fatal(path, err)
		}
		return out
	}
	read := func(orderID string, user User) orderDetailView {
		t.Helper()
		tx, err := a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		d, err := readOrderDetail(tx, orderID, user)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	wageRows := func(worker, paid string) orderListPage {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/orders?view=wages&month=2026-10&worker_id="+worker+"&wage_paid="+paid, nil)
		out := httptest.NewRecorder()
		if err := a.listOrders(out, req, admin); err != nil {
			t.Fatal(err)
		}
		var page orderListPage
		if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	order := Order{Company: "co", Address: "合成多工人工地", Date: "2026-10-04", Worker: "c", Lines: []Line{
		{CraftID: "water", SubitemID: "master", Worker: "a", WorkerName: "伪造姓名", Segments: []Segment{{Length: 2, Width: 3}}},
		{CraftID: "water", SubitemID: "kitchen", Worker: "b", Segments: []Segment{{Length: 2, Width: 2}}},
		{CraftID: "fiber", Worker: "b", Segments: []Segment{{Length: 2, Width: 4}}},
	}, Materials: []Material{{Product: "p", Qty: 1000000}}, Sales: []Sale{{Product: "p", InputUnit: "base", InputQty: "2", Price: 1500}}, DeliveryWage: 500, SalesTicketSlot: 1}
	out := must("/api/order", order).(map[string]any)
	orderID := out["id"].(string)
	d := read(orderID, admin)
	if d.TotalCents != 31000 || *d.WageCents != 5100 || len(d.Lines) != 3 || len(*d.WageRecords) != 3 {
		t.Fatalf("amounts/allocations wrong: %+v", d)
	}
	if d.Lines[0].WorkerID != "a" || d.Lines[0].WorkerName != "合成主卫师傅" || *d.Lines[0].Labor != 1800 || *d.Lines[1].Labor != 1200 || d.Lines[2].Total != 8000 || *d.Lines[2].Labor != 1600 {
		t.Fatalf("subitem assignment / full construction wages lost: %+v", d.Lines)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances WHERE warehouse_id='wh' AND qty_micro=17000000", 1)
	rows := wageRows("", "")
	if rows.Total != 3 || *rows.WageCents != 5100 {
		t.Fatal("wage list duplicated or missing", rows)
	}
	account := map[string]orderListItem{}
	for _, v := range rows.Rows {
		account[v.WorkerID] = v
		if v.OrderID != orderID || v.ID == orderID {
			t.Fatal("wage identity is not distinct", v)
		}
	}
	if *account["a"].WageCents != 1800 || *account["b"].WageCents != 2800 || *account["c"].WageCents != 500 {
		t.Fatal("wages assigned to wrong worker", account)
	}
	if !strings.Contains(d.WorkerNames, "合成主卫师傅") || !strings.Contains(d.WorkerNames, "合成厨房师傅") {
		t.Fatal("summary omitted actual workers", d.WorkerNames)
	}
	customer := read(orderID, User{ID: "no-wage", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"water-src", "fiber-src"}})
	raw := jsonText(customer)
	for _, key := range []string{"wage_cents", "labor_cents", "wage_paid", "wage_records", "delivery_wage_cents"} {
		if strings.Contains(raw, key) {
			t.Fatal("customer leaked wages", key)
		}
	}
	// Ticket 1 owns sales/delivery only; ticket 2 owns actual construction wages.
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	tickets, err := projectTicketIDs(tx, d.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	var ticketWages int64
	for _, ticketID := range tickets {
		detail, err := buildProjectTicketDetail(tx, ticketID, "", admin)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range detail.Orders {
			var sum int64
			for _, w := range *o.WageRecords {
				sum += w.WageCents
			}
			if sum != *o.WageCents {
				t.Fatal("ticket worker total disagrees", o)
			}
			ticketWages += sum
		}
	}
	tx.Rollback()
	if ticketWages != 5100 {
		t.Fatal("ticket printing duplicated wages", ticketWages)
	}
	// A forged/unknown/unskilled worker must fail before any inventory movement.
	for _, worker := range []string{"c", "missing"} {
		bad := order
		bad.Address = "合成无权" + worker
		bad.Lines = append([]Line{}, order.Lines...)
		bad.Lines[0].Worker = worker
		if _, err := call(admin, "/api/order", id(), bad); err == nil {
			t.Fatal("invalid line worker accepted", worker)
		}
	}
	noCraft := User{ID: "limited", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"fiber-src"}}
	if _, err := call(noCraft, "/api/order", id(), order); err == nil {
		t.Fatal("account craft permission bypassed")
	}
	// Partial payment locks the complete construction record, while unpaid A/C
	// remain listed and counted by the dashboard. Idempotent retries add no event.
	p := SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: account["b"].ID, Version: account["b"].Version}}}
	if _, err := call(admin, "/api/wage-settlement", "pay-a", p); err != nil {
		t.Fatal(err)
	}
	if _, err := call(admin, "/api/wage-settlement", "pay-a", p); err != nil {
		t.Fatal("retry failed", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM wage_settlements WHERE kind='pay'", 1)
	if rows = wageRows("", "0"); rows.Total != 2 || *rows.WageCents != 2300 {
		t.Fatal("partial pay hid unpaid wages", rows)
	}
	if rows = wageRows("b", "1"); rows.Total != 1 {
		t.Fatal("worker/state filter wrong", rows)
	}
	d = read(orderID, admin)
	if d.Editable {
		t.Fatal("partial wage payment did not lock edit")
	}
	cancel := Order{ID: d.ID, Version: d.Version, Company: d.CompanyID, Project: d.ProjectID, Address: d.Address, Date: d.WorkDate, Worker: d.WorkerID, Delete: true}
	if _, err := call(admin, "/api/order", id(), cancel); err == nil {
		t.Fatal("partially paid order could cancel")
	}
	// The real snapshot writer restores partial payment, row names, versions and
	// wage events to a different generated database without regrouping amounts.
	target, _ := maintenanceFaultDB(t)
	backup, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	if err = target.restore(filepath.Join(os.Getenv("BACKUP_DIR"), backup)); err != nil {
		t.Fatal(err)
	}
	tx, err = a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	sourceWages, err := readWorkerWages(tx, orderID)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	tx, err = target.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	restoredWages, err := readWorkerWages(tx, orderID)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sourceWages, restoredWages) {
		t.Fatal("restored mixed wages changed", sourceWages, restoredWages)
	}
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM wage_settlements WHERE kind='pay' AND worker_id='b' AND amount_cents=2800", 1)
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_lines WHERE worker_id='b' AND worker_name='合成厨房师傅'", 2)
	workbench := httptest.NewRecorder()
	if err = a.dashboard(workbench, httptest.NewRequest("GET", "/api/dashboard?month=2026-10", nil), admin); err != nil {
		t.Fatal(err)
	}
	var stats struct {
		Unpaid int64 `json:"unpaid_wage_cents"`
	}
	if err = json.Unmarshal(workbench.Body.Bytes(), &stats); err != nil || stats.Unpaid != 2300 {
		t.Fatal("dashboard hid unissued worker wages", stats, err)
	}
	// Old whole-record settlement must explicitly reject mixed-worker records.
	if _, err := call(admin, "/api/settle", id(), SettlementRequest{Action: "undo-pay", Orders: []SettlementOrder{{ID: d.ID, Version: d.Version}}}); err == nil {
		t.Fatal("old settlement could pay all workers")
	}
	paidB := wageRows("b", "").Rows[0]
	must("/api/wage-settlement", SettlementRequest{Action: "undo-pay", Note: "合成撤销", Orders: []SettlementOrder{{ID: paidB.ID, Version: paidB.Version}}})
	rows = wageRows("", "")
	all := []SettlementOrder{}
	for _, v := range rows.Rows {
		all = append(all, SettlementOrder{ID: v.ID, Version: v.Version})
	}
	conflict := append([]SettlementOrder{}, all...)
	conflict[1].Version++
	if _, err := call(admin, "/api/wage-settlement", id(), SettlementRequest{Action: "pay", Orders: conflict}); err == nil {
		t.Fatal("stale batch accepted")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE wage_paid=1", 0)
	before := read(orderID, admin).Version
	must("/api/wage-settlement", SettlementRequest{Action: "pay", Orders: all})
	if after := read(orderID, admin).Version; after != before+1 {
		t.Fatal("same-order batch advanced order multiple times", before, after)
	}
	rows = wageRows("", "")
	all = nil
	for _, v := range rows.Rows {
		all = append(all, SettlementOrder{ID: v.ID, Version: v.Version})
	}
	must("/api/wage-settlement", SettlementRequest{Action: "undo-pay", Orders: all})
	// Concurrent independent keys cannot pay the same account twice.
	aRow := wageRows("b", "").Rows[0]
	pay := SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: aRow.ID, Version: aRow.Version}}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := call(admin, "/api/wage-settlement", id(), pay); errs <- e }()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent pay results", success)
	}
	aRow = wageRows("b", "").Rows[0]
	must("/api/wage-settlement", SettlementRequest{Action: "undo-pay", Orders: []SettlementOrder{{ID: aRow.ID, Version: aRow.Version}}})
	// Worker names remain snapshots after renaming; no anonymous draft change
	// can rewrite a readonly fiber assignment.
	maintenanceFaultExec(t, a.db, "UPDATE workers SET name='合成改名' WHERE id='a'")
	d = read(orderID, admin)
	if d.Lines[0].WorkerName != "合成主卫师傅" {
		t.Fatal("history changed after rename")
	}
	// Wage CSV and monthly archives each contain three workers once.
	req := httptest.NewRequest("GET", "/api/export?view=wages&month=2026-10", nil)
	rec := httptest.NewRecorder()
	if err := a.exportFiltered(rec, req, admin); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"合成主卫师傅", "合成厨房师傅", "合成送货人"} {
		if strings.Count(rec.Body.String(), name) != 1 {
			t.Fatal("wage CSV attribution wrong", name)
		}
	}
	files, err := a.archive("2026-10", "co", false)
	if err != nil {
		t.Fatal(err)
	}
	wageFile := false
	for _, f := range files {
		if f.Kind == "工资明细" {
			wageFile = true
			if f.Count != 3 || f.Amount != 5100 {
				t.Fatal("archive attribution duplicated", f)
			}
			b, err := os.ReadFile(filepath.Join(os.Getenv("BACKUP_DIR"), "monthly", f.File))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"合成主卫师傅", "合成厨房师傅", "合成送货人"} {
				if strings.Count(string(b), name) != 1 {
					t.Fatal("archive missing worker", name)
				}
			}
		}
	}
	if !wageFile {
		t.Fatal("wage archive missing")
	}
	// A limited account may edit water rows, but cannot submit the readonly
	// fiber assignment or redirect the shared material/default worker.
	d = read(orderID, admin)
	editable := Order{ID: d.ID, Version: d.Version, Company: d.CompanyID, Project: d.ProjectID, Address: d.Address, Date: d.WorkDate, Worker: d.WorkerID, DeliveryWage: *d.DeliveryWage, Sales: d.Sales, SalesTicketSlot: d.SalesTicketSlot}
	for _, m := range d.Materials {
		editable.Materials = append(editable.Materials, Material{Product: m.ProductID, Warehouse: m.WarehouseID, Qty: m.Qty})
	}
	tx, err = a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := readStoredOrderLines(tx, d.ID)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range stored {
		if l.CraftID == "water" {
			editable.Lines = append(editable.Lines, l)
		}
	}
	limited := User{ID: "water-only", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"water-src"}}
	forged := editable
	forged.Lines = append(append([]Line{}, editable.Lines...), stored[2])
	forged.Lines[2].Worker = "a"
	if _, err = call(limited, "/api/order", id(), forged); err == nil {
		t.Fatal("readonly fiber worker was editable")
	}
	redirect := editable
	redirect.Worker = "a"
	if _, err = call(limited, "/api/order", id(), redirect); err == nil {
		t.Fatal("readonly row shared material worker could change")
	}
	editable.Lines[0].Segments[0].Width = 4
	if _, err = call(limited, "/api/order", id(), editable); err != nil {
		t.Fatal("allowed water edit failed", err)
	}
	d = read(orderID, admin)
	fiberFound := false
	for _, l := range d.Lines {
		if l.CraftID == "fiber" {
			fiberFound = true
			if l.WorkerID != "b" || l.WorkerName != "合成厨房师傅" || *l.Labor != 1600 {
				t.Fatal("readonly fiber attribution changed", l)
			}
		}
	}
	if !fiberFound {
		t.Fatal("readonly fiber line was omitted")
	}
	// Move the only A row to B. Old unpaid account disappears, its historical
	// pay/undo events stay, and no stale A account can be issued again.
	editable.Version = d.Version
	editable.Lines[0].Worker = "b"
	if _, err = call(limited, "/api/order", id(), editable); err != nil {
		t.Fatal("worker reassignment failed", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='a'", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM wage_settlements WHERE worker_id='a'", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='b' AND wage_cents=5200", 1)
	// A late account failure rolls the order, project, stock and wage accounts back.
	maintenanceFaultExec(t, a.db, `CREATE TRIGGER subitem_account_fault BEFORE INSERT ON order_worker_wages FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic account fault'`)
	bad := order
	bad.Address = "合成事务回滚"
	if _, err := call(admin, "/api/order", id(), bad); err == nil {
		t.Fatal("late account failure succeeded")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders WHERE address='合成事务回滚'", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM projects WHERE address='合成事务回滚'", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances WHERE qty_micro=17000000", 1)
	maintenanceFaultExec(t, a.db, "DROP TRIGGER subitem_account_fault")
	// Pure construction supports explicit line workers without a header default.
	pure := Order{Company: "co", Address: "合成无默认人", Date: order.Date, Lines: append([]Line{}, order.Lines[:2]...)}
	pureID := must("/api/order", pure).(map[string]any)["id"].(string)
	pureD := read(pureID, admin)
	if pureD.WorkerID != "" || len(*pureD.WageRecords) != 2 {
		t.Fatal("header incorrectly required or allocated", pureD)
	}
	d = read(orderID, admin)
	cancel.Version = d.Version
	must("/api/order", cancel)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances WHERE qty_micro=20000000", 1)
}

func TestSubitemWorkersLegacyMigrationMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name) VALUES('legacy-co','合成旧公司')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,total_cents,wage_cents,wage_paid,version,created_by) VALUES('legacy','合成旧单','legacy-co','合成旧地址','2026-10-01','deleted-worker','合成旧姓名','',1000,987,1,7,'admin')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES('legacy-line','legacy','开工','合成旧行','金额',JSON_ARRAY(),1000000,1000,111,1000,111,1)`)
	maintenanceFaultExec(t, a.db, "DELETE FROM schema_migrations WHERE version=434")
	if err := initSchema(a.db); err != nil {
		t.Fatal(err)
	}
	if err := initSchema(a.db); err != nil {
		t.Fatal("migration restart", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE order_id='legacy' AND worker_id='deleted-worker' AND worker_name='合成旧姓名' AND wage_cents=987 AND wage_paid=1 AND version=7 AND legacy=1", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE worker_id='deleted-worker' AND worker_name='合成旧姓名' AND labor_cents=111", 1)
	// A damaged mixed-person record without accounts must never fall back to
	// attributing every wage to the historical header.
	maintenanceFaultExec(t, a.db, "DELETE FROM order_worker_wages WHERE order_id='legacy'")
	maintenanceFaultExec(t, a.db, "UPDATE order_lines SET worker_id='different-worker' WHERE id='legacy-line'")
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = validateWorkerAccounts(tx); err == nil {
		t.Fatal("missing mixed-worker accounts silently accepted")
	}
}

func TestFreshSchemaMigrationThrough459MySQL(t *testing.T) {
	a, _ := maintenanceFaultDBWithoutSchema(t)
	if err := initSchema(a.db); err != nil {
		t.Fatal("initialize fresh schema", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=434", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=459", 1)

	var migrationCount int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := initSchema(a.db); err != nil {
		t.Fatal("repeat fresh-schema initialization", err)
	}
	var repeatedMigrationCount int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&repeatedMigrationCount); err != nil {
		t.Fatal(err)
	}
	if repeatedMigrationCount != migrationCount {
		t.Fatalf("repeated initialization changed migration count: before=%d after=%d", migrationCount, repeatedMigrationCount)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=434", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=459", 1)
}

func TestSubitemWorkersLegacySnapshotMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	file := maintenanceFaultSnapshot(t, dir,
		snapshotRecord{Format: "tegu-go-v1"},
		snapshotRecord{Table: "companies", Row: map[string]any{"id": "co", "name": "合成旧公司"}},
		snapshotRecord{Table: "orders", Row: map[string]any{"id": "old", "number": "OLD", "company_id": "co", "address": "合成旧地址", "work_date": "2026-10-01", "worker_id": "old-worker", "worker_name": "合成旧快照工人", "note": "", "created_by": "old-admin", "wage_cents": 987, "wage_paid": 1, "version": 5}},
		snapshotRecord{Table: "order_lines", Row: map[string]any{"id": "old-line", "order_id": "old", "category": "开工", "name": "合成旧项", "mode": "金额", "segments": "[]", "qty_micro": 1000000, "price_cents": 1000, "wage_cents": 111, "total_cents": 1000, "labor_cents": 111, "temporary": 1}},
		snapshotRecord{End: true, Counts: map[string]int{"companies": 1, "orders": 1, "order_lines": 1}},
	)
	if err := a.restore(file); err != nil {
		t.Fatal("old wage snapshot restore", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='old-worker' AND worker_name='合成旧快照工人' AND wage_cents=987 AND wage_paid=1 AND version=5 AND legacy=1", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE worker_id='old-worker' AND worker_name='合成旧快照工人' AND labor_cents=111", 1)
	if err := initSchema(a.db); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", 1)
}

func TestSubitemWorkerWagePaginationMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name) VALUES('co','合成分页公司')`)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		orderID := fmt.Sprintf("synthetic-order-%04d", i)
		if _, err = tx.Exec(`INSERT INTO orders(id,number,company_id,company_name,address,work_date,note,wage_cents,created_by) VALUES(?,?,'co','合成分页公司','合成地址','2026-10-04','',200,'test')`, orderID, orderID); err != nil {
			t.Fatal(err)
		}
		for _, worker := range []string{"a", "b"} {
			if _, err = tx.Exec(`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary,worker_id,worker_name) VALUES(?,?,'开工','合成分页施工','金额',JSON_ARRAY(),1000000,0,100,0,100,1,?,?)`, "line-"+orderID+"-"+worker, orderID, worker, "合成工人"+worker); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents) VALUES(?,?,?,?,100)`, orderID+"-"+worker, orderID, worker, "合成工人"+worker); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	admin := User{Role: "admin", ID: "pagination-admin"}
	seen := map[string]bool{}
	for page := 1; page <= 11; page++ {
		out := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/orders?view=wages&month=2026-10&page=%d", page), nil)
		if err = a.listOrders(out, req, admin); err != nil {
			t.Fatal(err)
		}
		var v orderListPage
		if err = json.Unmarshal(out.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Total != 1002 || *v.WageCents != 100200 {
			t.Fatal("complete wage totals wrong", v.Total, *v.WageCents)
		}
		for _, row := range v.Rows {
			if seen[row.ID] {
				t.Fatal("page duplicated account", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 1002 {
		t.Fatal("page omitted account", len(seen))
	}
	recorder := httptest.NewRecorder()
	if err = a.exportFiltered(recorder, httptest.NewRequest("GET", "/api/export?view=wages&month=2026-10", nil), admin); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(recorder.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 1003 {
		t.Fatal("complete wage CSV omitted rows", len(rows), err)
	}
	tx, err = a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cursor := ""
	count := 0
	total := int64(0)
	for {
		batch, err := archiveWorkerWages(tx, "o.work_date>=? AND o.work_date<? AND o.status<>'已取消'", []any{"2026-10-01", "2026-11-01", "co", cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, v := range batch {
			cursor = v.ID
			count++
			total += v.WageCents
		}
	}
	if count != 1002 || total != 100200 {
		t.Fatal("500-row archive cursor omitted mixed workers", count, total)
	}
}

// Keep the established whole-record wage settlement and financial reporting
// cases meaningful without also running unrelated large-scale fixture modules.
func TestSubitemWorkersLegacyConsumersMySQL(t *testing.T) {
	t.Run("single-worker-settlement", func(t *testing.T) { a, _ := maintenanceFaultDB(t); settlementAcceptance(t, a) })
	t.Run("financial-reporting", func(t *testing.T) {
		a, _ := maintenanceFaultDB(t)
		t.Setenv("BACKUP_DIR", t.TempDir())
		financialReportingAcceptance(t, a)
	})
}
