package main

import (
	"encoding/csv"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpenseExportAllPagesAndPermissionMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	for i := 0; i < 105; i++ {
		_, err := a.db.Exec(`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,note,status,actor_id,operation_id) VALUES(?,?,'2026-10-12','其他',12345,'合成导出','confirmed','test',?)`, fmt.Sprintf("export-%03d", i), "=合成导出", fmt.Sprintf("export-%03d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tab := range []string{"claims", "advances", "history"} {
		err := a.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/expenses?tab="+tab+"&export=1", nil), User{Role: "employee", Permissions: []string{"finance"}})
		maintenanceFaultError(t, err, 403)
	}
	w := httptest.NewRecorder()
	if err := a.get(w, httptest.NewRequest("GET", "/api/expenses?tab=claims&page=2&from=2026-10-01&to=2026-10-31&export=1", nil), User{Role: "employee", Permissions: []string{"expenses"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 106 {
		t.Fatalf("export lost rows: count=%d err=%v", len(rows), err)
	}
	if rows[1][1] != "'=合成导出" || rows[1][4] != "123.45" || rows[1][6] != "0.00" || rows[1][7] != "123.45" {
		t.Fatal("export amount or formula escaping", rows[1])
	}
	w = httptest.NewRecorder()
	if err := a.get(w, httptest.NewRequest("GET", "/api/expenses?tab=claims&from=2026-11-01&export=1", nil), User{Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	rows, err = csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 1 {
		t.Fatal("export ignored date filter", len(rows), err)
	}
	t.Log("105 expense rows exported beyond page 2; applied date filter, exact cents, formula guard and independent expense permission verified")
}
