package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOrderLocationsJSONCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, raw, location string
		omitted             bool
	}{
		{"old_client", `{"region":"旧混合原文","subitem_id":"part","segments":[{"surface":"旧表面原文"}]}`, "", true},
		{"explicit_location", `{"region":"旧原文","subitem_name":"小项快照","actual_location":"北墙🧱"}`, "北墙🧱", false},
		{"explicit_clear", `{"actual_location":""}`, "", false},
		{"explicit_null", `{"actual_location":null}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var l Line
			if err := json.Unmarshal([]byte(tc.raw), &l); err != nil {
				t.Fatal(err)
			}
			if l.ActualLocation != tc.location || l.actualLocationOmitted != tc.omitted {
				t.Fatalf("location/omission: %+v", l)
			}
			if tc.omitted && (l.Region != "旧混合原文" || l.SubitemName != "" || l.Segments[0].Surface != "旧表面原文") {
				t.Fatal("legacy text was interpreted as a new field", l)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(jsonText(l)), &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"subitem_name", "actual_location", "region"} {
				if _, ok := fields[field]; !ok {
					t.Fatal("round-trip omitted field", field)
				}
			}
			if strings.Contains(jsonText(l), "actualLocationOmitted") {
				t.Fatal("internal compatibility state escaped into JSON")
			}
		})
	}
	for _, raw := range []string{`{"actual_location":123}`, `{"actual_location":[]}`, `{"subitem_name":false}`} {
		var l Line
		if err := json.Unmarshal([]byte(raw), &l); err == nil {
			t.Fatal("invalid text field type accepted", raw)
		}
	}
}

func TestOrderLocationsSavedSnapshots(t *testing.T) {
	admin := User{ID: "location-admin", Role: "admin"}
	old := Line{ID: "saved-line", CraftID: "fixed", SubitemID: "part", SubitemName: "保存时的小项",
		Region: "原有混合文字 / 不解释含义", ActualLocation: "保存的北墙", Category: "开工", Name: "保存工艺",
		Spec: "保存规格", Mode: "平方", Price: 1800, Wage: 300,
		Segments: []Segment{{Length: 2, Width: 3, Surface: "旧表面"}}}
	for _, tc := range []struct {
		name, key string
		value     any
		want      string
	}{
		{"missing_preserves", "", nil, old.ActualLocation},
		{"edit_location", "actual_location", "新的东墙🧱", "新的东墙🧱"},
		{"clear_location", "actual_location", "", ""},
		{"null_location", "actual_location", nil, ""},
		{"mixed_case_edit", "AcTuAl_LoCaTiOn", "混合大小写的北墙🧱", "混合大小写的北墙🧱"},
		{"mixed_case_clear", "AcTuAl_LoCaTiOn", "", ""},
		{"mixed_case_null", "AcTuAl_LoCaTiOn", nil, ""},
		{"upper_case_edit", "ACTUAL_LOCATION", "大写字段的南墙🧱", "大写字段的南墙🧱"},
		{"upper_case_clear", "ACTUAL_LOCATION", "", ""},
		{"upper_case_null", "ACTUAL_LOCATION", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := old
			changed.Name, changed.Spec, changed.Region, changed.SubitemName = "伪造名称", "伪造规格", "篡改旧文字", "篡改小项"
			changed.Price, changed.Wage = 1, 9999
			changed.Segments = []Segment{{Length: 3, Width: 3, Surface: "尺寸段原文"}, {Length: 1, Width: 2}}
			var fields map[string]any
			if err := json.Unmarshal([]byte(jsonText(changed)), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "actual_location")
			if tc.key != "" {
				fields[tc.key] = tc.value
			} else {
				delete(fields, "subitem_name")
			}
			var l Line
			if err := json.Unmarshal([]byte(jsonText(fields)), &l); err != nil {
				t.Fatal(err)
			}
			decodedLocation, _ := tc.value.(string)
			if l.ActualLocation != decodedLocation {
				t.Fatalf("JSON location = %q, want %q", l.ActualLocation, decodedLocation)
			}
			omitted := l.actualLocationOmitted
			if err := prepareOrderLine(nil, admin, Order{}, &l, &old); err != nil {
				t.Fatal(err)
			}
			if omitted != (tc.key == "") || l.ActualLocation != tc.want {
				t.Fatalf("decode/save location = %q (omitted=%t), want %q (omitted=%t)", l.ActualLocation, omitted, tc.want, tc.key == "")
			}
			if l.Region != old.Region || l.SubitemName != old.SubitemName || l.ActualLocation != tc.want || l.Name != old.Name || l.Spec != old.Spec || l.Price != old.Price || l.Wage != old.Wage {
				t.Fatal("saved snapshots changed or editable location lost", l)
			}
			if l.Qty != 11000000 || l.Total != 19800 || l.Labor != 3300 || !reflect.DeepEqual(l.Segments, changed.Segments) {
				t.Fatal("independent dimensions or original pricing changed", l)
			}
		})
	}
	legacy := old
	legacy.SubitemName, legacy.ActualLocation = "", ""
	submitted := legacy
	submitted.SubitemName, submitted.ActualLocation = "不能按当前配置补旧名称", "新填写的真实部位"
	if err := prepareOrderLine(nil, admin, Order{}, &submitted, &legacy); err != nil {
		t.Fatal(err)
	}
	if submitted.SubitemName != "" || submitted.Region != legacy.Region || submitted.ActualLocation != "新填写的真实部位" {
		t.Fatal("empty historical fields were guessed", submitted)
	}
	temporary := old
	temporary.Temporary, temporary.CraftID = true, ""
	submitted = temporary
	submitted.Region, submitted.SubitemName, submitted.ActualLocation = "篡改旧文字", "篡改旧小项", "临时项新部位"
	if err := prepareOrderLine(nil, admin, Order{}, &submitted, &temporary); err != nil {
		t.Fatal(err)
	}
	if submitted.Region != temporary.Region || submitted.SubitemName != temporary.SubitemName || submitted.ActualLocation != "临时项新部位" {
		t.Fatal("temporary saved snapshots changed", submitted)
	}
}

func TestOrderLocationsUnicodeBoundaries(t *testing.T) {
	admin := User{ID: "location-admin", Role: "admin"}
	for _, tc := range []struct {
		name                  string
		location, subitemName string
		invalid               bool
	}{
		{"empty", "", "", false},
		{"unicode_200", strings.Repeat("部🧱", 100), "", false},
		{"unicode_201", strings.Repeat("部🧱", 100) + "界", "", true},
		{"spaces_preserved", "  北墙 / 实际部位  ", "", false},
		{"name_200", "部位", strings.Repeat("项", 200), false},
		{"name_201", "部位", strings.Repeat("项", 201), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Line{Temporary: true, Category: "开工", Name: "合成临时工艺", Mode: "平方", Region: "兼容旧客户端原文",
				SubitemName: tc.subitemName, ActualLocation: tc.location, Price: 1800, Wage: 300,
				Segments: []Segment{{Length: 2, Width: 3, Surface: "不能推断实际部位"}}}
			err := prepareOrderLine(nil, admin, Order{}, &l, nil)
			if tc.invalid {
				maintenanceFaultError(t, err, 400)
				return
			}
			if err != nil || l.ActualLocation != tc.location || l.SubitemName != "" || l.Region != "兼容旧客户端原文" || l.Qty != 6000000 || l.Total != 10800 || l.Labor != 1800 {
				t.Fatal("text boundary, legacy compatibility or pricing changed", l, err)
			}
		})
	}
}

func TestOrderLocationsReadonlyGuard(t *testing.T) {
	old := Line{ID: "saved", CraftID: "readonly-fixed", SubitemID: "part", SubitemName: "旧小项", Region: "旧原文", ActualLocation: "旧部位",
		Category: "开工", Name: "旧工艺", Mode: "平方", Segments: []Segment{{Length: 2, Width: 3}}}
	u := User{ID: "limited", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"different-fixed"}}
	changed := old
	changed.ActualLocation = "无权修改的新部位"
	maintenanceFaultError(t, prepareOrderLine(nil, u, Order{}, &changed, &old), 403)
	if old.ActualLocation != "旧部位" || old.SubitemName != "旧小项" || old.Region != "旧原文" {
		t.Fatal("guard mutated the saved line", old)
	}
}
