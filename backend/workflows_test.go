package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func workflowAcceptance(t *testing.T, a *App) {
	t.Helper()
	u := User{ID: "workflow-admin", Name: "流程测试", Role: "admin"}
	call := func(path string, input any) (any, error) {
		b := []byte(jsonText(input))
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		r.Header.Set("Idempotency-Key", id())
		return a.mutate(r, u, b, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, path, u, b, key) })
	}
	must := func(path string, input any) any {
		t.Helper()
		v, e := call(path, input)
		if e != nil {
			t.Fatalf("%s: %v", path, e)
		}
		if path == "/api/order" {
			if o, ok := input.(Order); ok && o.ID == "" {
				legacyOrderFixture(t, a, v.(map[string]any)["id"].(string))
			}
		}
		return v
	}
	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if e := a.db.QueryRow(query, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	for _, q := range []string{"INSERT INTO warehouses(id,name) VALUES('wf-wh','流程仓库')", "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('wf-p','流程材料','桶','组',6)", "INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('wf-public','','开工','固定整项','金额',0,2345,JSON_ARRAY())", "INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('wf-craft','c','wf-public','开工','固定整项','金额',12345,0)"} {
		if _, e := a.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	receipt := map[string]any{"kind": "进货", "date": "2026-09-19", "handler": "测试", "to": "wf-wh", "lines": []map[string]any{{"product_id": "wf-p", "input_unit": "组", "input_qty": "12", "price_cents": 1234}}}
	rid := must("/api/stock", receipt).(map[string]any)["id"].(string)
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wf-wh' AND product_id='wf-p'") != 2000000 {
		t.Fatal("aux quantity conversion")
	}
	if scalar("SELECT total_cents FROM stock_documents WHERE id=?", rid) != 2468 {
		t.Fatal("receipt total")
	}
	receipt["id"] = rid
	receipt["version"] = 1
	receipt["lines"] = []map[string]any{{"product_id": "wf-p", "input_unit": "桶", "input_qty": "3", "price_cents": 1234}}
	must("/api/stock", receipt)
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wf-wh' AND product_id='wf-p'") != 3000000 {
		t.Fatal("correction delta")
	}
	if _, e := call("/api/stock", receipt); e == nil {
		t.Fatal("stale stock correction accepted")
	}
	must("/api/stock-state", map[string]any{"id": rid, "version": 2, "action": "void", "reason": "测试纠错"})
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wf-wh' AND product_id='wf-p'") != 0 {
		t.Fatal("void stock mismatch")
	}
	must("/api/stock-state", map[string]any{"id": rid, "version": 3, "action": "restore"})
	if scalar("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wf-wh' AND product_id='wf-p'") != 3000000 {
		t.Fatal("restore stock mismatch")
	}
	projectID := must("/api/project", Project{Company: "c", Name: "流程测试项目", Address: "流程工地"}).(map[string]any)["id"].(string)
	o := Order{Company: "c", Project: projectID, Address: "流程工地", Date: "2026-09-19", Worker: "w", Lines: []Line{{CraftID: "wf-craft", Category: "开工", Price: 1, Mode: "金额"}}}
	segmentOrder := Order{Company: "c", Project: projectID, Address: "分段保存测试", Date: "2026-09-25", Worker: "w", Lines: []Line{{Category: "开工", Name: "分段", Mode: "平方", Temporary: true, Price: 1000, Wage: 200, Segments: []Segment{{Length: 2, Width: 3}, {Length: 4, Width: 5}}}}}
	segmentOrder.ID = must("/api/order", segmentOrder).(map[string]any)["id"].(string)
	if scalar("SELECT total_cents FROM orders WHERE id=?", segmentOrder.ID) != 26000 || scalar("SELECT qty_micro FROM order_lines WHERE order_id=?", segmentOrder.ID) != 26000000 {
		t.Fatal("dimension segment persistence mismatch")
	}
	var storedSegments string
	if e := a.db.QueryRow("SELECT segments FROM order_lines WHERE order_id=?", segmentOrder.ID).Scan(&storedSegments); e != nil {
		t.Fatal(e)
	}
	var decoded []Segment
	if e := json.Unmarshal([]byte(storedSegments), &decoded); e != nil || len(decoded) != 2 || decoded[1].Length != 4 || decoded[1].Width != 5 {
		t.Fatal("segment JSON roundtrip", e)
	}
	segmentOrder.Version = 1
	segmentOrder.Lines[0].Mode = "金额"
	segmentOrder.Lines[0].Segments = []Segment{{Mode: "金额", Amount: 3456}}
	must("/api/order", segmentOrder)
	if scalar("SELECT total_cents FROM orders WHERE id=?", segmentOrder.ID) != 3456 || scalar("SELECT qty_micro FROM order_lines WHERE order_id=?", segmentOrder.ID) != 1000000 {
		t.Fatal("amount-only update mismatch")
	}
	segmentOrder.Version = 2
	segmentOrder.Lines[0].Segments[0].Amount = -1
	if _, e := call("/api/order", segmentOrder); e == nil {
		t.Fatal("zero segment accepted")
	}
	if scalar("SELECT total_cents FROM orders WHERE id=?", segmentOrder.ID) != 3456 {
		t.Fatal("invalid segment changed saved order")
	}
	segmentOrder.Delete = true
	must("/api/order", segmentOrder)
	newWorker := must("/api/entity", map[string]any{"entity": "workers", "crafts": []string{"开工"}, "name": "无需绑定的师傅"}).(map[string]any)["id"].(string)
	if scalar("SELECT COUNT(*) FROM warehouses WHERE worker_id=? AND name='无需绑定的师傅' AND active=1", newWorker) != 1 {
		t.Fatal("worker warehouse not created")
	}
	must("/api/entity", map[string]any{"entity": "workers", "crafts": []string{"开工"}, "id": newWorker, "version": 1, "name": "更名师傅"})
	if scalar("SELECT COUNT(*) FROM warehouses WHERE worker_id=?", newWorker) != 1 {
		t.Fatal("worker edit created another warehouse")
	}
	// Worker permissions constrain even administrators and temporary lines.
	for _, category := range []string{"回填", "维修"} {
		denied := Order{Company: "c", Project: projectID, Address: "施工人越权测试", Date: "2026-09-25", Worker: newWorker, Lines: []Line{{Category: category, Name: category, Mode: "金额", Temporary: true, Price: 100}}}
		if _, err := call("/api/order", denied); err == nil || !strings.Contains(err.Error(), "未获准施工") {
			t.Fatal("unauthorized worker accepted", err)
		}
	}
	permitted := o
	permitted.Worker = newWorker
	permitted.ID = must("/api/order", permitted).(map[string]any)["id"].(string)
	permitted.Version = 1
	must("/api/entity", map[string]any{"entity": "workers", "id": newWorker, "version": 2, "name": "更名师傅", "crafts": []string{}})
	if _, err := call("/api/order", permitted); err == nil {
		t.Fatal("revoked worker accepted")
	}
	if scalar("SELECT total_cents FROM orders WHERE id=?", permitted.ID) != 12345 {
		t.Fatal("revocation changed historical order")
	}
	permitted.Delete = true
	must("/api/order", permitted) // Revocation must not prevent cancellation.
	if _, err := call("/api/entity", map[string]any{"entity": "workers", "id": newWorker, "version": 2, "name": "更名师傅", "crafts": []string{"开工"}}); err == nil {
		t.Fatal("stale permissions accepted")
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE address='施工人越权测试'") != 0 {
		t.Fatal("rejected order persisted")
	}
	limited := User{ID: "worker-manager", Role: "employee", Permissions: []string{"workers"}}
	payload := []byte(jsonText(map[string]any{"entity": "workers", "crafts": []string{"开工"}, "name": "授权员工新增师傅"}))
	r := httptest.NewRequest("POST", "/api/entity", strings.NewReader(string(payload)))
	r.Header.Set("Idempotency-Key", id())
	if _, e := a.mutate(r, limited, payload, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, "/api/entity", limited, payload, key) }); e != nil {
		t.Fatal("worker manager rejected", e)
	}
	if _, e := a.mutate(r, limited, payload, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, "/api/entity", limited, payload, key) }); e != nil {
		t.Fatal("worker retry rejected", e)
	}
	if scalar("SELECT COUNT(*) FROM workers WHERE name='授权员工新增师傅'") != 1 || scalar("SELECT COUNT(*) FROM warehouses WHERE name='授权员工新增师傅'") != 1 {
		t.Fatal("worker retry duplicated warehouse")
	}
	limited.Permissions = []string{"wages"}
	r.Header.Set("Idempotency-Key", id())
	if _, e := a.mutate(r, limited, payload, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, "/api/entity", limited, payload, key) }); e == nil {
		t.Fatal("wage permission allowed worker management")
	}
	oid := must("/api/order", o).(map[string]any)["id"].(string)
	if scalar("SELECT total_cents FROM orders WHERE id=?", oid) != 12345 {
		t.Fatal("fixed amount accepts client price")
	}
	if scalar("SELECT wage_cents FROM orders WHERE id=?", oid) != 2345 {
		t.Fatal("fixed labor")
	}
	for i, action := range []string{"submit", "deliver", "receive", "pay", "undo-pay", "return"} {
		payload := map[string]any{"action": action, "orders": []map[string]any{{"id": oid, "version": i + 1}}}
		if action == "deliver" {
			payload["ticket_no"], payload["delivered_at"], payload["recipient"] = "WF-001", "2026-09-19", "测试收票人"
		}
		if action == "return" {
			payload["note"] = "流程测试退回"
		}
		must("/api/settle", payload)
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND status='进行中' AND wage_paid=0 AND version=7", oid) != 1 {
		t.Fatal("settlement state")
	}
	if scalar("SELECT COUNT(*) FROM settlements WHERE order_id=?", oid) != 6 {
		t.Fatal("settlement audit trail")
	}
	// Entire batch must roll back when a later item has a stale version.
	second := must("/api/order", o).(map[string]any)["id"].(string)
	edits := []map[string]any{{"id": oid, "version": 7, "address": "不应保存", "work_date": "2026-09-19"}, {"id": second, "version": 99, "address": "不应保存", "work_date": "2026-09-19"}}
	if _, e := call("/api/finance-edit", map[string]any{"company_id": "c", "orders": edits}); e == nil {
		t.Fatal("batch conflict accepted")
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE address='不应保存'") != 0 {
		t.Fatal("partial batch persisted")
	}
	edits[1]["version"] = 1
	must("/api/finance-edit", map[string]any{"company_id": "c", "orders": edits})
	employee := User{ID: "restricted", Role: "employee", Permissions: []string{"orders", "backups", "users"}, Crafts: []string{"开工"}}
	if employee.can("backups") || employee.can("users") {
		t.Fatal("employee permission escalation")
	}
	operator := User{Role: "operator", Permissions: []string{"backups"}}
	if operator.can("backups") {
		t.Fatal("operator permission escalation")
	}
	for _, path := range []string{"/api/reference", "/api/order?id=" + oid, "/api/orders?month=2026-09"} {
		w := httptest.NewRecorder()
		if e := a.get(w, httptest.NewRequest("GET", path, nil), employee); e != nil {
			t.Fatal(path, e)
		}
		var data any
		if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(w.Body.String(), `"wage_cents":2345`) || strings.Contains(w.Body.String(), `"labor_cents":2345`) {
			t.Fatal("wages leaked", path)
		}
	}
	t.Log("stock auxiliary conversion, correction, stale rejection, void/restore, backend fixed pricing, full finance/wage lifecycle, batch rollback and permission checks passed")
}
