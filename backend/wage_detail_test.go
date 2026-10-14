package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func wageDetailHTTPFixture(t *testing.T) (*App, *http.Cookie, *http.Cookie, *http.Cookie) {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	a.attempts = map[string][]time.Time{}
	t.Setenv("ADMIN_INITIAL_PASSWORD", "synthetic-wage-detail-only")
	if err := a.bootstrap(); err != nil {
		t.Fatal(err)
	}
	login := func(username string) *http.Cookie {
		t.Helper()
		w := accountIdentityRequest(a, "POST", "/api/login", map[string]string{"username": username, "password": "synthetic-wage-detail-only"}, nil)
		if w.Code != 200 {
			t.Fatal("fixture login", w.Code, w.Body.String())
		}
		return w.Result().Cookies()[0]
	}
	admin := login("admin")
	for _, tc := range []struct{ username, permission string }{{"wage-viewer", "wages"}, {"construction-viewer", "orders"}} {
		w := accountIdentityRequest(a, "POST", "/api/user", map[string]any{"username": tc.username, "name": "合成查看人员", "password": "synthetic-wage-detail-only", "role": "employee", "active": true, "permissions": []string{tc.permission}, "crafts": []string{}}, admin)
		if w.Code != 200 {
			t.Fatal("fixture user", w.Code, w.Body.String())
		}
	}
	return a, admin, login("wage-viewer"), login("construction-viewer")
}

func wageDetailHTTPRead(t *testing.T, a *App, cookie *http.Cookie, accountID string) (wageDetailView, string) {
	t.Helper()
	w := accountIdentityRequest(a, "GET", "/api/wage?id="+url.QueryEscape(accountID), nil, cookie)
	if w.Code != 200 {
		t.Fatal("wage detail", w.Code, w.Body.String())
	}
	var d wageDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"price_cents", "total_cents", "amount_cents", "materials", "sales", "wage_records", "payment_receipts", "warehouse_id", "editable"} {
		if strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Fatal("wage DTO includes unrelated charge/material/editing fields", field)
		}
	}
	return d, w.Body.String()
}

func TestWorkerWageDetailHTTPMySQL(t *testing.T) {
	a, admin, wageViewer, constructionViewer := wageDetailHTTPFixture(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('detail-co','合成公司快照')`,
		`INSERT INTO workers(id,name,crafts) VALUES('detail-a','合成主卫甲',JSON_ARRAY('防水涂料')),('detail-b','合成厨房乙',JSON_ARRAY('防水涂料')),('detail-c','合成送货丙',JSON_ARRAY())`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('detail-wh','合成送货仓','detail-c')`,
		`INSERT INTO products(id,name,unit) VALUES('detail-product','不得混入工资详情的材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('detail-wh','detail-product',10000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('detail-source','','防水涂料','合成防水','平方',0,300,JSON_ARRAY(JSON_OBJECT('id','master','name','主卫'),JSON_OBJECT('id','kitchen','name','厨房'))),('detail-zero-source','','防水涂料','合成0工资工艺','平方',0,0,JSON_ARRAY())`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('detail-water','detail-co','detail-source','防水涂料','合成防水','平方',2000,0),('detail-zero','detail-co','detail-zero-source','防水涂料','合成0工资工艺','平方',1500,0)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	order := Order{Company: "detail-co", Worker: "detail-c", Address: "合成多人地址", Date: "2026-10-08", Lines: []Line{
		{CraftID: "detail-water", SubitemID: "master", Worker: "detail-a", Segments: []Segment{{Length: 2, Width: 3}}},
		{CraftID: "detail-water", SubitemID: "kitchen", Worker: "detail-b", Segments: []Segment{{Length: 2, Width: 2}}},
		{CraftID: "detail-zero", Region: "阳台", Worker: "detail-b", Segments: []Segment{{Length: 1, Width: 1}}},
	}, Materials: []Material{{Product: "detail-product", Qty: 1000000}}, Sales: []Sale{{Product: "detail-product", InputQty: "2", InputUnit: "base", Price: 1500}}, DeliveryWage: 500}
	w := accountIdentityRequest(a, "POST", "/api/order", order, admin)
	if w.Code != 200 {
		t.Fatal("real multi-worker save", w.Code, w.Body.String())
	}
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	w = accountIdentityRequest(a, "GET", "/api/orders?view=wages&month=2026-10", nil, wageViewer)
	var page orderListPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Rows) != 3 {
		t.Fatal("real wage list", w.Code, w.Body.String())
	}
	accounts := map[string]orderListItem{}
	for _, row := range page.Rows {
		accounts[row.WorkerID] = row
		if row.ID == saved.ID || row.OrderID != saved.ID {
			t.Fatal("list account identity", row)
		}
	}
	// Source names and prices can change after saving. This endpoint must read
	// saved work/account snapshots, even after workers are renamed.
	maintenanceFaultExec(t, a.db, "UPDATE companies SET name='更名公司' WHERE id='detail-co'")
	maintenanceFaultExec(t, a.db, "UPDATE workers SET name='更名工人' WHERE id IN ('detail-a','detail-b','detail-c')")
	maintenanceFaultExec(t, a.db, "UPDATE crafts SET wage_cents=9999 WHERE id='detail-source'")
	for _, tc := range []struct {
		worker, name string
		lines        int
		wage         int64
		delivery     bool
	}{
		{"detail-a", "合成主卫甲", 1, 1800, false},
		{"detail-b", "合成厨房乙", 2, 1200, false},
		{"detail-c", "合成送货丙", 0, 500, true},
	} {
		d, raw := wageDetailHTTPRead(t, a, wageViewer, accounts[tc.worker].ID)
		if d.WorkerID != tc.worker || d.WorkerName != tc.name || d.OrderID != saved.ID || d.CompanyName != "合成公司快照" || len(d.Lines) != tc.lines || d.WageCents != tc.wage || d.HandlesDelivery != tc.delivery || d.Legacy || d.WagePaid {
			t.Fatal("wrong person/snapshot/amount", d)
		}
		for _, l := range d.Lines {
			if l.WorkerID != tc.worker || l.WorkerName != tc.name {
				t.Fatal("another worker's line returned", l)
			}
		}
		if tc.worker == "detail-a" && (d.Lines[0].Region != "" || d.Lines[0].SubitemID != "master" || d.Lines[0].Qty != 6000000 || d.Lines[0].Wage != 300 || d.Lines[0].Labor != 1800 || d.Lines[0].Segments[0].Width != 3) {
			t.Fatal("master bathroom dimensions/wage snapshot", d.Lines)
		}
		if tc.worker == "detail-b" && (d.Lines[0].Region != "" || d.Lines[0].SubitemID != "kitchen" || d.Lines[1].Wage != 0 || d.Lines[1].Labor != 0 || !strings.Contains(raw, "阳台")) {
			t.Fatal("kitchen/zero-wage own work lost", d.Lines)
		}
		if tc.worker == "detail-c" && (d.DeliveryWage != 500 || !strings.Contains(raw, `"lines":[]`)) {
			t.Fatal("delivery-only account", d)
		}
	}
	// Paying A keeps B unpaid; details preserve each account's state rather than
	// showing the construction record's aggregate any-worker-paid flag.
	w = accountIdentityRequest(a, "POST", "/api/wage-settlement", SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: accounts["detail-a"].ID, Version: accounts["detail-a"].Version}}}, admin)
	if w.Code != 200 {
		t.Fatal("pay selected account", w.Code, w.Body.String())
	}
	da, _ := wageDetailHTTPRead(t, a, wageViewer, accounts["detail-a"].ID)
	db, _ := wageDetailHTTPRead(t, a, wageViewer, accounts["detail-b"].ID)
	if !da.WagePaid || db.WagePaid || da.Version != accounts["detail-a"].Version+1 {
		t.Fatal("individual account payment state lost", da, db)
	}
	for _, tc := range []struct {
		cookie *http.Cookie
		id     string
		status int
	}{
		{nil, accounts["detail-a"].ID, 401},
		{constructionViewer, accounts["detail-a"].ID, 403},
		{wageViewer, "missing-account", 404},
		{wageViewer, saved.ID, 404},
		{wageViewer, "", 400},
	} {
		if w = accountIdentityRequest(a, "GET", "/api/wage?id="+tc.id, nil, tc.cookie); w.Code != tc.status {
			t.Fatal("wage access/identity guard", tc.id, w.Code, w.Body.String())
		}
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM wage_settlements", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", 3)
	t.Log("real multi-worker save/list IDs produce own-only HTTP wage details, zero-wage rows and delivery snapshot; customer/material data absent, saved names/paid state retained")
}

func TestWorkerWageDetailLegacyAndDamagedHTTPMySQL(t *testing.T) {
	a, _, wageViewer, _ := wageDetailHTTPFixture(t)
	maintenanceFaultExec(t, a.db, "INSERT INTO companies(id,name) VALUES('legacy-detail-co','现公司名称')")
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,company_name,address,work_date,worker_id,worker_name,note,created_by,wage_cents,wage_paid,version) VALUES('legacy-detail','LEGACY-DETAIL','legacy-detail-co','历史公司快照','合成历史地址','2026-10-01','legacy-person','历史人员快照','','fixture',987,1,7)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,total_cents,wage_cents,labor_cents,temporary) VALUES('legacy-detail-line','legacy-detail','开工','历史整项','金额','[{"mode":"金额","amount_cents":123456}]',1000000,123456,123456,111,111,1)`)
	w := accountIdentityRequest(a, "GET", "/api/orders?view=wages&month=2026-10", nil, wageViewer)
	var page orderListPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Rows) != 1 {
		t.Fatal("legacy list", w.Code, w.Body.String())
	}
	accountID := page.Rows[0].ID
	for _, persisted := range []bool{false, true} {
		if persisted {
			if _, err := a.db.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,wage_paid,version,legacy) VALUES(?,'legacy-detail','legacy-person','历史人员快照',987,1,7,1)`, accountID); err != nil {
				t.Fatal(err)
			}
		}
		d, _ := wageDetailHTTPRead(t, a, wageViewer, accountID)
		if !d.Legacy || d.WageCents != 987 || !d.WagePaid || d.Version != 7 || d.WorkerName != "历史人员快照" || d.CompanyName != "历史公司快照" || len(d.Lines) != 1 || d.Lines[0].Labor != 111 || d.Lines[0].WorkerName != "历史人员快照" {
			t.Fatal("legacy snapshot was recalculated/current-name substituted", d)
		}
		wantAccounts := 0
		if persisted {
			wantAccounts = 1
		}
		maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", wantAccounts)
	}
	// Both incomplete persisted accounts and unsafe no-account fallback records
	// must fail; corruption outside the selected record must not block good data.
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,created_by,wage_cents) VALUES('damaged-detail','DAMAGED-DETAIL','legacy-detail-co','合成损坏地址','2026-10-01','person-a','甲','','fixture',3000),('damaged-fallback','DAMAGED-FALLBACK','legacy-detail-co','合成损坏回退','2026-10-01','person-a','甲','','fixture',1200)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,total_cents,wage_cents,labor_cents,temporary,worker_id,worker_name) VALUES('damaged-detail-line','damaged-detail','防水涂料','乙施工','金额',JSON_ARRAY(),1000000,0,0,1200,1200,1,'person-b','乙'),('damaged-fallback-line','damaged-fallback','防水涂料','乙施工','金额',JSON_ARRAY(),1000000,0,0,1200,1200,1,'person-b','乙')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents) VALUES('damaged-account','damaged-detail','person-a','甲',1800)`)
	var fallbackID string
	if err := a.db.QueryRow("SELECT MD5(CONCAT('legacy-wage:',id)) FROM orders WHERE id='damaged-fallback'").Scan(&fallbackID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"damaged-account", fallbackID} {
		w = accountIdentityRequest(a, "GET", "/api/wage?id="+id, nil, wageViewer)
		if w.Code != 422 {
			t.Fatal("damaged account attributed to wrong person", w.Code, w.Body.String())
		}
	}
	_, _ = wageDetailHTTPRead(t, a, wageViewer, accountID)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM users", 3)
	t.Log("legacy list ID and materialized legacy account preserve exact amount/name; incomplete restoration rejected only in selected record; endpoint does not materialize or edit accounts")
}

func TestWorkerWageDetailZeroDeliveryHTTPMySQL(t *testing.T) {
	a, admin, wageViewer, _ := wageDetailHTTPFixture(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('delivery-detail-co','合成送货公司')`,
		`INSERT INTO workers(id,name,crafts) VALUES('delivery-person','合成0工资送货人',JSON_ARRAY())`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('delivery-detail-wh','合成送货仓','delivery-person')`,
		`INSERT INTO products(id,name,unit) VALUES('delivery-detail-product','合成送货材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('delivery-detail-wh','delivery-detail-product',2000000)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	w := accountIdentityRequest(a, "POST", "/api/order", Order{Company: "delivery-detail-co", Worker: "delivery-person", Address: "合成纯送货地址", Date: "2026-10-08", Sales: []Sale{{Product: "delivery-detail-product", InputQty: "1", InputUnit: "base", Price: 1500}}}, admin)
	if w.Code != 200 {
		t.Fatal("delivery-only save", w.Code, w.Body.String())
	}
	w = accountIdentityRequest(a, "GET", "/api/orders?view=wages&month=2026-10", nil, wageViewer)
	var page orderListPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Rows) != 1 {
		t.Fatal("delivery-only list", w.Code, w.Body.String())
	}
	d, raw := wageDetailHTTPRead(t, a, wageViewer, page.Rows[0].ID)
	if !d.HandlesDelivery || d.WageCents != 0 || d.DeliveryWage != 0 || len(d.Lines) != 0 || !strings.Contains(raw, `"lines":[]`) || d.WorkerName != "合成0工资送货人" {
		t.Fatal("zero delivery fact lost", d)
	}
}
