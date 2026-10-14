package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSettlementValidation(t *testing.T) {
	base := SettlementRequest{Action: "receive_partial", AmountCents: 100, ReceivedAt: "2026-09-28", Orders: []SettlementOrder{{ID: "o", Version: 1}}}
	if e := base.validate(); e != nil {
		t.Fatal(e)
	}
	delivery := SettlementRequest{Action: "deliver", Recipient: "收票人", DeliveredAt: "2026-09-28", Orders: base.Orders}
	if e := delivery.validate(); e != nil {
		t.Fatal("delivery without ticket number or attachment rejected", e)
	}
	delivery.Recipient = "  "
	if e := delivery.validate(); e == nil {
		t.Fatal("delivery without recipient accepted")
	}
	for _, change := range []func(*SettlementRequest){
		func(p *SettlementRequest) { p.ReceivedAt = "2026-02-30" },
		func(p *SettlementRequest) { p.Method = strings.Repeat("a", 41) },
		func(p *SettlementRequest) { p.AmountCents = 0 },
		func(p *SettlementRequest) { p.Orders = append(p.Orders, p.Orders[0]) },
		func(p *SettlementRequest) { p.Action = "return"; p.Note = "  " },
		func(p *SettlementRequest) {
			p.Action = "deliver"
			p.TicketNo = "票据"
			p.Recipient = "收票人"
			p.DeliveredAt = "2026-99-01"
		},
	} {
		p := base
		change(&p)
		if p.validate() == nil {
			t.Fatal("invalid settlement accepted", p.Action)
		}
	}
}

func settlementAcceptance(t *testing.T, a *App) {
	t.Helper()
	var database string
	if e := a.db.QueryRow("SELECT DATABASE()").Scan(&database); e != nil || !strings.HasPrefix(database, "tegu_go_test_") {
		t.Fatal("requires isolated test database", e)
	}
	admin := User{ID: "settlement-admin", Name: "结算验收", Role: "admin"}
	limited := User{ID: "settlement-limited", Role: "employee", Permissions: []string{"orders", "orders_temporary", "rates"}}
	call := func(u User, path string, input any, key string) (any, error) {
		b := []byte(jsonText(input))
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		r.Header.Set("Idempotency-Key", key)
		return a.mutate(r, u, b, func(tx *sql.Tx, k string) (any, error) { return a.post(tx, path, u, b, k) })
	}
	must := func(path string, input any) any {
		t.Helper()
		v, e := call(admin, path, input, id())
		if e != nil {
			t.Fatal(path, e)
		}
		if path == "/api/order" {
			if o, ok := input.(Order); ok && o.ID == "" {
				legacyOrderFixture(t, a, v.(map[string]any)["id"].(string))
			}
		}
		return v
	}
	get := func(u User, order string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		if e := a.get(w, httptest.NewRequest("GET", "/api/order?id="+order, nil), u); e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	scalar := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if e := a.db.QueryRow(q, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	company, worker := id(), id()
	if _, e := a.db.Exec("INSERT INTO companies(id,name) VALUES(?,?)", company, "结算专项公司"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO workers(id,name,crafts) VALUES(?,?,?)", worker, "结算专项师傅", jsonText([]string{"开工"})); e != nil {
		t.Fatal(e)
	}
	project := must("/api/project", Project{Company: company, Name: "结算专项工地", Address: "独立库测试"}).(map[string]any)["id"].(string)
	order := Order{Company: company, Project: project, Worker: worker, Address: "独立库测试", Date: "2026-09-28", TicketNo: "伪造票据", ReturnNote: "伪造退回", Lines: []Line{{Temporary: true, Category: "开工", Name: "专项", Mode: "金额", Price: 10000, Wage: 500}}}
	oid := must("/api/order", order).(map[string]any)["id"].(string)
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND ticket_no='' AND return_note=''", oid) != 1 {
		t.Fatal("ordinary creation wrote financial metadata")
	}
	view := get(limited, oid)
	if _, ok := view["wage_cents"]; ok {
		t.Fatal("order wage total leaked")
	}
	line := view["lines"].([]any)[0].(map[string]any)
	for _, field := range []string{"wage_cents", "labor_cents"} {
		if _, ok := line[field]; ok {
			t.Fatal("saved order wages leaked to price manager", field)
		}
	}
	var edited Order
	if e := json.Unmarshal([]byte(jsonText(view)), &edited); e != nil {
		t.Fatal(e)
	}
	edited.Combinations = nil // Ordinary edits preserve the existing combination state.
	edited.Lines[0].Wage = 99999
	if _, e := call(limited, "/api/order", edited, id()); e != nil {
		t.Fatal(e)
	}
	if scalar("SELECT wage_cents FROM orders WHERE id=?", oid) != 500 {
		t.Fatal("price manager changed actual wage")
	}
	batch := func(action string, version int64) map[string]any {
		return map[string]any{"action": action, "company_id": company, "orders": []map[string]any{{"id": oid, "version": version}}}
	}
	must("/api/settle", batch("submit", 2))
	deliver := batch("deliver", 3)
	deliver["ticket_no"] = "票-001"
	deliver["recipient"] = "收票人"
	if _, e := call(admin, "/api/settle", deliver, id()); e == nil {
		t.Fatal("missing delivery date accepted")
	}
	if scalar("SELECT version FROM orders WHERE id=?", oid) != 3 {
		t.Fatal("invalid delivery changed order")
	}
	deliver["delivered_at"] = "2026-09-28"
	deliver["ticket_attachment"] = "纸票保管位置A"
	must("/api/settle", deliver)
	partial := batch("receive_partial", 4)
	partial["amount_cents"] = 4000
	partial["received_at"] = "2026-09-28"
	partial["method"] = "银行"
	partial["voucher_no"] = "凭-1"
	partial["note"] = "首笔"
	key := id()
	if _, e := call(admin, "/api/settle", partial, key); e != nil {
		t.Fatal(e)
	}
	if _, e := call(admin, "/api/settle", partial, key); e != nil {
		t.Fatal("same payment retry", e)
	}
	if scalar("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", oid) != 1 {
		t.Fatal("payment retry duplicated receipt")
	}
	partial["amount_cents"] = 3000
	if _, e := call(admin, "/api/settle", partial, key); e == nil {
		t.Fatal("different amount reused payment key")
	}
	view = get(admin, oid)
	if number(view, "received_cents") != 4000 || number(view, "outstanding_cents") != 6000 || text(view, "status") != "部分收款" {
		t.Fatal("partial balance mismatch", view)
	}
	partial = batch("receive_partial", 5)
	partial["amount_cents"] = 7000
	partial["received_at"] = "2026-09-28"
	if _, e := call(admin, "/api/settle", partial, id()); e == nil {
		t.Fatal("overpayment accepted")
	}
	if scalar("SELECT version FROM orders WHERE id=?", oid) != 5 {
		t.Fatal("overpayment did not roll back")
	}
	full := batch("receive_full", 5)
	full["received_at"] = "2026-09-28"
	full["voucher_no"] = "凭-2"
	must("/api/settle", full)
	if scalar("SELECT SUM(amount_cents) FROM payment_receipts WHERE order_id=? AND voided=0", oid) != 10000 {
		t.Fatal("full payment charged face amount again")
	}
	if _, ok := get(limited, oid)["payment_receipts"]; ok {
		t.Fatal("payment history leaked without finance permission")
	}
	if _, e := call(limited, "/api/settle", full, id()); e == nil {
		t.Fatal("unauthorized finance write")
	}
	ret := batch("return", 6)
	if _, e := call(admin, "/api/settle", ret, id()); e == nil {
		t.Fatal("return without reason accepted")
	}
	ret["note"] = "票据需更正"
	must("/api/settle", ret)
	view = get(admin, oid)
	if len(view["payment_receipts"].([]any)) != 2 || scalar("SELECT COUNT(*) FROM payment_receipts WHERE order_id=? AND voided=1", oid) != 2 {
		t.Fatal("return lost receipt history")
	}
	if e := json.Unmarshal([]byte(jsonText(view)), &edited); e != nil {
		t.Fatal(e)
	}
	edited.Combinations = nil // Ordinary edits preserve the existing combination state.
	edited.TicketNo = "伪造替换"
	edited.ReturnNote = "伪造退回"
	must("/api/order", edited)
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND ticket_no='票-001' AND return_note='票据需更正' AND recipient='收票人' AND delivered_at='2026-09-28'", oid) != 1 {
		t.Fatal("ordinary edit lost saved finance fields")
	}
	if scalar("SELECT COUNT(*) FROM audit WHERE target_id=? AND action='return' AND JSON_UNQUOTE(JSON_EXTRACT(detail,'$.note'))='票据需更正'", oid) != 1 {
		t.Fatal("return reason missing from audit")
	}
	edited.Version = 8
	edited.Delete = true
	must("/api/order", edited)
	view = get(admin, oid)
	if number(view, "received_cents") != 0 || number(view, "outstanding_cents") != 0 {
		t.Fatal("cancelled balance mismatch")
	}
	// Two concurrent requests based on the same version must not collect twice.
	second := must("/api/order", order).(map[string]any)["id"].(string)
	for _, x := range []struct {
		action  string
		version int64
	}{{"submit", 1}, {"deliver", 2}} {
		must("/api/settle", map[string]any{"action": x.action, "orders": []map[string]any{{"id": second, "version": x.version}}, "ticket_no": "票-002", "recipient": "收票人", "delivered_at": "2026-09-28"})
	}
	concurrent := map[string]any{"action": "receive_partial", "amount_cents": 6000, "received_at": "2026-09-28", "orders": []map[string]any{{"id": second, "version": 3}}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := call(admin, "/api/settle", concurrent, id()); results <- e }()
	}
	wg.Wait()
	close(results)
	ok := 0
	for e := range results {
		if e == nil {
			ok++
		}
	}
	if ok != 1 || scalar("SELECT SUM(amount_cents) FROM payment_receipts WHERE order_id=?", second) != 6000 {
		t.Fatal("concurrent overcollection", ok)
	}
	plain := must("/api/order", order).(map[string]any)["id"].(string)
	plainBatch := func(action string, version int64) map[string]any {
		return map[string]any{"action": action, "company_id": company, "orders": []map[string]any{{"id": plain, "version": version}}}
	}
	must("/api/settle", plainBatch("submit", 1))
	plainDelivery := plainBatch("deliver", 2)
	plainDelivery["recipient"] = "现场接收人"
	plainDelivery["delivered_at"] = "2026-09-29"
	plainKey := id()
	if _, e := call(admin, "/api/settle", plainDelivery, plainKey); e != nil {
		t.Fatal("delivery without ticket number or attachment failed", e)
	}
	if _, e := call(admin, "/api/settle", plainDelivery, plainKey); e != nil {
		t.Fatal("idempotent plain delivery retry failed", e)
	}
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND status='已交单' AND version=3 AND ticket_no='' AND ticket_attachment='' AND delivered_at='2026-09-29' AND recipient='现场接收人'", plain) != 1 ||
		scalar("SELECT COUNT(*) FROM settlements WHERE order_id=? AND kind='deliver'", plain) != 1 ||
		scalar("SELECT COUNT(*) FROM audit WHERE target_id=? AND action='deliver'", plain) != 1 {
		t.Fatal("plain delivery status, metadata, version, settlement or audit mismatch")
	}
	changedDelivery := plainBatch("deliver", 2)
	changedDelivery["recipient"] = "其他接收人"
	changedDelivery["delivered_at"] = "2026-09-29"
	if _, e := call(admin, "/api/settle", changedDelivery, plainKey); e == nil {
		t.Fatal("changed plain delivery reused operation key")
	}
	plainPayment := plainBatch("receive_full", 3)
	plainPayment["received_at"] = "2026-09-29"
	must("/api/settle", plainPayment)
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND status='已收款' AND version=4", plain) != 1 ||
		scalar("SELECT COALESCE(SUM(amount_cents),0) FROM payment_receipts WHERE order_id=? AND voided=0", plain) != 10000 {
		t.Fatal("plain delivery could not complete payment")
	}
	historical := must("/api/order", order).(map[string]any)["id"].(string)
	historicalBatch := func(action string, version int64) map[string]any {
		return map[string]any{"action": action, "company_id": company, "orders": []map[string]any{{"id": historical, "version": version}}}
	}
	must("/api/settle", historicalBatch("submit", 1))
	oldDelivery := historicalBatch("deliver", 2)
	oldDelivery["ticket_no"] = "旧票号"
	oldDelivery["ticket_attachment"] = "旧附件说明"
	oldDelivery["recipient"] = "原接收人"
	oldDelivery["delivered_at"] = "2026-09-28"
	must("/api/settle", oldDelivery)
	returnOld := historicalBatch("return", 3)
	returnOld["note"] = "重新交单"
	must("/api/settle", returnOld)
	must("/api/settle", historicalBatch("submit", 4))
	newDelivery := historicalBatch("deliver", 5)
	newDelivery["recipient"] = "新接收人"
	newDelivery["delivered_at"] = "2026-09-29"
	must("/api/settle", newDelivery)
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND status='已交单' AND version=6 AND ticket_no='旧票号' AND ticket_attachment='旧附件说明' AND recipient='新接收人' AND delivered_at='2026-09-29'", historical) != 1 {
		t.Fatal("redelivery erased historical ticket details")
	}
	legacy, zero := id(), id()
	for _, x := range []struct {
		id, status string
		amount     int64
	}{{legacy, "已收款", 12345}, {zero, "已交单", 0}} {
		if _, e := a.db.Exec("INSERT INTO orders(id,number,company_id,project_id,address,work_date,note,status,total_cents,created_by) VALUES(?,?,?,?,?,'2026-09-28','',?,?,'test')", x.id, x.id, company, project, "旧单", x.status, x.amount); e != nil {
			t.Fatal(e)
		}
	}
	view = get(admin, legacy)
	if number(view, "received_cents") != 12345 || number(view, "outstanding_cents") != 0 {
		t.Fatal("legacy paid detail mismatch")
	}
	must("/api/settle", map[string]any{"action": "receive_full", "received_at": "2026-09-28", "orders": []map[string]any{{"id": zero, "version": 1}}})
	if scalar("SELECT COUNT(*) FROM orders WHERE id=? AND status='已收款'", zero) != 1 || scalar("SELECT COUNT(*) FROM payment_receipts WHERE order_id=?", zero) != 0 {
		t.Fatal("zero-value completion invented payment")
	}
	t.Log("settlement forms contract: dates, receipt retry/concurrency, full remaining balance, return history, actual wage privacy and preserved financial metadata passed")
}
