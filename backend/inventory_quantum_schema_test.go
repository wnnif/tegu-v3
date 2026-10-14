package main

import "testing"

func TestInventoryMigrationLineQuantumRejectsAmbiguousSnapshots(t *testing.T) {
	definition := productUnitDefinition{BaseUnit: "桶", AuxUnit: "小组", BaseFactor: 6, AuxFactor: 1}
	valid := StockLine{
		Material:        Material{Product: "p", Qty: 1000000},
		Unit:            "桶",
		AuxUnit:         "小组",
		AuxRatio:        "6.000000",
		BaseFactor:      "6",
		AuxFactor:       "1",
		InputQuantity:   "1",
		InputUnit:       "base",
		QuantityQuantum: "6000000",
	}
	if quantity, err := inventoryMigrationLineQuantum(valid, definition); err != nil || quantity.String() != "6000000" {
		t.Fatalf("valid exact snapshot rejected: quantity=%v err=%v", quantity, err)
	}
	for name, line := range map[string]StockLine{
		"ratio changed":    func() StockLine { v := valid; v.AuxRatio = "5.000000"; return v }(),
		"factor changed":   func() StockLine { v := valid; v.BaseFactor = "5"; return v }(),
		"quantity changed": func() StockLine { v := valid; v.QuantityQuantum = "5000000"; return v }(),
		"input changed":    func() StockLine { v := valid; v.InputQuantity = "1"; v.InputUnit = "aux"; return v }(),
		"unit changed":     func() StockLine { v := valid; v.Unit = "袋"; return v }(),
	} {
		if _, err := inventoryMigrationLineQuantum(line, definition); err == nil {
			t.Fatalf("ambiguous %s snapshot was accepted", name)
		}
	}
	// Old rows without snapshots remain readable when their legacy base-micro
	// quantity is exactly convertible under the current definition.
	legacy := StockLine{Material: Material{Product: "p", Qty: 1000000}}
	if quantity, err := inventoryMigrationLineQuantum(legacy, definition); err != nil || quantity.String() != "6000000" {
		t.Fatalf("legacy base quantity should convert exactly: quantity=%v err=%v", quantity, err)
	}
}
