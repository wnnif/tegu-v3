package main

import (
	"testing"
)

func TestCraftSubitemsPreserveIdentityAndOrder(t *testing.T) {
	secondWage := int64(500)
	items, err := normalizeCraftSubitems([]CraftSubitem{{ID: "second", Name: " 后做 ", WageCents: &secondWage}, {ID: "first", Name: "先做"}})
	if err != nil || len(items) != 2 || items[0].ID != "second" || items[0].Name != "后做" || items[1].ID != "first" {
		t.Fatalf("changed template order or identity: %+v %v", items, err)
	}
	if items[0].WageCents == nil || *items[0].WageCents != secondWage || items[1].WageCents != nil {
		t.Fatalf("subitem wage override or inheritance was not preserved: %+v", items)
	}
	for _, invalid := range [][]CraftSubitem{{{Name: ""}}, {{ID: "a", Name: "同名"}, {ID: "b", Name: "同名"}}, {{ID: "a", Name: "甲"}, {ID: "a", Name: "乙"}}} {
		if _, err = normalizeCraftSubitems(invalid); err == nil {
			t.Fatalf("accepted invalid subitems: %+v", invalid)
		}
	}
	items, err = normalizeCraftSubitems(nil)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatal("empty template must be an empty array", items, err)
	}
	for _, invalid := range []int64{-1, 100000001} {
		if _, err = normalizeCraftSubitems([]CraftSubitem{{ID: "invalid-wage", Name: "工资边界", WageCents: &invalid}}); err == nil {
			t.Fatalf("invalid subitem wage was accepted: %d", invalid)
		}
	}
}

func TestPublicCraftGrantAcrossCompanies(t *testing.T) {
	u := User{Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"public-source", "开工"}, companyCraftSources: map[string]string{"company-a-fixed": "public-source", "company-b-fixed": "public-source", "ungranted-fixed": "other-public"}}
	if !u.canFillCraft("public-source") || !u.canFillCompanyCraft("company-a-fixed") || !u.canFillCompanyCraft("company-b-fixed") {
		t.Fatal("one public grant did not authorize both company configurations")
	}
	for _, id := range []string{"ungranted-fixed", "missing-fixed", "public-source", "开工", ""} {
		if u.canFillCompanyCraft(id) {
			t.Fatalf("grant widened to %q", id)
		}
	}
	u.Permissions = nil
	if u.canFillCompanyCraft("company-a-fixed") {
		t.Fatal("craft grant bypassed orders module")
	}
	u = User{Role: "operator"}
	if !u.canFillCompanyCraft("company-a-fixed") || !u.can("orders_temporary") {
		t.Fatal("operator lost business access")
	}
	u = User{Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"company-a-fixed"}, companyCraftSources: map[string]string{"company-a-fixed": "public-source"}}
	if u.canFillCompanyCraft("company-a-fixed") || u.can("orders_temporary") || u.can("rates") || u.can("wages") || u.can("customer_prices") {
		t.Fatal("legacy fixed grant or public craft grant widened independent rights")
	}
}

func TestCompanyIncludedCraftsUseSameTicket(t *testing.T) {
	valid := []companyCraftInput{{ID: "main", TicketSlot: 2}, {ID: "included", TicketSlot: 2, IncludedByCraftID: "main"}}
	if err := validateCompanyCraftLinks(valid, "split", map[string]string{"main": "防水涂料", "included": "丙纶"}); err != nil {
		t.Fatal(err)
	}
	for _, items := range [][]companyCraftInput{
		{{ID: "main", TicketSlot: 1}, {ID: "included", TicketSlot: 2, IncludedByCraftID: "main"}},
		{{ID: "main", TicketSlot: 1, IncludedByCraftID: "main"}},
		{{ID: "main", TicketSlot: 1, IncludedByCraftID: "other-company"}},
		{{ID: "a", TicketSlot: 1, IncludedByCraftID: "b"}, {ID: "b", TicketSlot: 1, IncludedByCraftID: "a"}},
	} {
		if err := validateCompanyCraftLinks(items, "split", map[string]string{"main": "防水涂料", "included": "丙纶"}); err == nil {
			t.Fatalf("accepted invalid group: %+v", items)
		}
	}
	if err := validateCompanyCraftLinks(valid, "single", map[string]string{"main": "防水涂料", "included": "丙纶"}); err == nil {
		t.Fatal("single ticket company accepted second slot")
	}
}

func TestPublicCraftRejectsCompanyPriceBeforeWriting(t *testing.T) {
	_, err := saveCraftEntity(nil, User{Role: "admin"}, []byte(`{"name":"合成公共工艺","category":"开工","mode":"个","price_cents":10000,"wage_cents":3000}`))
	if err == nil {
		t.Fatal("public customer charge was accepted")
	}
}

func TestCraftCountModesPreserveUnitsAndCalculateExactAmounts(t *testing.T) {
	for _, mode := range []string{"个", "家", "根", "处", "孔位", "副", "张", "套"} {
		if !validCraftMode(mode) || !countCraftMode(mode) {
			t.Fatalf("count mode was not recognized: %q", mode)
		}
		line := Line{Category: "维修", Name: "计数工艺", Mode: mode, Price: 125, Wage: 37, Segments: []Segment{{Mode: mode, Count: 2}, {Mode: mode, Count: 3}}}
		if err := calcLine(&line); err != nil {
			t.Fatalf("%s quantity rejected: %v", mode, err)
		}
		if line.Qty != 5000000 || line.Total != 625 || line.Labor != 185 {
			t.Fatalf("%s quantity or amount changed: qty=%d total=%d labor=%d", mode, line.Qty, line.Total, line.Labor)
		}
		if got := segmentQuantityLabel(mode, `[{"mode":"`+mode+`","count":5}]`, line.Qty); got != "5 "+mode {
			t.Fatalf("%s unit label was lost: %q", mode, got)
		}
		mismatchedMode := "个"
		if mode == mismatchedMode {
			mismatchedMode = "根"
		}
		wrongMode := Line{Category: "维修", Name: "计数工艺", Mode: mode, Segments: []Segment{{Mode: mismatchedMode, Count: 1}}}
		if err := calcLine(&wrongMode); err == nil {
			t.Fatalf("accepted mismatched segment mode for %s", mode)
		}
	}
	perHousehold := Line{Category: "开工", Name: "按家收费", Mode: "家", Price: 10000, Wage: 3000, Segments: []Segment{{Mode: "家", Count: 2}}}
	if err := calcLine(&perHousehold); err != nil || perHousehold.Qty != 2000000 || perHousehold.Total != 20000 || perHousehold.Labor != 6000 {
		t.Fatalf("2家×100元/家应为20000分并按独立工价计薪: qty=%d total=%d labor=%d err=%v", perHousehold.Qty, perHousehold.Total, perHousehold.Labor, err)
	}
	for _, count := range []int64{0, -1, 1000001} {
		invalid := Line{Category: "开工", Name: "按家数量校验", Mode: "家", Segments: []Segment{{Mode: "家", Count: count}}}
		if err := calcLine(&invalid); err == nil {
			t.Fatalf("accepted invalid household count %d", count)
		}
	}
	tooMany := Line{Category: "开工", Name: "按家合计校验", Mode: "家", Segments: []Segment{{Mode: "家", Count: 1000000}, {Mode: "家", Count: 1}}}
	if err := calcLine(&tooMany); err == nil {
		t.Fatal("accepted a household count sum above the existing limit")
	}
	for _, mode := range []string{"根", "处", "孔位", "副", "张", "套"} {
		if !validCraftMode(mode) {
			t.Fatalf("craft mode rejected: %q", mode)
		}
	}
	if countCraftMode("平方") || countCraftMode("金额") || countCraftMode("米") {
		t.Fatal("non-count mode was recognized as integer-count input")
	}
}

func TestWaterproofCombinationDirection(t *testing.T) {
	for _, parent := range []string{"防水涂料", "室外防水"} {
		if err := validateWaterproofCombination("丙纶", parent); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"防水涂料", "丙纶"}, {"丙纶", "开工"}, {"维修", "防水涂料"}, {"", "室外防水"}} {
		if err := validateWaterproofCombination(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted invalid combination: %v", pair)
		}
	}
}
