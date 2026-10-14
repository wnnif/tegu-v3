package main

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetreCraftWorkflowMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{ID: "length-admin", Name: "合成管理员", Role: "admin"}
	call := func(user User, path, key string, input any) (any, error) {
		t.Helper()
		raw := []byte(jsonText(input))
		request := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		request.Header.Set("Idempotency-Key", "metre-regression-"+key)
		return a.mutate(request, user, raw, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, path, user, raw, key) })
	}
	public, err := call(admin, "/api/entity", "length-public", map[string]any{"entity": "crafts", "name": "合成长项", "category": "包管", "mode": "米", "wage_cents": 101, "active": true})
	if err != nil {
		t.Fatal("metre craft could not be maintained", err)
	}
	publicID := public.(map[string]any)["id"].(string)
	company, err := call(admin, "/api/entity", "length-company", map[string]any{"entity": "companies", "name": "合成公司", "fixed_crafts": []map[string]any{{"source_id": publicID, "price_cents": 10001}}})
	if err != nil {
		t.Fatal(err)
	}
	companyID := company.(map[string]any)["id"].(string)
	var fixedID string
	if err = a.db.QueryRow("SELECT id FROM crafts WHERE company_id=? AND source_id=?", companyID, publicID).Scan(&fixedID); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO workers(id,name,crafts) VALUES('length-worker','合成师傅',JSON_ARRAY('包管'))`)
	project, err := call(admin, "/api/project", "length-project", Project{Company: companyID, Name: "合成工地", Address: "合成地址"})
	if err != nil {
		t.Fatal(err)
	}
	order := Order{Company: companyID, Project: project.(map[string]any)["id"].(string), Address: "合成地址", Date: "2026-10-01", Worker: "length-worker", Lines: []Line{{CraftID: fixedID, Mode: "个", Price: 1, Wage: 1, Qty: 1, Total: 1, Labor: 1, Segments: []Segment{{Length: 0.1}, {Mode: "米", Length: 0.2}, {Length: 1.25}}}}}
	result, err := call(admin, "/api/order", "length-order", order)
	if err != nil {
		t.Fatal(err)
	}
	orderID := result.(map[string]any)["id"].(string)
	if _, err = call(admin, "/api/order", "length-order", order); err != nil {
		t.Fatal("idempotent length submission failed", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders WHERE total_cents=15502 AND wage_cents=157", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE mode='米' AND qty_micro=1550000 AND price_cents=10001 AND wage_cents=101 AND total_cents=15502 AND labor_cents=157", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE total_cents=15502", 1)
	read := func(user User) orderDetailView {
		t.Helper()
		tx, err := a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		detail, err := readOrderDetail(tx, orderID, user)
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}
	detail := read(admin)
	if len(detail.Lines) != 1 || detail.Lines[0].Mode != "米" || len(detail.Lines[0].Segments) != 3 || detail.Lines[0].Segments[2].Length != 1.25 {
		t.Fatalf("saved lengths did not round trip: %+v", detail.Lines)
	}
	employee := User{ID: "length-employee", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{publicID}}
	for _, line := range read(employee).Lines {
		if line.Wage != nil || line.Labor != nil {
			t.Fatal("length mode disclosed wages to employee")
		}
	}
	order.Lines[0].Segments[0].Width = 1
	_, err = call(admin, "/api/order", "length-invalid", order)
	maintenanceFaultError(t, err, 400)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM requests WHERE request_key='metre-regression-length-invalid'", 0)
	order.Lines[0].Segments[0].Width = 0
	employee.Crafts = nil
	_, err = call(employee, "/api/order", "length-unauthorized", order)
	maintenanceFaultError(t, err, 403)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders", 1)
	if _, err = call(admin, "/api/entity", "length-public-edit", map[string]any{"entity": "crafts", "id": publicID, "version": 1, "name": "合成长项新版", "category": "包管", "mode": "米", "wage_cents": 999, "active": true}); err != nil {
		t.Fatal(err)
	}
	detail = read(admin)
	if detail.Lines[0].Name != "合成长项" || detail.Lines[0].Wage == nil || *detail.Lines[0].Wage != 101 || *detail.Lines[0].Labor != 157 {
		t.Fatal("public update silently changed historical length price/wage", detail.Lines)
	}
}

func TestMetreCraftImportMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	file := filepath.Join(t.TempDir(), "synthetic-craft.json")
	if err := os.WriteFile(file, []byte(`{"format":"tegu-craft-export-v2","customers":[],"rates":[{"ID":"synthetic-length","Name":"合成待补长项","Category":"包管","CalcMode":"米","WageCents":0,"Active":false,"Version":1}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.importCrafts(file); err != nil {
		t.Fatal("metre craft backup was rejected", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM crafts WHERE id='synthetic-length' AND mode='米' AND wage_cents=0 AND price_cents=0 AND active=0", 1)
}
