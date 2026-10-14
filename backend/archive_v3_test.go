package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV3ArchivePermissions(t *testing.T) {
	for _, test := range []struct {
		permission string
		allowed    []string
	}{
		{"wages", []string{"工资明细", "工资付款", "工资调整", "工资扣款", "工资预支"}},
		{"expenses", []string{"报销登记", "报销付款", "报账预支", "报账核销", "报账退回"}},
		{"warehouse", []string{"进货金额"}},
	} {
		u := User{Role: "employee", Permissions: []string{test.permission}}
		for _, kind := range archiveKinds {
			if got, want := canReadArchive(u, kind), contains(test.allowed, kind); got != want {
				t.Fatalf("%s permission for %s: got %v want %v", test.permission, kind, got, want)
			}
		}
		if canReadArchive(u, "unknown") {
			t.Fatal("unknown archive kind exposed")
		}
	}
	item := archiveCompanyRecord{CompanyID: archiveSystemCompanyID, Files: []ArchiveFile{{Kind: "报销登记", Amount: 20000}}}
	if err := summarizeArchiveCompany(&item, User{Role: "admin"}); err != nil || item.Construction != nil || item.Wage != nil || item.Receivable != nil {
		t.Fatalf("system costs were invented as company totals: %+v %v", item, err)
	}
}

func seedV3ArchiveFacts(t *testing.T, a *App) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('v3-archive-co','合成V3公司')`,
		`INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES('v3-archive-site','V3-SITE','v3-archive-co','合成工地','合成地址','fixture')`,
		`INSERT INTO orders(id,number,company_id,company_name,project_id,address,work_date,note,created_by,total_cents,wage_cents,worker_id,worker_name,ticket_managed) VALUES('v3-archive-order','V3-ORDER','v3-archive-co','合成V3公司','v3-archive-site','合成地址','2026-09-20','','fixture',100000,100000,'v3-worker','合成工人',1)`,
		`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,total_cents,wage_cents,labor_cents,temporary,worker_id,worker_name) VALUES('v3-archive-line','v3-archive-order','维修','合成施工','金额',JSON_ARRAY(),1000000,100000,100000,100000,100000,1,'v3-worker','合成工人')`,
		`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents) VALUES('v3-archive-wage','v3-archive-order','v3-worker','合成工人',100000)`,
		`INSERT INTO payroll_settlements(id,worker_id,worker_name,settlement_date,gross_cents,adjustment_cents,deduction_cents,advance_cents,payable_cents,actor_id,operation_id) VALUES('v3-archive-settle','v3-worker','合成工人','2026-10-05',100000,0,5000,20000,75000,'fixture','v3-archive-settle')`,
		`INSERT INTO payroll_payments(id,settlement_id,amount_cents,paid_date,actor_id,operation_id,status) VALUES('v3-archive-pay','v3-archive-settle',50000,'2026-10-05','fixture','v3-archive-pay','active'),('v3-archive-pay-reversed','v3-archive-settle',90000,'2026-10-06','fixture','v3-archive-pay-reversed','reversed')`,
		`INSERT INTO payroll_adjustments(id,worker_id,worker_name,amount_cents,effective_date,reason,actor_id) VALUES('v3-archive-adjust','v3-worker','合成工人',-1000,'2026-10-02','合成负数调整','fixture')`,
		`INSERT INTO payroll_deductions(id,worker_id,worker_name,amount_cents,deduction_date,reason,status,actor_id) VALUES('v3-archive-deduct','v3-worker','合成工人',5000,'2026-09-25','合成确认扣款','confirmed','fixture')`,
		`INSERT INTO payroll_advances(id,worker_id,worker_name,amount_cents,advance_date,actor_id) VALUES('v3-archive-advance','v3-worker','合成工人',20000,'2026-09-26','fixture')`,
		`INSERT INTO payroll_settlement_sources(settlement_id,source_type,source_id,worker_id,project_id,amount_cents) VALUES('v3-archive-settle','wage','v3-archive-wage','v3-worker','v3-archive-site',100000),('v3-archive-settle','deduction','v3-archive-deduct','v3-worker','',5000),('v3-archive-settle','advance','v3-archive-advance','v3-worker','',20000)`,
		`INSERT INTO stock_documents(id,kind,work_date,note,created_by,total_cents) VALUES('v3-archive-purchase','进货','2026-09-21','','fixture',20000)`,
		`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,status,purchase_document_id,purchase_number,actor_id,operation_id) VALUES('v3-archive-expense','合成报销人','2026-09-21','材料采购',20000,'confirmed','v3-archive-purchase','V3-PURCHASE','fixture','v3-archive-expense')`,
		`INSERT INTO expense_advances(id,person_name,advance_date,amount_cents,purpose,actor_id,operation_id) VALUES('v3-archive-expense-advance','合成报销人','2026-09-19',15000,'合成报账预支','fixture','v3-archive-expense-advance')`,
		`INSERT INTO expense_advance_allocations(id,claim_id,advance_id,amount_cents,applied_date,actor_id,operation_id) VALUES('v3-archive-apply','v3-archive-expense','v3-archive-expense-advance',10000,'2026-10-01','fixture','v3-archive-apply')`,
		`INSERT INTO expense_payments(id,claim_id,amount_cents,paid_date,actor_id,operation_id) VALUES('v3-archive-expense-pay','v3-archive-expense',10000,'2026-10-02','fixture','v3-archive-expense-pay')`,
		`INSERT INTO expense_advance_returns(id,advance_id,amount_cents,returned_date,actor_id,operation_id) VALUES('v3-archive-return','v3-archive-expense-advance',5000,'2026-11-01','fixture','v3-archive-return')`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
}

func TestV3MonthlyArchiveDatesAndPermissionsMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	seedV3ArchiveFacts(t, a)
	// The first partial payment marks its linked wage source as protected.
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = markPayrollWageSourcesPaid(tx, "v3-archive-settle"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultCount(t, a.db, `SELECT COUNT(*) FROM order_worker_wages w JOIN orders o ON o.id=w.order_id
	 JOIN payroll_settlement_sources s ON s.source_type='wage' AND s.source_id=w.id
	 WHERE w.id='v3-archive-wage' AND w.wage_paid=1 AND o.wage_paid=1 AND s.amount_cents=w.wage_cents`, 1)
	maintenanceFaultCount(t, a.db, `SELECT s.payable_cents-COALESCE(SUM(p.amount_cents),0)
	 FROM payroll_settlements s LEFT JOIN payroll_payments p ON p.settlement_id=s.id AND p.status='active'
	 WHERE s.id='v3-archive-settle' GROUP BY s.id,s.payable_cents`, 25000)
	backupDir := t.TempDir()
	t.Setenv("BACKUP_DIR", backupDir)
	for _, month := range []string{"2026-09", "2026-10"} {
		files, err := a.archive(month, "", false, User{Role: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		amounts, counts := map[string]int64{}, map[string]int{}
		for _, file := range files {
			amounts[file.Kind] += file.Amount
			counts[file.Kind] += file.Count
			if file.Kind == "报销登记" || file.Kind == "工资付款" || file.Kind == "工资调整" || file.Kind == "工资明细" {
				if file.Kind != "工资明细" && file.CompanyID != archiveSystemCompanyID {
					t.Fatal("cash/expense was guessed into a customer company", file)
				}
				contents, err := os.ReadFile(filepath.Join(backupDir, "monthly", file.File))
				if err != nil {
					t.Fatal(err)
				}
				records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(contents), "\ufeff"))).ReadAll()
				if err != nil || len(records) != file.Count+2 {
					t.Fatal("CSV incomplete", file.Kind, err)
				}
				if file.Kind == "工资付款" && records[len(records)-1][6] != "500.00" {
					t.Fatal("reversed wage payment included", records)
				}
				if file.Kind == "工资明细" && (records[1][6] != "1000.00" || !strings.Contains(records[1][7], "已有付款记录") || strings.Contains(records[1][7], "全付")) {
					t.Fatal("partial payment was printed as fully paid or changed earned wages", records)
				}
			}
		}
		if month == "2026-09" {
			for kind, want := range map[string]int64{"施工明细": 100000, "工资明细": 100000, "工资扣款": 5000, "工资预支": 20000, "进货金额": 20000, "报销登记": 20000, "报账预支": 15000} {
				if amounts[kind] != want {
					t.Fatalf("September %s=%d want %d", kind, amounts[kind], want)
				}
			}
			if counts["工资付款"]+counts["报销付款"]+counts["报账核销"] != 0 {
				t.Fatal("October cash/allocation leaked into expense/work month", amounts)
			}
		} else {
			for kind, want := range map[string]int64{"工资付款": 50000, "工资调整": -1000, "报销付款": 10000, "报账核销": 10000} {
				if amounts[kind] != want {
					t.Fatalf("October %s=%d want %d", kind, amounts[kind], want)
				}
			}
			if counts["施工明细"]+counts["工资明细"]+counts["报销登记"]+counts["进货金额"]+counts["报账退回"] != 0 {
				t.Fatal("business date shifted to payment or future month", amounts)
			}
			var expenseFile ArchiveFile
			for _, file := range files {
				if file.Kind == "报销付款" {
					expenseFile = file
				}
			}
			denied := User{ID: "wages-only", Role: "employee", Permissions: []string{"wages"}}
			w := httptest.NewRecorder()
			maintenanceFaultError(t, a.listArchives(w, httptest.NewRequest("GET", "/api/archives?file="+expenseFile.File, nil), denied), 403)
			w = httptest.NewRecorder()
			u := User{ID: "expense-only", Role: "employee", Permissions: []string{"expenses"}}
			if err := a.listArchiveCompanies(w, httptest.NewRequest("GET", "/api/archive-companies?month="+month, nil), u); err != nil {
				t.Fatal(err)
			}
			var page archiveCompanyResponse
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 1 || len(page.Rows) != 1 || page.Rows[0].CompanyID != archiveSystemCompanyID || page.Rows[0].Legacy {
				t.Fatal("system archive not reachable as latest group", w.Body.String(), err)
			}
			for _, file := range page.Rows[0].Files {
				if archivePermission(file.Kind) != "expenses" {
					t.Fatal("expense list exposed another module", file.Kind)
				}
			}
			w = httptest.NewRecorder()
			if err := a.downloadArchiveCompany(w, httptest.NewRequest("GET", "/api/archive-company-download?run_id="+page.Rows[0].RunID+"&key="+page.Rows[0].Key, nil), u); err != nil || w.Code != 200 {
				t.Fatal("system expenses ZIP inaccessible", err)
			}
		}
	}
	files, err := a.archive("2026-09", "v3-archive-co", false, User{Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.CompanyID == archiveSystemCompanyID {
			t.Fatal("company archive silently included unrelated global expense")
		}
	}
	months, err := a.pendingArchiveMonths(time.Date(2026, 11, 1, 0, 0, 0, 0, businessLocation))
	if err != nil || !contains(months, "2026-10") || contains(months, "2026-11") {
		t.Fatal("independent payroll/expense payment month missing or future included", months, err)
	}
	t.Log("work/wages, expense and purchase dates stay September; October wage cash=500, expense cash=100 and allocation=100 separately; reversed payments excluded; independent dates scheduled; module list/file/ZIP permissions enforced")
}
