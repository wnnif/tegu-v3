package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The configured identity is verified by the acceptance runner before opting
// in. maintenanceFaultDB creates/drops only a new random disposable schema;
// these C-prefixed fixtures never purge or reuse A1/A2/B rows in the base schema.
type stageCCombinationReadFixture struct {
	a *App
	u User
}

func newStageCCombinationReadFixture(t *testing.T) stageCCombinationReadFixture {
	t.Helper()
	a, _ := maintenanceFaultDB(t)
	f := stageCCombinationReadFixture{a: a, u: User{ID: "stage-c-read-admin", Name: "C 合成只读管理员", Role: "admin"}}
	for _, query := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('stage-c-read-co','C 合成快照公司','single'),('stage-c-read-foreign-co','C 外围公司','single')`,
		`INSERT INTO workers(id,name,crafts) VALUES('stage-c-read-a','C 快照甲',JSON_ARRAY('防水涂料','丙纶')),('stage-c-read-b','C 快照乙',JSON_ARRAY('防水涂料','丙纶'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('stage-c-read-wh','C 合成仓库','stage-c-read-a')`,
		`INSERT INTO products(id,name,unit) VALUES('stage-c-read-product','C 合成材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('stage-c-read-wh','stage-c-read-product',20000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('stage-c-read-w-source','','防水涂料','C 保存防水','平方',0,2000,JSON_ARRAY(JSON_OBJECT('id','stage-c-read-w-part','name','C 保存防水小项'))),('stage-c-read-p-source','','丙纶','C 保存丙纶','平方',0,1500,JSON_ARRAY(JSON_OBJECT('id','stage-c-read-p-part','name','C 保存丙纶小项')))`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('stage-c-read-w','stage-c-read-co','stage-c-read-w-source','防水涂料','C 保存防水','平方',5000,0),('stage-c-read-p','stage-c-read-co','stage-c-read-p-source','丙纶','C 保存丙纶','平方',3000,0),('stage-c-read-foreign-w','stage-c-read-foreign-co','stage-c-read-w-source','防水涂料','C 外围防水','平方',9900,0),('stage-c-read-foreign-p','stage-c-read-foreign-co','stage-c-read-p-source','丙纶','C 外围丙纶','平方',8800,0)`,
	} {
		f.exec(t, query)
	}
	return f
}

func (f stageCCombinationReadFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.a.db.Exec(query, args...); err != nil {
		t.Fatal("C synthetic fixture setup", err)
	}
	stockFixSyncLegacyBalances(t, f.a.db)
}

func (f stageCCombinationReadFixture) mutate(t *testing.T, path string, payload any) map[string]any {
	t.Helper()
	raw := []byte(jsonText(payload))
	r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	r.Header.Set("Idempotency-Key", "stage-c-read-"+id())
	out, err := f.a.mutate(r, f.u, raw, func(tx *sql.Tx, operation string) (any, error) {
		return f.a.post(tx, path, f.u, raw, operation)
	})
	if err != nil {
		t.Fatal("C synthetic fixture save through existing writer", err)
	}
	return out.(map[string]any)
}

func (f stageCCombinationReadFixture) read(t *testing.T, orderID string, u User) (orderDetailView, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := f.a.readOrder(w, httptest.NewRequest("GET", "/api/order?id="+orderID, nil), u); err != nil {
		t.Fatal("C read order", err)
	}
	if w.Code != 200 {
		t.Fatal("C order response", w.Code, w.Body.String())
	}
	var detail orderDetailView
	var fields map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	return detail, fields
}

func (f stageCCombinationReadFixture) save(t *testing.T, o Order) orderDetailView {
	t.Helper()
	result := f.mutate(t, "/api/order", o)
	detail, _ := f.read(t, result["id"].(string), f.u)
	return detail
}

func (f stageCCombinationReadFixture) set(t *testing.T, d orderDetailView, desired []ProjectCombination) {
	t.Helper()
	current, _ := f.read(t, d.ID, f.u)
	f.mutate(t, "/api/project-combination", combinationPayload(current, desired))
}

func (f stageCCombinationReadFixture) details(t *testing.T, orderID string, u User) []orderCombinationDetail {
	t.Helper()
	tx, err := f.a.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	details, err := readOrderCombinationDetails(tx, orderID, u)
	if err != nil {
		t.Fatal("C snapshot enrichment query", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return details
}

func stageCReadLine(craft, worker, part, location string, length float64) Line {
	return Line{CraftID: craft, Worker: worker, SubitemID: part, ActualLocation: location,
		Description: "C 保存施工备注", Segments: []Segment{{Length: length, Width: 10}}}
}

func stageCReadOrderLine(t *testing.T, d orderDetailView, craft string) orderLineView {
	t.Helper()
	for _, l := range d.Lines {
		if l.CraftID == craft {
			return l
		}
	}
	t.Fatal("missing C actual line", d.ID, craft)
	return orderLineView{}
}

func combinationDetailSavedLine(d orderDetailView, l orderLineView) CombinationSavedLine {
	price, total := l.Price, l.Total
	return CombinationSavedLine{ID: l.ID, OrderID: d.ID, Number: d.Number, WorkDate: d.WorkDate,
		CraftID: l.CraftID, Category: l.Category, Name: l.Name, SubitemID: l.SubitemID, SubitemName: l.SubitemName,
		ActualLocation: l.ActualLocation, WorkerID: l.WorkerID, WorkerName: l.WorkerName, Mode: l.Mode,
		Qty: l.Qty, WaivedQty: l.WaivedQty, ChargeableQty: l.ChargeableQty, Price: &price, Total: &total}
}

func stageCReadWithoutPrices(details []orderCombinationDetail) []orderCombinationDetail {
	out := append([]orderCombinationDetail{}, details...)
	for i := range out {
		out[i].Waterproof.Price, out[i].Waterproof.Total = nil, nil
		out[i].Polypropylene.Price, out[i].Polypropylene.Total = nil, nil
	}
	return out
}

func (f stageCCombinationReadFixture) assertWages(t *testing.T, orderID string, want int64) {
	t.Helper()
	var orderWage, accountWage int64
	if err := f.a.db.QueryRow("SELECT wage_cents FROM orders WHERE id=?", orderID).Scan(&orderWage); err != nil {
		t.Fatal(err)
	}
	if err := f.a.db.QueryRow("SELECT COALESCE(SUM(wage_cents),0) FROM order_worker_wages WHERE order_id=?", orderID).Scan(&accountWage); err != nil {
		t.Fatal(err)
	}
	if orderWage != want || accountWage != want {
		t.Fatal("C full-actual-area wages changed", orderID, orderWage, accountWage, want)
	}
}

func TestOrderCombinationDetailsReadMySQL(t *testing.T) {
	f := newStageCCombinationReadFixture(t)
	main := f.save(t, Order{Company: "stage-c-read-co", Address: "stage-c-read-跨日快照工地", Date: "2026-10-10", Worker: "stage-c-read-a",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-a", "stage-c-read-w-part", "C 实际主卫", 6),
			stageCReadLine("stage-c-read-p", "stage-c-read-b", "stage-c-read-p-part", "C 实际厨房", 10)},
		Materials: []Material{{Product: "stage-c-read-product", Qty: 1000000}},
		Sales:     []Sale{{Product: "stage-c-read-product", Qty: 2000000, Price: 800}}, DeliveryWage: 900})
	old := f.save(t, Order{Company: main.CompanyID, Project: main.ProjectID, Address: main.Address, Date: "2026-10-01",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-a", "stage-c-read-w-part", "C 旧日次卫", 2)}})
	legacy := f.save(t, Order{Company: main.CompanyID, Project: main.ProjectID, Address: main.Address, Date: "2026-10-05", Worker: "stage-c-read-b",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-b", "stage-c-read-w-part", "C 旧日阳台", 1)}})
	none := f.save(t, Order{Company: main.CompanyID, Project: main.ProjectID, Address: main.Address, Date: "2026-10-07",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-a", "stage-c-read-w-part", "", 0.5)}})
	scope := f.save(t, Order{Company: main.CompanyID, Project: main.ProjectID, Address: main.Address, Date: "2026-10-06",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-a", "stage-c-read-w-part", "C 范围防水", 0.2),
			stageCReadLine("stage-c-read-p", "stage-c-read-b", "stage-c-read-p-part", "C 范围丙纶", 0.2)}})
	other := f.save(t, Order{Company: main.CompanyID, Address: "stage-c-read-另一工地", Date: "2026-10-03",
		Lines: []Line{stageCReadLine("stage-c-read-w", "stage-c-read-a", "stage-c-read-w-part", "C 外围工地防水", 0.2),
			stageCReadLine("stage-c-read-p", "stage-c-read-b", "stage-c-read-p-part", "C 外围工地丙纶", 0.2)}})
	foreign := f.save(t, Order{Company: "stage-c-read-foreign-co", Address: "stage-c-read-另一公司", Date: "2026-10-02",
		Lines: []Line{stageCReadLine("stage-c-read-foreign-w", "stage-c-read-a", "stage-c-read-w-part", "C 外围公司防水", 0.2),
			stageCReadLine("stage-c-read-foreign-p", "stage-c-read-b", "stage-c-read-p-part", "C 外围公司丙纶", 0.2)}})

	payrollBefore := orderLocationsState(t, f.a.db)["order_worker_wages"]
	water := stageCReadOrderLine(t, main, "stage-c-read-w")
	poly := stageCReadOrderLine(t, main, "stage-c-read-p")
	oldWater := old.Lines[0]
	legacyWater := legacy.Lines[0]
	f.set(t, main, []ProjectCombination{{WaterproofLine: water.ID, PolypropyleneLine: poly.ID,
		WaterproofVersion: main.Version, PolypropyleneVersion: main.Version, Direction: "polypropylene", WaivedQty: 40000000}})
	main, _ = f.read(t, main.ID, f.u)
	f.set(t, old, []ProjectCombination{{WaterproofLine: oldWater.ID, PolypropyleneLine: poly.ID,
		WaterproofVersion: old.Version, PolypropyleneVersion: main.Version, Direction: "waterproof", WaivedQty: 20000000}})
	main, _ = f.read(t, main.ID, f.u)
	f.set(t, legacy, []ProjectCombination{{WaterproofLine: legacyWater.ID, PolypropyleneLine: poly.ID,
		WaterproofVersion: legacy.Version, PolypropyleneVersion: main.Version, Direction: "polypropylene", WaivedQty: 10000000}})
	if !reflect.DeepEqual(payrollBefore, orderLocationsState(t, f.a.db)["order_worker_wages"]) {
		t.Fatal("existing B customer waivers changed actual payroll fixture")
	}

	// Legacy nullable locations are intentionally not reconstructed from region,
	// and an empty line worker falls back only to the saved order worker snapshot.
	f.exec(t, `UPDATE order_lines SET worker_id='',worker_name='',subitem_name=NULL,actual_location=NULL,region='C 旧混合部位不得猜测' WHERE id=?`, legacyWater.ID)

	// These malformed C-only relations bypass the writer solely to exercise the
	// reader's scope guards. Test each side independently, including same project
	// IDs with a mismatching company; do not broaden the existing base array.
	scopeW, scopeP := stageCReadOrderLine(t, scope, "stage-c-read-w"), stageCReadOrderLine(t, scope, "stage-c-read-p")
	otherW, otherP := stageCReadOrderLine(t, other, "stage-c-read-w"), stageCReadOrderLine(t, other, "stage-c-read-p")
	foreignW, foreignP := stageCReadOrderLine(t, foreign, "stage-c-read-foreign-w"), stageCReadOrderLine(t, foreign, "stage-c-read-foreign-p")
	f.exec(t, "UPDATE orders SET project_id=? WHERE id=?", main.ProjectID, foreign.ID)
	for _, bad := range []struct{ name, project, water, poly, direction string }{
		{"relation-project", other.ProjectID, scopeW.ID, scopeP.ID, "waterproof"},
		{"poly-project", main.ProjectID, scopeW.ID, otherP.ID, "polypropylene"},
		{"water-project", main.ProjectID, otherW.ID, scopeP.ID, "waterproof"},
		{"poly-company", main.ProjectID, scopeW.ID, foreignP.ID, "polypropylene"},
		{"water-company", main.ProjectID, foreignW.ID, scopeP.ID, "waterproof"},
	} {
		f.exec(t, `INSERT INTO project_combinations(id,project_id,waterproof_line_id,polypropylene_line_id,waive_direction,waived_qty_micro,created_by,updated_by) VALUES(?,?,?,?,?,1,?,?)`,
			"stage-c-read-bad-"+bad.name, bad.project, bad.water, bad.poly, bad.direction, f.u.ID, f.u.ID)
	}

	// Seed receipts so read-only fingerprints cover values, not just empty tables.
	f.exec(t, `INSERT INTO payment_receipts(id,order_id,amount_cents,received_at,method,created_by) VALUES('stage-c-read-order-receipt',?,17,'2026-10-10','C 合成收款',?)`, main.ID, f.u.ID)
	var ticketID string
	if err := f.a.db.QueryRow("SELECT id FROM project_tickets WHERE project_id=? ORDER BY id LIMIT 1", main.ProjectID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,method,created_by) VALUES('stage-c-read-ticket-receipt',?,23,'2026-10-10','C 合成收款',?)`, ticketID, f.u.ID)

	main, _ = f.read(t, main.ID, f.u)
	old, _ = f.read(t, old.ID, f.u)
	legacy, _ = f.read(t, legacy.ID, f.u)
	scope, _ = f.read(t, scope.ID, f.u)
	water, poly = stageCReadOrderLine(t, main, "stage-c-read-w"), stageCReadOrderLine(t, main, "stage-c-read-p")
	if water.Qty != 60000000 || water.WaivedQty != 0 || water.Total != 300000 || water.WorkerName != "C 快照甲" || water.SubitemName != "C 保存防水小项" || water.ActualLocation != "C 实际主卫" ||
		poly.Qty != 100000000 || poly.WaivedQty != 50000000 || poly.ChargeableQty != 50000000 || poly.Total != 150000 || poly.WorkerName != "C 快照乙" || poly.SubitemName != "C 保存丙纶小项" || poly.ActualLocation != "C 实际厨房" ||
		main.TotalCents != 451600 || old.Lines[0].Total != 0 || old.Lines[0].WaivedQty != 20000000 || old.Lines[0].ActualLocation != "C 旧日次卫" ||
		legacy.Lines[0].SubitemName != "" || legacy.Lines[0].ActualLocation != "" || legacy.Lines[0].WorkerID != "stage-c-read-b" || legacy.Lines[0].WorkerName != "C 快照乙" {
		t.Fatal("unexpected saved B/A2 fixture quantities, actual locations or snapshots", main, old, legacy)
	}
	f.assertWages(t, main.ID, 270900)
	f.assertWages(t, old.ID, 40000)
	f.assertWages(t, legacy.ID, 20000)
	if water.Wage == nil || *water.Wage != 2000 || water.Labor == nil || *water.Labor != 120000 ||
		poly.Wage == nil || *poly.Wage != 1500 || poly.Labor == nil || *poly.Labor != 150000 ||
		old.Lines[0].Labor == nil || *old.Lines[0].Labor != 40000 {
		t.Fatal("full-area saved wages were waived with customer amounts")
	}

	lineSnapshots := map[string]CombinationSavedLine{}
	baseCombinations := map[string][]ProjectCombination{}
	for _, d := range []orderDetailView{main, old, legacy} {
		baseCombinations[d.ID] = d.Combinations
		for _, l := range d.Lines {
			lineSnapshots[l.ID] = combinationDetailSavedLine(d, l)
		}
	}
	allExpected := map[string]orderCombinationDetail{}
	for _, c := range main.Combinations {
		allExpected[c.ID] = orderCombinationDetail{ID: c.ID, Waterproof: lineSnapshots[c.WaterproofLine], Polypropylene: lineSnapshots[c.PolypropyleneLine]}
	}
	if len(allExpected) != 3 {
		t.Fatal("expected one same-date and two cross-date relations", main.Combinations)
	}
	wantFor := func(orderID string) []orderCombinationDetail {
		want := []orderCombinationDetail{}
		for _, d := range allExpected {
			if d.Waterproof.OrderID == orderID || d.Polypropylene.OrderID == orderID {
				want = append(want, d)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
		return want
	}

	// Configuration changes precede the before/after read-only baseline. Saved
	// names, subitems, prices, personnel, quantities and amounts must not drift.
	f.exec(t, `UPDATE crafts SET name='C 改后的当前防水',price_cents=9876,wage_cents=4321,subitems=JSON_ARRAY(JSON_OBJECT('id','stage-c-read-w-part','name','C 改后防水小项')) WHERE id IN ('stage-c-read-w','stage-c-read-w-source')`)
	f.exec(t, `UPDATE crafts SET name='C 改后的当前丙纶',price_cents=8765,wage_cents=3210,subitems=JSON_ARRAY(JSON_OBJECT('id','stage-c-read-p-part','name','C 改后丙纶小项')) WHERE id IN ('stage-c-read-p','stage-c-read-p-source')`)
	f.exec(t, `UPDATE workers SET name='C 改后的当前人员' WHERE id IN ('stage-c-read-a','stage-c-read-b')`)
	before := orderLocationsState(t, f.a.db)
	for _, table := range []string{"orders", "order_lines", "order_worker_wages", "project_combinations", "project_tickets", "project_ticket_items", "stock_balances", "stock_movements", "order_materials", "order_sales", "payment_receipts", "project_ticket_receipts", "audit", "requests"} {
		if len(before[table]) == 0 {
			t.Fatal("read-only baseline lacks meaningful C fixture rows", table)
		}
	}

	priced := User{ID: "stage-c-read-priced", Role: "employee", Permissions: []string{"orders", customerPricePermission}}
	t.Run("saved_same_and_cross_date", func(t *testing.T) {
		for _, orderID := range []string{main.ID, old.ID, legacy.ID} {
			got, fields := f.read(t, orderID, priced)
			if !reflect.DeepEqual(got.CombinationDetails, wantFor(orderID)) || !reflect.DeepEqual(got.Combinations, baseCombinations[orderID]) {
				t.Fatalf("saved counterpart snapshots/base relations drifted for %s: got %+v want %+v", orderID, got.CombinationDetails, wantFor(orderID))
			}
			assertNoCombinationWages(t, fields)
			if !reflect.DeepEqual(f.details(t, orderID, priced), wantFor(orderID)) {
				t.Fatal("direct read query differs from saved order response", orderID)
			}
		}
		got, fields := f.read(t, old.ID, f.u)
		if len(got.CombinationDetails) != 1 || got.CombinationDetails[0].Waterproof.Total == nil || *got.CombinationDetails[0].Waterproof.Total != 0 {
			t.Fatal("authorized full-waiver zero omitted", got.CombinationDetails)
		}
		assertNoCombinationWages(t, fields["combination_details"])
	})
	t.Run("independent_price_and_wage_permissions", func(t *testing.T) {
		for _, permission := range []string{"orders", "wages", "finance"} {
			u := User{ID: "stage-c-read-unpriced-" + permission, Role: "employee", Permissions: []string{permission}}
			want := stageCReadWithoutPrices(wantFor(main.ID))
			if got := f.details(t, main.ID, u); !reflect.DeepEqual(got, want) {
				t.Fatal("unpriced query populated prices or lost ordinary snapshots", permission, got)
			}
			got, fields := f.read(t, main.ID, u)
			if !reflect.DeepEqual(got.CombinationDetails, want) {
				t.Fatal("restricted order lost ordinary snapshots", permission, got.CombinationDetails)
			}
			assertNoCustomerMoney(t, fields)
			assertNoCombinationWages(t, fields["combination_details"])
			if permission == "wages" && (got.WageCents == nil || *got.WageCents != 270900) {
				t.Fatal("hidden customer prices changed separate authorized full wages")
			}
		}
		maintenanceFaultError(t, f.a.readOrder(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/order?id="+main.ID, nil),
			User{Role: "employee", Permissions: []string{customerPricePermission}}), 403)
	})
	t.Run("no_combinations_is_empty_array", func(t *testing.T) {
		got, fields := f.read(t, none.ID, priced)
		items, ok := fields["combination_details"].([]any)
		if !ok || len(items) != 0 || got.CombinationDetails == nil || f.details(t, none.ID, priced) == nil {
			t.Fatal("no relations must return [], not null or omitted", fields)
		}
	})
	t.Run("both_sides_and_relation_scope", func(t *testing.T) {
		for _, orderID := range []string{scope.ID, other.ID, foreign.ID} {
			if got := f.details(t, orderID, f.u); got == nil || len(got) != 0 {
				t.Fatal("foreign company/project/relationship snapshot leaked", orderID, got)
			}
		}
		got, fields := f.read(t, scope.ID, f.u)
		if len(got.CombinationDetails) != 0 || !reflect.DeepEqual(got.Combinations, scope.Combinations) || len(got.Combinations) != 5 {
			t.Fatal("scope filtering changed the existing base array or leaked details", got.Combinations, got.CombinationDetails)
		}
		assertNoCombinationWages(t, fields["combination_details"])
	})
	t.Run("project_work_page_includes_saved_counterparts", func(t *testing.T) {
		for _, u := range []User{priced, {ID: "stage-c-read-wage-view", Role: "employee", Permissions: []string{"wages"}}} {
			w := httptest.NewRecorder()
			if err := f.a.readProjectWork(w, httptest.NewRequest("GET", "/api/project-work?id="+main.ProjectID+"&page=1&page_size=1", nil), u); err != nil {
				t.Fatal("read C project work", err)
			}
			var page projectWorkPage
			var fields map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			want := wantFor(main.ID)
			if !u.can(customerPricePermission) {
				want = stageCReadWithoutPrices(want)
				assertNoCustomerMoney(t, fields)
			}
			if w.Code != 200 || len(page.Orders) != 1 || page.Orders[0].ID != main.ID || !reflect.DeepEqual(page.Orders[0].CombinationDetails, want) {
				t.Fatal("paged project work omitted cross-date actual counterparts", w.Code, page.Orders)
			}
			orders := fields["orders"].([]any)
			assertNoCombinationWages(t, orders[0].(map[string]any)["combination_details"])
		}
	})
	orderLocationsUnchanged(t, f.a.db, before)
	f.assertWages(t, main.ID, 270900)
	f.assertWages(t, old.ID, 40000)
	f.assertWages(t, legacy.ID, 20000)
	t.Logf("read-only snapshots verified; all %d backed-up tables identical, including orders/lines/combinations/payroll/tickets/stock/receipts/audit/idempotency", len(before))
}
