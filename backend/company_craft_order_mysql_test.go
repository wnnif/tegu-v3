package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Reuse the opt-in, disposable-schema fixture. The runner must verify the
// instance, dedicated identity and CREATE/DROP boundary before enabling it.
// No test below connects to a production/preview schema or changes its grants.
type companyCraftOrderMySQLFixture struct {
	priceMySQLFixture
	orderID string
}

func newCompanyCraftOrderMySQLFixture(t *testing.T) companyCraftOrderMySQLFixture {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	base := orderLocationsFixture{a: a, u: User{ID: "cco-admin", Name: "合成排序管理员", Role: "admin"}}
	f := companyCraftOrderMySQLFixture{priceMySQLFixture: priceMySQLFixture{orderLocationsFixture: base}}
	for _, query := range []string{
		`INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES('cco-admin','cco-admin','合成排序管理员','unused-fixture-hash','admin',JSON_ARRAY(),JSON_ARRAY())`,
		`INSERT INTO companies(id,name,contact,phone,note,ticket_mode) VALUES('cco-a','合成排序公司甲','合成联系人甲','10001','保留甲备注','single'),('cco-b','合成排序公司乙','合成联系人乙','10002','保留乙备注','single')`,
		`INSERT INTO workers(id,name,crafts) VALUES('cco-worker','合成排序师傅',JSON_ARRAY('开工','防水涂料','丙纶','室外防水'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('cco-wh','合成排序仓库','cco-worker')`,
		`INSERT INTO products(id,name,unit) VALUES('cco-product','合成排序材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('cco-wh','cco-product',20000000)`,
		// Names deliberately do not identify categories; exact registry keys do.
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,spec,subitems) VALUES('cco-open-source','','开工','合成名字含防水涂料','平方',0,325,'保留合成规格',JSON_ARRAY(JSON_OBJECT('id','part-a','name','合成配置小项'))),('cco-water-source','','防水涂料','合成名字含开工','平方',0,450,'保留合成规格',JSON_ARRAY()),('cco-poly-source','','丙纶','合成丙纶','平方',0,280,'保留合成规格',JSON_ARRAY()),('cco-outdoor-source','','室外防水','合成室外','平方',0,330,'保留合成规格',JSON_ARRAY())`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,spec,included_by_craft_id) VALUES('cco-a-open','cco-a','cco-open-source','开工','合成名字含防水涂料','平方',1250,0,'保留合成规格',''),('cco-a-water','cco-a','cco-water-source','防水涂料','合成名字含开工','平方',2600,0,'保留合成规格',''),('cco-a-poly','cco-a','cco-poly-source','丙纶','合成丙纶','平方',1800,0,'保留合成规格','cco-a-water'),('cco-a-outdoor','cco-a','cco-outdoor-source','室外防水','合成室外','平方',3100,0,'保留合成规格',''),('cco-b-open','cco-b','cco-open-source','开工','合成名字含防水涂料','平方',1350,0,'保留合成规格',''),('cco-b-water','cco-b','cco-water-source','防水涂料','合成名字含开工','平方',2700,0,'保留合成规格',''),('cco-b-poly','cco-b','cco-poly-source','丙纶','合成丙纶','平方',1900,0,'保留合成规格',''),('cco-b-outdoor','cco-b','cco-outdoor-source','室外防水','合成室外','平方',3200,0,'保留合成规格','')`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	f.admin = f.session(t, base.u.ID)
	order := Order{Company: "cco-a", Address: "合成排序历史工地", Date: "2026-10-11", Worker: "cco-worker", Warehouse: "cco-wh", DeliveryWage: 47,
		Lines:     []Line{{CraftID: "cco-a-open", SubitemID: "part-a", ActualLocation: "保存的合成部位", Worker: "cco-worker", Description: "保留历史备注", Segments: []Segment{{Length: 1, Width: 2}}}},
		Materials: []Material{{Product: "cco-product", Warehouse: "cco-wh", InputQty: "1", InputUnit: "base"}},
		Sales:     []Sale{{Product: "cco-product", Warehouse: "cco-wh", InputQty: "2", InputUnit: "base", Price: 812}},
	}
	result := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", order, 200))
	if result["total_cents"] != float64(4124) {
		t.Fatal("synthetic historical order total", result)
	}
	f.orderID = result["id"].(string)
	for _, table := range []string{"orders", "order_lines", "order_worker_wages", "order_materials", "order_sales", "stock_movements", "project_tickets"} {
		var exists bool
		if err := a.db.QueryRow("SELECT EXISTS(SELECT 1 FROM " + table + ")").Scan(&exists); err != nil || !exists {
			t.Fatalf("missing historical fixture in %s: %v", table, err)
		}
	}
	return f
}

// Include ordinary company fields, but intentionally omit ordering/fixed crafts
// unless a test explicitly changes them. This represents an older form client.
func companyCraftOrderPayload(t *testing.T, db *sql.DB, companyID string) map[string]any {
	t.Helper()
	var input companyEntityInput
	if err := db.QueryRow("SELECT id,version,name,contact,phone,customer_type,note,ticket_mode FROM companies WHERE id=?", companyID).Scan(&input.ID, &input.Version, &input.Name, &input.Contact, &input.Phone, &input.CustomerType, &input.Note, &input.TicketMode); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"entity": "companies", "id": input.ID, "version": input.Version, "name": input.Name, "contact": input.Contact, "phone": input.Phone, "customer_type": input.CustomerType, "note": input.Note, "ticket_mode": input.TicketMode}
}

func companyCraftOrderStored(t *testing.T, db *sql.DB, companyID string, want []string, wantSQLNull bool) {
	t.Helper()
	var raw sql.NullString
	if err := db.QueryRow("SELECT craft_category_order FROM companies WHERE id=?", companyID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.Valid == wantSQLNull {
		t.Fatalf("%s SQL null state: got %#v, want null=%v", companyID, raw, wantSQLNull)
	}
	order, err := readCompanyCraftCategoryOrder([]byte(raw.String))
	if err != nil || order == nil || !reflect.DeepEqual(order, want) {
		t.Fatalf("%s stored order %#v, want %#v: %v", companyID, order, want, err)
	}
}

func companyCraftOrderRead(t *testing.T, f companyCraftOrderMySQLFixture, actor priceTestActor, path string) map[string]companiesRecord {
	t.Helper()
	w := f.request(t, actor, "GET", path, "", nil, 200)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	raw := document["rows"]
	if raw == nil {
		raw = document["companies"]
	}
	var records []companiesRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal("company response", path, err)
	}
	out := map[string]companiesRecord{}
	for _, record := range records {
		if record.CraftCategoryOrder == nil {
			t.Fatalf("company read exposed null or omitted sorting: %s %s", path, w.Body.String())
		}
		out[record.ID] = record
	}
	return out
}

func companyCraftOrderHistory(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	state := orderLocationsState(t, db)
	for _, table := range []string{"companies", "audit", "requests"} {
		delete(state, table)
	}
	return state
}

func TestCompanyCraftCategoryOrderSaveReadMySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	writer := f.employee(t, []string{"customers"})
	reader := f.employee(t, []string{"orders"})
	before := companyCraftOrderHistory(t, f.a.db)
	paths := []string{"/api/catalog?entity=companies", "/api/reference?tables=companies", "/api/reference-options?entity=companies"}
	for _, path := range paths {
		rows := companyCraftOrderRead(t, f, writer, path)
		for _, companyID := range []string{"cco-a", "cco-b"} {
			if record, ok := rows[companyID]; !ok || !reflect.DeepEqual(record.CraftCategoryOrder, []string{}) {
				t.Fatalf("default company order missing: %s %#v", path, rows)
			}
		}
	}
	companyCraftOrderStored(t, f.a.db, "cco-a", []string{}, true)
	orderA := []string{"丙纶", "室外防水", "开工"}
	input := companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = orderA
	f.request(t, writer, "POST", "/api/entity", "", input, 200)
	companyCraftOrderStored(t, f.a.db, "cco-a", orderA, false)
	companyCraftOrderStored(t, f.a.db, "cco-b", []string{}, true)
	if companyCraftOrderPayload(t, f.a.db, "cco-b")["version"] != int64(1) {
		t.Fatal("saving company A changed company B version")
	}
	for _, path := range paths {
		rows := companyCraftOrderRead(t, f, writer, path)
		if !reflect.DeepEqual(rows["cco-a"].CraftCategoryOrder, orderA) || !reflect.DeepEqual(rows["cco-b"].CraftCategoryOrder, []string{}) {
			t.Fatal("company isolation/read ordering", path, rows)
		}
	}
	for _, path := range paths[1:] {
		rows := companyCraftOrderRead(t, f, reader, path)
		if !reflect.DeepEqual(rows["cco-a"].CraftCategoryOrder, orderA) || rows["cco-a"].Contact != nil || rows["cco-a"].Phone != nil || rows["cco-a"].Note != nil {
			t.Fatal("order-only reader lost sorting or gained private company fields", path, rows)
		}
	}
	// An older client must preserve the stored order, not implicitly reset it.
	f.request(t, writer, "POST", "/api/entity", "", companyCraftOrderPayload(t, f.a.db, "cco-a"), 200)
	companyCraftOrderStored(t, f.a.db, "cco-a", orderA, false)
	orderB := []string{"室外防水", "防水涂料"}
	input = companyCraftOrderPayload(t, f.a.db, "cco-b")
	input["craft_category_order"] = orderB
	f.request(t, writer, "POST", "/api/entity", "", input, 200)
	input = companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{}
	f.request(t, writer, "POST", "/api/entity", "", input, 200)
	companyCraftOrderStored(t, f.a.db, "cco-a", []string{}, false)
	companyCraftOrderStored(t, f.a.db, "cco-b", orderB, false)
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{"omitted", nil}, {"reset", []string{}}, {"custom", []string{"维修", "回填"}},
	} {
		input = map[string]any{"entity": "companies", "name": "合成新公司-" + tc.name, "ticket_mode": "single"}
		if tc.order != nil {
			input["craft_category_order"] = tc.order
		}
		result := priceTestJSON(t, f.request(t, writer, "POST", "/api/entity", "", input, 200))
		want := tc.order
		if want == nil {
			want = []string{}
		}
		companyCraftOrderStored(t, f.a.db, result["id"].(string), want, tc.order == nil)
	}
	var permissions string
	if err := f.a.db.QueryRow("SELECT permissions FROM users WHERE id=?", writer.id).Scan(&permissions); err != nil || permissions != `["customers"]` {
		t.Fatal("sorting changed permissions", permissions, err)
	}
	orderLocationsUnchanged(t, f.a.db, before)
}

func TestCompanyCraftCategoryOrderRejectAndConflictMySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	maintenanceFaultExec(t, f.a.db, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('cco-unselected-source','','维修','合成尚未选择的公共工艺','平方',0,500)`)
	writer := f.employee(t, []string{"customers"})
	unauthorized := f.employee(t, []string{"orders", "customer_prices", "wages", "combo_pricing", "rates"})
	before := orderLocationsState(t, f.a.db)
	for _, raw := range []string{`null`, `{}`, `"开工"`, `1`, `true`, `[1]`, `[null]`, `[false]`, `[["开工"]]`, `["开工","开工"]`, `["未知类别"]`, `["cco-a-open"]`, `["开工墙面"]`, `[" 开工 "]`, jsonText(append(append([]string{}, categories...), "开工"))} {
		input := companyCraftOrderPayload(t, f.a.db, "cco-a")
		input["craft_category_order"] = json.RawMessage(raw)
		f.request(t, writer, "POST", "/api/entity", "", input, 400)
		orderLocationsUnchanged(t, f.a.db, before)
	}
	input := companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{"丙纶", "开工"}
	f.request(t, unauthorized, "POST", "/api/entity", "", input, 403)
	f.request(t, unauthorized, "GET", "/api/catalog?entity=companies", "", nil, 403)
	orderLocationsUnchanged(t, f.a.db, before)
	key := "cco-idempotency-" + id()
	first := priceTestJSON(t, f.request(t, writer, "POST", "/api/entity", key, input, 200))
	before = orderLocationsState(t, f.a.db)
	second := priceTestJSON(t, f.request(t, writer, "POST", "/api/entity", key, input, 200))
	if !reflect.DeepEqual(first, second) {
		t.Fatal("idempotent save response changed", first, second)
	}
	orderLocationsUnchanged(t, f.a.db, before)
	// The old version must fail even if the requested order is otherwise valid.
	f.request(t, writer, "POST", "/api/entity", "", input, 409)
	orderLocationsUnchanged(t, f.a.db, before)
	input["craft_category_order"] = []string{"开工", "丙纶"}
	f.request(t, writer, "POST", "/api/entity", key, input, 409)
	orderLocationsUnchanged(t, f.a.db, before)
	// Fixed-craft errors occur after the company UPDATE and must roll it back.
	input = companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{"室外防水"}
	input["fixed_crafts"] = []map[string]any{{"id": "cco-a-open", "source_id": "cco-open-source", "version": int64(999), "ticket_slot": 1}}
	f.request(t, writer, "POST", "/api/entity", "", input, 409)
	orderLocationsUnchanged(t, f.a.db, before)
	input["fixed_crafts"] = []map[string]any{{"id": "cco-b-open", "source_id": "cco-open-source", "version": int64(1), "ticket_slot": 1}}
	f.request(t, writer, "POST", "/api/entity", "", input, 400)
	orderLocationsUnchanged(t, f.a.db, before)
	// An existing source now reselects its stable V3 company identity. Use a
	// genuinely new public source to test the independent new-price boundary.
	input["fixed_crafts"] = []map[string]any{{"source_id": "cco-unselected-source", "ticket_slot": 1, "price_cents": 0}}
	f.request(t, writer, "POST", "/api/entity", "", input, 403)
	orderLocationsUnchanged(t, f.a.db, before)
}

// Revocation must be checked before cached idempotent responses are returned.
// This fixture revokes only its disposable employee's permission directly so
// the same existing session exercises the route's fresh user lookup as well.
func TestCompanyCraftCategoryOrderRevokedIdempotentReplayMySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	writer := f.employee(t, []string{"customers"})
	input := companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{"丙纶", "开工"}
	key := "cco-revocation-" + id()
	first := priceTestJSON(t, f.request(t, writer, "POST", "/api/entity", key, input, 200))
	before := orderLocationsState(t, f.a.db)
	second := priceTestJSON(t, f.request(t, writer, "POST", "/api/entity", key, input, 200))
	if !reflect.DeepEqual(first, second) {
		t.Fatal("authorized idempotent replay changed", first, second)
	}
	orderLocationsUnchanged(t, f.a.db, before)
	if _, err := f.a.db.Exec("UPDATE users SET permissions=JSON_ARRAY('orders','customer_prices','wages','combo_pricing'),version=version+1 WHERE id=?", writer.id); err != nil {
		t.Fatal(err)
	}
	before = orderLocationsState(t, f.a.db)
	f.request(t, writer, "POST", "/api/entity", key, input, 403)
	f.request(t, writer, "POST", "/api/entity", "", input, 403)
	orderLocationsUnchanged(t, f.a.db, before)
}

func companyCraftOrderWithoutVersions(t *testing.T, rows []string) []string {
	t.Helper()
	out := []string{}
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		delete(row, "version")
		out = append(out, jsonText(row))
	}
	sort.Strings(out)
	return out
}

func TestCompanyCraftCategoryOrderNoPriceSaveMySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	writer := f.employee(t, []string{"customers"})
	before := companyCraftOrderHistory(t, f.a.db)
	rows, err := f.a.db.Query("SELECT id,source_id,version FROM crafts WHERE company_id='cco-a' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{}
	for rows.Next() {
		var craftID, sourceID string
		var version int64
		if err := rows.Scan(&craftID, &sourceID, &version); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		items = append(items, map[string]any{"id": craftID, "source_id": sourceID, "version": version, "ticket_slot": 1, "price_cents": 999999, "wage_cents": 999999, "included_by_craft_id": "cco-b-water"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	input := companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{"丙纶", "防水涂料", "室外防水", "开工"}
	input["fixed_crafts"] = items
	f.request(t, writer, "POST", "/api/entity", "", input, 200)
	after := orderLocationsState(t, f.a.db)
	// V3 saves the submitted company-item order as well. Only these explicit
	// display ranks may change; every price, wage, link and other company row
	// still receives the original complete-row comparison below.
	for i, raw := range before["crafts"] {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		for rank, item := range items {
			if row["id"] == item["id"] {
				row["display_order"] = rank
			}
		}
		before["crafts"][i] = jsonText(row)
	}
	if !reflect.DeepEqual(companyCraftOrderWithoutVersions(t, before["crafts"]), companyCraftOrderWithoutVersions(t, after["crafts"])) {
		t.Fatal("sorting/full no-price company save changed craft prices, wages, metadata or included links")
	}
	delete(before, "crafts") // Existing full fixed-craft saves increment versions.
	orderLocationsUnchanged(t, f.a.db, before)
	w := f.request(t, writer, "GET", "/api/reference?tables=companies,crafts&craft_scope=order&company_id=cco-a", "", nil, 200)
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	assertNoCustomerMoney(t, response)
	for _, raw := range response["crafts"].([]any) {
		if _, ok := raw.(map[string]any)["wage_cents"]; ok {
			t.Fatal("customers-only sorting reader gained wage access")
		}
	}
}

// Used only to compute expected snapshot rows; this never writes to a database.
func companyCraftOrderDefaultRows(t *testing.T, rows []string) []string {
	t.Helper()
	out := []string{}
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		row["craft_category_order"] = nil
		out = append(out, jsonText(row))
	}
	sort.Strings(out)
	return out
}

func TestCompanyCraftCategoryOrderMigration438MySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	// Simulate the additive migration boundary in this disposable schema only.
	maintenanceFaultExec(t, f.a.db, "ALTER TABLE companies DROP COLUMN craft_category_order")
	before := orderLocationsState(t, f.a.db)
	before["companies"] = companyCraftOrderDefaultRows(t, before["companies"])
	if err := initSchema(f.a.db); err != nil {
		t.Fatal("migration 438 in locked schema chain", err)
	}
	var dataType, nullable string
	var defaultValue sql.NullString
	if err := f.a.db.QueryRow("SELECT DATA_TYPE,IS_NULLABLE,COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='companies' AND COLUMN_NAME='craft_category_order'").Scan(&dataType, &nullable, &defaultValue); err != nil || dataType != "json" || nullable != "YES" || defaultValue.Valid {
		t.Fatal("migration column must be nullable JSON without backfill", dataType, nullable, defaultValue, err)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=438", 1)
	orderLocationsUnchanged(t, f.a.db, before)
	maintenanceFaultExec(t, f.a.db, `UPDATE companies SET craft_category_order=JSON_ARRAY('室外防水','丙纶') WHERE id='cco-a'`)
	before = orderLocationsState(t, f.a.db)
	var appliedAt string
	if err := f.a.db.QueryRow("SELECT CAST(applied_at AS CHAR) FROM schema_migrations WHERE version=438").Scan(&appliedAt); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := initSchema(f.a.db); err != nil {
			t.Fatal("idempotent initSchema", err)
		}
	}
	var afterAppliedAt string
	if err := f.a.db.QueryRow("SELECT CAST(applied_at AS CHAR) FROM schema_migrations WHERE version=438").Scan(&afterAppliedAt); err != nil || afterAppliedAt != appliedAt {
		t.Fatal("idempotent migration replaced marker", afterAppliedAt, appliedAt, err)
	}
	orderLocationsUnchanged(t, f.a.db, before)
	// Resume the DDL-before-marker window without clearing a saved order.
	maintenanceFaultExec(t, f.a.db, "DELETE FROM schema_migrations WHERE version=438")
	if err := initSchema(f.a.db); err != nil {
		t.Fatal("resume migration 438", err)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=438", 1)
	orderLocationsUnchanged(t, f.a.db, before)
	companyCraftOrderStored(t, f.a.db, "cco-a", []string{"室外防水", "丙纶"}, false)
	companyCraftOrderStored(t, f.a.db, "cco-b", []string{}, true)
}

func TestCompanyCraftCategoryOrderSnapshotCompatibilityMySQL(t *testing.T) {
	f := newCompanyCraftOrderMySQLFixture(t)
	input := companyCraftOrderPayload(t, f.a.db, "cco-a")
	input["craft_category_order"] = []string{"室外防水", "丙纶", "开工"}
	f.request(t, f.admin, "POST", "/api/entity", "", input, 200)
	f.request(t, f.admin, "POST", "/api/entity", "", map[string]any{"entity": "companies", "name": "合成显式默认公司", "craft_category_order": []string{}}, 200)
	before := orderLocationsState(t, f.a.db)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	name, err := f.a.backup()
	if err != nil {
		t.Fatal("actual snapshot writer", err)
	}
	file := filepath.Join(dir, name)
	target, _ := maintenanceFaultDB(t)
	if err := target.restore(file); err != nil {
		t.Fatal("new company order snapshot restore", err)
	}
	orderLocationsUnchanged(t, target.db, before)
	companyCraftOrderStored(t, target.db, "cco-a", []string{"室外防水", "丙纶", "开工"}, false)
	companyCraftOrderStored(t, target.db, "cco-b", []string{}, true)
	// Remove only the additive field to model a pre-438 snapshot; retain the
	// original counts and every order/price/wage/location field and account.
	inputFile, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer inputFile.Close()
	decoder := json.NewDecoder(inputFile)
	decoder.UseNumber()
	records := []snapshotRecord{}
	for {
		var record snapshotRecord
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if record.Table == "companies" {
			delete(record.Row, "craft_category_order")
		}
		records = append(records, record)
	}
	legacyFile := maintenanceFaultSnapshot(t, dir, records...)
	legacyTarget, _ := maintenanceFaultDB(t)
	if err := legacyTarget.restore(legacyFile); err != nil {
		t.Fatal("older snapshot without company order", err)
	}
	before["companies"] = companyCraftOrderDefaultRows(t, before["companies"])
	orderLocationsUnchanged(t, legacyTarget.db, before)
	companyCraftOrderStored(t, legacyTarget.db, "cco-a", []string{}, true)
	companyCraftOrderStored(t, legacyTarget.db, "cco-b", []string{}, true)
	rows, err := readCatalog(legacyTarget.db, "companies", "companies", " ORDER BY id", nil, f.u)
	if err != nil {
		t.Fatal(err)
	}
	for _, company := range rows.([]companiesRecord) {
		if company.CraftCategoryOrder == nil || len(company.CraftCategoryOrder) != 0 {
			t.Fatal("legacy snapshot read must expose a default array", company)
		}
	}
}
