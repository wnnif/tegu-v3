package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
)

func TestAcceptanceWorkbook(t *testing.T) {
	var output bytes.Buffer
	orders := []acceptanceOrder{{
		ID: "one", Number: "A-001", Address: "测试工地", Date: "2026-09-29", Company: "公司 & 客户", Contact: "张<三", Total: 360400,
		Worker: "内部施工人", Status: "进行中", WageTotal: 8765,
		Lines: []archiveLine{
			{Category: "防水涂料", Name: "蓝防水", Region: "厨房", Mode: "平方", Segments: `[{"length":12.4,"width":1,"surface":"墙面"},{"length":4.1,"width":2.16,"surface":"地面"}]`, QtyMicro: 21256000, PriceCents: 15000, TotalCents: 318840, WageCents: 400, LaborCents: 8765},
			{Category: "包管", Name: "手工包管", Mode: "金额", Segments: `[]`, QtyMicro: 1000000, PriceCents: 41560, TotalCents: 41560},
		},
		Materials: []acceptanceMaterial{{Product: "内部材料", Warehouse: "施工人仓库", Unit: "桶", Quantity: 1250000}},
	}}
	count, total, err := writeAcceptanceWorkbook(&output, "验收单", "施工单位", false, func(yield func(acceptanceOrder) error) error {
		for _, o := range orders {
			if err := yield(o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || count != 1 || total != 360400 {
		t.Fatalf("workbook count/total: %d %d %v", count, total, err)
	}
	if path := os.Getenv("TEGU_ACCEPTANCE_SAMPLE"); path != "" {
		if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	z, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = string(b)
		decoder := xml.NewDecoder(bytes.NewReader(b))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid OOXML %s: %v", f.Name, err)
			}
		}
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if parts[name] == "" {
			t.Fatal("missing workbook part", name)
		}
	}
	sheet := parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"公司 &amp; 客户", "张&lt;三", "12.4 × 1", "4.1 × 2.16", "防水涂料 / 蓝防水", "厨房", "施工项目", "墙面尺寸 / 尺寸", "地面 / 其他尺寸", "防水涂料 / 蓝防水金额", `<v>3188.40</v>`, `<v>3604.00</v>`, `<mergeCell ref="A1:F1"/>`} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("missing acceptance content %q", want)
		}
	}
	for _, forbidden := range []string{"wage_cents", "工资", "人工单价", "内部施工人", "内部材料", "8765"} {
		if strings.Contains(sheet, forbidden) {
			t.Fatalf("private/non-numeric content in workbook: %q", forbidden)
		}
	}
}

func TestPhase7AcceptanceKeepsIdentityLocationAndZeroCharge(t *testing.T) {
	base := archiveLine{CraftID: "craft-a", Category: "维修", Name: "同名保存工艺", SubitemName: "保存厨房", ActualLocation: "北墙", Region: "旧部位", Mode: "平方", Segments: `[{"surface":"墙面","length":1,"width":1.234567}]`, QtyMicro: 1234567, PriceCents: 2000, TotalCents: 2469}
	other, free, redo := base, base, base
	other.CraftID = "craft-b"
	free.TotalCents, free.ChargeMode = 0, "free"
	redo.TotalCents, redo.ChargeMode = 0, "redo"
	groups, err := acceptanceGroups(acceptanceOrder{Lines: []archiveLine{base, other, free, redo}})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 4 || groups[0].rows[0].region != "保存厨房；实际部位：北墙；原部位：旧部位" {
		t.Fatalf("snapshot grouping/location: %+v", groups)
	}
	if !strings.Contains(groups[2].rows[0].floor, "免费施工") || !strings.Contains(groups[3].rows[0].floor, "补做不收费") {
		t.Fatal("zero-charge labels missing", groups)
	}
	if groups[0].total+groups[1].total+groups[2].total+groups[3].total != 4938 {
		t.Fatal("zero-charge duplicated amount")
	}
}

func TestInternalAcceptanceWorkbook(t *testing.T) {
	order := acceptanceOrder{
		ID: "internal", Number: "I-001", Address: "现场", Date: "2026-09-29", Company: "客户公司", Total: 50000,
		Worker: "张施工", Status: "进行中", WagePaid: true, WageTotal: 12345,
		Lines:     []archiveLine{{Category: "开工", Name: "人工", Region: "一层", Mode: "平方", Segments: `[]`, QtyMicro: 2500000, PriceCents: 20000, TotalCents: 50000, WageCents: 3000, LaborCents: 12345}},
		Materials: []acceptanceMaterial{{Product: "防水材料", Warehouse: "张施工仓库", Unit: "桶", Quantity: 1250000}},
	}
	var output bytes.Buffer
	count, total, err := writeAcceptanceWorkbook(&output, "施工验收单", "施工单位", true, func(yield func(acceptanceOrder) error) error { return yield(order) })
	if err != nil || count != 1 || total != 50000 {
		t.Fatalf("internal workbook count/total: %d %d %v", count, total, err)
	}
	z, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, f := range z.File {
		if f.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		sheet = string(content)
	}
	for _, want := range []string{"客户公司", "公司内部记录（含工人工资）", "张施工", "进行中 / 已有付款记录（实际金额见工资管理）", "人工单价（元）", "工人工资（元）", `<v>30.00</v>`, `<v>123.45</v>`, "防水材料", "张施工仓库", "1.250000 桶"} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("missing internal content %q", want)
		}
	}
}
