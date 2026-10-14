package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func craftImportFixture(t *testing.T, rates []map[string]any) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "synthetic-crafts.json")
	data, err := json.Marshal(map[string]any{"format": "tegu-craft-export-v2", "customers": []any{}, "rates": rates})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestCraftImportSubitemsMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	items := []CraftSubitem{{ID: "position-b", Name: "合成位置乙"}, {ID: "position-a", Name: "合成位置甲"}}
	rates := []map[string]any{}
	for i, mode := range []string{"平方", "立方", "米", "个", "金额"} {
		rate := map[string]any{"id": fmt.Sprintf("synthetic-import-%d", i), "name": "合成导入" + mode, "category": "包管", "calcMode": mode, "priceCents": 999, "wageCents": 123, "active": false, "spec": "合成待补说明", "version": 3}
		if i == 0 {
			rate["subitems"] = items
		}
		rates = append(rates, rate)
	}
	file := craftImportFixture(t, rates)
	if err := a.importCrafts(file); err != nil {
		t.Fatal(err)
	}
	for i, mode := range []string{"平方", "立方", "米", "个", "金额"} {
		var gotMode, spec, raw string
		var price, wage, version int64
		var active bool
		if err := a.db.QueryRow("SELECT mode,spec,price_cents,wage_cents,active,version,subitems FROM crafts WHERE id=?", fmt.Sprintf("synthetic-import-%d", i)).Scan(&gotMode, &spec, &price, &wage, &active, &version, &raw); err != nil {
			t.Fatal(err)
		}
		if gotMode != mode || spec != "合成待补说明" || price != 0 || wage != 123 || active || version != 3 {
			t.Fatalf("import fields changed: %s %s %d %d %v %d", gotMode, spec, price, wage, active, version)
		}
		var got []CraftSubitem
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if len(got) != 2 || got[0] != items[0] || got[1] != items[1] {
				t.Fatalf("stable subitem ID/order lost: %+v", got)
			}
		} else if len(got) != 0 {
			t.Fatal("legacy file without subitems gained positions", got)
		}
	}
	maintenanceFaultError(t, a.importCrafts(file), 409)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM crafts", 5)
}

func TestCraftImportSubitemsRollbackMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	tooMany := make([]CraftSubitem, 101)
	for i := range tooMany {
		tooMany[i] = CraftSubitem{ID: fmt.Sprintf("item-%d", i), Name: fmt.Sprintf("合成位置%d", i)}
	}
	cases := []struct {
		name  string
		items []CraftSubitem
	}{
		{"duplicate-id", []CraftSubitem{{ID: "same", Name: "甲"}, {ID: "same", Name: "乙"}}},
		{"duplicate-name", []CraftSubitem{{ID: "a", Name: "同名"}, {ID: "b", Name: "同名"}}},
		{"blank-name", []CraftSubitem{{ID: "a", Name: " "}}},
		{"long-name", []CraftSubitem{{ID: "a", Name: strings.Repeat("字", 101)}}},
		{"too-many", tooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rates := []map[string]any{
				{"id": "synthetic-first", "name": "合成先写项", "category": "包管", "calcMode": "米", "active": false},
				{"id": "synthetic-invalid", "name": "合成非法小项", "category": "包管", "calcMode": "个", "subitems": tc.items},
			}
			maintenanceFaultError(t, a.importCrafts(craftImportFixture(t, rates)), 400)
			maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM crafts", 0)
		})
	}
}
