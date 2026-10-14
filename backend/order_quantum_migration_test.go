package main

import "testing"

func TestExactOrderSaleSnapshotQuantumRequiresEverySnapshot(t *testing.T) {
	definition := productUnitDefinition{BaseUnit: "桶", AuxUnit: "小组", BaseFactor: 6, AuxFactor: 1}
	tests := []struct {
		name string
		row  orderSaleQuantumSnapshot
		want int64
		ok   bool
	}{
		{
			name: "whole base unit",
			row: orderSaleQuantumSnapshot{ID: "base", QtyMicro: 1_000_000, SaleQtyMicro: 1_000_000,
				InputQty: "1", InputUnit: "base", ConversionRatio: "6"},
			want: 6_000_000, ok: true,
		},
		{
			name: "exact auxiliary multiple",
			row: orderSaleQuantumSnapshot{ID: "aux", QtyMicro: 1_000_000, SaleQtyMicro: 6_000_000,
				InputQty: "6", InputUnit: "aux", ConversionRatio: "6"},
			want: 6_000_000, ok: true,
		},
		{
			name: "rounded auxiliary legacy quantity is refused",
			row: orderSaleQuantumSnapshot{ID: "rounded", QtyMicro: 166_667, SaleQtyMicro: 1_000_000,
				InputQty: "1", InputUnit: "aux", ConversionRatio: "6"},
			ok: false,
		},
		{
			name: "saved ratio changed",
			row: orderSaleQuantumSnapshot{ID: "ratio", QtyMicro: 1_000_000, SaleQtyMicro: 1_000_000,
				InputQty: "1", InputUnit: "base", ConversionRatio: "5"},
			ok: false,
		},
		{
			name: "missing input snapshot",
			row: orderSaleQuantumSnapshot{ID: "missing", QtyMicro: 1_000_000, SaleQtyMicro: 1_000_000,
				InputUnit: "base", ConversionRatio: "6"},
			ok: false,
		},
		{
			name: "sale quantity disagrees",
			row: orderSaleQuantumSnapshot{ID: "sale-mismatch", QtyMicro: 1_000_000, SaleQtyMicro: 2_000_000,
				InputQty: "1", InputUnit: "base", ConversionRatio: "6"},
			ok: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := exactOrderSaleSnapshotQuantum(tc.row, definition)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("got quantum=%d err=%v, want %d", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("ambiguous row was accepted with quantum %d", got)
			}
		})
	}
}

func TestExactOrderMaterialSnapshotQuantumAllowsLegacyEmptySnapshot(t *testing.T) {
	definition := productUnitDefinition{BaseUnit: "桶", AuxUnit: "小组", BaseFactor: 6, AuxFactor: 1}
	got, err := exactOrderMaterialSnapshotQuantum(orderMaterialQuantumSnapshot{
		ID: "legacy", QtyMicro: 2_000_000,
	}, definition)
	if err != nil || got != 12_000_000 {
		t.Fatalf("legacy base material conversion got quantum=%d err=%v", got, err)
	}
	if _, err = exactOrderMaterialSnapshotQuantum(orderMaterialQuantumSnapshot{
		ID: "mismatch", QtyMicro: 1_000_000, InputQty: "1", InputUnit: "aux", ConversionRatio: "6",
	}, definition); err == nil {
		t.Fatal("inexact auxiliary material snapshot was accepted")
	}
}
