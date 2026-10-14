package main

import "testing"

func TestPhase3CombinationWaiverPreservesFreeRedoWorkAndActualLabor(t *testing.T) {
	for _, mode := range []string{"free", "redo"} {
		for _, waived := range []int64{0, 500000, 1001501} {
			l := Line{CraftID: "water", Category: "防水涂料", Mode: "平方", ChargeMode: mode, Qty: 1001501, Price: 2333, Wage: 500, Total: 1234, Labor: 987, Worker: "actual-worker"}
			override := int64(987)
			l.WageOverrideCents = &override
			if err := applyComboWaiver(&l, waived); err != nil {
				t.Fatal(mode, waived, err)
			}
			if l.Total != 0 || l.WaivedQty != waived || l.Qty != 1001501 || l.Price != 2333 || l.Wage != 500 || l.Labor != 987 || l.Worker != "actual-worker" || l.WageOverrideCents == nil || *l.WageOverrideCents != 987 {
				t.Fatalf("%s/%d changed actual work/payroll or created a customer charge: %+v", mode, waived, l)
			}
		}
	}
	// Free/redo is not an exemption from actual area and eligible-craft checks.
	for _, mode := range []string{"free", "redo"} {
		l := Line{CraftID: "water", Category: "防水涂料", Mode: "平方", ChargeMode: mode, Qty: 1000000, Total: 0, Labor: 400}
		if err := applyComboWaiver(&l, 1000001); err == nil {
			t.Fatal("free/redo accepted excess reserved area", mode)
		}
		l.Mode = "米"
		if err := applyComboWaiver(&l, 500000); err == nil {
			t.Fatal("free/redo bypassed combination eligibility", mode)
		}
	}
}
