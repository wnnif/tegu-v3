package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Uses the opt-in disposable schema fixture; the runner's instance identity and
// grants must be checked externally before GO_MYSQL_TEST=1 is supplied.
type priceTestActor struct{ id, token string }
type priceMySQLFixture struct {
	orderLocationsFixture
	admin priceTestActor
}

func newPriceMySQLFixture(t *testing.T) priceMySQLFixture {
	t.Helper()
	base := newOrderLocationsFixture(t)
	maintenanceFaultExec(t, base.a.db, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('loc-amount-source','','开工','合成整项金额','金额',0,600)`)
	maintenanceFaultExec(t, base.a.db, `INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('loc-amount','loc-co','loc-amount-source','开工','合成整项金额','金额',4567,0)`)
	// V2 initialization requires an explicit password; keep it synthetic and
	// local to this disposable-schema test rather than restoring defaults.
	t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", "")
	t.Setenv("ADMIN_INITIAL_PASSWORD", "synthetic-customer-price-only")
	if err := base.a.bootstrap(); err != nil {
		t.Fatal("bootstrap isolated synthetic administrator", err)
	}
	var adminID string
	if err := base.a.db.QueryRow("SELECT id FROM users WHERE role='admin' AND active=1").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	f := priceMySQLFixture{orderLocationsFixture: base}
	f.admin = f.session(t, adminID)
	return f
}

func (f priceMySQLFixture) session(t *testing.T, userID string) priceTestActor {
	t.Helper()
	token := id() + id()
	if _, err := f.a.db.Exec("INSERT INTO sessions(token,user_id,expires_at) VALUES(?,?,DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR))", hash([]byte(token)), userID); err != nil {
		t.Fatal(err)
	}
	return priceTestActor{userID, token}
}

func (f priceMySQLFixture) request(t *testing.T, actor priceTestActor, method, path, key string, payload any, status int) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if payload != nil {
		body = jsonText(payload)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: "tegu_session", Value: actor.token})
	if method == "POST" {
		if key == "" {
			key = id()
		}
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.a.route(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func priceTestJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return result
}

func (f priceMySQLFixture) employee(t *testing.T, permissions []string, crafts ...string) priceTestActor {
	t.Helper()
	name := "price-" + id()[:12]
	f.request(t, f.admin, "POST", "/api/user", "", map[string]any{"username": name, "name": "合成客户价员工", "role": "employee", "active": true, "password": "synthetic-price-password", "permissions": permissions, "crafts": crafts}, 200)
	var userID string
	if err := f.a.db.QueryRow("SELECT id FROM users WHERE username=?", name).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return f.session(t, userID)
}

func (f priceMySQLFixture) permissions(t *testing.T, actor priceTestActor, permissions []string) priceTestActor {
	t.Helper()
	var u User
	var rawCrafts []byte
	if err := f.a.db.QueryRow("SELECT id,username,name,role,active,version,crafts FROM users WHERE id=?", actor.id).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active, &u.Version, &rawCrafts); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawCrafts, &u.Crafts); err != nil {
		t.Fatal(err)
	}
	u.Permissions = permissions
	f.request(t, f.admin, "POST", "/api/user", "", u, 200)
	// Account edits already revoke login sessions; this behavior must not regress.
	f.request(t, actor, "GET", "/api/me", "", nil, 401)
	return f.session(t, actor.id)
}

func assertNoCustomerMoney(t *testing.T, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "price_cents", "total_cents", "amount_cents", "received_cents", "outstanding_cents", "receivable_cents", "undelivered_cents", "delivered_outstanding_cents":
				t.Fatalf("restricted customer money field %s exposed: %s", key, jsonText(value))
			}
			assertNoCustomerMoney(t, child)
		}
	case []any:
		for _, child := range v {
			assertNoCustomerMoney(t, child)
		}
	}
}

func (f priceMySQLFixture) saveFixture(t *testing.T) string {
	t.Helper()
	o := Order{Company: "loc-co", Address: "合成独立客户价工地", Date: "2026-10-10", Worker: "loc-a", Lines: []Line{
		{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: "厨房", Worker: "loc-a", Segments: []Segment{{Length: 2, Width: 3}}},
		{CraftID: "loc-amount", ActualLocation: "主卫", Worker: "loc-a", Qty: 1000000},
		{Temporary: true, Category: "开工", Name: "合成已保存临时项", Mode: "金额", Price: 5432, Wage: 789, ActualLocation: "次卫", Worker: "loc-a", Segments: []Segment{{Mode: "金额", Amount: 5432}}},
	}, Sales: []Sale{{Product: "loc-product", InputQty: "2", InputUnit: "base", Price: 1234}}}
	result := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", o, 200))
	if result["total_cents"] != float64(23267) {
		t.Fatal("server calculation fixture", result)
	}
	return result["id"].(string)
}

func (f priceMySQLFixture) edit(t *testing.T, actor priceTestActor, orderID string) Order {
	t.Helper()
	w := f.request(t, actor, "GET", "/api/order?id="+orderID, "", nil, 200)
	var o Order
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	lines := o.Lines[:0]
	for _, line := range o.Lines {
		if line.Editable {
			lines = append(lines, line)
		}
	}
	o.Lines = lines
	o.Combinations = nil
	return o
}

func TestCustomerPriceAccountGrantRevokeMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	orderID := f.saveFixture(t)
	worker := f.employee(t, []string{"orders"}, "loc-source")
	read := func(actor priceTestActor) map[string]any {
		return priceTestJSON(t, f.request(t, actor, "GET", "/api/order?id="+orderID, "", nil, 200))
	}
	assertNoCustomerMoney(t, read(worker))
	var beforePermissions string
	if err := f.a.db.QueryRow("SELECT permissions FROM users WHERE id=?", worker.id).Scan(&beforePermissions); err != nil {
		t.Fatal(err)
	}
	// The application initialization/migration is allowed to run repeatedly but
	// must not rewrite historical employees' JSON permission arrays.
	if err := initSchema(f.a.db); err != nil {
		t.Fatal(err)
	}
	var afterPermissions string
	if err := f.a.db.QueryRow("SELECT permissions FROM users WHERE id=?", worker.id).Scan(&afterPermissions); err != nil || afterPermissions != beforePermissions {
		t.Fatal("startup automatically granted employee prices", afterPermissions, err)
	}
	worker = f.permissions(t, worker, []string{"orders", customerPricePermission})
	visible := read(worker)
	if visible["total_cents"] != float64(23267) || visible["lines"].([]any)[0].(map[string]any)["price_cents"] != float64(1800) {
		t.Fatal("granted prices missing", visible)
	}
	for _, field := range []string{"wage_cents", "wage_records", "received_cents", "payment_receipts"} {
		if _, ok := visible[field]; ok {
			t.Fatal("price permission implies wage/finance access", field)
		}
	}
	worker = f.permissions(t, worker, []string{"orders"})
	assertNoCustomerMoney(t, read(worker))
	priceOnly := f.employee(t, []string{customerPricePermission})
	for _, path := range []string{"/api/order?id=" + orderID, "/api/orders?view=orders&month=2026-10", "/api/projects", "/api/reference-options?entity=crafts&company_id=loc-co", "/api/finance-companies", "/api/wage-workers"} {
		f.request(t, priceOnly, "GET", path, "", nil, 403)
	}
	f.request(t, worker, "POST", "/api/user", "", map[string]any{"id": worker.id, "permissions": []string{"orders", customerPricePermission}}, 403)
}

func TestCustomerPriceReadBoundariesMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	orderID := f.saveFixture(t)
	var projectID, ticketID, accountID string
	if err := f.a.db.QueryRow("SELECT project_id FROM orders WHERE id=?", orderID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if err := f.a.db.QueryRow("SELECT ticket_id FROM project_ticket_items WHERE order_id=? LIMIT 1", orderID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	if err := f.a.db.QueryRow("SELECT id FROM order_worker_wages WHERE order_id=? LIMIT 1", orderID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	hidden := f.employee(t, []string{"orders", "orders_temporary", "customers", "rates", "wages", "finance"}, "loc-source")
	paths := []string{
		"/api/order?id=" + orderID, "/api/orders?view=orders&month=2026-10", "/api/orders?view=wages&month=2026-10",
		"/api/projects?company_id=loc-co", "/api/projects?id=" + projectID, "/api/project-work?id=" + projectID,
		"/api/reference?company_id=loc-co", "/api/reference-options?entity=crafts&company_id=loc-co&craft_scope=order",
		"/api/catalog?entity=crafts&company_id=loc-co", "/api/catalog?entity=companies", "/api/dashboard?month=2026-10",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			assertNoCustomerMoney(t, priceTestJSON(t, f.request(t, hidden, "GET", path, "", nil, 200)))
		})
	}
	detail := priceTestJSON(t, f.request(t, hidden, "GET", "/api/order?id="+orderID, "", nil, 200))
	first := detail["lines"].([]any)[0].(map[string]any)
	if first["actual_location"] != "厨房" || first["subitem_name"] != "合成配置小项" || first["qty_micro"] != float64(6000000) || detail["wage_cents"] != float64(3189) {
		t.Fatal("ordinary construction or independent wage permission lost", detail)
	}
	wage := priceTestJSON(t, f.request(t, hidden, "GET", "/api/wage?id="+accountID, "", nil, 200))
	assertNoCustomerMoney(t, wage)
	f.request(t, hidden, "GET", "/api/wage-workers?month=2026-10", "", nil, 200)
	f.request(t, hidden, "GET", "/api/export?view=wages&month=2026-10", "", nil, 200)
	for _, path := range []string{
		"/api/orders?view=finance&month=2026-10", "/api/finance-companies", "/api/finance-companies?export=1",
		"/api/project-tickets?company_id=loc-co", "/api/project-tickets?company_id=loc-co&export=1", "/api/project-ticket?id=" + ticketID,
		"/api/export?view=orders&month=2026-10", "/api/export?view=finance&month=2026-10", "/api/export?view=project&project_id=" + projectID,
		"/api/attachment-file?id=" + strings.Repeat("1", 32),
	} {
		t.Run("denied_"+path, func(t *testing.T) { f.request(t, hidden, "GET", path, "", nil, 403) })
	}
	capability := priceTestJSON(t, f.request(t, hidden, "GET", "/api/attachment-capability", "", nil, 200))
	if capability["enabled"] != false {
		t.Fatal("financial attachment capability bypass", capability)
	}
	for _, kind := range archiveKinds {
		if archivePermission(kind) == "wages" && kind != "施工内部留档表" {
			if !canReadArchive(User{Role: "employee", Permissions: []string{"wages"}}, kind) {
				t.Fatal("wage archive affected")
			}
			continue
		}
		if canReadArchive(User{Role: "employee", Permissions: []string{"orders", "wages", "finance"}}, kind) {
			t.Fatal("customer-price archive bypass", kind)
		}
	}
	prices := f.employee(t, []string{"orders", customerPricePermission}, "loc-source")
	for _, path := range []string{"/api/finance-companies", "/api/project-tickets?company_id=loc-co", "/api/wage?id=" + accountID, "/api/export?view=wages&month=2026-10"} {
		f.request(t, prices, "GET", path, "", nil, 403)
	}
	ticket := priceTestJSON(t, f.request(t, prices, "GET", "/api/project-ticket?id="+ticketID, "", nil, 200))
	for _, field := range []string{"received_cents", "outstanding_cents", "receipts", "wage_cents", "wage_records"} {
		if _, ok := ticket[field]; ok {
			t.Fatal("customer-price permission bypasses financial/wage ticket boundary", field)
		}
	}
	visible := priceTestJSON(t, f.request(t, prices, "GET", "/api/project-work?id="+projectID, "", nil, 200))
	if visible["total_cents"] != float64(23267) {
		t.Fatal("price-authorized project total missing", visible)
	}
}

func TestCustomerPriceHiddenEditPreservesSnapshotsMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	orderID := f.saveFixture(t)
	hidden := f.employee(t, []string{"orders", "orders_temporary"}, "loc-source", "loc-amount-source")
	// Omitted prices, including an amount-mode segment, are exactly what the
	// employee Vue form receives and sends. The stored financial/wage snapshots
	// must remain authoritative even if current catalog prices later change.
	maintenanceFaultExec(t, f.a.db, `UPDATE crafts SET price_cents=9900 WHERE id IN ('loc-fixed','loc-amount')`)
	for _, forge := range []bool{false, true} {
		o := f.edit(t, hidden, orderID)
		o.Lines[0].ActualLocation = "厨房已编辑"
		o.Lines[0].Description = "无价格权限仍可保存部位与备注"
		if forge {
			for i := range o.Lines {
				o.Lines[i].Price = 1
				for j := range o.Lines[i].Segments {
					o.Lines[i].Segments[j].Amount = 1
				}
			}
			o.Sales[0].Price = 1
		}
		reply := priceTestJSON(t, f.request(t, hidden, "POST", "/api/order", "", o, 200))
		assertNoCustomerMoney(t, reply)
		detail := priceTestJSON(t, f.request(t, f.admin, "GET", "/api/order?id="+orderID, "", nil, 200))
		if detail["total_cents"] != float64(23267) || detail["wage_cents"] != float64(3189) {
			t.Fatal("hidden edit cleared/changed financial snapshot", detail)
		}
		lines := detail["lines"].([]any)
		if lines[0].(map[string]any)["price_cents"] != float64(1800) || lines[1].(map[string]any)["price_cents"] != float64(4567) || lines[2].(map[string]any)["price_cents"] != float64(5432) || detail["sales"].([]any)[0].(map[string]any)["price_cents"] != float64(1234) {
			t.Fatal("snapshot price forged", detail)
		}
	}
	o := f.edit(t, hidden, orderID)
	o.Lines[0].Segments[0].Length = 3
	o.Sales[0].InputQty = "3"
	o.Sales[0].QtyQuantum = ""
	f.request(t, hidden, "POST", "/api/order", "", o, 200)
	detail := priceTestJSON(t, f.request(t, f.admin, "GET", "/api/order?id="+orderID, "", nil, 200))
	if detail["total_cents"] != float64(29901) || detail["wage_cents"] != float64(4089) {
		t.Fatal("quantity edit did not use saved server rates", detail)
	}
	before := orderLocationsState(t, f.a.db)
	o = f.edit(t, hidden, orderID)
	o.Sales[0].InputUnit = "aux"
	f.request(t, hidden, "POST", "/api/order", "", o, 403)
	orderLocationsUnchanged(t, f.a.db, before)
	o = f.edit(t, hidden, orderID)
	o.Sales = append(o.Sales, Sale{Product: "loc-product", InputUnit: "base", InputQty: "1", Price: 1})
	f.request(t, hidden, "POST", "/api/order", "", o, 403)
	orderLocationsUnchanged(t, f.a.db, before)
	o = f.edit(t, hidden, orderID)
	o.Lines = append(o.Lines, Line{Temporary: true, Category: "开工", Name: "伪造新临时项", Mode: "金额", Price: 1, Worker: "loc-a"})
	f.request(t, hidden, "POST", "/api/order", "", o, 403)
	orderLocationsUnchanged(t, f.a.db, before)
	// A new fixed construction line is allowed and priced on the server, not from
	// any fake amount field or missing client-side price.
	fresh := Order{Company: "loc-co", Address: "无价合成新工地", Date: "2026-10-10", Lines: []Line{{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: "主卫", Worker: "loc-b", Price: 1, Segments: []Segment{{Length: 1, Width: 2}}}}}
	saved := priceTestJSON(t, f.request(t, hidden, "POST", "/api/order", "", fresh, 200))
	assertNoCustomerMoney(t, saved)
	var total int64
	if err := f.a.db.QueryRow("SELECT total_cents FROM orders WHERE id=?", saved["id"]).Scan(&total); err != nil || total != 19800 {
		t.Fatal("new fixed server price", total, err)
	}
	// Legacy mixed-region rows are not guessed, rewritten or zero-priced on edit.
	if _, err := f.a.db.Exec("UPDATE order_lines SET region='合成历史混用原文',subitem_name=NULL,actual_location=NULL WHERE order_id=? AND craft_id='loc-fixed'", orderID); err != nil {
		t.Fatal(err)
	}
	o = f.edit(t, hidden, orderID)
	f.request(t, hidden, "POST", "/api/order", "", o, 200)
	var region, subitem, location string
	if err := f.a.db.QueryRow("SELECT region,COALESCE(subitem_name,''),COALESCE(actual_location,'') FROM order_lines WHERE order_id=? AND craft_id='loc-fixed'", orderID).Scan(&region, &subitem, &location); err != nil || region != "合成历史混用原文" || subitem != "" || location != "" {
		t.Fatal("legacy order changed", region, subitem, location, err)
	}
	if err := f.a.db.QueryRow("SELECT total_cents FROM orders WHERE id=?", orderID).Scan(&total); err != nil || total != 29901 {
		t.Fatal("legacy amount lost", total, err)
	}
}

func TestCustomerPriceCachedMutationRevocationMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	orderID := f.saveFixture(t)
	worker := f.employee(t, []string{"orders", "orders_temporary", customerPricePermission, "finance", "wages"}, "loc-source", "loc-amount-source")
	o := f.edit(t, worker, orderID)
	key := id()
	orderKey := key
	f.request(t, worker, "POST", "/api/order", key, o, 200)
	worker = f.permissions(t, worker, []string{"orders", "orders_temporary", "finance", "wages"})
	before := orderLocationsState(t, f.a.db)
	cached := priceTestJSON(t, f.request(t, worker, "POST", "/api/order", key, o, 200))
	assertNoCustomerMoney(t, cached)
	orderLocationsUnchanged(t, f.a.db, before)
	// Simulate historical idempotent responses containing financial amounts. The
	// route must recheck current privileges before mutate can return the cache.
	for _, path := range []string{"/api/project-ticket-action", "/api/project-ticket-batch", "/api/finance-edit", "/api/settle"} {
		payload := map[string]any{"action": "collect", "id": orderID}
		raw := []byte(jsonText(payload))
		key = id()
		if _, err := f.a.db.Exec("INSERT INTO requests(user_id,request_key,body_hash,response) VALUES(?,?,?,?)", worker.id, key, hash(append([]byte("POST "+path+"\n"), raw...)), `{"total_cents":23267,"amount_cents":500}`); err != nil {
			t.Fatal(err)
		}
		f.request(t, worker, "POST", path, key, payload, 403)
	}
	// Wage settlement remains an independent permission even on cached requests.
	payload := map[string]any{"action": "pay", "id": orderID}
	key = id()
	if _, err := f.a.db.Exec("INSERT INTO requests(user_id,request_key,body_hash,response) VALUES(?,?,?,?)", worker.id, key, hash(append([]byte("POST /api/settle\n"), []byte(jsonText(payload))...)), `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	f.request(t, worker, "POST", "/api/settle", key, payload, 200)
	worker = f.permissions(t, worker, []string{"orders", customerPricePermission})
	f.request(t, worker, "POST", "/api/settle", key, payload, 403)
	// Price-only must not replay a cached order after its module was revoked.
	worker = f.permissions(t, worker, []string{customerPricePermission})
	f.request(t, worker, "POST", "/api/order", orderKey, o, 403)
}
