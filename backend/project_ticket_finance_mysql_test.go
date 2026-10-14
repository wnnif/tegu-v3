package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectTicketFinanceBatchMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{ID: "finance-batch-admin", Name: "合成财务管理员", Role: "admin"}
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name) VALUES('finance-company','=合成财务公司'),('finance-other','合成其他公司')`)
	for _, suffix := range []string{"a", "b"} {
		project, order, ticket, line := "finance-project-"+suffix, "finance-order-"+suffix, "finance-ticket-"+suffix, "finance-line-"+suffix
		if _, e := a.db.Exec(`INSERT INTO projects(id,number,company_id,name,address,created_by,ticket_mode_snapshot) VALUES(?,?,'finance-company','合成批量工地','合成地址','test','single')`, project, project); e != nil {
			t.Fatal(e)
		}
		if _, e := a.db.Exec(`INSERT INTO orders(id,number,company_id,project_id,address,work_date,note,created_by,total_cents,wage_cents,worker_id,ticket_managed) VALUES(?,?,'finance-company',?,'合成地址','2026-10-01','','test',10000,3000,'synthetic-worker',1)`, order, order, project); e != nil {
			t.Fatal(e)
		}
		if _, e := a.db.Exec(`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES(?,?,'开工','合成施工','金额','[{"mode":"金额","amount_cents":10000}]',1000000,10000,3000,10000,3000,1)`, line, order); e != nil {
			t.Fatal(e)
		}
		if _, e := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,slot,total_cents,created_at,updated_at) VALUES(?,?,'finance-company','=合成财务公司','合成批量工地','合成地址',?,'normal',1,10000,'2026-10-01','2026-10-01')`, ticket, project, ticket); e != nil {
			t.Fatal(e)
		}
		if _, e := a.db.Exec(`INSERT INTO project_ticket_items(item_id,ticket_id,order_id,item_kind) VALUES(?,?,?,'line')`, line, ticket, order); e != nil {
			t.Fatal(e)
		}
	}
	call := func(path, key string, payload any) (any, error) {
		t.Helper()
		raw := []byte(jsonText(payload))
		r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		r.Header.Set("Idempotency-Key", "finance-batch-"+key)
		return a.mutate(r, admin, raw, func(tx *sql.Tx, k string) (any, error) { return a.post(tx, path, admin, raw, k) })
	}
	batch := map[string]any{"company_id": "finance-company", "action": "deliver", "delivered_at": "2026-10-01", "recipient": "合成收票人", "tickets": []SettlementOrder{{ID: "finance-ticket-a", Version: 1}, {ID: "finance-ticket-b", Version: 99}}}
	wrongCompany := map[string]any{"company_id": "finance-other", "action": "deliver", "delivered_at": "2026-10-01", "recipient": "合成收票人", "tickets": []SettlementOrder{{ID: "finance-ticket-a", Version: 1}}}
	_, err := call("/api/project-ticket-batch", "cross-company-conflict", wrongCompany)
	maintenanceFaultError(t, err, 409)
	_, err = call("/api/project-ticket-batch", "deliver-conflict", batch)
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='草稿' AND version=1", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_versions", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM requests", 0)
	batch["tickets"] = []SettlementOrder{{ID: "finance-ticket-a", Version: 1}, {ID: "finance-ticket-b", Version: 1}}
	_, err = call("/api/project-ticket-batch", "deliver-retired", batch)
	maintenanceFaultError(t, err, 409)
	_, err = call("/api/project-ticket-batch", "deliver-retired", batch)
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='草稿' AND version=1", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM requests", 0)
	// Existing delivered-ticket histories remain payable. Seed only these two
	// synthetic fixtures; the retired delivery endpoint must not create them.
	for _, ticket := range []string{"finance-ticket-a", "finance-ticket-b"} {
		fixtureLegacyDeliveredProjectTicket(t, a.db, admin, ticket, 1, "2026-10-01")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='已交票' AND version=2", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_versions", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit", 0)
	_, err = call("/api/project-ticket-action", "partial-retired", ProjectTicketActionRequest{ID: "finance-ticket-a", Version: 2, Action: "receive", Amount: 2000, ReceivedAt: "2026-10-01", Method: "合成转账"})
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM company_receipts", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts", 0)
	createReceipt := func(key string, amount int64) companyReceiptDetail {
		t.Helper()
		v, e := call("/api/company-receipt", key, companyReceiptRequest{CompanyID: "finance-company", Amount: amount, ReceivedAt: "2026-10-01", Method: "合成转账"})
		if e != nil {
			t.Fatal(e)
		}
		return v.(companyReceiptDetail)
	}
	partial := createReceipt("partial-cash", 2000)
	if _, e := call("/api/receipt-allocation", "partial-a", receiptAllocationRequest{ReceiptID: partial.Receipt.ID, Version: partial.Receipt.Version, Reason: "合成部分分配", Allocations: []receiptAllocationInput{{TicketID: "finance-ticket-a", Amount: 2000}}}); e != nil {
		t.Fatal(e)
	}
	batch = map[string]any{"company_id": "finance-company", "action": "receive_full", "received_at": "2026-10-01", "method": "合成转账", "tickets": []SettlementOrder{{ID: "finance-ticket-a", Version: 3}, {ID: "finance-ticket-b", Version: 99}}}
	_, err = call("/api/project-ticket-batch", "receive-retired", batch)
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE id='finance-ticket-a' AND version=3 AND status='部分收款'", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_versions", 3)
	batch["tickets"] = []SettlementOrder{{ID: "finance-ticket-a", Version: 3}, {ID: "finance-ticket-b", Version: 2}}
	_, err = call("/api/project-ticket-batch", "receive-valid-retired", batch)
	maintenanceFaultError(t, err, 409)
	remaining := createReceipt("remaining-cash", 18000)
	allocation := receiptAllocationRequest{ReceiptID: remaining.Receipt.ID, Version: remaining.Receipt.Version, Reason: "合成同公司跨工地收齐", Allocations: []receiptAllocationInput{{TicketID: "finance-ticket-a", Amount: 7999}, {TicketID: "finance-ticket-b", Amount: 10001}}}
	_, err = call("/api/receipt-allocation", "overallocate-conflict", allocation)
	maintenanceFaultError(t, err, 400)
	allocation.Allocations = []receiptAllocationInput{{TicketID: "finance-ticket-a", Amount: 8000}, {TicketID: "finance-ticket-b", Amount: 10000}}
	allocation.Version = 99
	_, err = call("/api/receipt-allocation", "allocation-version-conflict", allocation)
	maintenanceFaultError(t, err, 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_versions", 3)
	allocation.Version = remaining.Receipt.Version
	if _, e := call("/api/receipt-allocation", "allocate-success", allocation); e != nil {
		t.Fatal(e)
	}
	if _, e := call("/api/receipt-allocation", "allocate-success", allocation); e != nil {
		t.Fatal("idempotent allocation", e)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM company_receipts", 2)
	maintenanceFaultCount(t, a.db, "SELECT COALESCE(SUM(amount_cents),0) FROM company_receipts WHERE status='active'", 20000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts", 3)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE status='已收款'", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_versions", 5)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM settlements", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders WHERE wage_cents=3000 AND wage_paid=0", 2)
	for _, actor := range []User{{Role: "employee", Permissions: []string{"orders"}}, {Role: "employee", Permissions: []string{"finance"}}} {
		maintenanceFaultError(t, a.readProjectTicket(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/project-ticket?id=finance-ticket-a&version=2", nil), actor), 403)
	}
	// A historical financial snapshot is immutable and is redacted for the
	// current reader even though the stored snapshot contains internal wages.
	for _, actor := range []User{{Role: "employee", Permissions: []string{"orders", "customer_prices"}}, {Role: "employee", Permissions: []string{"finance", "customer_prices"}}} {
		w := httptest.NewRecorder()
		if e := a.readProjectTicket(w, httptest.NewRequest("GET", "/api/project-ticket?id=finance-ticket-a&version=2", nil), actor); e != nil {
			t.Fatal(e)
		}
		var snapshot projectTicketDetail
		if e := json.Unmarshal(w.Body.Bytes(), &snapshot); e != nil {
			t.Fatal(e)
		}
		if snapshot.Ticket.ReceivedCents != 0 || snapshot.Ticket.TotalCents != 10000 || snapshot.Ticket.Status != "已交票" || len(snapshot.Orders) != 1 || len(snapshot.Orders[0].Lines) != 1 || snapshot.Orders[0].Lines[0].Total != 10000 {
			t.Fatal("old ticket snapshot was changed", snapshot)
		}
		if strings.Contains(w.Body.String(), "wage_cents") || strings.Contains(w.Body.String(), "labor_cents") || strings.Contains(w.Body.String(), "delivery_wage_cents") {
			t.Fatal("historical snapshot leaked wages")
		}
	}
	// More than one export batch and six list pages ensure all matching rows
	// are included, including normal and append tickets under the same company.
	for i := 0; i < 601; i++ {
		ticket := fmt.Sprintf("finance-extra-%04d", i)
		if _, e := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,slot,total_cents,created_at,updated_at) VALUES(?,'finance-project-a','finance-company','=合成财务公司','合成批量工地','合成地址',?,'append',NULL,200,'2026-10-01','2026-10-01')`, ticket, ticket); e != nil {
			t.Fatal(e)
		}
	}
	maintenanceFaultExec(t, a.db, "UPDATE companies SET name='更名后公司' WHERE id='finance-company'")
	maintenanceFaultExec(t, a.db, "UPDATE projects SET name='更名后工地',address='更正后主地址' WHERE company_id='finance-company'")
	finance := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	w := httptest.NewRecorder()
	if e := a.listProjectTickets(w, httptest.NewRequest("GET", "/api/project-tickets?company_id=finance-company&export=1", nil), finance); e != nil {
		t.Fatal(e)
	}
	records, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if e != nil || len(records) != 604 {
		t.Fatal("full ticket export truncated", len(records), e)
	}
	seen := map[string]bool{}
	for _, row := range records[1:] {
		if seen[row[3]] {
			t.Fatal("duplicate exported ticket", row[3])
		}
		seen[row[3]] = true
		if row[0] != "'=合成财务公司" || row[1] != "合成批量工地" || row[2] != "合成地址" {
			t.Fatal("historical names/address changed or unsafe spreadsheet company cell", row)
		}
	}
	w = httptest.NewRecorder()
	if e := a.listProjectTickets(w, httptest.NewRequest("GET", "/api/project-tickets?company_id=finance-company&page=7", nil), finance); e != nil {
		t.Fatal(e)
	}
	var page struct {
		Rows        []ticketFinanceRow `json:"rows"`
		Total       int64              `json:"total"`
		Outstanding int64              `json:"outstanding_cents"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if page.Total != 603 || len(page.Rows) != 3 || page.Outstanding != 120200 {
		t.Fatal("ticket list totals/pagination", page)
	}
	if e := a.listProjectTickets(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/project-tickets?company_id=finance-company&export=1", nil), User{Role: "employee", Permissions: []string{"orders"}}); e == nil {
		t.Fatal("export bypassed finance permission")
	}
	tx, e := a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = a.projectTicketBatch(tx, User{Role: "employee", Permissions: []string{"orders"}}, []byte(jsonText(batch)), "denied"); e == nil {
		t.Fatal("batch bypassed finance permission")
	}
}

func TestProjectTicketModeNamesMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name,ticket_mode) VALUES('names','合成公司','split')`)
	for _, mode := range []string{"single", "split"} {
		if _, err := a.db.Exec(`INSERT INTO projects(id,number,company_id,name,address,created_by,ticket_mode_snapshot) VALUES(?,?,'names','合成工地','合成地址','test',?)`, mode, mode, mode); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := []struct {
		id, project, kind, label string
		slot                     int
	}{{"single-normal", "single", "normal", "统一票", 1}, {"split-first", "split", "normal", "第一票", 1}, {"split-second", "split", "normal", "第二票", 2}, {"single-append", "single", "append", "追加票", 0}}
	for _, f := range fixtures {
		var slot any
		if f.slot > 0 {
			slot = f.slot
		}
		if _, err := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,number,kind,slot,total_cents,created_at,updated_at) VALUES(?,?,'names',?,?,?,115000,'2026-10-05','2026-10-05')`, f.id, f.project, f.id, f.kind, slot); err != nil {
			t.Fatal(err)
		}
	}
	// Changing the live company mode cannot change either existing project's names.
	maintenanceFaultExec(t, a.db, `UPDATE companies SET ticket_mode='single' WHERE id='names'`)
	actor := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	w := httptest.NewRecorder()
	if err := a.listProjectTickets(w, httptest.NewRequest("GET", "/api/project-tickets?company_id=names", nil), actor); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Rows   []ticketFinanceRow
		Total  int
		Amount int64 `json:"total_cents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || page.Amount != 460000 || len(page.Rows) != 4 {
		t.Fatal("list money/count changed", page)
	}
	labels := map[string]string{}
	for _, v := range page.Rows {
		labels[v.ID] = projectTicketLabel(v.Kind, v.TicketMode, v.Slot)
	}
	for _, f := range fixtures {
		if labels[f.id] != f.label {
			t.Fatal("wrong snapshot label", f.id, labels)
		}
		w = httptest.NewRecorder()
		if err := a.readProjectTicket(w, httptest.NewRequest("GET", "/api/project-ticket?id="+f.id, nil), actor); err != nil {
			t.Fatal(err)
		}
		var d projectTicketDetail
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if d.Ticket.TicketMode != f.project || d.Ticket.TotalCents != 115000 {
			t.Fatal("detail mode/money changed", d.Ticket)
		}
	}
	w = httptest.NewRecorder()
	if err := a.listProjectTickets(w, httptest.NewRequest("GET", "/api/project-tickets?company_id=names&export=1", nil), actor); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 5 {
		t.Fatal("export", err, len(records))
	}
	for _, row := range records[1:] {
		if labels[row[3]] != row[4] || row[6] != "1150.00" || row[7] != "0.00" || row[8] != "1150.00" {
			t.Fatal("export mode/money", row)
		}
	}
	// Historical payloads lacking the new display field retain their old contents.
	detail := projectTicketDetail{Ticket: projectTicketView{ID: "split-first", ProjectID: "split", Kind: "normal", TotalCents: 115000}}
	if _, err = a.db.Exec(`INSERT INTO project_ticket_versions(ticket_id,version,event,snapshot,created_by) VALUES('split-first',1,'合成交票',?,'test')`, jsonText(detail)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	if err = a.readProjectTicket(w, httptest.NewRequest("GET", "/api/project-ticket?id=split-first&version=1", nil), actor); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Ticket.TicketMode != "split" || detail.Ticket.TotalCents != 115000 {
		t.Fatal("old snapshot display", detail)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM requests", 0)
	if err = a.listProjectTickets(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/project-tickets?company_id=names", nil), User{Role: "employee", Permissions: []string{"orders"}}); err == nil {
		t.Fatal("permission bypass")
	}
}
