package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestWorkerAccountIntegrityIndexedPlanMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, "INSERT INTO companies(id,name) VALUES('plan-company','合成索引计划公司')")
	for i := 0; i < 32; i++ {
		key := fmt.Sprintf("plan-%03d", i)
		if _, err := a.db.Exec(`INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,created_by,wage_cents) VALUES(?,?,'plan-company','合成地址','2026-10-01','plan-worker','合成人员','','fixture',100)`, key, key); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,total_cents,wage_cents,labor_cents,temporary,worker_id,worker_name) VALUES(?,?,'开工','合成工资行','金额',JSON_ARRAY(),1000000,0,0,100,100,1,'plan-worker','合成人员')`, key, key); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents) VALUES(?,?,'plan-worker','合成人员',100)`, key, key); err != nil {
			t.Fatal(err)
		}
	}
	// The actual optimizer plan is the regression: every per-order line/account
	// lookup must use the existing leading order_id index, without a table scan.
	var raw string
	if err := a.db.QueryRow("EXPLAIN FORMAT=JSON SELECT COUNT(*) FROM orders o WHERE (1=1) AND " + invalidWorkerAccountsSQL).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	lineLookups, accountLookups := 0, 0
	var inspect func(any)
	inspect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if table, ok := v["table_name"].(string); ok && (table == "l" || table == "w") {
				if table == "l" {
					lineLookups++
				} else {
					accountLookups++
				}
				if v["key"] != "order_id" || v["access_type"] == "ALL" {
					t.Errorf("per-order integrity lookup can scan entire %s table: %v", table, v)
				}
			}
			for _, child := range v {
				inspect(child)
			}
		case []any:
			for _, child := range v {
				inspect(child)
			}
		}
	}
	inspect(plan)
	if lineLookups < 5 || accountLookups < 5 {
		t.Fatal("not enough integrity subqueries inspected", lineLookups, accountLookups, raw)
	}
	check := func(code int) {
		t.Helper()
		tx, err := a.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		err = validateWorkerAccounts(tx)
		if code == 0 {
			if err != nil {
				t.Fatal("valid accounts rejected", err)
			}
		} else {
			maintenanceFaultError(t, err, code)
		}
	}
	check(0)
	maintenanceFaultExec(t, a.db, "UPDATE order_lines SET worker_id='missing-person' WHERE id='plan-000'")
	check(422)
	maintenanceFaultExec(t, a.db, "UPDATE order_lines SET worker_id='plan-worker' WHERE id='plan-000'")
	maintenanceFaultExec(t, a.db, "UPDATE order_worker_wages SET wage_cents=999 WHERE id='plan-000'")
	check(422)
	maintenanceFaultExec(t, a.db, "UPDATE order_worker_wages SET wage_cents=100,wage_paid=1 WHERE id='plan-000'")
	check(422)
	// Legacy whole-record amounts deliberately may differ from historical rows.
	maintenanceFaultExec(t, a.db, "UPDATE order_worker_wages SET wage_cents=987,wage_paid=0,legacy=1 WHERE id='plan-000'")
	maintenanceFaultExec(t, a.db, "UPDATE orders SET wage_cents=987 WHERE id='plan-000'")
	check(0)
	t.Logf("actual plan uses order_id indexes for %d line/%d account lookups; missing allocation, mismatched amounts/paid flags rejected and exact legacy amount retained", lineLookups, accountLookups)
}
