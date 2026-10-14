package main

import "testing"

// The fixture remains opt-in and uses only a runner-verified disposable schema.
func TestPhase3CombinationFreeRedoCustomerChargeRemainsZeroMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	for _, mode := range []string{"free", "redo"} {
		for _, target := range []string{"waterproof", "polypropylene"} {
			t.Run(mode+"-"+target, func(t *testing.T) {
				lines := combinationActualLines()
				index := 0
				if target == "polypropylene" {
					index = 1
				}
				lines[index].ChargeMode = mode
				d := f.save(t, Order{Company: "loc-co", Address: "合成免费补做组合-" + mode + "-" + target, Date: "2026-10-11", Lines: lines})
				assertCharge := func(expected int64) {
					t.Helper()
					if d.TotalCents != expected {
						t.Fatalf("%s customer total = %d, want %d", target, d.TotalCents, expected)
					}
					var modeSaved string
					var total, labor int64
					freeID := ""
					for _, line := range d.Lines {
						if (target == "waterproof" && line.Category == "防水涂料") || (target == "polypropylene" && line.Category == "丙纶") {
							freeID = line.ID
						}
					}
					if err := f.a.db.QueryRow("SELECT charge_mode,total_cents,labor_cents FROM order_lines WHERE id=?", freeID).Scan(&modeSaved, &total, &labor); err != nil {
						t.Fatal(err)
					}
					if modeSaved != mode || total != 0 || labor <= 0 {
						t.Fatal("combination changed free/redo charge or actual wage", modeSaved, total, labor)
					}
					f.assertWages(t, d, 120000, 150000)
					f.assertDraftTicket(t, d.ProjectID, expected)
				}
				assertCharge(300000)
				d = f.set(t, d, []ProjectCombination{combinationPair(d, target, 20000000)}, 200)
				assertCharge(300000)
				// Changing the waived side may discount normal work but cannot make
				// the previous, still-free target chargeable again.
				other, expected := "polypropylene", int64(240000)
				if target == "polypropylene" {
					other, expected = "waterproof", 200000
				}
				d = f.set(t, d, []ProjectCombination{combinationPair(d, other, 20000000)}, 200)
				assertCharge(expected)
				d = f.set(t, d, []ProjectCombination{}, 200)
				assertCharge(300000)
			})
		}
	}
}
