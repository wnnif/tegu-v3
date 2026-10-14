package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// HTTP handlers, sessions and committed MySQL rows are exercised together.
// maintenanceFaultDB only creates disposable schemas after the external runner
// has verified the new instance, account and exact grant boundary.
func newV3Phase1Fixture(t *testing.T) priceMySQLFixture {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", "")
	t.Setenv("ADMIN_INITIAL_PASSWORD", "synthetic-v3-phase1-only")
	if err := a.bootstrap(); err != nil {
		t.Fatal(err)
	}
	var adminID string
	if err := a.db.QueryRow("SELECT id FROM users WHERE role='admin' AND active=1").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	f := priceMySQLFixture{orderLocationsFixture: orderLocationsFixture{a: a}}
	f.admin = f.session(t, adminID)
	return f
}

func v3Phase1Entity(t *testing.T, f priceMySQLFixture, input map[string]any) string {
	t.Helper()
	return priceTestJSON(t, f.request(t, f.admin, "POST", "/api/entity", "", input, 200))["id"].(string)
}

func v3Phase1Craft(t *testing.T, f priceMySQLFixture, name, category string, active bool) string {
	t.Helper()
	return v3Phase1Entity(t, f, map[string]any{"entity": "crafts", "name": name, "category": category, "mode": "平方", "spec": "合成验收规格", "wage_cents": 500, "active": active,
		"subitems": []CraftSubitem{{ID: "v3-kitchen", Name: "厨房"}, {ID: "v3-bath", Name: "主卫"}}})
}

func v3Phase1Fixed(t *testing.T, f priceMySQLFixture, company, source string) string {
	t.Helper()
	var fixed string
	if err := f.a.db.QueryRow("SELECT id FROM crafts WHERE company_id=? AND source_id=?", company, source).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	return fixed
}

func v3Phase1UserInput(t *testing.T, f priceMySQLFixture, actor priceTestActor, crafts []string) User {
	t.Helper()
	var u User
	var permissions string
	if err := f.a.db.QueryRow("SELECT id,username,name,role,active,version,permissions FROM users WHERE id=?", actor.id).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active, &u.Version, &permissions); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(permissions), &u.Permissions); err != nil {
		t.Fatal(err)
	}
	u.Crafts = crafts
	return u
}

func TestV3Phase1PublicGrantCompanyPricesMySQL(t *testing.T) {
	f := newV3Phase1Fixture(t)
	source := v3Phase1Craft(t, f, "V3公共防水", "防水涂料", true)
	otherSource := v3Phase1Craft(t, f, "V3未授权维修", "维修", true)
	company := func(name string, price int64) string {
		return v3Phase1Entity(t, f, map[string]any{"entity": "companies", "name": name, "ticket_mode": "single", "fixed_crafts": []map[string]any{{"source_id": source, "price_cents": price}, {"source_id": otherSource, "price_cents": 1300}}})
	}
	companyA, companyB := company("V3甲公司", 2000), company("V3乙公司", 2500)
	fixedA, fixedB := v3Phase1Fixed(t, f, companyA, source), v3Phase1Fixed(t, f, companyB, source)
	worker := v3Phase1Entity(t, f, map[string]any{"entity": "workers", "name": "V3验收师傅", "crafts": []string{"防水涂料", "维修"}})
	writer := f.employee(t, []string{"orders"}, source)
	var rawGrants string
	if err := f.a.db.QueryRow("SELECT crafts FROM users WHERE id=?", writer.id).Scan(&rawGrants); err != nil {
		t.Fatal(err)
	}
	var grants []string
	if err := json.Unmarshal([]byte(rawGrants), &grants); err != nil || !reflect.DeepEqual(grants, []string{source}) {
		t.Fatal("employee must hold exactly one public grant", rawGrants, err)
	}
	for _, companyID := range []string{companyA, companyB} {
		w := f.request(t, writer, "GET", "/api/reference?tables=crafts&craft_scope=order&company_id="+companyID, "", nil, 200)
		var refs catalogReferences
		if err := json.Unmarshal(w.Body.Bytes(), &refs); err != nil {
			t.Fatal(err)
		}
		if len(refs.Crafts) != 2 {
			t.Fatal("company crafts must stay visible", len(refs.Crafts))
		}
		for _, row := range refs.Crafts {
			if row.Editable != (row.SourceID == source) || row.WageCents != nil {
				t.Fatal("one public grant must authorize both companies without wage access", row)
			}
		}
		var document any
		if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		assertNoCustomerMoney(t, document)
	}
	order := func(companyID, craftID, address string) Order {
		return Order{Company: companyID, Address: address, Date: "2026-10-11", Worker: worker,
			Lines: []Line{{CraftID: craftID, SubitemID: "v3-kitchen", ActualLocation: "厨房地面", Worker: worker, Segments: []Segment{{Length: 5, Width: 2}}, Price: 999999, Wage: 999999}}}
	}
	firstOrder, firstKey := order(companyA, fixedA, "甲公司合成十平方"), id()
	for _, example := range []struct {
		input Order
		key   string
		total int64
	}{{firstOrder, firstKey, 20000}, {order(companyB, fixedB, "乙公司合成十平方"), id(), 25000}} {
		result := priceTestJSON(t, f.request(t, writer, "POST", "/api/order", example.key, example.input, 200))
		orderID := result["id"].(string)
		var total, labor, qty int64
		if err := f.a.db.QueryRow("SELECT total_cents,wage_cents FROM orders WHERE id=?", orderID).Scan(&total, &labor); err != nil || total != example.total || labor != 5000 {
			t.Fatalf("stored price/wage: got %d/%d want %d/5000: %v", total, labor, example.total, err)
		}
		if err := f.a.db.QueryRow("SELECT qty_micro FROM order_lines WHERE order_id=?", orderID).Scan(&qty); err != nil || qty != 10000000 {
			t.Fatal("server dimensions must calculate exactly 10㎡", qty, err)
		}
		read := priceTestJSON(t, f.request(t, f.admin, "GET", "/api/order?id="+orderID, "", nil, 200))
		if read["total_cents"] != float64(example.total) || read["wage_cents"] != float64(5000) {
			t.Fatal("read model money differs", read)
		}
		t.Logf("company charge %d cents, shared public wage %d cents, 10㎡", total, labor)
	}
	before := orderLocationsState(t, f.a.db)
	f.request(t, writer, "POST", "/api/order", "", order(companyA, v3Phase1Fixed(t, f, companyA, otherSource), "未授权工艺拒绝"), 403)
	f.request(t, writer, "POST", "/api/order", "", order(companyA, source, "公共ID不能冒充公司固定工艺"), 403)
	// A client-provided source/name/category cannot substitute for the real
	// fixed/public relationship queried by the server.
	var forged map[string]any
	if err := json.Unmarshal([]byte(jsonText(order(companyA, v3Phase1Fixed(t, f, companyA, otherSource), "伪造公共来源拒绝"))), &forged); err != nil {
		t.Fatal(err)
	}
	forgedLine := forged["lines"].([]any)[0].(map[string]any)
	forgedLine["source_id"], forgedLine["name"], forgedLine["category"] = source, "V3公共防水", "防水涂料"
	f.request(t, writer, "POST", "/api/order", "", forged, 403)
	f.request(t, writer, "POST", "/api/order", "", order(companyA, fixedB, "跨公司固定工艺拒绝"), 403)
	f.request(t, writer, "POST", "/api/entity", "", map[string]any{"entity": "crafts", "id": source, "version": 1, "name": "伪改公共防水", "category": "防水涂料", "mode": "平方", "wage_cents": 1, "active": true}, 403)
	f.request(t, f.admin, "POST", "/api/user", "", map[string]any{"username": "v3-forged-grant", "name": "伪固定授权", "role": "employee", "active": true, "password": "synthetic-only", "permissions": []string{"orders"}, "crafts": []string{fixedA}}, 400)
	badCompany := companyCraftOrderPayload(t, f.a.db, companyA)
	badCompany["fixed_crafts"] = []map[string]any{{"id": fixedA, "source_id": otherSource, "version": 1, "price_cents": 2000}}
	f.request(t, f.admin, "POST", "/api/entity", "", badCompany, 400)
	orderLocationsUnchanged(t, f.a.db, before)
	// Revoke through the public account API, then create a new authenticated
	// session for the same user; an old request key must re-check today's grant.
	f.request(t, f.admin, "POST", "/api/user", "", v3Phase1UserInput(t, f, writer, []string{}), 200)
	f.request(t, writer, "GET", "/api/me", "", nil, 401)
	writer = f.session(t, writer.id)
	before = orderLocationsState(t, f.a.db)
	f.request(t, writer, "POST", "/api/order", firstKey, firstOrder, 403)
	f.request(t, writer, "POST", "/api/order", "", order(companyB, fixedB, "撤权后拒绝"), 403)
	orderLocationsUnchanged(t, f.a.db, before)
}

func TestV3Phase1CompanyOrderReselectAndUnitsMySQL(t *testing.T) {
	f := newV3Phase1Fixture(t)
	a := v3Phase1Craft(t, f, "V3同类甲防水", "防水涂料", true)
	b := v3Phase1Craft(t, f, "V3同类乙防水", "防水涂料", true)
	c := v3Phase1Craft(t, f, "V3开工", "开工", true)
	company := v3Phase1Entity(t, f, map[string]any{"entity": "companies", "name": "V3排序公司", "craft_category_order": []string{"防水涂料", "开工"}, "fixed_crafts": []map[string]any{{"source_id": b, "price_cents": 2500}, {"source_id": a, "price_cents": 2000}, {"source_id": c, "price_cents": 800}}})
	oldFixed := v3Phase1Fixed(t, f, company, a)
	assertOrder := func(want []string) {
		w := f.request(t, f.admin, "GET", "/api/reference?tables=crafts&craft_scope=order&company_id="+company, "", nil, 200)
		var refs catalogReferences
		if err := json.Unmarshal(w.Body.Bytes(), &refs); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, row := range refs.Crafts {
			got = append(got, row.SourceID)
			if len(row.Subitems) != 2 || row.Subitems[0].ID != "v3-kitchen" || row.Subitems[1].ID != "v3-bath" {
				t.Fatal("stable positions/order missing", row)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("company order did not reopen", got, want)
		}
	}
	assertOrder([]string{b, a, c})
	input := companyCraftOrderPayload(t, f.a.db, company)
	input["fixed_crafts"] = []map[string]any{{"source_id": b, "price_cents": 2500}, {"source_id": c, "price_cents": 800}}
	f.request(t, f.admin, "POST", "/api/entity", "", input, 200)
	input = companyCraftOrderPayload(t, f.a.db, company)
	input["fixed_crafts"] = []map[string]any{{"source_id": a, "price_cents": 2200}, {"source_id": b, "price_cents": 2500}, {"source_id": c, "price_cents": 800}}
	f.request(t, f.admin, "POST", "/api/entity", "", input, 200)
	if current := v3Phase1Fixed(t, f, company, a); current != oldFixed {
		t.Fatal("reselection duplicated company/public identity", current, oldFixed)
	}
	assertOrder([]string{a, b, c})
	var count int
	if err := f.a.db.QueryRow("SELECT COUNT(*) FROM crafts WHERE company_id=?", company).Scan(&count); err != nil || count != 3 {
		t.Fatal("reselection left duplicates", count, err)
	}
	product := v3Phase1Entity(t, f, map[string]any{"entity": "products", "name": "V3六小组一桶", "unit": "桶", "aux_unit": "小组", "aux_ratio": "6", "active": true})
	var definition productUnitDefinition
	var scale int64
	if err := f.a.db.QueryRow("SELECT base_unit,aux_unit,base_factor,aux_factor,quantity_scale FROM product_unit_definitions WHERE product_id=?", product).Scan(&definition.BaseUnit, &definition.AuxUnit, &definition.BaseFactor, &definition.AuxFactor, &scale); err != nil || definition.BaseFactor != 6 || definition.AuxFactor != 1 || scale != 6 {
		t.Fatal("exact unit foundation not saved", definition, scale, err)
	}
	bucket, err := definition.quantityQuantum("1", "base")
	if err != nil {
		t.Fatal(err)
	}
	group, err := definition.quantityQuantum("1", "aux")
	if err != nil || group*6 != bucket {
		t.Fatal("one bucket differs from six separate groups", bucket, group, err)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM stock_movements", 0)
	// Restore the actual snapshot into another disposable empty schema and
	// compare full fields, including public grants and company display order.
	f.employee(t, []string{"orders"}, a)
	before := orderLocationsState(t, f.a.db)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	file, err := f.a.backup()
	if err != nil {
		t.Fatal("V3 snapshot", err)
	}
	target, _ := maintenanceFaultDB(t)
	if err := target.restore(filepath.Join(dir, file)); err != nil {
		t.Fatal("V3 exact-unit/public-grant/company-order restore", err)
	}
	orderLocationsUnchanged(t, target.db, before)
}

func TestV3Phase1InactivePublicAccountGrantMySQL(t *testing.T) {
	f := newV3Phase1Fixture(t)
	source := v3Phase1Craft(t, f, "V3停用授权防水", "防水涂料", true)
	actor := f.employee(t, []string{"orders"}, source)
	v3Phase1Entity(t, f, map[string]any{"entity": "crafts", "id": source, "version": 1, "name": "V3停用授权防水", "category": "防水涂料", "mode": "平方", "spec": "合成规格", "wage_cents": 500, "active": false, "subitems": []CraftSubitem{}})
	before := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "POST", "/api/user", "", map[string]any{"username": "v3-inactive-new", "name": "新停用授权应拒绝", "role": "employee", "active": true, "password": "synthetic-only", "permissions": []string{"orders"}, "crafts": []string{source}}, 400)
	orderLocationsUnchanged(t, f.a.db, before)
	f.request(t, f.admin, "POST", "/api/user", "", v3Phase1UserInput(t, f, actor, []string{source}), 200)
	f.request(t, f.admin, "POST", "/api/user", "", v3Phase1UserInput(t, f, actor, []string{}), 200)
}
