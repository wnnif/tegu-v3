package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func secondaryPriceEmployee(permissions ...string) User {
	return User{Role: "employee", Permissions: permissions}
}

func secondaryPriceInt(value int64) *int64 { return &value }

func secondaryPriceObject(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func secondaryPriceAbsent(t *testing.T, fields map[string]json.RawMessage, names ...string) {
	t.Helper()
	for _, name := range names {
		if value, ok := fields[name]; ok {
			t.Fatalf("%s leaked: %s", name, value)
		}
	}
}

func secondaryPriceFailure(t *testing.T, err error, code int) {
	t.Helper()
	if code == 0 {
		if err != nil {
			t.Fatalf("unexpected rejection: %v", err)
		}
		return
	}
	var f failure
	if !errors.As(err, &f) || f.Code != code {
		t.Fatalf("got %v, want HTTP %d", err, code)
	}
}

// Only explicit order/ticket fixtures are walked here; wage and stock views are
// tested separately and must not be subjected to a global monetary-key filter.
func secondaryPriceNoOrderMoney(t *testing.T, data json.RawMessage) {
	t.Helper()
	data = json.RawMessage(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return
	}
	switch data[0] {
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		secondaryPriceAbsent(t, fields, "price_cents", "total_cents", "amount_cents", "received_cents", "outstanding_cents")
		for _, value := range fields {
			secondaryPriceNoOrderMoney(t, value)
		}
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			t.Fatal(err)
		}
		for _, value := range items {
			secondaryPriceNoOrderMoney(t, value)
		}
	}
}

func TestCustomerPriceSecondaryDashboardSQL(t *testing.T) {
	cases := []struct {
		name      string
		user      User
		financial bool
	}{
		{"orders-wages-finance-no-price", secondaryPriceEmployee("orders", "wages", "finance"), false},
		{"orders-only", secondaryPriceEmployee("orders"), false},
		{"wages-only", secondaryPriceEmployee("wages"), false},
		{"price-without-finance", secondaryPriceEmployee("orders", "customer_prices"), false},
		{"finance-and-price", secondaryPriceEmployee("finance", "customer_prices"), true},
		{"admin", User{Role: "admin"}, true},
		{"operator", User{Role: "operator"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := dashboardOrderStatsSQL(tc.user)
			if strings.Contains(query, "oWHERE") || !strings.Contains(query, " WHERE o.work_date>=? AND o.work_date<?") {
				t.Fatalf("missing FROM/WHERE boundary: %s", query)
			}
			if strings.Count(query, "?") != 2 {
				t.Fatalf("month placeholders changed: %s", query)
			}
			if tc.financial {
				if !strings.Contains(query, orderReceiptTotalsJoinSQL) || !strings.Contains(query, orderOutstandingCentsSQL) {
					t.Fatalf("authorized receivable calculation changed: %s", query)
				}
			} else {
				if !strings.Contains(query, " FROM orders o WHERE ") || strings.Contains(query, "payment_receipts") || strings.Contains(query, "o.total_cents") {
					t.Fatalf("hidden receivable branch is unsafe or malformed: %s", query)
				}
			}
			if !strings.Contains(query, "COUNT(*)") || strings.Contains(query, "wage_paid") || strings.Contains(query, "o.wage_cents") {
				t.Fatalf("dashboard order summary must leave wages to the payroll ledger: %s", query)
			}
		})
	}
}

func TestDashboardUnpaidWagesUseAllTimePayrollLedger(t *testing.T) {
	if strings.Contains(payrollWorkerSummarySQL, "wage_paid=") || !strings.Contains(payrollWorkerSummarySQL, "unpaid_cents") {
		t.Fatal("payroll summary no longer derives unpaid balance from the ledger")
	}
	query := "SELECT COALESCE(SUM(unpaid_cents),0) FROM (" + payrollWorkerSummarySQL + ") payroll_summary"
	if !strings.Contains(query, "payroll_settlements") || !strings.Contains(query, "payroll_payments") {
		t.Fatalf("dashboard wage balance is not based on settlements and payments: %s", query)
	}
}

func TestCustomerPriceSecondaryMutationGuards(t *testing.T) {
	orders := secondaryPriceEmployee("orders")
	price := secondaryPriceEmployee("customer_prices")
	wages := secondaryPriceEmployee("wages")
	finance := secondaryPriceEmployee("finance")
	financial := secondaryPriceEmployee("finance", "customer_prices")
	cases := []struct {
		name, path, body string
		user             User
		code             int
	}{
		{"order-without-price", "/api/order", `{"delete":false}`, orders, 0},
		{"revoked-orders-kept-price", "/api/order", `{}`, price, 403},
		{"delete-requires-delete-module", "/api/order", `{"delete":true}`, orders, 403},
		{"delete-case-insensitive-decode", "/api/order", `{"Delete":true}`, orders, 403},
		{"delete-without-price", "/api/order", `{"delete":true}`, secondaryPriceEmployee("orders", "order_delete"), 0},
		{"delete-only-is-not-orders", "/api/order", `{"delete":true}`, secondaryPriceEmployee("order_delete", "customer_prices"), 403},
		{"invalid-order-body", "/api/order", "not-json", orders, 400},
		{"wage-settlement-retired-no-price", "/api/wage-settlement", `{"action":"pay"}`, wages, 409},
		{"wage-undo-retired-no-price", "/api/wage-settlement", `{"action":"undo-pay"}`, wages, 409},
		{"revoked-wages-kept-price", "/api/wage-settlement", `{"action":"pay"}`, price, 403},
		{"finance-cannot-replay-wages", "/api/wage-settlement", `{}`, financial, 403},
		{"payroll-without-price", "/api/payroll-action", `{"action":"create-payment"}`, wages, 0},
		{"payroll-requires-wages", "/api/payroll-action", `{"action":"create-payment"}`, price, 403},
		{"ticket-action-no-price", "/api/project-ticket-action", `{}`, finance, 403},
		{"ticket-action-no-finance", "/api/project-ticket-action", `{}`, price, 403},
		{"ticket-action-authorized", "/api/project-ticket-action", `{}`, financial, 0},
		{"ticket-batch-no-price", "/api/project-ticket-batch", `{}`, finance, 403},
		{"ticket-batch-authorized", "/api/project-ticket-batch", `{}`, financial, 0},
		{"issue-no-price", "/api/ticket-batch", `{}`, finance, 403},
		{"issue-no-finance", "/api/ticket-batch", `{}`, price, 403},
		{"issue-authorized", "/api/ticket-batch", `{}`, financial, 0},
		{"issue-void-no-price", "/api/ticket-batch-action", `{}`, finance, 403},
		{"issue-void-no-finance", "/api/ticket-batch-action", `{}`, price, 403},
		{"issue-void-authorized", "/api/ticket-batch-action", `{}`, financial, 0},
		{"finance-edit-no-price", "/api/finance-edit", `{}`, finance, 403},
		{"finance-edit-authorized", "/api/finance-edit", `{}`, financial, 0},
		{"legacy-pay-retired-no-price", "/api/settle", `{"action":"pay"}`, wages, 409},
		{"legacy-undo-retired-no-price", "/api/settle", `{"action":"undo-pay"}`, wages, 409},
		{"legacy-pay-revoked-wages", "/api/settle", `{"action":"pay"}`, financial, 403},
		{"receive-no-price", "/api/settle", `{"action":"receive_partial"}`, finance, 403},
		{"receive-no-finance", "/api/settle", `{"action":"receive_full"}`, price, 403},
		{"receive-authorized", "/api/settle", `{"action":"receive"}`, financial, 0},
		{"deliver-no-price", "/api/settle", `{"action":"deliver"}`, finance, 403},
		{"company-hidden-save-not-blanket-denied", "/api/entity", `{"entity":"companies"}`, secondaryPriceEmployee("customers"), 0},
		{"ordinary-project-not-blanket-denied", "/api/project", `{}`, orders, 0},
		{"warehouse-not-price-gated", "/api/stock", `{}`, secondaryPriceEmployee("warehouse"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secondaryPriceFailure(t, customerPriceMutationAllowed(tc.path, tc.user, []byte(tc.body)), tc.code)
		})
	}
}

func TestCustomerPriceSecondaryViewProjections(t *testing.T) {
	const exact int64 = 9007199254740993
	cases := []struct {
		name    string
		value   any
		missing []string
		present map[string]string
	}{
		{"craft-hidden-keeps-wage", craftsRecord{customerPricesHidden: true, Name: "工艺", Spec: "尺寸", PriceCents: exact, WageCents: secondaryPriceInt(exact)}, []string{"price_cents"}, map[string]string{"wage_cents": strconv.FormatInt(exact, 10), "name": `"工艺"`, "spec": `"尺寸"`}},
		{"craft-visible-real-zero", craftsRecord{PriceCents: 0}, nil, map[string]string{"price_cents": "0"}},
		{"project-hidden", projectRecord{customerPricesHidden: true, Name: "工地", OrderCount: 3, TotalCents: exact, ReceivedCents: 1}, []string{"total_cents", "received_cents", "undelivered_cents", "delivered_outstanding_cents", "outstanding_cents"}, map[string]string{"order_count": "3", "name": `"工地"`}},
		{"project-price-not-finance", projectRecord{financeHidden: true, TotalCents: exact}, []string{"received_cents", "undelivered_cents", "delivered_outstanding_cents", "outstanding_cents"}, map[string]string{"total_cents": strconv.FormatInt(exact, 10)}},
		{"project-visible-real-zero", projectRecord{}, nil, map[string]string{"total_cents": "0", "received_cents": "0", "outstanding_cents": "0"}},
		{"project-order-hidden", projectOrderRecord{customerPricesHidden: true, WorkerName: "师傅", TotalCents: exact}, []string{"total_cents", "received_cents", "outstanding_cents"}, map[string]string{"worker_name": `"师傅"`}},
		{"unassigned-order-hidden", unassignedProjectOrderRecord{customerPricesHidden: true, Version: 8, TotalCents: exact}, []string{"total_cents"}, map[string]string{"version": "8"}},
		{"ticket-hidden", projectTicketView{customerPricesHidden: true, Number: "票号", TotalCents: exact, PackageChargeCents: exact, PackageCharge: &projectPackageChargeView{ID: "pkg", Name: "合成套餐", Amount: exact}}, []string{"total_cents", "package_charge_cents", "package_charge", "received_cents", "outstanding_cents"}, map[string]string{"number": `"票号"`}},
		{"ticket-price-not-finance", projectTicketView{financeHidden: true, TotalCents: exact, PackageChargeCents: exact, PackageCharge: &projectPackageChargeView{ID: "pkg", Name: "合成套餐", Amount: exact}}, []string{"received_cents", "outstanding_cents"}, map[string]string{"total_cents": strconv.FormatInt(exact, 10), "package_charge_cents": strconv.FormatInt(exact, 10)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := secondaryPriceObject(t, tc.value)
			secondaryPriceAbsent(t, fields, tc.missing...)
			for name, expected := range tc.present {
				if string(fields[name]) != expected {
					t.Fatalf("%s got %s, want %s", name, fields[name], expected)
				}
			}
		})
	}
	data, err := marshalSecondaryCustomerView(map[string]any{
		"total_cents": exact, "wage_cents": exact,
		"stock": map[string]int64{"price_cents": exact, "total_cents": exact, "amount_cents": exact},
	}, true, true, []string{"total_cents"}, []string{"received_cents"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	secondaryPriceAbsent(t, fields, "total_cents")
	if string(fields["wage_cents"]) != strconv.FormatInt(exact, 10) {
		t.Fatal("wage integer changed")
	}
	var stock map[string]int64
	if err := json.Unmarshal(fields["stock"], &stock); err != nil {
		t.Fatal(err)
	}
	if len(stock) != 3 || stock["price_cents"] != exact || stock["amount_cents"] != exact {
		t.Fatal("stock costs were globally redacted", stock)
	}
}

const secondaryPriceTicketFixture = `{
    "ticket":{"id":"ticket","number":"票号","total_cents":15000,"received_cents":800,"outstanding_cents":14200},
    "orders":[{
        "id":"order","address":"施工地址","worker_id":"worker","worker_name":"师傅","total_cents":15000,
        "received_cents":800,"outstanding_cents":14200,"wage_cents":600,"wage_paid":false,"delivery_wage_cents":100,
        "ticket_attachment":"file:financial","payment_receipts":[{"id":"receipt","amount_cents":800}],
        "wage_records":[{"worker_id":"worker","wage_cents":600}],
        "lines":[{"id":"line","name":"工艺","actual_location":"厨房","subitem_name":"配置小项","worker_id":"worker","worker_name":"师傅","qty_micro":12000000,"price_cents":3000,"total_cents":12300,"wage_cents":50,"labor_cents":500,"segments":[{"length":3,"width":4,"mode":"金额","amount_cents":12300}]}],
        "sales":[{"id":"sale","product_name":"材料","qty_micro":3000000,"price_cents":900,"total_cents":2700}]
    }],
    "payment_receipts":[{"id":"receipt","amount_cents":800}]
}`

func secondaryPriceSnapshot(t *testing.T) projectTicketDetail {
	t.Helper()
	var detail projectTicketDetail
	if err := json.Unmarshal([]byte(secondaryPriceTicketFixture), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestCustomerPriceSecondaryTicketSnapshotPermissions(t *testing.T) {
	detail := secondaryPriceSnapshot(t)
	redactProjectTicket(&detail, secondaryPriceEmployee("orders", "customer_prices"))
	ticket := secondaryPriceObject(t, detail.Ticket)
	secondaryPriceAbsent(t, ticket, "received_cents", "outstanding_cents")
	if string(ticket["total_cents"]) != "15000" {
		t.Fatal("ordinary orders+price ticket total disappeared")
	}
	order := secondaryPriceObject(t, detail.Orders[0])
	secondaryPriceAbsent(t, order, "received_cents", "outstanding_cents", "payment_receipts", "ticket_attachment", "wage_cents", "wage_paid", "delivery_wage_cents", "wage_records")
	if string(order["total_cents"]) != "15000" || string(secondaryPriceObject(t, detail.Orders[0].Lines[0])["price_cents"]) != "3000" {
		t.Fatal("price view incorrectly required finance")
	}
	secondaryPriceAbsent(t, secondaryPriceObject(t, detail.Orders[0].Lines[0]), "wage_cents", "labor_cents")
	if detail.PaymentReceipts != nil {
		t.Fatal("historical ticket receipts leaked")
	}

	hidden := secondaryPriceSnapshot(t)
	redactProjectTicket(&hidden, secondaryPriceEmployee("orders", "wages", "finance"))
	data, err := json.Marshal(hidden)
	if err != nil {
		t.Fatal(err)
	}
	secondaryPriceNoOrderMoney(t, data)
	if hidden.Orders[0].TotalCents != 15000 || hidden.Orders[0].Lines[0].Price != 3000 || hidden.Orders[0].Lines[0].Segments[0].Amount != 12300 {
		t.Fatal("redaction changed server-side snapshots")
	}
	hiddenOrder := secondaryPriceObject(t, hidden.Orders[0])
	if string(hiddenOrder["wage_cents"]) != "600" || hidden.Orders[0].Lines[0].ActualLocation != "厨房" {
		t.Fatal("wages or ordinary work fields were removed")
	}
	secondaryPriceAbsent(t, hiddenOrder, "payment_receipts", "ticket_attachment")
}

func TestCustomerPriceSecondaryProjectWorkAndTicketCopies(t *testing.T) {
	user := secondaryPriceEmployee("orders", "wages")
	detail := secondaryPriceSnapshot(t)
	redactProjectTicket(&detail, user)
	order := detail.Orders[0]
	filterOrderForTicket(&order, map[string]bool{"line": true})
	if !order.customerPricesHidden || !order.financeHidden || len(order.Lines) != 1 || !order.Lines[0].customerPricesHidden {
		t.Fatal("ticket cropping lost current permission flags")
	}
	if order.TotalCents != 12300 || order.WageCents == nil || *order.WageCents != 500 {
		t.Fatal("ticket cropping changed its existing calculation")
	}
	page := projectWorkPage{
		Project: projectWorkProject{ID: "project", Address: "施工地址"},
		Orders:  []orderDetailView{order}, Tickets: []projectTicketView{detail.Ticket},
		Total: 1, TotalCents: secondaryPriceInt(12300), Page: 1, PageSize: 20,
	}
	page.applyCustomerPricePermissions(user)
	if page.TotalCents != nil {
		t.Fatal("work page exposed aggregate charges")
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	secondaryPriceNoOrderMoney(t, data)
	if string(secondaryPriceObject(t, page)["total"]) != "1" || page.Orders[0].Lines[0].ActualLocation != "厨房" || page.Project.Address != "施工地址" {
		t.Fatal("ordinary project details were hidden")
	}
	if string(secondaryPriceObject(t, page.Orders[0])["wage_cents"]) != "500" {
		t.Fatal("wage values were removed with customer prices")
	}
}

func TestCustomerPriceSecondaryCompanyHiddenSave(t *testing.T) {
	noPrice := secondaryPriceEmployee("customers")
	for _, supplied := range []*catalogCents{nil, func() *catalogCents { v := catalogCents(1); return &v }()} {
		item := companyCraftInput{PriceCents: supplied, IncludedByCraftID: "forged-parent"}
		secondaryPriceFailure(t, protectCompanyCraftPrice(noPrice, &item, savedCompanyCraft{price: 1234}, true), 0)
		if item.PriceCents == nil || int64(*item.PriceCents) != 1234 || item.IncludedByCraftID != "" {
			t.Fatal("hidden/forged input overwrote saved price or combination", item)
		}
	}
	item := companyCraftInput{}
	secondaryPriceFailure(t, protectCompanyCraftPrice(noPrice, &item, savedCompanyCraft{}, false), 403)
	secondaryPriceFailure(t, protectCompanyCraftPrice(noPrice, &item, savedCompanyCraft{includedBy: "parent", price: 100}, true), 0)
	if item.PriceCents == nil || *item.PriceCents != 100 || item.IncludedByCraftID != "parent" {
		t.Fatal("omitted existing normal price or legacy relationship changed")
	}
	item = companyCraftInput{IncludedByCraftID: "forged-parent"}
	secondaryPriceFailure(t, protectCompanyCraftPrice(noPrice, &item, savedCompanyCraft{includedBy: "parent", price: 0}, true), 0)
	if item.PriceCents == nil || *item.PriceCents != 0 || item.IncludedByCraftID != "parent" {
		t.Fatal("existing included-price relationship changed")
	}
	authorized := secondaryPriceEmployee("customers", "customer_prices")
	value := catalogCents(5678)
	item = companyCraftInput{PriceCents: &value, IncludedByCraftID: "new-parent"}
	secondaryPriceFailure(t, protectCompanyCraftPrice(authorized, &item, savedCompanyCraft{price: 1234}, true), 0)
	if *item.PriceCents != value || item.IncludedByCraftID != "new-parent" {
		t.Fatal("authorized price rules were changed")
	}
	item = companyCraftInput{}
	secondaryPriceFailure(t, protectCompanyCraftPrice(authorized, &item, savedCompanyCraft{price: 1234}, true), 0)
	if item.PriceCents == nil || *item.PriceCents != 1234 {
		t.Fatal("omitted existing price was zeroed")
	}
}

func TestCustomerPriceSecondaryArchivePermissions(t *testing.T) {
	cases := []struct {
		name  string
		user  User
		kinds []string
	}{
		{"orders-no-price", secondaryPriceEmployee("orders"), []string{}},
		{"price-only", secondaryPriceEmployee("customer_prices"), []string{}},
		{"wages-no-price", secondaryPriceEmployee("wages"), []string{"工资明细", "工资付款", "工资调整", "工资扣款", "工资预支"}},
		{"all-business-no-price", secondaryPriceEmployee("orders", "finance", "wages"), []string{"工资明细", "工资付款", "工资调整", "工资扣款", "工资预支"}},
		{"orders-with-price", secondaryPriceEmployee("orders", "customer_prices"), []string{"施工明细", "售料明细", "施工验收表"}},
		{"wages-with-price", secondaryPriceEmployee("wages", "customer_prices"), []string{"工资明细", "施工内部留档表", "工资付款", "工资调整", "工资扣款", "工资预支"}},
		{"finance-with-price", secondaryPriceEmployee("finance", "customer_prices"), []string{"财务应收", "旧单据应收", "票据应收", "收款流水"}},
		{"admin", User{Role: "admin"}, archiveKinds},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveReadableKinds(tc.user); !reflect.DeepEqual(got, tc.kinds) {
				t.Fatalf("got %v, want %v", got, tc.kinds)
			}
			if canReadArchive(tc.user, "unknown") {
				t.Fatal("unknown archive type allowed")
			}
		})
	}
	user := secondaryPriceEmployee("orders", "finance", "wages")
	item := archiveCompanyRecord{CompanyID: "company", Files: []ArchiveFile{{Kind: "工资明细", Amount: 0, Count: 1}}}
	if err := summarizeArchiveCompany(&item, user); err != nil {
		t.Fatal(err)
	}
	fields := secondaryPriceObject(t, item)
	secondaryPriceAbsent(t, fields, "order_count", "construction_cents", "receivable_cents", "received_cents", "outstanding_cents")
	if string(fields["wage_cents"]) != "0" {
		t.Fatal("actual zero wage was hidden")
	}
	item = archiveCompanyRecord{CompanyID: "company"}
	if err := summarizeArchiveCompany(&item, user); err != nil {
		t.Fatal(err)
	}
	secondaryPriceAbsent(t, secondaryPriceObject(t, item), "construction_cents", "receivable_cents", "received_cents", "outstanding_cents")
	item = archiveCompanyRecord{Files: []ArchiveFile{{Kind: "票据应收", Amount: 100}}}
	secondaryPriceFailure(t, summarizeArchiveCompany(&item, user), 422)
}

func TestCustomerPriceSecondaryDeniedBeforeDatabase(t *testing.T) {
	app := &App{} // A nil database/transaction proves these denials occur first.
	noPrice := secondaryPriceEmployee("orders", "finance", "wages")
	priceOnly := secondaryPriceEmployee("customer_prices")
	reads := []struct {
		name, path string
		user       User
		handler    func(http.ResponseWriter, *http.Request, User) error
		code       int
	}{
		{"attachment-no-price", "/api/attachment-file?id=11111111111111111111111111111111", noPrice, app.attachmentFile, 403},
		{"attachment-no-finance", "/api/attachment-file?id=11111111111111111111111111111111", priceOnly, app.attachmentFile, 403},
		{"ticket-no-price", "/api/project-ticket?id=ticket", noPrice, app.readProjectTicket, 403},
		{"ticket-no-original-module", "/api/project-ticket?id=ticket", priceOnly, app.readProjectTicket, 403},
		{"ticket-list-no-price", "/api/project-tickets", noPrice, app.listProjectTickets, 403},
		{"finance-summary-no-price", "/api/finance-companies", noPrice, app.listFinanceCompanies, 403},
		{"order-export-no-price", "/api/export?view=orders&month=2026-10", noPrice, app.exportFiltered, 403},
		{"finance-export-no-price", "/api/export?view=finance&month=2026-10", noPrice, app.exportFiltered, 403},
		{"project-export-no-price", "/api/export?view=project&project_id=project", noPrice, app.exportFiltered, 403},
		{"archive-list-no-readable-kind", "/api/archives", secondaryPriceEmployee("orders", "finance"), app.listArchives, 403},
		{"archive-company-list-no-readable-kind", "/api/archive-companies", priceOnly, app.listArchiveCompanies, 403},
		{"archive-download-no-readable-kind", "/api/archive-company-download", priceOnly, app.downloadArchiveCompany, 403},
		{"wage-module-still-required", "/api/wage?id=account", priceOnly, app.readWage, 403},
		{"wages-without-price-reach-validation", "/api/wage", secondaryPriceEmployee("wages"), app.readWage, 400},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			secondaryPriceFailure(t, tc.handler(httptest.NewRecorder(), httptest.NewRequest("GET", tc.path, nil), tc.user), tc.code)
		})
	}
	_, err := app.financeEdit(nil, noPrice, []byte("{}"))
	secondaryPriceFailure(t, err, 403)
	_, err = app.projectTicketAction(nil, noPrice, []byte("{}"), "key")
	secondaryPriceFailure(t, err, 403)
	_, err = app.projectTicketBatch(nil, noPrice, []byte("{}"), "key")
	secondaryPriceFailure(t, err, 403)
	_, err = app.settle(nil, noPrice, []byte(`{"action":"receive"}`), "key")
	secondaryPriceFailure(t, err, 403)
	_, err = app.settleWorkerWages(nil, secondaryPriceEmployee("wages"), []byte(`{"action":"pay"}`), "key")
	secondaryPriceFailure(t, err, 409) // Retired before a database write; prices are not the wage gate.
}

func TestCustomerPriceSecondaryWageDetailsStayIndependent(t *testing.T) {
	var detail wageDetailView
	if err := json.Unmarshal([]byte(`{"wage_cents":600,"delivery_wage_cents":100,"total_cents":15000,"lines":[{"wage_cents":50,"labor_cents":500,"price_cents":3000,"total_cents":12300,"qty_micro":12000000,"segments":[{"length":3,"width":4,"mode":"金额","amount_cents":12300}]}]}`), &detail); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	secondaryPriceNoOrderMoney(t, data)
	if string(secondaryPriceObject(t, detail)["wage_cents"]) != "600" || detail.Lines[0].Labor == nil || *detail.Lines[0].Labor != 500 || detail.Lines[0].Segments[0].Length != 3 || detail.Lines[0].Segments[0].Width != 4 {
		t.Fatal("ordinary wage/measurement values changed")
	}
}
