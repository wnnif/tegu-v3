package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestOrderListTotalsIgnoreCancelledButKeepHistoryMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('order-total-company','合成金额公司')`,
		`INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES('order-total-project','ORDER-TOTAL-PROJECT','order-total-company','合成金额工地','合成地址','fixture')`,
		`INSERT INTO orders(id,number,company_id,project_id,project_name,project_number,address,work_date,note,status,total_cents,wage_cents,created_by) VALUES
		 ('order-total-valid','ORDER-TOTAL-VALID','order-total-company','order-total-project','合成金额工地','ORDER-TOTAL-PROJECT','合成地址','2026-09-10','','进行中',222000,5000,'fixture'),
		 ('order-total-cancelled','ORDER-TOTAL-CANCELLED','order-total-company','order-total-project','合成金额工地','ORDER-TOTAL-PROJECT','合成地址','2026-09-11','','已取消',100000,3000,'fixture')`,
	} {
		if _, err := a.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	read := func(path string) map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		if err := a.get(response, httptest.NewRequest("GET", path, nil), User{Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	all := read("/api/orders?view=orders&month=2026-09")
	if all["total"] != float64(2) || all["total_cents"] != float64(222000) || all["wage_cents"] != float64(5000) {
		t.Fatalf("all-status list should retain two rows but total effective charges and wages only: %#v", all)
	}

	cancelled := read("/api/orders?view=orders&month=2026-09&status=%E5%B7%B2%E5%8F%96%E6%B6%88")
	rows, ok := cancelled["rows"].([]any)
	if !ok || cancelled["total"] != float64(1) || cancelled["total_cents"] != float64(0) || cancelled["wage_cents"] != float64(0) || len(rows) != 1 {
		t.Fatalf("cancelled list should remain visible but not contribute to effective charge or wage totals: %#v", cancelled)
	}
	row, ok := rows[0].(map[string]any)
	if !ok || row["id"] != "order-total-cancelled" || row["total_cents"] != float64(100000) || row["wage_cents"] != float64(3000) {
		t.Fatalf("cancelled row's historical charge and wage must remain visible: %#v", rows)
	}
}
