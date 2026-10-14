package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Root preflights the isolated instance and grants before opting in. The
// existing helper creates disposable schemas and never reuses business data.
func TestPhase3WorkflowMySQL(t *testing.T) {
	f := newOrderLocationsFixture(t)
	f.execPhase3(t, `UPDATE products SET aux_unit='小组',aux_ratio=6 WHERE id='loc-product'`)
	f.execPhase3(t, `UPDATE product_unit_definitions SET aux_unit='小组',base_factor=6,aux_factor=1 WHERE product_id='loc-product'`)
	f.execPhase3(t, `UPDATE stock_balances SET qty_quantum=120000000 WHERE product_id='loc-product'`)
	override := int64(2250)
	o := Order{Company: "loc-co", Address: "第三阶段合成工地", Date: "2026-10-11", Worker: "loc-a",
		Lines: []Line{
			{ClientRef: "draft-a", CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-a", ActualLocation: "主卫", ChargeMode: "free", WageOverrideCents: &override, WageAdjustmentReason: "补做专项工资", Segments: []Segment{{Length: 2, Width: 3}}},
			{ClientRef: "draft-b", CraftID: "loc-fixed", SubitemID: "part", Worker: "loc-b", ActualLocation: "主卫", Segments: []Segment{{Length: 1, Width: 2}}},
		},
		Materials: []Material{{LineID: "draft-a", Product: "loc-product", InputQty: "6", InputUnit: "aux"}, {Product: "loc-product", InputQty: "0.5", InputUnit: "base"}},
		Sales:     []Sale{{Product: "loc-product", InputQty: "6", InputUnit: "aux", Price: 200}},
	}
	key := id()
	out, err := f.call(f.u, "/api/order", key, o)
	if err != nil {
		t.Fatal(err)
	}
	d := orderLocationsRead(t, f.a, out.(map[string]any)["id"].(string), f.u)
	if len(d.Lines) != 2 || len(d.Materials) != 2 || d.TotalCents != 4800 || d.WageCents == nil || *d.WageCents != 2850 {
		t.Fatalf("actual lines/total/wages: %+v", d)
	}
	var freeLine orderLineView
	for _, l := range d.Lines {
		if l.ChargeMode == "free" {
			freeLine = l
		}
	}
	if freeLine.Qty != 6000000 || freeLine.Total != 0 || freeLine.Labor == nil || *freeLine.Labor != 2250 || freeLine.Price != 1800 || len(freeLine.WageAdjustments) != 1 {
		t.Fatalf("free line snapshots: %+v", freeLine)
	}
	var attributed bool
	for _, m := range d.Materials {
		if m.LineID == freeLine.ID && m.QtyQuantum == "6000000" {
			attributed = true
		}
	}
	if !attributed {
		t.Fatal("material draft reference not resolved")
	}
	f.phase3Scalar(t, "SELECT qty_quantum FROM stock_balances WHERE warehouse_id='loc-wh' AND product_id='loc-product'", 105000000)
	f.phase3Scalar(t, "SELECT COUNT(*) FROM stock_movements WHERE source_id=?", 2, d.ID)
	if _, err = f.call(f.u, "/api/order", key, o); err != nil {
		t.Fatal("idempotent retry", err)
	}
	f.phase3Scalar(t, "SELECT qty_quantum FROM stock_balances WHERE warehouse_id='loc-wh' AND product_id='loc-product'", 105000000)
	f.phase3Scalar(t, "SELECT COUNT(*) FROM order_line_wage_adjustments WHERE order_id=?", 1, d.ID)

	edit := orderLocationsEdit(t, d)
	for i := range edit.Materials {
		if edit.Materials[i].LineID == freeLine.ID {
			edit.Materials[i].InputQty, edit.Materials[i].QtyQuantum = "12", ""
		}
	}
	edit.Sales[0].InputQty, edit.Sales[0].QtyQuantum = "3", ""
	edit.Lines[0].Description = "普通备注"
	f.must(t, edit)
	d = orderLocationsRead(t, f.a, d.ID, f.u)
	f.phase3Scalar(t, "SELECT qty_quantum FROM stock_balances WHERE warehouse_id='loc-wh' AND product_id='loc-product'", 102000000)
	f.phase3Scalar(t, "SELECT COUNT(*) FROM order_line_wage_adjustments WHERE order_id=?", 1, d.ID)

	edit = orderLocationsEdit(t, d)
	for i := range edit.Lines {
		if edit.Lines[i].ID == freeLine.ID {
			edit.Lines[i].WageOverrideCents = nil
			edit.Lines[i].WageAdjustmentReason = "恢复标准工资"
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonText(edit)), &payload); err != nil {
		t.Fatal(err)
	}
	for _, raw := range payload["lines"].([]any) {
		l := raw.(map[string]any)
		if l["id"] == freeLine.ID {
			l["wage_override_cents"] = nil
		}
	}
	f.must(t, payload)
	d = orderLocationsRead(t, f.a, d.ID, f.u)
	f.phase3Scalar(t, "SELECT COUNT(*) FROM order_line_wage_adjustments WHERE order_id=?", 2, d.ID)
	f.phase3Scalar(t, "SELECT wage_cents FROM orders WHERE id=?", 2400, d.ID)

	bad := orderLocationsEdit(t, d)
	bad.Materials[0].LineID = "other-order-line"
	before := orderLocationsState(t, f.a.db)
	if _, err = f.call(f.u, "/api/order", id(), bad); err == nil {
		t.Fatal("cross-order material link accepted")
	}
	orderLocationsUnchanged(t, f.a.db, before)
	bad = orderLocationsEdit(t, d)
	for i, l := range bad.Lines {
		if l.ID == freeLine.ID {
			bad.Lines = append(bad.Lines[:i], bad.Lines[i+1:]...)
			break
		}
	}
	if _, err = f.call(f.u, "/api/order", id(), bad); err == nil {
		t.Fatal("orphan material after line deletion accepted")
	}
	orderLocationsUnchanged(t, f.a.db, before)

	employee := User{ID: "phase3-wage-employee", Role: "employee", Name: "合成工资员", Permissions: []string{"orders", "wages", customerPricePermission}, Crafts: []string{"loc-source"}}
	edit = orderLocationsEdit(t, d)
	for i := range edit.Lines {
		if edit.Lines[i].ID == freeLine.ID {
			v := int64(1999)
			edit.Lines[i].WageOverrideCents = &v
			edit.Lines[i].WageAdjustmentReason = "授权工资调整"
		}
	}
	replayKey := id()
	if _, err = f.call(employee, "/api/order", replayKey, edit); err != nil {
		t.Fatal(err)
	}
	employee.Permissions = []string{"orders", customerPricePermission}
	if _, err = f.call(employee, "/api/order", replayKey, edit); err == nil {
		t.Fatal("revoked wages permission replay accepted")
	}
	d = orderLocationsRead(t, f.a, d.ID, f.u)
	w := httptest.NewRecorder()
	if err = f.a.readOrder(w, httptest.NewRequest("GET", "/api/order?id="+d.ID, nil), employee); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"wage_override_cents", "wage_adjustment_reason", "wage_adjustments", "labor_cents", "wage_cents"} {
		if strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Fatal("hidden wage field leaked", field)
		}
	}

	pureSale := f.must(t, Order{Company: d.CompanyID, Project: d.ProjectID, Address: d.Address, Date: "2026-10-12", Worker: "loc-a", Sales: []Sale{{Product: "loc-product", InputQty: "6", InputUnit: "aux", Price: 100}}})
	saleDetail := orderLocationsRead(t, f.a, pureSale["id"].(string), f.u)
	if len(saleDetail.Lines) != 0 || len(saleDetail.Sales) != 1 || saleDetail.ProjectID != d.ProjectID || saleDetail.TotalCents != 600 {
		t.Fatalf("pure-sale order: %+v", saleDetail)
	}
	f.phase3Scalar(t, "SELECT COUNT(*) FROM orders WHERE project_id=?", 2, d.ProjectID)
	f.phase3Scalar(t, "SELECT COUNT(*) FROM order_lines WHERE order_id=?", 2, d.ID)
	account := (*d.WageRecords)[0]
	w = httptest.NewRecorder()
	if err = f.a.readWage(w, httptest.NewRequest("GET", "/api/wage?id="+account.ID, nil), f.u); err != nil {
		t.Fatal("actual wage detail", err)
	}
	var wd wageDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &wd); err != nil || wd.WageCents != account.WageCents {
		t.Fatal("actual wage mismatch", wd, err)
	}

	before = orderLocationsState(t, f.a.db)
	if err = migrateOrderPhase3(f.a.db); err != nil {
		t.Fatal("migration rerun", err)
	}
	if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
		t.Fatal("migration rewrote stored data")
	}
	f.phase3Scalar(t, "SELECT COUNT(*) FROM order_materials WHERE order_id=? AND line_id=''", 1, d.ID)
	target, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	file, err := f.a.backup()
	if err != nil {
		t.Fatal(err)
	}
	if err = target.restore(filepath.Join(dir, file)); err != nil {
		t.Fatal("phase3 snapshot restore", err)
	}
	for _, table := range []string{"order_lines", "order_materials", "order_line_wage_adjustments", "order_worker_wages"} {
		if !reflect.DeepEqual(orderLocationsState(t, target.db)[table], before[table]) {
			t.Fatal("restore snapshot changed", table)
		}
	}
	edit = orderLocationsEdit(t, d)
	keptLines := []Line{}
	for _, line := range edit.Lines {
		if line.ID != freeLine.ID {
			keptLines = append(keptLines, line)
		}
	}
	edit.Lines = keptLines
	keptMaterials := []Material{}
	for _, material := range edit.Materials {
		if material.LineID != freeLine.ID {
			keptMaterials = append(keptMaterials, material)
		}
	}
	edit.Materials = keptMaterials
	f.must(t, edit)
	d = orderLocationsRead(t, f.a, d.ID, f.u)
	if len(d.WageAdjustments) != 3 {
		t.Fatal("deleted line history lost", d.WageAdjustments)
	}
	for _, adjustment := range d.WageAdjustments {
		if adjustment.LineID != freeLine.ID || !adjustment.LineDeleted || adjustment.WorkerName == "" || adjustment.ActorName == "" || adjustment.CreatedAt == "" {
			t.Fatal("deleted line history snapshot incomplete", adjustment)
		}
	}
	f.execPhase3(t, "UPDATE order_worker_wages SET wage_paid=1 WHERE order_id=?", d.ID)
	f.execPhase3(t, "UPDATE orders SET wage_paid=1 WHERE id=?", d.ID)
	if _, err = f.call(f.u, "/api/order", id(), orderLocationsEdit(t, d)); err == nil {
		t.Fatal("paid wage edit accepted")
	}
}

func (f orderLocationsFixture) execPhase3(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.a.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f orderLocationsFixture) phase3Scalar(t *testing.T, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := f.a.db.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("phase3 scalar got=%d want=%d error=%v", got, want, err)
	}
}
