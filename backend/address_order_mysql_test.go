package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type addressOrderFixture struct {
	a *App
	u User
}

func newAddressOrderFixture(t *testing.T) addressOrderFixture {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('address-company','合成地址公司','split'),('address-other','合成另一公司','single')`,
		`INSERT INTO workers(id,name,crafts) VALUES('address-worker','合成师傅',JSON_ARRAY('开工','防水涂料','丙纶'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('address-warehouse','合成仓库','address-worker')`,
		`INSERT INTO products(id,name,unit) VALUES('address-product','合成商品','袋')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('address-warehouse','address-product',20000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('address-public-a','','开工','合成开工','金额',0,101),('address-public-b','','防水涂料','合成防水','金额',0,203),('address-public-c','','丙纶','合成含价丙纶','金额',0,307)`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,ticket_slot,included_by_craft_id) VALUES('address-fixed-a','address-company','address-public-a','开工','合成开工','金额',1000,0,1,''),('address-fixed-b','address-company','address-public-b','防水涂料','合成防水','金额',2000,0,2,''),('address-fixed-c','address-company','address-public-c','丙纶','合成含价丙纶','金额',9999,0,1,'address-fixed-b')`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	return addressOrderFixture{a: a, u: User{ID: "address-admin", Name: "合成管理员", Role: "admin"}}
}

func (f addressOrderFixture) call(u User, path, key string, payload any) (any, error) {
	raw := []byte(jsonText(payload))
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.Header.Set("Idempotency-Key", "address-order-"+key)
	return f.a.mutate(r, u, raw, func(tx *sql.Tx, operation string) (any, error) { return f.a.post(tx, path, u, raw, operation) })
}

func (f addressOrderFixture) must(t *testing.T, path, key string, payload any) map[string]any {
	t.Helper()
	r, err := f.call(f.u, path, key, payload)
	if err != nil {
		t.Fatal(path, err)
	}
	return r.(map[string]any)
}

func (f addressOrderFixture) order(address string) Order {
	return Order{Company: "address-company", Address: address, Date: "2026-10-03", Worker: "address-worker", Lines: []Line{{CraftID: "address-fixed-a"}}}
}

func addressOrderState(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	state := map[string]int64{}
	for _, table := range []string{"projects", "project_craft_slots", "orders", "order_lines", "order_materials", "order_sales", "project_tickets", "project_ticket_items", "project_ticket_versions", "stock_movements", "audit", "requests"} {
		var count int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		state[table] = count
	}
	var quantity int64
	if err := db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='address-warehouse' AND product_id='address-product'").Scan(&quantity); err != nil {
		t.Fatal(err)
	}
	state["stock_quantity"] = quantity
	var version int64
	if err := db.QueryRow("SELECT version FROM stock_balances WHERE warehouse_id='address-warehouse' AND product_id='address-product'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	state["stock_version"] = version
	return state
}

func TestAddressOrderCreationMySQL(t *testing.T) {
	f := newAddressOrderFixture(t)
	o := f.order(" \t合成首次地址\u3000")
	o.Lines = append(o.Lines, Line{CraftID: "address-fixed-b", Price: 1, Wage: 1}, Line{CraftID: "address-fixed-c"})
	o.Materials = []Material{{Product: "address-product", InputQty: "1"}}
	o.Sales = []Sale{{Product: "address-product", InputQty: "2", Price: 1500}}
	o.SalesTicketSlot, o.DeliveryWage = 2, 55
	result := f.must(t, "/api/order", "first", o)
	projectID, orderID := result["project_id"].(string), result["id"].(string)
	if projectID == "" || result["project_name"] != "合成首次地址" || result["project_number"] == "" {
		t.Fatal("new order response omitted its project", result)
	}
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM projects WHERE address='合成首次地址' AND name='合成首次地址' AND ticket_mode_snapshot='split'`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM orders WHERE address='合成首次地址' AND project_id<>'' AND total_cents=15999 AND wage_cents=666 AND ticket_managed=1`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM project_craft_slots`, 3)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM project_tickets WHERE (slot=1 AND total_cents=10999) OR (slot=2 AND total_cents=5000)`, 2)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM stock_balances WHERE qty_micro=17000000`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM stock_movements WHERE delta_micro=-3000000`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM audit WHERE action='新增工地项目'`, 1)
	before := addressOrderState(t, f.a.db)
	retry := f.must(t, "/api/order", "first", o)
	if retry["id"] != orderID || retry["project_id"] != projectID || !reflect.DeepEqual(before, addressOrderState(t, f.a.db)) {
		t.Fatal("retry duplicated project/stock/ticket/audit")
	}
	o.Address = "合成不同内容"
	_, err := f.call(f.u, "/api/order", "first", o)
	maintenanceFaultError(t, err, 409)
	if !reflect.DeepEqual(before, addressOrderState(t, f.a.db)) {
		t.Fatal("changed idempotent payload changed records")
	}
	// A later company edit cannot move this project's ticket slots. New orders
	// use server prices and full wages; old included configuration is not a waiver.
	maintenanceFaultExec(t, f.a.db, `UPDATE companies SET ticket_mode='single' WHERE id='address-company'`)
	maintenanceFaultExec(t, f.a.db, `UPDATE crafts SET ticket_slot=1,included_by_craft_id='address-fixed-a' WHERE id='address-fixed-c'`)
	maintenanceFaultExec(t, f.a.db, `UPDATE crafts SET ticket_slot=1 WHERE id='address-fixed-b'`)
	continued := f.order("合成首次地址")
	continued.Project = projectID
	continued.Lines = []Line{{CraftID: "address-fixed-b"}, {CraftID: "address-fixed-c"}}
	f.must(t, "/api/order", "continued", continued)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM projects`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM project_tickets WHERE slot=2 AND total_cents=7000`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM order_lines WHERE craft_id='address-fixed-c' AND included_by_craft_id='' AND use_included_price=0 AND total_cents=9999 AND labor_cents=307`, 2)
	// Fresh pure delivery shares the same atomic address flow and freezes the
	// current single-ticket configuration, forcing sale slot 1.
	sale := f.order("合成纯售料地址")
	sale.Lines = nil
	sale.Sales = []Sale{{Product: "address-product", InputQty: "1", Price: 700}}
	sale.SalesTicketSlot, sale.DeliveryWage = 2, 50
	f.must(t, "/api/order", "sale-only", sale)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM orders WHERE address='合成纯售料地址' AND total_cents=700 AND wage_cents=50 AND sales_ticket_slot=1`, 1)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM projects WHERE address='合成纯售料地址' AND ticket_mode_snapshot='single'`, 1)
}

func TestAddressOrderRollbackMySQL(t *testing.T) {
	f := newAddressOrderFixture(t)
	before := addressOrderState(t, f.a.db)
	cases := []struct {
		name string
		code int
		u    User
		edit func(*Order)
	}{
		{"no-orders-permission", 403, User{ID: "no-orders", Role: "employee"}, func(o *Order) {}},
		{"no-fixed-craft-permission", 403, User{ID: "limited", Role: "employee", Permissions: []string{"orders"}}, func(o *Order) {}},
		{"bad-date", 400, f.u, func(o *Order) { o.Date = "invalid" }},
		{"bad-address", 400, f.u, func(o *Order) { o.Address = strings.Repeat("地", 301) }},
		{"blank-address", 400, f.u, func(o *Order) { o.Address = " \t\u3000" }},
		{"unknown-company", 400, f.u, func(o *Order) { o.Company = "missing" }},
		{"bad-worker", 400, f.u, func(o *Order) { o.Worker = "missing" }},
		{"no-content", 400, f.u, func(o *Order) { o.Lines = nil }},
		{"bad-sale-slot", 400, f.u, func(o *Order) { o.SalesTicketSlot = 3 }},
		{"bad-ticket-action", 400, f.u, func(o *Order) { o.TicketAction = "invalid" }},
		{"bad-correction", 400, f.u, func(o *Order) { o.TicketAction, o.TicketID, o.TicketVersion = "correct", "missing", 1 }},
		{"new-append", 400, f.u, func(o *Order) { o.TicketAction = "append" }},
		{"new-stale-ticket", 400, f.u, func(o *Order) { o.TicketID, o.TicketVersion = "old-ticket", 1 }},
		{"bad-craft", 400, f.u, func(o *Order) { o.Lines = []Line{{CraftID: "missing"}} }},
		{"no-stock", 409, f.u, func(o *Order) { o.Sales = []Sale{{Product: "address-product", InputQty: "21", Price: 1000}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := f.order("合成失败地址-" + tc.name)
			tc.edit(&o)
			_, err := f.call(tc.u, "/api/order", tc.name, o)
			maintenanceFaultError(t, err, tc.code)
			if !reflect.DeepEqual(before, addressOrderState(t, f.a.db)) {
				t.Fatal("failed operation left project/snapshot/order/stock/ticket/audit/request")
			}
		})
	}
	// Fail after inventory, tickets, and their versions were written, proving
	// that project creation participates in the outer transaction as well.
	maintenanceFaultExec(t, f.a.db, `CREATE TRIGGER synthetic_address_audit_failure BEFORE INSERT ON audit FOR EACH ROW BEGIN IF NEW.action='保存施工记录' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic late failure'; END IF; END`)
	o := f.order("合成晚失败地址")
	o.Sales = []Sale{{Product: "address-product", InputQty: "1", Price: 500}}
	_, err := f.call(f.u, "/api/order", "late-failure", o)
	maintenanceFaultError(t, err, 0)
	if !reflect.DeepEqual(before, addressOrderState(t, f.a.db)) {
		t.Fatal("late failure leaked transaction records")
	}
	maintenanceFaultExec(t, f.a.db, `DROP TRIGGER synthetic_address_audit_failure`)
	f.must(t, "/api/order", "late-failure", o)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM projects", 1)
}

func TestAddressProjectConflictsMySQL(t *testing.T) {
	f := newAddressOrderFixture(t)
	for i, status := range []string{"进行中", "已停用", "已结项"} {
		address := "合成状态地址-" + status
		p := f.must(t, "/api/project", "status-create-"+status, Project{Company: "address-company", Name: "合成工地", Address: address})
		if i != 0 {
			if _, err := f.a.db.Exec("UPDATE projects SET status=? WHERE id=?", status, p["id"]); err != nil {
				t.Fatal(err)
			}
		}
		_, err := f.call(f.u, "/api/order", "status-duplicate-"+status, f.order(" \t"+address+"\u3000"))
		maintenanceFaultError(t, err, 409)
		if !strings.Contains(err.Error(), "查找并选择原地址") || !strings.Contains(err.Error(), "启用") {
			t.Fatal("address conflict was not actionable", err)
		}
	}
	longAddress := strings.Repeat("地😀", 150)
	long := f.must(t, "/api/order", "long-address", f.order(longAddress))
	var name, address string
	if err := f.a.db.QueryRow("SELECT name,address FROM projects WHERE id=?", long["project_id"]).Scan(&name, &address); err != nil || name != strings.Repeat("地😀", 100) || address != longAddress {
		t.Fatal("300-rune address or 200-rune display name changed", name, address, err)
	}
	q := "/api/projects?company_id=address-company&q=" + url.QueryEscape(longAddress)
	w := httptest.NewRecorder()
	if err := f.a.listProjects(w, httptest.NewRequest("GET", q, nil), f.u); err != nil {
		t.Fatal("complete 300-rune address cannot be searched", err)
	}
	var listed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || listed.Total != 1 {
		t.Fatal("complete address lookup did not return its project", err, w.Body.String())
	}
	// Exact matching must not fold case, accents, punctuation, or prefixes.
	for _, exact := range []string{"Case地址", "case地址", "Case地址-另一户"} {
		f.must(t, "/api/order", "exact-"+exact, f.order(exact))
	}
	other := f.order("Case地址")
	other.Company, other.Lines = "address-other", []Line{{Category: "开工", Name: "合成临时", Mode: "金额", Price: 100, Temporary: true}}
	f.must(t, "/api/order", "other-company", other)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM projects WHERE BINARY address=BINARY 'Case地址'`, 2)
	// The manual menu and edits share the address serialization rule.
	_, err := f.call(f.u, "/api/project", "manual-duplicate", Project{Company: "address-company", Name: "合成重名", Address: "Case地址"})
	maintenanceFaultError(t, err, 409)
	_, err = f.call(f.u, "/api/project", "edit-duplicate", Project{ID: long["project_id"].(string), Company: "address-company", Name: "合成长址", Address: "Case地址", Version: 1})
	maintenanceFaultError(t, err, 409)
	// Restored history may already contain duplicate exact addresses. The new
	// flow neither picks one implicitly nor merges or disables those records.
	maintenanceFaultExec(t, f.a.db, `INSERT INTO projects(id,number,company_id,name,address,created_by,ticket_mode_snapshot) VALUES('duplicate-history-a','DUP-A','address-company','合成历史甲','合成历史重复址','test','single'),('duplicate-history-b','DUP-B','address-company','合成历史乙','合成历史重复址','test','split')`)
	_, err = f.call(f.u, "/api/order", "historical-duplicates", f.order("合成历史重复址"))
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, f.a.db, `SELECT COUNT(*) FROM projects WHERE address='合成历史重复址'`, 2)
	// A maintenance action which keeps the same address must still work on
	// such history; otherwise these records could never be reopened/renamed.
	f.must(t, "/api/project", "historical-rename", Project{ID: "duplicate-history-a", Company: "address-company", Name: "合成历史甲更名", Address: "合成历史重复址", Version: 1})
}

func TestAddressProjectConcurrentMySQL(t *testing.T) {
	f := newAddressOrderFixture(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errors := make([]error, 2)
	for i := range errors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errors[i] = f.call(f.u, "/api/order", "concurrent-"+string(rune('a'+i)), f.order("合成并发首址"))
		}(i)
	}
	close(start)
	wg.Wait()
	success, conflict := 0, 0
	for _, err := range errors {
		if err == nil {
			success++
		} else {
			maintenanceFaultError(t, err, 409)
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent first submissions were not serialized", errors)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM projects", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM orders", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM project_tickets", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM requests", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM audit WHERE action='新增工地项目'", 1)
	// Both public entry points, including a simultaneous address edit, must
	// participate in the same serialization boundary.
	for _, kind := range []string{"manual-create", "address-edit"} {
		t.Run(kind, func(t *testing.T) {
			address := "合成入口竞争址-" + kind
			p := Project{Company: "address-company", Name: "合成维护工地", Address: address}
			if kind == "address-edit" {
				original := f.must(t, "/api/project", "original-before-race", Project{Company: p.Company, Name: p.Name, Address: "合成改址前"})
				p.ID, p.Version = original["id"].(string), 1
			}
			begin := make(chan struct{})
			var pair sync.WaitGroup
			outcomes := make([]error, 2)
			pair.Add(2)
			go func() {
				defer pair.Done()
				<-begin
				_, outcomes[0] = f.call(f.u, "/api/order", "race-order-"+kind, f.order(address))
			}()
			go func() {
				defer pair.Done()
				<-begin
				_, outcomes[1] = f.call(f.u, "/api/project", "race-project-"+kind, p)
			}()
			close(begin)
			pair.Wait()
			wins := 0
			for _, err := range outcomes {
				if err == nil {
					wins++
				} else {
					maintenanceFaultError(t, err, 409)
				}
			}
			if wins != 1 {
				t.Fatal("manual/order address race was not serialized", outcomes)
			}
			var exact int
			if err := f.a.db.QueryRow("SELECT COUNT(*) FROM projects WHERE company_id='address-company' AND BINARY address=BINARY ?", address).Scan(&exact); err != nil || exact != 1 {
				t.Fatal("manual/order address race duplicated project", exact, err)
			}
		})
	}
}
