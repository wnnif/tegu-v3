package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func combinationDetailTestLine() CombinationSavedLine {
	zero := int64(0)
	return CombinationSavedLine{
		ID: "stage-c-read-actual-water", OrderID: "stage-c-read-order-old", Number: "C-saved-number",
		WorkDate: "2026-10-01", CraftID: "stage-c-read-water", Category: "防水涂料", Name: "C 保存工艺",
		SubitemID: "stage-c-read-part", SubitemName: "C 保存小项", ActualLocation: "C 实际主卫",
		WorkerID: "stage-c-read-worker", WorkerName: "C 保存人员", Mode: "平方",
		Qty: 9007199254740993, WaivedQty: 1000000, ChargeableQty: 9007199253740993,
		Price: &zero, Total: &zero,
	}
}

func combinationDetailsJSON(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

// Apply this only to the new ordinary snapshots; an authorized parent order can
// legitimately contain wage information in its existing, separate projection.
func assertNoCombinationWages(t *testing.T, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "wage") || strings.Contains(lower, "labor") {
				t.Fatalf("combination snapshot exposed wage field %q", key)
			}
			assertNoCombinationWages(t, child)
		}
	case []any:
		for _, child := range v {
			assertNoCombinationWages(t, child)
		}
	}
}

func TestOrderCombinationDetailsJSON(t *testing.T) {
	line := combinationDetailTestLine()
	detail := orderCombinationDetail{ID: "stage-c-read-relation", Waterproof: line, Polypropylene: line}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var decoded orderCombinationDetail
	if err = json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, detail) {
		t.Fatalf("saved ordinary snapshots changed in JSON: %+v, %v", decoded, err)
	}
	fields := combinationDetailsJSON(t, line)
	wantKeys := []string{"id", "order_id", "number", "work_date", "craft_id", "category", "name",
		"subitem_id", "subitem_name", "actual_location", "worker_id", "worker_name", "mode",
		"qty_micro", "waived_qty_micro", "chargeable_qty_micro", "price_cents", "total_cents"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("ordinary snapshot contract keys: %v", fields)
	}
	for _, key := range wantKeys {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing ordinary snapshot field", key)
		}
	}
	for _, key := range []string{"price_cents", "total_cents"} {
		if string(fields[key]) != "0" {
			t.Fatal("authorized saved zero omitted or replaced", key, string(fields[key]))
		}
	}
	var object any
	if err = json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	assertNoCombinationWages(t, object)
}

func TestOrderCombinationDetailsNoPriceJSON(t *testing.T) {
	line := combinationDetailTestLine()
	line.Price, line.Total = nil, nil
	detail := orderCombinationDetail{ID: "stage-c-read-relation", Waterproof: line, Polypropylene: line}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var object any
	if err = json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	assertNoCustomerMoney(t, object)
	assertNoCombinationWages(t, object)
	var decoded orderCombinationDetail
	if err = json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, detail) {
		t.Fatalf("hidden prices changed ordinary snapshots: %+v, %v", decoded, err)
	}
}

func TestOrderCombinationDetailsRecursivePriceFilter(t *testing.T) {
	line := combinationDetailTestLine()
	combination := ProjectCombination{ID: "stage-c-read-relation", Version: 2,
		WaterproofLine: line.ID, PolypropyleneLine: "stage-c-read-actual-poly", Direction: "waterproof", WaivedQty: 1000000}
	wage := int64(12345)
	view := orderDetailView{
		customerPricesHidden: true, financeHidden: true,
		Combinations:       []ProjectCombination{combination},
		CombinationDetails: []orderCombinationDetail{{ID: combination.ID, Waterproof: line, Polypropylene: line}},
		WageCents:          &wage,
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err = json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	assertNoCustomerMoney(t, object)
	assertNoCombinationWages(t, object["combination_details"])
	if object["wage_cents"] != float64(wage) {
		t.Fatal("customer-price defense changed the independent wage projection")
	}
	var decoded orderDetailView
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	wantLine := line
	wantLine.Price, wantLine.Total = nil, nil
	if len(decoded.CombinationDetails) != 1 || !reflect.DeepEqual(decoded.CombinationDetails[0].Waterproof, wantLine) ||
		!reflect.DeepEqual(decoded.CombinationDetails[0].Polypropylene, wantLine) {
		t.Fatal("recursive filtering changed exact quantities or saved ordinary fields", decoded.CombinationDetails)
	}
	if !reflect.DeepEqual(decoded.Combinations, []ProjectCombination{combination}) {
		t.Fatal("additive enrichment changed the existing combinations contract", decoded.Combinations)
	}
	if view.CombinationDetails[0].Waterproof.Price == nil || view.CombinationDetails[0].Waterproof.Total == nil {
		t.Fatal("JSON filtering mutated the server-side snapshot")
	}
}

func TestOrderCombinationDetailsEmptyJSON(t *testing.T) {
	view := orderDetailView{Combinations: []ProjectCombination{}, CombinationDetails: []orderCombinationDetail{}}
	fields := combinationDetailsJSON(t, view)
	if string(fields["combination_details"]) != "[]" || string(fields["combinations"]) != "[]" {
		t.Fatal("empty relation collections must remain JSON arrays", fields["combination_details"], fields["combinations"])
	}
}
