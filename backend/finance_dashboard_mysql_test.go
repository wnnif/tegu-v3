package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func financeDashboardExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func financeDashboardTestRead(t *testing.T, a *App, user User, query string) financeDashboardResponse {
	t.Helper()
	w := httptest.NewRecorder()
	if err := a.financeDashboard(w, httptest.NewRequest("GET", "/api/finance-summary?"+query, nil), user); err != nil {
		t.Fatal(err)
	}
	var result financeDashboardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func financeDashboardTestMetric(t *testing.T, result financeDashboardResponse, key, expected string) {
	t.Helper()
	for _, group := range result.Groups {
		for _, m := range group.Metrics {
			if m.Key == key {
				if m.Cents != expected {
					t.Fatalf("metric %s=%s, want %s; %#v", key, m.Cents, expected, result)
				}
				return
			}
		}
	}
	t.Fatalf("metric %s missing", key)
}

func TestFinanceDashboardCustomerMonthsAllocationsAndDiscountMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "财务阶段七合成跨月工地", 100000)
	financeDashboardExec(t, f.a.db, "UPDATE orders SET work_date='2026-09-15' WHERE id=?", o.ID)
	companyReceiptTestDetail(t, f.request(t, f.admin, "POST", "/api/company-receipt", "", companyReceiptRequest{CompanyID: "loc-co", Amount: 40000, ReceivedAt: "2026-10-02", Method: "合成转账"}, 200))
	september := financeDashboardTestRead(t, f.a, f.u, "from=2026-09-01&to=2026-09-30")
	financeDashboardTestMetric(t, september, "construction", "100000")
	financeDashboardTestMetric(t, september, "received", "0")
	financeDashboardTestMetric(t, september, "outstanding", "100000")
	october := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, october, "construction", "0")
	financeDashboardTestMetric(t, october, "received", "40000")
	financeDashboardTestMetric(t, october, "outstanding", "60000")
	// Overpayment remains company cash; allocation and customer discount cannot
	// create a second receipt or cancel a different company's actual debt.
	second := companyReceiptTestDetail(t, f.request(t, f.admin, "POST", "/api/company-receipt", "", companyReceiptRequest{CompanyID: "loc-co", Amount: 110000, ReceivedAt: "2026-10-03", Method: "合成转账"}, 200))
	ticket := companyReceiptTestTicket(t, f, o.ProjectID)
	second = companyReceiptTestAllocate(t, f, second, receiptAllocationInput{TicketID: ticket, Amount: 70000})
	financeDashboardExec(t, f.a.db, "UPDATE project_ticket_receipts SET created_at='2026-10-04 01:00:00' WHERE receipt_id=?", second.Receipt.ID)
	financeDashboardExec(t, f.a.db, `INSERT INTO customer_discounts(id,company_id,ticket_id,batch_id,amount_cents,reason,created_by,created_by_name,created_at) VALUES('finance-discount','loc-co',?,'',10000,'合成减免','test','合成','2026-10-05 01:00:00')`, ticket)
	maintenanceFaultExec(t, f.a.db, "INSERT INTO companies(id,name,ticket_mode) VALUES('finance-other','合成独立欠款公司','single')")
	maintenanceFaultExec(t, f.a.db, `INSERT INTO orders(id,number,company_id,address,work_date,note,total_cents,status,created_by) VALUES('finance-other-order','finance-other-number','finance-other','合成其他欠款地址','2026-10-01','',30000,'进行中','test')`)
	financeOnly := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	before := orderLocationsState(t, f.a.db)
	r := financeDashboardTestRead(t, f.a, financeOnly, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, r, "received", "150000")
	financeDashboardTestMetric(t, r, "unallocated", "80000")
	financeDashboardTestMetric(t, r, "discount", "10000")
	financeDashboardTestMetric(t, r, "outstanding", "30000")
	financeDashboardTestMetric(t, r, "customer_advance", "60000")
	w := httptest.NewRecorder()
	if err := f.a.financeDashboard(w, httptest.NewRequest("GET", "/api/finance-summary?from=2026-10-01&to=2026-10-31&export=1", nil), financeOnly); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "1500.00,150000") || strings.Contains(w.Body.String(), "工资") || strings.Contains(w.Body.String(), "库存") || strings.Contains(w.Body.String(), "报销") {
		t.Fatalf("cash CSV differs or leaks permission: %s", w.Body.String())
	}
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "orders", "order_lines", "order_worker_wages", "stock_balances", "company_receipts", "project_ticket_receipts", "customer_discounts")
}

func TestFinanceDashboardPayrollExpenseAndInventoryMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	o := companyReceiptTestOrder(t, f, "财务阶段七合成工资工地", 100000)
	financeDashboardExec(t, f.a.db, "UPDATE orders SET wage_cents=100000 WHERE id=?", o.ID)
	financeDashboardExec(t, f.a.db, "UPDATE order_worker_wages SET wage_cents=100000 WHERE order_id=?", o.ID)
	financeDashboardExec(t, f.a.db, "UPDATE order_lines SET labor_cents=100000,wage_cents=100000 WHERE order_id=?", o.ID)
	for _, query := range []string{
		`INSERT INTO payroll_deductions(id,worker_id,worker_name,amount_cents,deduction_date,reason,status,actor_id) VALUES('fd','loc-a','合成甲师傅',5000,'2026-10-12','合成确认扣款','confirmed','test')`,
		`INSERT INTO payroll_advances(id,worker_id,worker_name,amount_cents,advance_date,actor_id) VALUES('fa','loc-a','合成甲师傅',20000,'2026-10-12','test')`,
		`INSERT INTO payroll_settlements(id,worker_id,worker_name,settlement_date,gross_cents,adjustment_cents,deduction_cents,advance_cents,payable_cents,actor_id,operation_id) VALUES('fs','loc-a','合成甲师傅','2026-10-12',100000,0,5000,20000,75000,'test','finance-payroll')`,
		`INSERT INTO payroll_settlement_sources(settlement_id,source_type,source_id,worker_id,project_id,amount_cents) VALUES('fs','deduction','fd','loc-a','',5000),('fs','advance','fa','loc-a','',20000)`,
		`INSERT INTO payroll_payments(id,settlement_id,amount_cents,paid_date,actor_id,operation_id) VALUES('fp','fs',50000,'2026-10-12','test','finance-pay')`,
		`INSERT INTO stock_documents(id,kind,work_date,note,created_by,total_cents,line_items) VALUES('finance-purchase','进货','2026-10-12','合成进货','test',20000,JSON_ARRAY())`,
		`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,purchase_document_id,purchase_number,purchase_date,purchase_total_cents,status,actor_id,operation_id) VALUES('fc','合成员工','2026-10-12','材料采购',20000,'finance-purchase','finance-purchase','2026-10-12',20000,'confirmed','test','finance-claim')`,
		`INSERT INTO expense_payments(id,claim_id,amount_cents,paid_date,actor_id,operation_id) VALUES('fecp','fc',20000,'2026-10-12','test','finance-expense-pay')`,
		`INSERT INTO expense_advances(id,person_name,advance_date,amount_cents,purpose,actor_id,operation_id) VALUES('fea','合成采购人','2026-10-12',100000,'合成采购备用金','test','finance-advance')`,
		`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,status,actor_id,operation_id) VALUES('fc2','合成采购人','2026-10-12','材料采购',120000,'confirmed','test','finance-claim2')`,
		`INSERT INTO expense_advance_allocations(id,claim_id,advance_id,amount_cents,applied_date,actor_id,operation_id) VALUES('fea2','fc2','fea',80000,'2026-10-12','test','finance-allocate')`,
		`INSERT INTO expense_advance_returns(id,advance_id,amount_cents,returned_date,actor_id,operation_id) VALUES('fer','fea',10000,'2026-10-12','test','finance-return')`,
		`INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor) VALUES('loc-product','桶','',1,1) ON DUPLICATE KEY UPDATE base_factor=1,aux_factor=1`,
		`INSERT INTO product_purchase_costs(product_id,purchased_quantum,purchase_cents) VALUES('loc-product',20000000,20000) ON DUPLICATE KEY UPDATE purchased_quantum=20000000,purchase_cents=20000`,
		`UPDATE stock_balances SET qty_quantum=20000000,qty_micro=20000000 WHERE warehouse_id='loc-wh' AND product_id='loc-product'`,
	} {
		maintenanceFaultExec(t, f.a.db, query)
	}
	r := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	for key, want := range map[string]string{"wage_balance": "100000", "wage_deduction": "5000", "wage_advance": "20000", "wage_paid": "70000", "wage_final_paid": "50000", "wage_unpaid": "25000", "wage_advance_balance": "0", "purchase": "20000", "inventory_value": "20000", "expense_registered": "140000", "expense_paid": "20000", "expense_unpaid": "40000", "expense_advance_balance": "10000", "expense_cash": "110000"} {
		financeDashboardTestMetric(t, r, key, want)
	}
	maintenanceFaultExec(t, f.a.db, "INSERT INTO warehouses(id,name) VALUES('finance-main','合成总仓')")
	maintenanceFaultExec(t, f.a.db, "UPDATE stock_balances SET qty_quantum=5000000,qty_micro=5000000 WHERE warehouse_id='loc-wh' AND product_id='loc-product'")
	maintenanceFaultExec(t, f.a.db, "INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum) VALUES('finance-main','loc-product',15000000,15000000)")
	afterTransfer := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, afterTransfer, "inventory_value", "20000")
	financeDashboardTestMetric(t, afterTransfer, "purchase", "20000")
	// A future dated expense that was paid early must not make an unrelated
	// existing expense balance disappear or become negative at this cutoff.
	maintenanceFaultExec(t, f.a.db, `INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,status,actor_id,operation_id) VALUES('future-claim','合成未来员工','2026-11-01','其他',100000,'confirmed','test','future-claim')`)
	maintenanceFaultExec(t, f.a.db, `INSERT INTO expense_payments(id,claim_id,amount_cents,paid_date,actor_id,operation_id) VALUES('future-paid','future-claim',100000,'2026-10-12','test','future-paid')`)
	cutoff := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, cutoff, "expense_unpaid", "40000")
	financeDashboardTestMetric(t, cutoff, "expense_paid", "120000")
}

func TestFinanceDashboardPermissionGateMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	for _, permissions := range [][]string{{"customer_prices", "wages", "expenses", "warehouse"}, {"orders"}, nil} {
		err := f.a.financeDashboard(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/finance-summary?all=1", nil), User{Role: "employee", Permissions: permissions})
		if payrollMySQLFailureCode(err) != 403 {
			t.Fatalf("permission %v got %v", permissions, err)
		}
	}
	r := financeDashboardTestRead(t, f.a, User{Role: "employee", Permissions: []string{"finance"}}, "all=1")
	if len(r.Groups) != 0 {
		t.Fatalf("finance-only leaked groups: %#v", r.Groups)
	}
	r = financeDashboardTestRead(t, f.a, User{Role: "employee", Permissions: []string{"finance", "wages"}}, "all=1")
	if len(r.Groups) != 1 || r.Groups[0].Key != "payroll" {
		t.Fatalf("payroll-only projection: %#v", r.Groups)
	}
}

func TestFinanceDashboardDeliveryAndSaleSourceOnceMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	saved := f.must(t, Order{Company: "loc-co", Address: "财务阶段七合成施工售料", Date: "2026-10-12", Worker: "loc-a", Lines: []Line{{Temporary: true, Category: "开工", Name: "合成正常收费", Mode: "金额", Price: 100000, Wage: 1000, Worker: "loc-a"}, {Temporary: true, Category: "开工", Name: "合成免费施工", Mode: "金额", Price: 9000, Wage: 500, Worker: "loc-a", ChargeMode: "free"}}, Sales: []Sale{{Product: "loc-product", InputQty: "1", InputUnit: "base", Price: 20000}}})
	o := orderLocationsRead(t, f.a, saved["id"].(string), f.u)
	before := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, before, "construction", "100000")
	financeDashboardTestMetric(t, before, "sales", "20000")
	financeDashboardTestMetric(t, before, "delivered", "0")
	financeDashboardTestMetric(t, before, "undelivered", "120000")
	batch := ticketIssueMySQLCreate(t, f, "finance-source-once", ticketIssueMySQLSelection(t, f, o.Lines[0].ID, o.Lines[1].ID, o.Sales[0].ID))
	after := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, after, "construction", "100000")
	financeDashboardTestMetric(t, after, "sales", "20000")
	financeDashboardTestMetric(t, after, "charge_balance", "120000")
	financeDashboardTestMetric(t, after, "delivered", "120000")
	financeDashboardTestMetric(t, after, "undelivered", "0")
	// Moving a construction date forward cannot make a backdated delivery
	// exceed the construction charge that exists by the requested cutoff.
	financeDashboardExec(t, f.a.db, "UPDATE orders SET work_date='2026-11-01' WHERE id=?", o.ID)
	prior := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, prior, "charge_balance", "0")
	financeDashboardTestMetric(t, prior, "delivered", "0")
	financeDashboardExec(t, f.a.db, "UPDATE orders SET work_date='2026-10-12' WHERE id=?", o.ID)
	// A historical original delivery with the same active batch still counts
	// each actual line only once, then remains readable without the new batch.
	financeDashboardExec(t, f.a.db, "UPDATE project_tickets SET delivered_at='2026-10-13',status='已交单' WHERE project_id=?", o.ProjectID)
	legacyAndBatch := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, legacyAndBatch, "delivered", "120000")
	financeDashboardExec(t, f.a.db, "UPDATE order_lines SET ticket_batch_id=NULL WHERE order_id=?", o.ID)
	financeDashboardExec(t, f.a.db, "UPDATE order_sales SET ticket_batch_id=NULL WHERE order_id=?", o.ID)
	_ = batch
	legacy := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, legacy, "delivered", "120000")
	financeDashboardTestMetric(t, legacy, "undelivered", "0")
}

func TestFinanceDashboardLegacyWholeOrderDeliveryFallbackMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	// Legacy unmanaged records can predate line/sale snapshots. Their stored
	// order total remains the only saved charge source, and an issued status is
	// the delivery fact when the old delivered_at column was never populated.
	maintenanceFaultExec(t, f.a.db, `INSERT INTO orders(id,number,company_id,company_name,address,work_date,status,total_cents,wage_cents,note,created_by,ticket_managed)
 VALUES('finance-legacy-empty','finance-legacy-empty','loc-co','合成历史公司','合成历史无明细工地','2026-10-05','已交单',50000,0,'','test',0)`)
	saved := f.must(t, Order{Company: "loc-co", Address: "财务阶段七合成有明细旧票", Date: "2026-10-06", Worker: "loc-a", Lines: []Line{{Temporary: true, Category: "开工", Name: "合成旧票明细", Mode: "金额", Price: 70000, Wage: 1000, Worker: "loc-a"}}, Sales: []Sale{{Product: "loc-product", InputQty: "1", InputUnit: "base", Price: 20000}}})
	lineOrder := saved["id"].(string)
	financeDashboardExec(t, f.a.db, "UPDATE orders SET ticket_managed=0,status='已交单',delivered_at=NULL WHERE id=?", lineOrder)
	newOrder := companyReceiptTestOrder(t, f, "财务阶段七混合新交票", 30000)
	ticketIssueMySQLCreate(t, f, "finance-mixed-old-new", ticketIssueMySQLSelection(t, f, newOrder.Lines[0].ID))
	r := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, r, "construction", "150000")
	financeDashboardTestMetric(t, r, "sales", "20000")
	financeDashboardTestMetric(t, r, "delivered", "170000")
	financeDashboardTestMetric(t, r, "undelivered", "0")
	// Giving the empty legacy row a delivered_at date must not add it again.
	maintenanceFaultExec(t, f.a.db, "UPDATE orders SET delivered_at='2026-10-10' WHERE id='finance-legacy-empty'")
	r = financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-31")
	financeDashboardTestMetric(t, r, "delivered", "170000")
	financeDashboardTestMetric(t, r, "undelivered", "0")
	beforeDelivery := financeDashboardTestRead(t, f.a, f.u, "from=2026-10-01&to=2026-10-09")
	financeDashboardTestMetric(t, beforeDelivery, "charge_balance", "140000")
	financeDashboardTestMetric(t, beforeDelivery, "delivered", "90000")
	financeDashboardTestMetric(t, beforeDelivery, "undelivered", "50000")
}

// Ledger corrections permit civil date backfills. A settlement/allocation dated
// before a future advance must not reduce another person's already-issued cash
// at an earlier cutoff; each advance carries only its own effective children.
func TestFinanceDashboardAdvanceBalancesRespectIssuedDateMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	for _, query := range []string{
		`INSERT INTO payroll_advances(id,worker_id,worker_name,amount_cents,advance_date,actor_id) VALUES
 ('advance-cutoff-wage-issued','loc-a','合成已发预支师傅',100000,'2026-10-01','test'),
 ('advance-cutoff-wage-future','loc-b','合成未来预支师傅',200000,'2026-11-01','test'),
 ('advance-cutoff-wage-other','loc-other','合成其他师傅',50000,'2026-10-01','test')`,
		`INSERT INTO payroll_settlements(id,worker_id,worker_name,settlement_date,gross_cents,adjustment_cents,deduction_cents,advance_cents,payable_cents,actor_id,operation_id) VALUES
 ('advance-cutoff-wage-s1','loc-a','合成已发预支师傅','2026-10-15',40000,0,0,40000,0,'test','advance-cutoff-wage-s1'),
 ('advance-cutoff-wage-s2','loc-b','合成未来预支师傅','2026-10-15',150000,0,0,150000,0,'test','advance-cutoff-wage-s2'),
 ('advance-cutoff-wage-s3','loc-b','合成未来预支师傅','2026-11-15',50000,0,0,50000,0,'test','advance-cutoff-wage-s3')`,
		`INSERT INTO payroll_settlement_sources(settlement_id,source_type,source_id,worker_id,project_id,amount_cents) VALUES
 ('advance-cutoff-wage-s1','advance','advance-cutoff-wage-issued','loc-a','',40000),
 ('advance-cutoff-wage-s2','advance','advance-cutoff-wage-future','loc-b','',150000),
 ('advance-cutoff-wage-s3','advance','advance-cutoff-wage-future','loc-b','',50000)`,
		`INSERT INTO expense_advances(id,person_name,advance_date,amount_cents,purpose,actor_id,operation_id) VALUES
 ('advance-cutoff-expense-issued','合成已发采购人','2026-10-01',100000,'合成已发备用金','test','advance-cutoff-expense-issued'),
 ('advance-cutoff-expense-future','合成未来采购人','2026-11-01',200000,'合成未来备用金','test','advance-cutoff-expense-future'),
 ('advance-cutoff-expense-other','合成其他采购人','2026-10-01',50000,'合成其他人备用金','test','advance-cutoff-expense-other')`,
		`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,status,actor_id,operation_id) VALUES
 ('advance-cutoff-expense-claim1','合成已发采购人','2026-10-10','其他',40000,'confirmed','test','advance-cutoff-expense-claim1'),
 ('advance-cutoff-expense-claim2','合成未来采购人','2026-10-10','其他',150000,'confirmed','test','advance-cutoff-expense-claim2')`,
		`INSERT INTO expense_advance_allocations(id,claim_id,advance_id,amount_cents,applied_date,actor_id,operation_id) VALUES
 ('advance-cutoff-expense-apply1','advance-cutoff-expense-claim1','advance-cutoff-expense-issued',40000,'2026-10-15','test','advance-cutoff-expense-apply1'),
 ('advance-cutoff-expense-apply2','advance-cutoff-expense-claim2','advance-cutoff-expense-future',150000,'2026-10-15','test','advance-cutoff-expense-apply2')`,
		`INSERT INTO expense_advance_returns(id,advance_id,amount_cents,returned_date,actor_id,operation_id) VALUES
 ('advance-cutoff-expense-return-issued','advance-cutoff-expense-issued',10000,'2026-10-20','test','advance-cutoff-expense-return-issued'),
 ('advance-cutoff-expense-return-future','advance-cutoff-expense-future',50000,'2026-11-15','test','advance-cutoff-expense-return-future')`,
	} {
		maintenanceFaultExec(t, f.a.db, query)
	}
	assert := func(to, wage, expense string) {
		t.Helper()
		tx, err := f.a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, tc := range []struct {
			payroll bool
			want    string
		}{{true, wage}, {false, expense}} {
			got, err := financeDashboardAdvanceBalance(tx, financeDashboardRange{To: to}, tc.payroll)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("advance balance as of %s payroll=%t got=%s want=%s", to, tc.payroll, got, tc.want)
			}
		}
	}
	// Before any application, both October-issued people have their full money.
	assert("2026-10-14", "150000", "150000")
	// The future person's backdated application cannot consume the third person's
	// independent 500-yuan balance. No future advance belongs to this cutoff.
	assert("2026-10-31", "110000", "100000")
	// Once the future advance has actually been issued, only its own remaining
	// 500 yuan enters the total; the later November settlement/return is excluded.
	assert("2026-11-14", "160000", "150000")
	assert("2026-11-30", "110000", "100000")
}
