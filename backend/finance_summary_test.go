package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type wageSummaryPage struct {
	Rows     []wageWorkerSummary `json:"rows"`
	Total    int64               `json:"total"`
	PageSize int                 `json:"page_size"`
	Wage     int64               `json:"wage_cents"`
	Paid     int64               `json:"paid_cents"`
	Unpaid   int64               `json:"unpaid_cents"`
}

type companySummaryPage struct {
	Rows        []financeCompanySummary `json:"rows"`
	Total       int64                   `json:"total"`
	PageSize    int                     `json:"page_size"`
	Amount      int64                   `json:"total_cents,string"`
	Received    int64                   `json:"received_cents,string"`
	Outstanding int64                   `json:"outstanding_cents,string"`
}

func TestFinancePartySummaryFiltersAndPermissions(t *testing.T) {
	for _, view := range []string{"orders", "finance", "wages"} {
		q := url.Values{"view": {view}, "all_months": {"1"}}
		where, args, err := orderFilter(q)
		if view == "wages" {
			if err != nil || strings.Contains(where, "work_date") || len(args) != 0 {
				t.Fatal("all-month wages still restricted", where, args)
			}
		} else if err == nil {
			t.Fatal("non-wage missing-month rejection changed", where, args)
		}
		q.Set("month", "2026-13")
		if _, _, err = orderFilter(q); err == nil {
			t.Fatal("invalid month bypassed by all_months")
		}
	}
	where, args, err := orderFilter(url.Values{"view": {"wages"}})
	if err == nil {
		t.Fatal("old wage default changed", where, args, err)
	}
	for _, q := range []url.Values{{"wage_paid": {"yes"}}, {"month": {"bad"}}} {
		if _, _, _, _, err = wageWorkerSummaryQuery(q); err == nil {
			t.Fatal("invalid wage filter accepted", q)
		}
	}
	for _, q := range []url.Values{{"status": {"unknown"}}, {"month": {"bad"}}} {
		if _, _, err = financeCompanySummaryQuery(q); err == nil {
			t.Fatal("invalid company filter accepted", q)
		}
	}
	a := &App{}
	for _, tc := range []struct {
		path string
		user User
	}{
		{"/api/wage-workers", User{Role: "employee", Permissions: []string{"finance"}}},
		{"/api/finance-companies", User{Role: "employee", Permissions: []string{"wages"}}},
		{"/api/wage-workers", User{Role: "employee", Permissions: []string{"orders"}}},
		{"/api/finance-companies", User{Role: "employee", Permissions: []string{"orders"}}},
		{"/api/finance-companies", User{Role: "employee", Permissions: []string{"finance"}}},
		{"/api/finance-companies", User{Role: "employee", Permissions: []string{"customer_prices"}}},
	} {
		for _, suffix := range []string{"", "?export=1"} {
			maintenanceFaultError(t, a.get(httptest.NewRecorder(), httptest.NewRequest("GET", tc.path+suffix, nil), tc.user), 403)
		}
	}
}

func TestOrderFilterExactDateRange(t *testing.T) {
	where, args, err := orderFilter(url.Values{"view": {"orders"}, "from": {"2026-09-24"}, "to": {"2026-10-12"}})
	if err != nil {
		t.Fatal("valid exact order date range rejected:", err)
	}
	if !strings.Contains(where, "o.work_date>=? AND o.work_date<=?") || len(args) != 2 || args[0] != "2026-09-24" || args[1] != "2026-10-12" {
		t.Fatal("exact order dates not applied inclusively:", where, args)
	}
	for _, query := range []url.Values{
		{"view": {"orders"}, "from": {"2026-09-24"}},
		{"view": {"orders"}, "from": {"2026-02-30"}, "to": {"2026-10-12"}},
		{"view": {"orders"}, "from": {"2026-10-12"}, "to": {"2026-09-24"}},
		{"view": {"orders"}, "month": {"2026-09"}, "from": {"2026-09-24"}, "to": {"2026-10-12"}},
		{"view": {"wages"}, "from": {"2026-09-24"}, "to": {"2026-10-12"}},
	} {
		if _, _, err := orderFilter(query); err == nil {
			t.Fatal("invalid or unsupported exact order range accepted:", query)
		}
	}
}

func TestWageWorkerSummaryMySQL(t *testing.T) {
	t.Skip("legacy balance endpoint is deprecated; payroll ledger summaries are covered by the phase 5 payroll tests")
	a, admin, wageViewer, _ := wageDetailHTTPFixture(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('summary-co','合成工资公司'),('summary-other','其他公司')`,
		`INSERT INTO workers(id,name,crafts) VALUES('summary-a','保存甲',JSON_ARRAY('开工')),('summary-b','保存乙',JSON_ARRAY('开工')),('summary-zero','@零工资',JSON_ARRAY('开工'))`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('summary-source','','开工','合成施工','平方',0,300),('summary-zero-source','','开工','零工资施工','平方',0,0)`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('summary-fixed','summary-co','summary-source','开工','合成施工','平方',2000,0),('summary-zero-fixed','summary-co','summary-zero-source','开工','零工资施工','平方',1500,0)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	order := Order{Company: "summary-co", Address: "合成汇总工地", Date: "2026-10-08", Lines: []Line{
		{CraftID: "summary-fixed", Worker: "summary-a", Segments: []Segment{{Length: 2, Width: 1}}},
		{CraftID: "summary-fixed", Worker: "summary-b", Segments: []Segment{{Length: 3, Width: 1}}},
		{CraftID: "summary-zero-fixed", Worker: "summary-zero", Segments: []Segment{{Length: 1, Width: 1}}},
	}}
	w := accountIdentityRequest(a, "POST", "/api/order", order, admin)
	if w.Code != 200 {
		t.Fatal("real multi-worker save", w.Code, w.Body.String())
	}
	maintenanceFaultExec(t, a.db, `UPDATE workers SET name='同名人员' WHERE id IN ('summary-a','summary-b')`)
	maintenanceFaultExec(t, a.db, `UPDATE crafts SET active=0 WHERE id IN ('summary-fixed','summary-zero-fixed')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,worker_id,worker_name,address,work_date,note,created_by,wage_cents,wage_paid) VALUES('summary-old','summary-old','summary-co','summary-a','旧姓名','旧月地址','2026-09-01','','test',100,1),('summary-cancelled','summary-cancelled','summary-co','summary-a','旧姓名','取消地址','2026-10-01','','test',99999,0)`)
	maintenanceFaultExec(t, a.db, `UPDATE orders SET status='已取消' WHERE id='summary-cancelled'`)
	read := func(query string) wageSummaryPage {
		t.Helper()
		w := accountIdentityRequest(a, "GET", "/api/wage-workers?"+query, nil, wageViewer)
		var p wageSummaryPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatal("wage summary", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "price_cents") || strings.Contains(w.Body.String(), "total_cents") {
			t.Fatal("wage summary leaked customer charges")
		}
		return p
	}
	p := read("")
	if p.Total != 3 || p.Wage != 1600 || p.Paid != 100 || p.Unpaid != 1500 || p.PageSize != 100 {
		t.Fatal("all-month real totals", p)
	}
	byID := map[string]wageWorkerSummary{}
	for _, row := range p.Rows {
		byID[row.WorkerID] = row
	}
	if byID["summary-a"].OrderCount != 2 || byID["summary-a"].Wage != 700 || byID["summary-b"].Wage != 900 || byID["summary-zero"].Wage != 0 || len(byID) != 3 {
		t.Fatal("same-name/zero records merged", p)
	}
	for _, id := range []string{"summary-a", "summary-b", "summary-zero"} {
		w = accountIdentityRequest(a, "GET", "/api/orders?view=wages&all_months=1&worker_id="+id, nil, wageViewer)
		var detail orderListPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.WageCents == nil || *detail.WageCents != byID[id].Wage || detail.Total != byID[id].OrderCount {
			t.Fatal("summary/detail mismatch", id, w.Body.String())
		}
	}
	if p = read("month=2026-09"); p.Total != 1 || p.Wage != 100 || p.Unpaid != 0 {
		t.Fatal("previous month", p)
	}
	if p = read("wage_paid=0"); p.Total != 3 || p.Wage != 1500 || p.Paid != 0 {
		t.Fatal("unpaid account scope", p)
	}
	if p = read("q=" + url.QueryEscape("同名人员")); p.Total != 2 || p.Wage != 1600 {
		t.Fatal("person search", p)
	}
	if p = read("q=" + url.QueryEscape("合成汇总工地")); p.Total != 0 || len(p.Rows) != 0 {
		t.Fatal("name search accidentally searches address", p)
	}
	if p = read("company_id=summary-other"); p.Total != 0 || p.Wage != 0 {
		t.Fatal("company scope", p)
	}
	// Existing settlement writes must be reflected without recounting the whole
	// shared order or excluding the still-unpaid second worker.
	w = accountIdentityRequest(a, "GET", "/api/orders?view=wages&month=2026-10&worker_id=summary-a", nil, wageViewer)
	var detail orderListPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || len(detail.Rows) != 1 {
		t.Fatal(w.Body.String())
	}
	account := detail.Rows[0]
	for _, action := range []string{"pay", "undo-pay"} {
		w = accountIdentityRequest(a, "POST", "/api/wage-settlement", SettlementRequest{Action: action, Orders: []SettlementOrder{{ID: account.ID, Version: account.Version}}, Note: "合成汇总测试"}, admin)
		if w.Code != 200 {
			t.Fatal("settlement", action, w.Code, w.Body.String())
		}
		account.Version++
		p = read("")
		wantPaid := int64(100)
		if action == "pay" {
			wantPaid = 700
		}
		if p.Paid != wantPaid || p.Wage != 1600 || p.Unpaid != 1600-wantPaid {
			t.Fatal("settlement summary refresh", p)
		}
	}
	for i := 0; i < 103; i++ {
		id := fmt.Sprintf("summary-extra-%03d", i)
		if _, err := a.db.Exec(`INSERT INTO workers(id,name) VALUES(?,?)`, id, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO orders(id,number,company_id,worker_id,worker_name,address,work_date,note,created_by,wage_cents) VALUES(?,?,'summary-co',?,?,'合成分页','2026-09-02','','test',1)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	p = read("page=2")
	if p.Total != 106 || len(p.Rows) != 6 || p.Wage != 1703 || p.Unpaid != 1603 {
		t.Fatal("pagination/full totals", p)
	}
	w = accountIdentityRequest(a, "GET", "/api/wage-workers?export=1&page=2", nil, wageViewer)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if w.Code != 200 || err != nil || len(rows) != 107 {
		t.Fatal("complete grouped CSV", w.Code, len(rows), err)
	}
	safeZero := false
	for _, r := range rows[1:] {
		if r[0] == "summary-zero" {
			safeZero = r[1] == "'@零工资"
		}
	}
	if !safeZero {
		t.Fatal("unsafe person CSV")
	}
	w = accountIdentityRequest(a, "GET", "/api/export?view=wages&all_months=1&worker_id=summary-a", nil, wageViewer)
	rows, err = csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if w.Code != 200 || err != nil || len(rows) != 3 {
		t.Fatal("all-month detail CSV", w.Code, len(rows), err)
	}
	// A partial restored account is rejected even behind a named-person filter.
	maintenanceFaultExec(t, a.db, `DELETE FROM order_worker_wages WHERE worker_id='summary-b'`)
	for _, suffix := range []string{"", "&export=1"} {
		w = accountIdentityRequest(a, "GET", "/api/wage-workers?worker_id=summary-a&q="+url.QueryEscape("同名人员")+suffix, nil, wageViewer)
		if w.Code != 422 {
			t.Fatal("damaged account summary accepted", w.Code, w.Body.String())
		}
	}
}

func TestFinanceCompanySummaryMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	finance := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name) VALUES('party-a','=同名公司'),('party-b','=同名公司'),('party-zero','零金额公司')`)
	for _, id := range []string{"party-a", "party-b", "party-zero"} {
		if _, err := a.db.Exec(`INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES(?,?,?,'合成公司工地','合成地址','test')`, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	maintenanceFaultExec(t, a.db, `UPDATE projects SET status='已结项' WHERE id='party-a'`)
	for _, f := range []struct {
		id, company, state, utc string
		amount                  int64
	}{
		{"ticket-partial", "party-a", "部分收款", "2026-09-30 16:00:00", 10000},
		{"ticket-draft", "party-a", "草稿", "2026-10-31 15:59:59", 5000},
		{"ticket-old", "party-a", "已交票", "2026-09-30 15:59:59", 100},
		{"ticket-paid", "party-b", "已收款", "2026-10-01 00:00:00", 4000},
		{"ticket-zero", "party-zero", "草稿", "2026-10-01 00:00:00", 0},
	} {
		if _, err := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,status,total_cents,created_at,updated_at) VALUES(?,?,?,'保存公司名','保存工地','保存地址',?,'append',?,?,?,?)`, f.id, f.company, f.company, f.id, f.state, f.amount, f.utc, f.utc); err != nil {
			t.Fatal(err)
		}
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,created_by,voided) VALUES('party-r1','ticket-partial',1000,'2026-10-01','test',0),('party-r2','ticket-partial',2000,'2026-10-01','test',0),('party-rvoid','ticket-partial',500,'2026-10-01','test',1),('party-paid','ticket-paid',4000,'2026-10-01','test',0)`)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = backfillCompanyReceiptsTx(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM company_receipts", 4)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts WHERE receipt_id<>''", 4)
	// Unmanaged historical order charges remain a read-only company projection;
	// they appear once in lifetime balances, not in the worksite ticket drilldown.
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,worker_id,address,work_date,note,created_by,total_cents) VALUES('party-old-order','party-old-order','party-a','','合成旧财务','2026-10-01','','test',99999)`)
	read := func(query string) companySummaryPage {
		t.Helper()
		w := httptest.NewRecorder()
		if err := a.get(w, httptest.NewRequest("GET", "/api/finance-companies?"+query, nil), finance); err != nil {
			t.Fatal(err)
		}
		var p companySummaryPage
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "wage") {
			t.Fatal("finance summary leaked wages")
		}
		return p
	}
	p := read("")
	if p.Total != 3 || p.Amount != 119099 || p.Received != 7000 || p.Outstanding != 112099 || p.PageSize != 100 {
		t.Fatal("company amounts/counts", p)
	}
	if p.Rows[0].CompanyID != "party-a" || p.Rows[0].TicketCount != 4 || p.Rows[0].Total != 115099 {
		t.Fatal("joined receipts inflated count or same-name grouping", p)
	}
	for _, row := range p.Rows {
		w := httptest.NewRecorder()
		if err := a.listProjectTickets(w, httptest.NewRequest("GET", "/api/project-tickets?company_id="+row.CompanyID, nil), finance); err != nil {
			t.Fatal(err)
		}
		var detail struct {
			Total       int64 `json:"total"`
			Amount      int64 `json:"total_cents"`
			Received    int64 `json:"received_cents"`
			Outstanding int64 `json:"outstanding_cents"`
		}
		legacyCount, legacyCharge := int64(0), int64(0)
		if row.CompanyID == "party-a" {
			legacyCount, legacyCharge = 1, 99999
		}
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || detail.Total != row.TicketCount-legacyCount || detail.Amount != row.Total-legacyCharge || detail.Received != row.Allocated || detail.Outstanding != row.Unsettled-legacyCharge {
			t.Fatal("company detail totals differ", row, detail, err)
		}
	}
	for _, query := range []string{"month=2026-10", "month=2026-09", "status=" + url.QueryEscape("草稿")} {
		w := httptest.NewRecorder()
		maintenanceFaultError(t, a.get(w, httptest.NewRequest("GET", "/api/finance-companies?"+query, nil), finance), 400)
	}
	if p = read("q=" + url.QueryEscape("同名公司")); p.Total != 2 || p.Amount != 119099 {
		t.Fatal("current-name query", p)
	}
	if p = read("q=" + url.QueryEscape("保存地址")); p.Total != 0 || len(p.Rows) != 0 {
		t.Fatal("company search includes unrelated address", p)
	}
	if p = read("company_id=party-b"); p.Total != 1 || p.Outstanding != 0 || p.Amount != 4000 {
		t.Fatal("company ID scope", p)
	}
	if p = read("status=" + url.QueryEscape("结清")); p.Total != 2 || p.Amount != 4000 || p.Outstanding != 0 {
		t.Fatal("settled company status", p)
	}
	if p = read("status=" + url.QueryEscape("欠款")); p.Total != 1 || p.Amount != 115099 || p.Outstanding != 112099 {
		t.Fatal("outstanding company status", p)
	}
	if p = read("status=" + url.QueryEscape("预收")); p.Total != 0 || len(p.Rows) != 0 {
		t.Fatal("company without surplus reported as advance", p)
	}
	// A controlled cash void also releases its allocation without deleting history.
	payload := []byte(jsonText(map[string]any{"id": "party-r2", "version": 1, "action": "void", "reason": "合成重复登记纠错"}))
	request := httptest.NewRequest("POST", "/api/company-receipt-action", strings.NewReader(string(payload)))
	request.Header.Set("Idempotency-Key", "summary-void-cash")
	admin := User{ID: "summary-finance-admin", Name: "合成测试", Role: "admin"}
	if _, err := a.mutate(request, admin, payload, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, request.URL.Path, admin, payload, key) }); err != nil {
		t.Fatal(err)
	}
	if p = read(""); p.Received != 5000 || p.Outstanding != 114099 {
		t.Fatal("voided receipt still counted", p)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM company_receipts WHERE id='party-r2' AND status='voided'", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_receipts WHERE receipt_id='party-r2' AND voided=1", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM company_receipt_versions WHERE receipt_id='party-r2'", 2)
	for i := 0; i < 103; i++ {
		id := fmt.Sprintf("party-extra-%03d", i)
		if _, err := a.db.Exec(`INSERT INTO companies(id,name) VALUES(?,?)`, id, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES(?,?,?,'合成分页','合成地址','test')`, id, id, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,number,kind,total_cents,created_at,updated_at) VALUES(?,?,?,?,'append',1,'2026-10-01','2026-10-01')`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	if p = read("page=2"); p.Total != 106 || len(p.Rows) != 6 || p.Amount != 119202 || p.Received != 5000 || p.Outstanding != 114202 {
		t.Fatal("company pagination/full totals", p)
	}
	w := httptest.NewRecorder()
	if err := a.get(w, httptest.NewRequest("GET", "/api/finance-companies?export=1&page=2", nil), finance); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 107 {
		t.Fatal("complete company CSV", len(rows), err)
	}
	seen := map[string]bool{}
	for _, row := range rows[1:] {
		if seen[row[0]] {
			t.Fatal("duplicate company ID", row)
		}
		seen[row[0]] = true
		if row[0] == "party-a" && row[1] != "'=同名公司" {
			t.Fatal("unsafe company CSV", row)
		}
	}
}
