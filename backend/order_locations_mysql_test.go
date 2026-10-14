package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// All database tests below use maintenanceFaultDB's opt-in gate and disposable
// schema. They must only be run after the target instance and grants are verified.
type orderLocationsFixture struct {
	a *App
	u User
}

func newOrderLocationsFixture(t *testing.T) orderLocationsFixture {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('loc-co','合成部位公司','single')`,
		`INSERT INTO workers(id,name,crafts) VALUES('loc-a','合成甲师傅',JSON_ARRAY('开工')),('loc-b','合成乙师傅',JSON_ARRAY('开工'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('loc-wh','合成甲仓库','loc-a')`,
		`INSERT INTO products(id,name,unit) VALUES('loc-product','合成部位材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('loc-wh','loc-product',20000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,spec,subitems) VALUES('loc-source','','开工','合成部位工艺','平方',0,300,'保存的合成规格',JSON_ARRAY(JSON_OBJECT('id','part','name','合成配置小项')))`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,spec,subitems) SELECT 'loc-other-source',company_id,category,'合成只读工艺',mode,price_cents,wage_cents,spec,subitems FROM crafts WHERE id='loc-source'`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('loc-fixed','loc-co','loc-source','开工','合成部位工艺','平方',1800,0),('loc-other','loc-co','loc-other-source','开工','合成只读工艺','平方',1800,0)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	return orderLocationsFixture{a: a, u: User{ID: "locations-admin", Name: "合成部位管理员", Role: "admin"}}
}

func (f orderLocationsFixture) call(u User, path, key string, payload any) (any, error) {
	raw := []byte(jsonText(payload))
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.Header.Set("Idempotency-Key", "order-locations-"+key)
	return f.a.mutate(r, u, raw, func(tx *sql.Tx, operation string) (any, error) {
		return f.a.post(tx, path, u, raw, operation)
	})
}

func (f orderLocationsFixture) must(t *testing.T, payload any) map[string]any {
	t.Helper()
	out, err := f.call(f.u, "/api/order", id(), payload)
	if err != nil {
		t.Fatal("save independent locations", err)
	}
	return out.(map[string]any)
}

func orderLocationsRead(t *testing.T, a *App, orderID string, u User) orderDetailView {
	t.Helper()
	w := httptest.NewRecorder()
	if err := a.readOrder(w, httptest.NewRequest("GET", "/api/order?id="+orderID, nil), u); err != nil {
		t.Fatal("read independent locations", err)
	}
	if w.Code != 200 {
		t.Fatal("order response", w.Code, w.Body.String())
	}
	var detail orderDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Lines []map[string]json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, line := range fields.Lines {
		for _, field := range []string{"region", "subitem_name", "actual_location"} {
			if _, present := line[field]; !present {
				t.Fatal("order response omitted location contract", field)
			}
		}
	}
	return detail
}

func orderLocationsEdit(t *testing.T, detail orderDetailView) Order {
	t.Helper()
	var order Order
	if err := json.Unmarshal([]byte(jsonText(detail)), &order); err != nil {
		t.Fatal(err)
	}
	// Ordinary edits do not request a customer-charge combination change.
	order.Combinations = nil
	return order
}

func orderLocationsOldClient(t *testing.T, order Order) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(jsonText(order)), &fields); err != nil {
		t.Fatal(err)
	}
	for _, raw := range fields["lines"].([]any) {
		line := raw.(map[string]any)
		delete(line, "subitem_name")
		delete(line, "actual_location")
	}
	return fields
}

// Compare complete committed rows, not just counts: an edit may delete/reinsert
// the same number of lines while losing snapshots, stock or account versions.
func orderLocationsState(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	state := map[string][]string{}
	for _, table := range backupTables {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(table, err)
		}
		data := []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(table, err)
			}
			row := map[string]any{}
			for i, column := range columns {
				value := values[i]
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				row[column] = value
			}
			data = append(data, jsonText(row))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(table, err)
		}
		rows.Close()
		sort.Strings(data)
		state[table] = data
	}
	return state
}

func orderLocationsUnchanged(t *testing.T, db *sql.DB, before map[string][]string) {
	t.Helper()
	after := orderLocationsState(t, db)
	for table, want := range before {
		if !reflect.DeepEqual(after[table], want) {
			t.Fatalf("committed %s changed: before=%v after=%v", table, want, after[table])
		}
	}
}

func TestOrderLocationsWorkflowMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	order := Order{Company: "loc-co", Address: "合成独立多部位工地", Date: "2026-10-10", Lines: []Line{
		{CraftID: "loc-fixed", SubitemID: "part", SubitemName: "客户端伪造名称", ActualLocation: "厨房东墙🧱", Worker: "loc-a", WorkerName: "伪造姓名", SortOrder: 20, Description: "厨房独立备注", Segments: []Segment{{Length: 2, Width: 3, Surface: "尺寸表面不是部位"}, {Length: 1, Width: 2}}},
		{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: "主卫北墙", Worker: "loc-b", SortOrder: 10, Description: "主卫独立备注", Segments: []Segment{{Length: 2, Width: 2}}},
	}}
	key := id()
	out, err := f.call(f.u, "/api/order", key, order)
	if err != nil {
		t.Fatal(err)
	}
	orderID := out.(map[string]any)["id"].(string)
	repeated, err := f.call(f.u, "/api/order", key, order)
	if err != nil || jsonText(repeated) != jsonText(out) {
		t.Fatal("idempotent location save", repeated, err)
	}
	d := orderLocationsRead(t, f.a, orderID, f.u)
	if len(d.Lines) != 2 || d.TotalCents != 21600 || d.WageCents == nil || *d.WageCents != 3600 || d.WageRecords == nil || len(*d.WageRecords) != 2 || d.WorkerID != "" {
		t.Fatal("independent quantities/workers or original calculation lost", d)
	}
	if d.Lines[0].ID == "" || d.Lines[0].ID == d.Lines[1].ID {
		t.Fatal("same craft/subitem rows collapsed", d.Lines)
	}
	for i, want := range []Line{order.Lines[1], order.Lines[0]} {
		line := d.Lines[i]
		if line.CraftID != "loc-fixed" || line.SubitemID != "part" || line.SubitemName != "合成配置小项" || line.Region != "" || line.ActualLocation != want.ActualLocation || line.Description != want.Description || line.WorkerID != want.Worker || line.SortOrder != want.SortOrder || !reflect.DeepEqual(line.Segments, want.Segments) {
			t.Fatal("location row identity/snapshot/order lost", line, want)
		}
	}
	if d.Lines[0].Qty != 4000000 || d.Lines[1].Qty != 8000000 || *d.Lines[0].Labor != 1200 || *d.Lines[1].Labor != 2400 || d.Lines[1].WorkerName != "合成甲师傅" {
		t.Fatal("per-location dimensions or worker snapshot changed", d.Lines)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM orders", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM requests", 1)
	before := orderLocationsState(t, f.a.db)
	changed := order
	changed.Lines = append([]Line{}, order.Lines...)
	changed.Lines[0].ActualLocation = "同编号不同部位"
	_, err = f.call(f.u, "/api/order", key, changed)
	maintenanceFaultError(t, err, 409)
	stale := orderLocationsEdit(t, d)
	stale.Version = 0
	_, err = f.call(f.u, "/api/order", id(), stale)
	maintenanceFaultError(t, err, 409)
	duplicate := orderLocationsEdit(t, d)
	duplicate.Lines = append(duplicate.Lines, duplicate.Lines[0])
	_, err = f.call(f.u, "/api/order", id(), duplicate)
	maintenanceFaultError(t, err, 400)
	orderLocationsUnchanged(t, f.a.db, before)

	maintenanceFaultExec(t, f.a.db, `UPDATE crafts SET subitems=JSON_ARRAY(JSON_OBJECT('id','part','name','改名后的配置小项')) WHERE id='loc-source'`)
	edit := orderLocationsEdit(t, d)
	edit.Lines[0].ActualLocation, edit.Lines[0].SortOrder = "", 30
	edit.Lines[0].Region, edit.Lines[0].SubitemName = "篡改旧文字", "篡改旧小项"
	edit.Lines[1].ActualLocation, edit.Lines[1].SortOrder = "厨房西墙🧱", 10
	edit.Lines[1].Description = "厨房修改后的独立备注"
	edit.Lines[1].Segments = []Segment{{Length: 2, Width: 5}, {Length: 1, Width: 1}}
	edit.Lines = append(edit.Lines, Line{CraftID: "loc-fixed", SubitemID: "part", SubitemName: "伪造新名称", ActualLocation: "次卫南墙", Worker: "loc-b", SortOrder: 20, Description: "新部位备注", Segments: []Segment{{Length: 3, Width: 2}}})
	f.must(t, edit)
	updated := orderLocationsRead(t, f.a, orderID, f.u)
	if len(updated.Lines) != 3 || updated.Version != 2 || updated.TotalCents != 37800 || *updated.WageCents != 6300 {
		t.Fatal("location edit/add totals or count", updated)
	}
	if updated.Lines[0].ID != d.Lines[1].ID || updated.Lines[2].ID != d.Lines[0].ID || updated.Lines[1].ID == updated.Lines[0].ID || updated.Lines[1].ID == updated.Lines[2].ID {
		t.Fatal("reordering reused or lost independent IDs", updated.Lines)
	}
	for i, name := range []string{"合成配置小项", "改名后的配置小项", "合成配置小项"} {
		if updated.Lines[i].SubitemName != name || updated.Lines[i].Region != "" {
			t.Fatal("configuration rename rewrote saved snapshots", updated.Lines)
		}
	}
	if updated.Lines[0].ActualLocation != "厨房西墙🧱" || updated.Lines[0].Qty != 11000000 || updated.Lines[1].ActualLocation != "次卫南墙" || updated.Lines[1].Qty != 6000000 || updated.Lines[2].ActualLocation != "" || updated.Lines[2].Qty != 4000000 || updated.Lines[0].Description != edit.Lines[1].Description || !reflect.DeepEqual(updated.Lines[0].Segments, edit.Lines[1].Segments) {
		t.Fatal("independent location/clear/segments did not survive edit", updated.Lines)
	}

	// An old client can still edit a record without knowing either new field.
	legacyEdit := orderLocationsEdit(t, updated)
	legacyEdit.Lines[0].Description = "旧客户端只修改备注"
	f.must(t, orderLocationsOldClient(t, legacyEdit))
	reopened := orderLocationsRead(t, f.a, orderID, f.u)
	for i, old := range updated.Lines {
		got := reopened.Lines[i]
		if got.ID != old.ID || got.ActualLocation != old.ActualLocation || got.SubitemName != old.SubitemName || got.Region != old.Region || got.WorkerID != old.WorkerID || got.Qty != old.Qty || !reflect.DeepEqual(got.Segments, old.Segments) {
			t.Fatal("old-client edit lost an independent location/snapshot", got, old)
		}
	}
	viewer := User{ID: "locations-viewer", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"loc-source"}}
	public := orderLocationsRead(t, f.a, orderID, viewer)
	for _, line := range public.Lines {
		if line.Wage != nil || line.Labor != nil || line.SubitemName == "" || line.Region != "" {
			t.Fatal("new fields changed wage visibility or snapshot", line)
		}
	}
}

func TestOrderLocationsLegacyReadonlyMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	order := Order{Company: "loc-co", Address: "合成旧字段与只读工地", Date: "2026-10-10", Lines: []Line{
		{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-a", SortOrder: 10, Segments: []Segment{{Length: 2, Width: 3, Surface: "旧表面不是实际部位"}}},
		{CraftID: "loc-other", SubitemID: "part", Worker: "loc-b", SortOrder: 20, Segments: []Segment{{Length: 1, Width: 2}}},
		{CraftID: "loc-other", SubitemID: "part", Worker: "loc-b", SortOrder: 30, Segments: []Segment{{Length: 2, Width: 2}}},
		{CraftID: "loc-other", SubitemID: "part", Worker: "loc-b", SortOrder: 40, Segments: []Segment{{Length: 3, Width: 2}}},
	}}
	orderID := f.must(t, order)["id"].(string)
	d := orderLocationsRead(t, f.a, orderID, f.u)
	legacy := []struct {
		region, name, location string
		nameValue, locValue    any
	}{
		{"主卫 / 历史混合原文", "", "", nil, nil},
		{"厨房 / 历史混合原文", "保存的小项快照", "", "保存的小项快照", nil},
		{"次卫 / 历史混合原文", "", "已存实际东墙", nil, "已存实际东墙"},
		{"阳台 / 历史混合原文", "独立保存的小项", "独立保存的北墙", "独立保存的小项", "独立保存的北墙"},
	}
	for i, old := range legacy {
		if _, err := f.a.db.Exec("UPDATE order_lines SET region=?,subitem_name=?,actual_location=? WHERE id=?", old.region, old.nameValue, old.locValue, d.Lines[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	d = orderLocationsRead(t, f.a, orderID, f.u)
	for i, old := range legacy {
		line := d.Lines[i]
		expectedName := "合成部位工艺"
		if line.CraftID == "loc-other" {
			expectedName = "合成只读工艺"
		}
		if line.Region != old.region || line.SubitemName != old.name || line.ActualLocation != old.location || line.Name != expectedName {
			t.Fatal("legacy/partial fields were inferred on read", line, old)
		}
	}
	limited := User{ID: "locations-limited", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"loc-source"}}
	visible := orderLocationsRead(t, f.a, orderID, limited)
	if !visible.Lines[0].Editable || visible.Lines[1].Editable || visible.Lines[2].Editable || visible.Lines[3].Editable || visible.WageCents != nil {
		t.Fatal("existing read-only/wage boundaries changed", visible)
	}
	before := orderLocationsState(t, f.a.db)
	forged := orderLocationsEdit(t, visible)
	forged.Lines[1].ActualLocation = "无权更改的部位"
	_, err := f.call(limited, "/api/order", id(), forged)
	maintenanceFaultError(t, err, 403)
	unauthorized := orderLocationsEdit(t, visible)
	unauthorized.Lines = unauthorized.Lines[:1]
	unauthorized.Lines = append(unauthorized.Lines, Line{CraftID: "loc-other", SubitemID: "part", ActualLocation: "无权新增的部位", Worker: "loc-b", Segments: []Segment{{Length: 2, Width: 3}}})
	_, err = f.call(limited, "/api/order", id(), unauthorized)
	maintenanceFaultError(t, err, 403)
	orderLocationsUnchanged(t, f.a.db, before)

	// Omitted protected rows are restored as-is, including partially populated
	// snapshots. Legacy clients cannot replace the editable row's old region.
	allowed := orderLocationsEdit(t, visible)
	allowed.Lines = allowed.Lines[:1]
	allowed.Lines[0].Region = "旧客户端想改原文"
	allowed.Lines[0].Description = "授权工艺的新备注"
	f.mustWithUser(t, limited, orderLocationsOldClient(t, allowed))
	preserved := orderLocationsRead(t, f.a, orderID, f.u)
	if len(preserved.Lines) != 4 || preserved.Lines[0].Region != legacy[0].region || preserved.Lines[0].SubitemName != "" || preserved.Lines[0].ActualLocation != "" || preserved.Lines[0].Segments[0].Surface != "旧表面不是实际部位" || preserved.Lines[0].Description != allowed.Lines[0].Description {
		t.Fatal("old editable row was inferred or protected rows were dropped", preserved)
	}
	for i := 1; i < len(d.Lines); i++ {
		if !reflect.DeepEqual(preserved.Lines[i], d.Lines[i]) {
			t.Fatal("hidden/read-only legacy snapshot changed", preserved.Lines[i], d.Lines[i])
		}
	}

	maintenanceFaultExec(t, f.a.db, `UPDATE crafts SET subitems=JSON_ARRAY(JSON_OBJECT('id','part','name','当前改名的配置')) WHERE id='loc-source'`)
	edit := orderLocationsEdit(t, preserved)
	for i := range edit.Lines {
		edit.Lines[i].Region, edit.Lines[i].SubitemName = "试图覆盖旧region", "试图覆盖旧小项"
		edit.Lines[i].ActualLocation = []string{"新填写实际西墙", "部分字段行的新部位", "修改后实际东墙", ""}[i]
	}
	f.must(t, edit)
	updated := orderLocationsRead(t, f.a, orderID, f.u)
	for i, old := range legacy {
		if updated.Lines[i].Region != old.region || updated.Lines[i].SubitemName != old.name || updated.Lines[i].ActualLocation != edit.Lines[i].ActualLocation {
			t.Fatal("legacy region/name snapshot changed on an authorized location edit", updated.Lines[i], old)
		}
	}
	account := (*updated.WageRecords)[0]
	_, err = f.call(f.u, "/api/wage-settlement", id(), SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: account.ID, Version: account.Version}}})
	if err != nil {
		t.Fatal("settle existing worker account", err)
	}
	locked := orderLocationsRead(t, f.a, orderID, f.u)
	if locked.Editable || locked.Lines[0].Editable || locked.WagePaid == nil || !*locked.WagePaid {
		t.Fatal("paid account no longer protects the whole construction record", locked)
	}
	before = orderLocationsState(t, f.a.db)
	paidEdit := orderLocationsEdit(t, locked)
	paidEdit.Lines[0].ActualLocation = "已发工资后的非法修改"
	_, err = f.call(f.u, "/api/order", id(), paidEdit)
	maintenanceFaultError(t, err, 400)
	orderLocationsUnchanged(t, f.a.db, before)
}

func (f orderLocationsFixture) mustWithUser(t *testing.T, u User, payload any) {
	t.Helper()
	if _, err := f.call(u, "/api/order", id(), payload); err != nil {
		t.Fatal("authorized location edit", err)
	}
}

func TestOrderLocationsRollbackMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	order := Order{Company: "loc-co", Address: "合成部位回滚原地址", Date: "2026-10-10", Worker: "loc-a", Lines: []Line{
		{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: "保存的北墙", Worker: "loc-a", SortOrder: 10, Description: "保存的北墙备注", Segments: []Segment{{Length: 2, Width: 3}}},
		{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: "保存的东墙", Worker: "loc-b", SortOrder: 20, Description: "保存的东墙备注", Segments: []Segment{{Length: 2, Width: 2}}},
	}, Materials: []Material{{Product: "loc-product", Qty: 1000000}}}
	orderID := f.must(t, order)["id"].(string)
	d := orderLocationsRead(t, f.a, orderID, f.u)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM stock_balances WHERE qty_micro=19000000", 1)
	maintenanceFaultExec(t, f.a.db, `CREATE TRIGGER location_line_fault BEFORE INSERT ON order_lines FOR EACH ROW BEGIN IF NEW.actual_location='合成故障部位' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic location insert fault'; END IF; END`)
	// Existing line IDs are now updated in place to preserve combination links.
	maintenanceFaultExec(t, f.a.db, `CREATE TRIGGER location_line_update_fault BEFORE UPDATE ON order_lines FOR EACH ROW BEGIN IF NEW.actual_location='合成故障部位' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic location update fault'; END IF; END`)
	before := orderLocationsState(t, f.a.db)
	edit := orderLocationsEdit(t, d)
	edit.Materials[0].Qty = 3000000
	edit.Lines[0].ActualLocation, edit.Lines[0].Description, edit.Lines[0].Worker = "先插入的修改部位", "修改后的独立备注", "loc-b"
	edit.Lines[0].Segments = []Segment{{Length: 4, Width: 3}}
	edit.Lines[1].ActualLocation = "合成故障部位"
	editKey := id()
	_, err := f.call(f.u, "/api/order", editKey, edit)
	maintenanceFaultError(t, err, 0)
	orderLocationsUnchanged(t, f.a.db, before)
	if got := orderLocationsRead(t, f.a, orderID, f.u); !reflect.DeepEqual(got, d) {
		t.Fatal("late location INSERT failure changed the original detail", got, d)
	}

	fresh := order
	fresh.Address = "合成故障自动建档地址"
	fresh.Lines = append([]Line{}, order.Lines...)
	fresh.Lines[1].ActualLocation = "合成故障部位"
	fresh.Materials = []Material{{Product: "loc-product", Qty: 2000000}}
	freshKey := id()
	_, err = f.call(f.u, "/api/order", freshKey, fresh)
	maintenanceFaultError(t, err, 0)
	orderLocationsUnchanged(t, f.a.db, before)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM projects WHERE address='合成故障自动建档地址'", 0)
	maintenanceFaultExec(t, f.a.db, "DROP TRIGGER location_line_fault")
	maintenanceFaultExec(t, f.a.db, "DROP TRIGGER location_line_update_fault")

	// Failed operation keys were rolled back with their stock/order/audit rows.
	if _, err = f.call(f.u, "/api/order", editKey, edit); err != nil {
		t.Fatal("retry failed location edit after removing synthetic fault", err)
	}
	updated := orderLocationsRead(t, f.a, orderID, f.u)
	if updated.Version != 2 || updated.Lines[0].ActualLocation != edit.Lines[0].ActualLocation || updated.Lines[1].ActualLocation != "合成故障部位" || updated.Lines[0].WorkerID != "loc-b" {
		t.Fatal("successful retry lost independently edited fields", updated)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM stock_balances WHERE qty_micro=17000000", 1)
	if _, err = f.call(f.u, "/api/order", freshKey, fresh); err != nil {
		t.Fatal("retry failed automatic project creation", err)
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM projects WHERE address='合成故障自动建档地址'", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM stock_balances WHERE qty_micro=15000000", 1)
}

func TestOrderLocationsTextBoundariesMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	name, location := strings.Repeat("项🧩", 100), strings.Repeat("墙🧱", 100)
	if _, err := f.a.db.Exec("UPDATE crafts SET subitems=? WHERE id='loc-source'", jsonText([]map[string]string{{"id": "part", "name": name}})); err != nil {
		t.Fatal(err)
	}
	order := Order{Company: "loc-co", Address: "合成Unicode部位边界", Date: "2026-10-10", Lines: []Line{{CraftID: "loc-fixed", SubitemID: "part", ActualLocation: location, Worker: "loc-a", Segments: []Segment{{Length: 2, Width: 3}}}}}
	orderID := f.must(t, order)["id"].(string)
	d := orderLocationsRead(t, f.a, orderID, f.u)
	if d.Lines[0].ActualLocation != location || d.Lines[0].SubitemName != name || d.Lines[0].Region != "" {
		t.Fatal("200 Unicode characters were truncated or mixed into region", d.Lines)
	}
	before := orderLocationsState(t, f.a.db)
	edit := orderLocationsEdit(t, d)
	edit.Lines[0].ActualLocation += "界"
	_, err := f.call(f.u, "/api/order", id(), edit)
	maintenanceFaultError(t, err, 400)
	order.Address = "合成Unicode超长失败地址"
	order.Lines[0].ActualLocation += "界"
	_, err = f.call(f.u, "/api/order", id(), order)
	maintenanceFaultError(t, err, 400)
	orderLocationsUnchanged(t, f.a.db, before)
}

func orderLocationsNullableColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, column := range []string{"subitem_name", "actual_location"} {
		var dataType, nullable string
		var length int64
		var defaultValue sql.NullString
		if err := db.QueryRow(`SELECT DATA_TYPE,CHARACTER_MAXIMUM_LENGTH,IS_NULLABLE,COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_lines' AND COLUMN_NAME=?`, column).Scan(&dataType, &length, &nullable, &defaultValue); err != nil {
			t.Fatal(column, err)
		}
		if dataType != "varchar" || length != 200 || nullable != "YES" || defaultValue.Valid {
			t.Fatal("location column must be nullable VARCHAR(200), default NULL", column, dataType, length, nullable, defaultValue)
		}
	}
	maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version=435", 1)
	maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version=436", 1)
}

func TestOrderLocationsMigrationRerunMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	maintenanceFaultExec(t, f.a.db, `INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,total_cents,wage_cents,wage_paid,version,created_by) VALUES('loc-history','合成历史部位单','loc-co','合成历史地址','2026-09-01','loc-a','历史姓名快照','历史备注',4321,987,1,7,'old-admin')`)
	maintenanceFaultExec(t, f.a.db, `INSERT INTO order_lines(id,order_id,craft_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary,region,description,subitem_id,sort_order,worker_id,worker_name) VALUES('loc-history-line','loc-history','loc-fixed','开工','历史工艺快照','平方',JSON_ARRAY(JSON_OBJECT('length',2,'width',3,'surface','历史厨房表面')),7654321,111,222,333,444,0,'主卫 / 历史混合文字','历史独立备注','part',9,'loc-a','历史姓名快照')`)
	before := orderLocationsState(t, f.a.db)
	// Recreate pre-435 structure only in the disposable helper schema. History
	// intentionally differs from current prices, names, dimensions and totals.
	maintenanceFaultExec(t, f.a.db, "ALTER TABLE order_lines DROP COLUMN subitem_name, DROP COLUMN actual_location")
	maintenanceFaultExec(t, f.a.db, "DELETE FROM schema_migrations WHERE version IN (435,436)")
	for i := 0; i < 2; i++ {
		if err := initSchema(f.a.db); err != nil {
			t.Fatal("location migration/restart", i, err)
		}
	}
	orderLocationsNullableColumns(t, f.a.db)
	orderLocationsUnchanged(t, f.a.db, before)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM order_lines WHERE id='loc-history-line' AND subitem_name IS NULL AND actual_location IS NULL AND region='主卫 / 历史混合文字' AND qty_micro=7654321 AND total_cents=333 AND labor_cents=444", 1)
	d := orderLocationsRead(t, f.a, "loc-history", f.u)
	if d.Version != 7 || d.TotalCents != 4321 || *d.WageCents != 987 || d.Lines[0].SubitemName != "" || d.Lines[0].ActualLocation != "" || d.Lines[0].Region != "主卫 / 历史混合文字" || d.Lines[0].Name != "历史工艺快照" || d.Lines[0].Qty != 7654321 || d.Lines[0].Total != 333 || *d.Lines[0].Labor != 444 || d.Lines[0].Segments[0].Surface != "历史厨房表面" {
		t.Fatal("migration/read inferred text or recalculated historical values", d)
	}

	// Partial DDL: 435's column exists with a saved snapshot, but its marker was
	// not committed, and 436's column is absent. Resume must keep the snapshot.
	maintenanceFaultExec(t, f.a.db, "UPDATE order_lines SET subitem_name='部分升级已存小项' WHERE id='loc-history-line'")
	before = orderLocationsState(t, f.a.db)
	maintenanceFaultExec(t, f.a.db, "ALTER TABLE order_lines DROP COLUMN actual_location")
	maintenanceFaultExec(t, f.a.db, "DELETE FROM schema_migrations WHERE version IN (435,436)")
	if err := initSchema(f.a.db); err != nil {
		t.Fatal("resume partial location DDL", err)
	}
	orderLocationsNullableColumns(t, f.a.db)
	orderLocationsUnchanged(t, f.a.db, before)

	// Both DDL steps already succeeded but their markers were lost. Even real
	// location text must survive recording the migrations on the next run.
	maintenanceFaultExec(t, f.a.db, "UPDATE order_lines SET actual_location='部分升级已存实际北墙' WHERE id='loc-history-line'")
	before = orderLocationsState(t, f.a.db)
	maintenanceFaultExec(t, f.a.db, "DELETE FROM schema_migrations WHERE version IN (435,436)")
	for i := 0; i < 2; i++ {
		if err := initSchema(f.a.db); err != nil {
			t.Fatal("resume after successful location DDL", i, err)
		}
	}
	orderLocationsNullableColumns(t, f.a.db)
	orderLocationsUnchanged(t, f.a.db, before)
}

func TestOrderLocationsSnapshotRoundTripMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	order := Order{Company: "loc-co", Address: "合成部位快照恢复地址", Date: "2026-10-10", Lines: []Line{
		{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-a", ActualLocation: "快照厨房东墙🧱", SortOrder: 10, Description: "快照独立备注", Segments: []Segment{{Length: 2, Width: 3}, {Length: 1, Width: 2}}},
		{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-b", ActualLocation: "快照主卫北墙", SortOrder: 20, Segments: []Segment{{Length: 2, Width: 2, Surface: "历史表面"}}},
		{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-b", SortOrder: 30, Segments: []Segment{{Length: 3, Width: 2}}},
		{CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-a", SortOrder: 40, Segments: []Segment{{Length: 1, Width: 2}}},
	}}
	orderID := f.must(t, order)["id"].(string)
	d := orderLocationsRead(t, f.a, orderID, f.u)
	for i, fields := range []struct{ name, location any }{{nil, nil}, {"", ""}, {"部分旧小项快照", nil}} {
		if _, err := f.a.db.Exec("UPDATE order_lines SET region=?,subitem_name=?,actual_location=? WHERE id=?", "历史快照原文", fields.name, fields.location, d.Lines[i+1].ID); err != nil {
			t.Fatal(err)
		}
	}
	d = orderLocationsRead(t, f.a, orderID, f.u)
	before := orderLocationsState(t, f.a.db)
	name, err := f.a.backup()
	if err != nil {
		t.Fatal("backup new and partially populated location fields", err)
	}
	target, _ := maintenanceFaultDB(t)
	if err := target.restore(filepath.Join(dir, name)); err != nil {
		t.Fatal("restore location fields", err)
	}
	// SELECT * snapshot and recorded-column INSERT restore must retain SQL NULL
	// versus explicit empty strings as well as all separate rows and workers.
	orderLocationsUnchanged(t, target.db, before)
	restored := orderLocationsRead(t, target, orderID, f.u)
	if !reflect.DeepEqual(restored, d) {
		t.Fatal("location detail changed through snapshot round trip", restored, d)
	}
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_lines WHERE subitem_name IS NULL AND actual_location IS NULL", 1)
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_lines WHERE subitem_name='' AND actual_location=''", 1)
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_lines WHERE subitem_name='部分旧小项快照' AND actual_location IS NULL", 1)
	if err := initSchema(target.db); err != nil {
		t.Fatal("migration after current snapshot restore", err)
	}
	orderLocationsUnchanged(t, target.db, before)
}

func TestOrderLocationsLegacySnapshotMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	u := User{ID: "locations-legacy-admin", Name: "合成历史管理员", Role: "admin"}
	line := func(lineID, region string, sortOrder int) map[string]any {
		return map[string]any{"id": lineID, "order_id": "old-location", "craft_id": "historical-fixed", "category": "开工", "name": "历史名称不推断小项", "mode": "平方", "segments": `[{"length":2,"width":3,"surface":"旧厨房表面不推断位置"}]`, "qty_micro": 7123456, "price_cents": 111, "wage_cents": 222, "total_cents": 333, "labor_cents": 444, "temporary": 0, "subitem_id": "part", "region": region, "sort_order": sortOrder}
	}
	partialName := line("old-location-name", "厨房 / 旧原文", 20)
	partialName["subitem_name"] = "已存旧小项快照"
	partialLocation := line("old-location-place", "次卫 / 旧原文", 30)
	partialLocation["actual_location"] = "已存旧实际北墙"
	file := maintenanceFaultSnapshot(t, t.TempDir(),
		snapshotRecord{Format: "tegu-go-v1"},
		snapshotRecord{Table: "companies", Row: map[string]any{"id": "old-location-co", "name": "合成旧部位公司"}},
		snapshotRecord{Table: "orders", Row: map[string]any{"id": "old-location", "number": "OLD-LOCATION", "company_id": "old-location-co", "address": "合成历史部位地址", "work_date": "2026-09-01", "worker_id": "deleted-worker", "worker_name": "合成历史姓名快照", "note": "旧备注", "total_cents": 4321, "wage_cents": 987, "created_by": "old-admin", "version": 7}},
		snapshotRecord{Table: "order_lines", Row: line("old-location-empty", "主卫 / 旧原文", 10)},
		snapshotRecord{Table: "order_lines", Row: partialName},
		snapshotRecord{Table: "order_lines", Row: partialLocation},
		snapshotRecord{End: true, Counts: map[string]int{"companies": 1, "orders": 1, "order_lines": 3}},
	)
	if err := a.restore(file); err != nil {
		t.Fatal("restore snapshot with omitted/partial new location fields", err)
	}
	d := orderLocationsRead(t, a, "old-location", u)
	if d.Version != 7 || d.TotalCents != 4321 || *d.WageCents != 987 || len(d.Lines) != 3 {
		t.Fatal("legacy restore recalculated historical record", d)
	}
	for i, want := range []struct{ region, name, location string }{
		{"主卫 / 旧原文", "", ""}, {"厨房 / 旧原文", "已存旧小项快照", ""}, {"次卫 / 旧原文", "", "已存旧实际北墙"},
	} {
		got := d.Lines[i]
		if got.Region != want.region || got.SubitemName != want.name || got.ActualLocation != want.location || got.Qty != 7123456 || got.Total != 333 || *got.Labor != 444 || got.WorkerName != "合成历史姓名快照" || got.Segments[0].Surface != "旧厨房表面不推断位置" {
			t.Fatal("legacy recorded columns changed or missing location fields were inferred", got, want)
		}
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE id='old-location-empty' AND subitem_name IS NULL AND actual_location IS NULL", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE id='old-location-name' AND subitem_name='已存旧小项快照' AND actual_location IS NULL", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE id='old-location-place' AND subitem_name IS NULL AND actual_location='已存旧实际北墙'", 1)
	before := orderLocationsState(t, a.db)
	for i := 0; i < 2; i++ {
		if err := initSchema(a.db); err != nil {
			t.Fatal("migration after legacy snapshot restore", i, err)
		}
	}
	orderLocationsNullableColumns(t, a.db)
	orderLocationsUnchanged(t, a.db, before)
}
