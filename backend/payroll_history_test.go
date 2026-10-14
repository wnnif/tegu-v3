package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPayrollHistoryFiltersAndScope(t *testing.T) {
	valid := url.Values{"worker_id": {"worker-1"}, "from": {"2026-10-01"}, "to": {"2026-10-31"}, "project_id": {"project-1"}}
	workerID, from, to, projectID, err := validatePayrollHistoryFilters(valid)
	if err != nil || workerID != "worker-1" || from != "2026-10-01" || to != "2026-10-31" || projectID != "project-1" {
		t.Fatalf("valid history filters = %q %q %q %q, %v", workerID, from, to, projectID, err)
	}
	for _, query := range []url.Values{
		{},
		{"worker_id": {"worker-1"}, "from": {"2026-02-30"}},
		{"worker_id": {"worker-1"}, "from": {"2026-10-02"}, "to": {"2026-10-01"}},
		{"worker_id": {"worker-1"}, "project_id": {strings.Repeat("x", 65)}},
	} {
		if _, _, _, _, err = validatePayrollHistoryFilters(query); err == nil {
			t.Fatalf("invalid history filters accepted: %v", query)
		}
	}
	where, args := payrollHistoryQuery(workerID, from, to, projectID)
	if strings.Count(where, "?") != len(args) {
		t.Fatalf("placeholder/argument mismatch: %d placeholders, %d args", strings.Count(where, "?"), len(args))
	}
	for _, fragment := range []string{
		"h.created_at>=?",
		"h.created_at<DATE_ADD(?,INTERVAL 1 DAY)",
		"h.entity_type='payment'",
		"JOIN payroll_settlements s ON s.id=p.settlement_id",
		"payroll_settlement_sources src",
		"src.project_id=?",
		"$.ProjectID",
	} {
		if !strings.Contains(where, fragment) {
			t.Fatalf("history scope lacks %q: %s", fragment, where)
		}
	}
	if len(args) != 24 || args[0] != from || args[1] != to || args[14] != workerID || args[15] != projectID || args[20] != workerID || args[21] != projectID || args[22] != workerID || args[23] != projectID {
		t.Fatalf("unexpected scoped arguments: %#v", args)
	}
	if strings.Contains(where, "o.project_id=?") || strings.Contains(where, "LEFT JOIN orders o") {
		t.Fatalf("settlement history scope uses mutable order association: %s", where)
	}
}

func TestPayrollSettlementProjectFilterUsesSourceSnapshot(t *testing.T) {
	filter := payrollSettlementProjectFilter("s")
	if !strings.Contains(filter, "src.project_id=?") {
		t.Fatalf("settlement project filter lacks the source snapshot: %s", filter)
	}
	for _, mutableJoin := range []string{"orders", "order_worker_wages", "payroll_adjustments", "payroll_deductions", "payroll_advances"} {
		if strings.Contains(filter, mutableJoin) {
			t.Fatalf("settlement project filter reads mutable %s rows: %s", mutableJoin, filter)
		}
	}
}

func TestPayrollHistoryRequiresWagePermissionBeforeDatabase(t *testing.T) {
	app := &App{}
	err := app.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/payroll-history?worker_id=private-worker", nil), User{Role: "employee"})
	var f failure
	if !errors.As(err, &f) || f.Code != 403 {
		t.Fatalf("unauthorized history read = %v, want 403 before database access", err)
	}
	app = &App{}
	err = app.listWageWorkers(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/wage-workers", nil), User{Role: "employee", Permissions: []string{"wages"}})
	if !errors.As(err, &f) || f.Code != 409 {
		t.Fatalf("stale wage summary = %v, want 409 migration response", err)
	}
}

func TestPayrollHistorySnapshotsRemainStructuredJSON(t *testing.T) {
	before, after, err := payrollHistorySnapshots([]byte(`{"amount_cents":100}`), []byte(`{"amount_cents":75}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]json.RawMessage{"before_snapshot": before, "after_snapshot": after})
	if err != nil || string(encoded) != `{"after_snapshot":{"amount_cents":75},"before_snapshot":{"amount_cents":100}}` {
		t.Fatalf("snapshots were not encoded as objects: %s (%v)", encoded, err)
	}
	before, _, err = payrollHistorySnapshots(nil, []byte(`{"id":"created"}`))
	if err != nil || string(before) != "null" {
		t.Fatalf("missing prior snapshot = %s, %v", before, err)
	}
	if _, _, err = payrollHistorySnapshots(nil, []byte(`not-json`)); err == nil {
		t.Fatal("invalid stored snapshot accepted")
	}
}
