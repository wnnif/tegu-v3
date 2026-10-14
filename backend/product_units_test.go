package main

import (
	"encoding/json"
	"math"
	"testing"
)

func TestProductQuantityQuantumExactSixGroups(t *testing.T) {
	d, err := newProductUnitDefinition("桶", "小组", "6")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := d.quantityQuantum("1", "base")
	if err != nil {
		t.Fatal(err)
	}
	group, err := d.quantityQuantum("1", "aux")
	if err != nil {
		t.Fatal(err)
	}
	remaining := bucket
	for range 6 {
		remaining -= group
	}
	if remaining != 0 || bucket != 6000000 || group != 1000000 {
		t.Fatalf("六次小组必须精确归零: %d %d %d", bucket, group, remaining)
	}
	minimum, err := d.quantityQuantum("0.000001", "aux")
	if err != nil || minimum != 1 {
		t.Fatalf("最小量子: %d %v", minimum, err)
	}
}

func TestProductQuantityQuantumFractionalRatioAndValidation(t *testing.T) {
	d, err := newProductUnitDefinition("盒", "件", "1.5")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := d.quantityQuantum("2", "base")
	aux, _ := d.quantityQuantum("3", "aux")
	if base != aux || base != 6000000 {
		t.Fatal(base, aux)
	}
	for _, input := range []string{"-1", "1.0000001", "1/6", "NaN", "1e3", "9223372036854775807"} {
		if _, err = d.quantityQuantum(input, "base"); err == nil {
			t.Fatalf("invalid quantity accepted: %s", input)
		}
	}
	if _, err = d.quantityQuantum("1", "unknown"); err == nil {
		t.Fatal("unknown unit")
	}
	d.BaseFactor = math.MaxInt64
	if _, err = d.quantityQuantum("1", "base"); err == nil {
		t.Fatal("overflow")
	}
	for _, input := range []string{`"0"`, `"0.0000001"`, `"1.0000001"`, `"1000001"`, `1e999999999`, `null`, `true`, `{}`} {
		if _, err = parseProductUnitRatio(json.RawMessage(input)); err == nil {
			t.Fatalf("invalid ratio accepted: %s", input)
		}
	}
	for _, input := range []string{`6`, `6e0`, `"6.000000"`} {
		if ratio, err := parseProductUnitRatio(json.RawMessage(input)); err != nil || ratio != "6.000000" {
			t.Fatalf("合法比例未精确规范化: %s %s %v", input, ratio, err)
		}
	}
}

func TestProductUnitSnapshotPreservesLargeIntegersAsStrings(t *testing.T) {
	snapshot := productQuantitySnapshot{productUnitDefinition: productUnitDefinition{"桶", "小组", 6, 1}, InputQuantity: "9007199254.740993", InputUnit: "aux", QuantityQuantum: "9007199254740993"}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["quantity_quantum"] != "9007199254740993" || decoded["base_factor"] != "6" || decoded["input_quantity"] != snapshot.InputQuantity {
		t.Fatal(string(encoded))
	}
}
