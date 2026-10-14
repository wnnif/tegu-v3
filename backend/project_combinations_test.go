package main

import (
	"testing"
)

func TestProjectCombinationCustomerWaiverNeverChangesActualWages(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		waterWaived, polyWaived, total int64
	}{
		{"none", 0, 0, 600000}, {"polypropylene-part", 0, 40000000, 480000},
		{"waterproof-part", 20000000, 0, 500000}, {"polypropylene-full", 0, 100000000, 300000},
		{"waterproof-full", 60000000, 0, 300000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Line{CraftID: "w", Category: "防水涂料", Name: "防水", Mode: "平方", Price: 5000, Wage: 2000, Worker: "甲", Segments: []Segment{{Length: 6, Width: 10}}}
			p := Line{CraftID: "p", Category: "丙纶", Name: "丙纶", Mode: "平方", Price: 3000, Wage: 1500, Worker: "乙", Segments: []Segment{{Length: 10, Width: 10}}}
			if err := calcLine(&w); err != nil {
				t.Fatal(err)
			}
			if err := calcLine(&p); err != nil {
				t.Fatal(err)
			}
			if err := applyComboWaiver(&w, tc.waterWaived); err != nil {
				t.Fatal(err)
			}
			if err := applyComboWaiver(&p, tc.polyWaived); err != nil {
				t.Fatal(err)
			}
			if w.Total+p.Total != tc.total || w.Labor != 120000 || p.Labor != 150000 || w.Qty != 60000000 || p.Qty != 100000000 || w.Worker != "甲" || p.Worker != "乙" {
				t.Fatalf("customer waiver changed actual work/payroll: water=%+v poly=%+v", w, p)
			}
		})
	}
}

func TestProjectCombinationEligibilityBoundaryAndRounding(t *testing.T) {
	base := Line{CraftID: "w", Category: "室外防水", Mode: "平方", Qty: 1001501, Price: 2333, Labor: 987}
	if comboLineKind(base) != "waterproof" {
		t.Fatal("outside waterproof square rule missing")
	}
	for _, change := range []func(*Line){func(l *Line) { l.Temporary = true }, func(l *Line) { l.UseIncludedPrice = true }, func(l *Line) { l.Mode = "米" }, func(l *Line) { l.CraftID = "" }, func(l *Line) { l.Category = "开工" }, func(l *Line) { l.Qty = 0 }} {
		l := base
		change(&l)
		if comboLineKind(l) != "" {
			t.Fatal("ineligible line accepted", l)
		}
	}
	for _, waived := range []int64{-1, 1001502} {
		l := base
		if err := applyComboWaiver(&l, waived); err == nil {
			t.Fatal("invalid waiver accepted", waived)
		}
	}
	l := base
	if err := applyComboWaiver(&l, 500000); err != nil || l.Total != 1170 || l.Labor != 987 || l.Qty != 1001501 {
		t.Fatal("micro quantity / cents rounding changed", l, err)
	}
	// Waiver zero must not silently normalize an unrelated historical snapshot.
	l.Total = 1234
	if err := applyComboWaiver(&l, 0); err != nil || l.Total != 1234 {
		t.Fatal("zero waiver rewrote historical amount", l, err)
	}
}

func TestProjectCombinationOperationPermissionIsIndependent(t *testing.T) {
	for _, u := range []User{{Role: "operator"}, {Role: "employee", Permissions: []string{"orders", "customer_prices", "wages", "finance"}}, {Role: "employee", Permissions: []string{"combo_pricing"}}} {
		if err := requireCombinationOperation(u); err == nil {
			t.Fatal("default/other permissions granted customer waiver", u)
		}
	}
	for _, u := range []User{{Role: "admin"}, {Role: "operator", Permissions: []string{"combo_pricing"}}, {Role: "employee", Permissions: []string{"orders", "combo_pricing"}}} {
		if err := requireCombinationOperation(u); err != nil {
			t.Fatal(u, err)
		}
	}
	u := User{Role: "employee", Permissions: []string{"orders", "combo_pricing"}}
	for _, permission := range []string{"customer_prices", "wages", "finance"} {
		if u.can(permission) {
			t.Fatal("waiver grant widened independent access", permission)
		}
	}
}

func TestProjectCombinationProjectionCannotRewriteClaimedHistory(t *testing.T) {
	line := Line{CraftID: "poly", Category: "丙纶", Mode: "平方", Qty: 6000000, Price: 1000, Total: 4001}
	saved := line.Total
	line.Total = scaledAmount(line.Qty, line.Price)
	if err := applyComboWaiver(&line, 2000000); err != nil {
		t.Fatal(err)
	}
	if line.Total != 4000 {
		t.Fatalf("unexpected current projection: %d", line.Total)
	}
	if err := validateCombinationTotalChange(saved, line.Total, "valid-batch"); err == nil {
		t.Fatal("replaying an unchanged combination must not rewrite an active batch's historical amount")
	} else if rejection, ok := err.(failure); !ok || rejection.Code != 409 {
		t.Fatalf("claimed projection must report a conflict, got %v", err)
	}
	if err := validateCombinationTotalChange(saved, line.Total, ""); err != nil {
		t.Fatal("an unclaimed row must remain correctable after its batch is voided:", err)
	}
	if err := validateCombinationTotalChange(line.Total, line.Total, "valid-batch"); err != nil {
		t.Fatal("unchanged claimed amount must remain valid:", err)
	}
}
