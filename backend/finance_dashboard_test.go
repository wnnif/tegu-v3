package main

import (
	"encoding/csv"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFinanceDashboardDatesDefaultAndAll(t *testing.T) {
	now := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	r, err := financeDashboardDates(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.From != "2026-10-01" || r.To != "2026-10-12" || r.All {
		t.Fatalf("default range = %#v", r)
	}
	r, err = financeDashboardDates(url.Values{"all": {"1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.From != "1000-01-01" || r.To != "2026-10-12" || !r.All {
		t.Fatalf("all range = %#v", r)
	}
	if _, err = financeDashboardDates(url.Values{"from": {"2026-11-01"}, "to": {"2026-10-01"}}, now); err == nil {
		t.Fatal("reversed date range accepted")
	}
}

func TestFinanceDashboardSumKeepsIntegerCents(t *testing.T) {
	got := financeDashboardSum(map[string]string{"a": "10000000000000000001", "b": "2", "c": "-3"}, "a", "b", "-c")
	if got != "10000000000000000006" {
		t.Fatalf("sum = %s", got)
	}
	if financeDashboardYuan("-12550") != "-125.50" || financeDashboardYuan("5") != "0.05" {
		t.Fatal("yuan formatting lost exact cents")
	}
}

func TestFinanceDashboardCsvUsesSameAmountAndExplainsCutoff(t *testing.T) {
	result := financeDashboardResponse{Range: financeDashboardRange{From: "2026-10-01", To: "2026-10-12"}, QueriedAt: "2026-10-12 12:00:00", Groups: []financeDashboardGroup{{Label: "客户业务与收款", Metrics: []financeDashboardMetric{{Label: "施工收费", Cents: "100000", Basis: "施工日期"}}}}}
	w := httptest.NewRecorder()
	if err := writeFinanceDashboardCSV(w, result); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][2] != "1000.00" || rows[1][3] != "100000" || rows[1][5] != "2026-10-01" || rows[1][6] != "2026-10-12" {
		t.Fatalf("CSV values = %#v", rows)
	}
}
