package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPhase3ChargeAndWageValidation(t *testing.T) {
	standard := int64(600)
	override := int64(525)
	l := Line{ChargeMode: "free", Qty: 2000000, Price: 1000, Wage: 300, Labor: 600, standardLaborCents: standard, WageOverrideCents: &override, WageAdjustmentReason: "补做实际工资"}
	if err := applyLineChargeAndWage(&l, nil); err != nil {
		t.Fatal(err)
	}
	if l.Total != 0 || l.Labor != override {
		t.Fatalf("free charge or wage override changed: total=%d labor=%d", l.Total, l.Labor)
	}
	for _, mode := range []string{"", "normal", "free", "redo"} {
		if !validChargeMode(normalizeChargeMode(mode)) {
			t.Fatalf("legacy/default charge mode rejected: %q", mode)
		}
	}
	if validChargeMode("discount") {
		t.Fatal("unknown charge mode accepted")
	}
}

func TestPhase3MaterialLineReferences(t *testing.T) {
	o := Order{Lines: []Line{{ID: "server-line", ClientRef: "draft-a"}, {ID: "second"}}, Materials: []Material{{LineID: "draft-a", Product: "p", Warehouse: "w"}, {Product: "p2", Warehouse: "w"}}}
	if err := resolveMaterialLineReferences(&o); err != nil {
		t.Fatal(err)
	}
	if o.Materials[0].LineID != "server-line" || o.Materials[1].LineID != "" {
		t.Fatalf("material line mapping lost: %+v", o.Materials)
	}
	bad := Order{Lines: []Line{{ID: "known"}}, Materials: []Material{{LineID: "missing"}}}
	if err := resolveMaterialLineReferences(&bad); err == nil {
		t.Fatal("orphan material line accepted")
	}
}

func TestPhase3MaterialSnapshotKeySeparatesLines(t *testing.T) {
	a := Material{LineID: "line-a", Product: "p", Warehouse: "w"}
	b := Material{LineID: "line-b", Product: "p", Warehouse: "w"}
	if materialSnapshotKey(a) == materialSnapshotKey(b) {
		t.Fatal("line-associated materials collapsed into one key")
	}
}

func TestPhase3ChargeAndWagePermissions(t *testing.T) {
	wage := int64(750)
	old := Line{ChargeMode: "redo", WageOverrideCents: &wage, WageAdjustmentReason: "原调整", Labor: wage, Total: 0, Qty: 1000000}
	u := User{Role: "employee", Permissions: []string{"orders"}}
	var submitted Line
	if err := json.Unmarshal([]byte(`{"qty_micro":1000000}`), &submitted); err != nil {
		t.Fatal(err)
	}
	if err := protectLineChargeAndWage(u, &submitted, &old); err != nil {
		t.Fatal(err)
	}
	if submitted.ChargeMode != "redo" || !sameWageOverride(submitted.WageOverrideCents, &wage) || submitted.WageAdjustmentReason != "原调整" {
		t.Fatal("omitted protected state not preserved", submitted)
	}
	for _, line := range []Line{{ChargeMode: "free"}, {WageOverrideCents: &wage}, {WageAdjustmentReason: "伪造原因"}} {
		if err := protectLineChargeAndWage(u, &line, nil); err == nil {
			t.Fatal("unprivileged override accepted", line)
		}
	}
	admin := User{Role: "admin"}
	var reset Line
	if err := json.Unmarshal([]byte(`{"charge_mode":"redo","wage_override_cents":null,"wage_adjustment_reason":"恢复标准"}`), &reset); err != nil {
		t.Fatal(err)
	}
	if reset.wageOverrideOmitted {
		t.Fatal("explicit null treated as omission")
	}
	if err := protectLineChargeAndWage(admin, &reset, &old); err != nil {
		t.Fatal(err)
	}
	reset.standardLaborCents, reset.Labor = 300, old.Labor
	if err := applyLineChargeAndWage(&reset, &old); err != nil || reset.Labor != 300 || reset.Total != 0 {
		t.Fatal("reset standard salary", reset, err)
	}
}

func TestPhase3TicketHistoryWageRedaction(t *testing.T) {
	wage := int64(750)
	d := projectTicketDetail{Orders: []orderDetailView{{WageAdjustments: []wageAdjustmentView{{Before: 100, After: wage, Reason: "敏感整单原因"}}, Lines: []orderLineView{{Wage: &wage, Labor: &wage, WageOverrideCents: &wage, WageAdjustmentReason: "敏感调整原因", WageAdjustments: []wageAdjustmentView{{Before: 100, After: wage, Reason: "敏感历史"}}}}}}}
	redactProjectTicket(&d, User{Role: "employee", Permissions: []string{"orders", customerPricePermission}})
	raw := jsonText(d)
	for _, field := range []string{"wage_override_cents", "wage_adjustment_reason", "wage_adjustments", "labor_cents", "wage_cents"} {
		if strings.Contains(raw, `"`+field+`"`) {
			t.Fatal("historical salary escaped", field, raw)
		}
	}
}

func TestPhase3MaterialDeltaAggregatesWithoutDuplicateDeduction(t *testing.T) {
	old := []Material{{LineID: "a", Product: "p", Warehouse: "w", QtyQuantum: "6000000"}, {Product: "p", Warehouse: "w", QtyQuantum: "3000000"}}
	current := []Material{{LineID: "a", Product: "p", Warehouse: "w", QtyQuantum: "12000000"}, {Product: "p", Warehouse: "w", QtyQuantum: "3000000"}}
	m, s, err := orderStockDeltas(old, current, nil, nil)
	if err != nil || m["w|p"] != -6000000 || len(s) != 0 {
		t.Fatal("actual material change not exact", m, s, err)
	}
	current[0].LineID = "b"
	m, s, err = orderStockDeltas(current, current, nil, nil)
	if err != nil || m["w|p"] != 0 || len(s) != 0 {
		t.Fatal("unchanged stock reposted", m, s, err)
	}
}
