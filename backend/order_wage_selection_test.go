package main

import "testing"

func TestEffectiveOrderLineWagePrecedence(t *testing.T) {
	craftDefault := int64(120)
	subitemWage := int64(45)
	explicitZero := int64(0)
	companyWage := int64(60)
	zeroCompanyWage := int64(0)
	legacyWage := int64(55)
	subitems := []CraftSubitem{
		{ID: "wall", Name: "墙面", WageCents: &subitemWage},
		{ID: "floor", Name: "地面"},
		{ID: "free", Name: "免工资", WageCents: &explicitZero},
	}
	tests := []struct {
		name            string
		subitemID       string
		companyOverride *int64
		legacyOverride  *int64
		wantName        string
		wantWage        int64
	}{
		{name: "craft default", wantWage: craftDefault},
		{name: "subitem wage", subitemID: "wall", wantName: "墙面", wantWage: subitemWage},
		{name: "subitem inherits craft", subitemID: "floor", wantName: "地面", wantWage: craftDefault},
		{name: "explicit zero subitem", subitemID: "free", wantName: "免工资", wantWage: 0},
		{name: "company overrides subitem", subitemID: "wall", companyOverride: &companyWage, wantName: "墙面", wantWage: companyWage},
		{name: "company explicit zero", subitemID: "wall", companyOverride: &zeroCompanyWage, wantName: "墙面", wantWage: 0},
		{name: "legacy company override remains compatible", subitemID: "wall", legacyOverride: &legacyWage, wantName: "墙面", wantWage: legacyWage},
		{name: "independent company override takes precedence", subitemID: "wall", companyOverride: &companyWage, legacyOverride: &legacyWage, wantName: "墙面", wantWage: companyWage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, wage, err := effectiveOrderLineWage(test.subitemID, craftDefault, test.companyOverride, test.legacyOverride, subitems)
			if err != nil {
				t.Fatalf("effectiveOrderLineWage() error = %v", err)
			}
			if name != test.wantName || wage != test.wantWage {
				t.Fatalf("effectiveOrderLineWage() = (%q, %d), want (%q, %d)", name, wage, test.wantName, test.wantWage)
			}
		})
	}
}

func TestEffectiveOrderLineWageStatusDistinguishesPendingAndConfiguredZero(t *testing.T) {
	explicitZero := int64(0)
	for _, tc := range []struct {
		name            string
		subitemID       string
		configured      bool
		companyOverride *int64
		subitems        []CraftSubitem
		wantWage        int64
		wantConfigured  bool
	}{
		{name: "missing standard wage", configured: false, wantConfigured: false},
		{name: "explicit zero standard wage", configured: true, wantConfigured: true},
		{name: "explicit zero subitem wage", subitemID: "zero", configured: false, subitems: []CraftSubitem{{ID: "zero", Name: "免工资", WageCents: &explicitZero}}, wantConfigured: true},
		{name: "explicit zero company override", configured: false, companyOverride: &explicitZero, wantConfigured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, wage, configured, err := effectiveOrderLineWageStatus(tc.subitemID, 0, tc.configured, tc.companyOverride, nil, tc.subitems)
			if err != nil {
				t.Fatal(err)
			}
			if wage != tc.wantWage || configured != tc.wantConfigured {
				t.Fatalf("wage status = (%d, %v), want (%d, %v)", wage, configured, tc.wantWage, tc.wantConfigured)
			}
		})
	}
}

func TestEffectiveOrderLineWageRejectsUnknownSubitem(t *testing.T) {
	if _, _, err := effectiveOrderLineWage("removed", 100, nil, nil, []CraftSubitem{{ID: "current", Name: "当前小项"}}); err == nil {
		t.Fatal("unknown subitem should be rejected")
	}
}

func TestEffectiveOrderLineWageFeedsLaborCalculation(t *testing.T) {
	rate := int64(45)
	_, wage, err := effectiveOrderLineWage("wall", 120, nil, nil, []CraftSubitem{{ID: "wall", Name: "墙面", WageCents: &rate}})
	if err != nil {
		t.Fatal(err)
	}
	line := Line{Category: "开工", Name: "墙地固", Mode: "平方", Wage: wage, Price: 0, Segments: []Segment{{Length: 2, Width: 3}}}
	if err := calcLine(&line); err != nil {
		t.Fatal(err)
	}
	if line.Labor != 270 || line.Wage != 45 {
		t.Fatalf("line wage/labor = %d/%d, want 45/270 cents", line.Wage, line.Labor)
	}
}
