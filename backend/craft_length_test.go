package main

import (
	"archive/zip"
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
)

func TestMetreSegmentsAndRounding(t *testing.T) {
	line := Line{Category: "包管", Name: "合成长项", Mode: "米", Price: 10001, Wage: 101, Segments: []Segment{{Length: 0.1}, {Mode: "米", Length: 0.2}, {Length: 1.25}}}
	if err := calcLine(&line); err != nil || line.Qty != 1550000 || line.Total != 15502 || line.Labor != 157 {
		t.Fatalf("length sum or charge/wage cent rounding incorrect: %+v %v", line, err)
	}
	line.Segments = []Segment{{Length: 0.0000005}}
	if err := calcLine(&line); err != nil || line.Qty != 1 {
		t.Fatalf("minimum length precision incorrect: %+v %v", line, err)
	}
	line.Segments = make([]Segment, 100)
	for i := range line.Segments {
		line.Segments[i].Length = 10000
	}
	line.Price, line.Wage = 100000000, 100000000
	if err := calcLine(&line); err != nil || line.Qty != 1000000000000 || line.Total != 100000000000000 || line.Labor != line.Total {
		t.Fatalf("maximum length or amount overflowed: %+v %v", line, err)
	}
}

func TestMetreRejectsInvalidAndMixedDimensions(t *testing.T) {
	for _, segments := range [][]Segment{
		nil,
		{{Length: 0}}, {{Length: -1}}, {{Length: 10000.000001}}, {{Length: 0.0000004}},
		{{Length: math.NaN()}}, {{Length: math.Inf(1)}},
		{{Length: 2, Width: 1}}, {{Length: 2, Height: 1}}, {{Length: 2, Count: 1}}, {{Length: 2, Amount: 1}},
		{{Length: 2, Width: math.NaN()}},
		{{Length: 2, Mode: "平方"}}, {{Length: 2, Mode: "立方"}}, {{Length: 2, Mode: "金额"}}, {{Length: 2, Mode: "个"}},
		{{Length: 1}, {Length: 2, Mode: "个"}},
		make([]Segment, 101),
	} {
		line := Line{Category: "包管", Name: "合成长项", Mode: "米", Price: 100, Wage: 10, Segments: segments}
		if err := calcLine(&line); err == nil {
			t.Fatalf("accepted invalid metre segments: %+v", segments)
		}
	}
	for _, mode := range []string{"平方", "立方", "个"} {
		line := Line{Category: "包管", Name: "其他单位", Mode: mode, Segments: []Segment{{Mode: "米", Length: 2, Width: 3, Height: 4, Count: 1}}}
		if err := calcLine(&line); err == nil {
			t.Fatalf("accepted metre segment in %s line", mode)
		}
	}
}

func TestMetreQuantityAndAcceptanceWorkbook(t *testing.T) {
	for _, raw := range []string{`[{"length":1.25},{"mode":"米","length":0.3}]`, `[]`, `invalid`, `[{"length":2,"width":1}]`} {
		if got := segmentQuantityLabel("米", raw, 1550000); got != "1.550000 米" {
			t.Fatalf("length unit lost: %q from %s", got, raw)
		}
	}
	order := acceptanceOrder{Number: "SYN-LENGTH", Company: "合成公司", Total: 15502, Lines: []archiveLine{{Category: "包管", Name: "合成长项", Mode: "米", Segments: `[{"length":1.25},{"mode":"米","length":0.3}]`, QtyMicro: 1550000, PriceCents: 10001, TotalCents: 15502, WageCents: 101, LaborCents: 157}}}
	var output bytes.Buffer
	_, _, err := writeAcceptanceWorkbook(&output, "验收单", "施工单位", false, func(yield func(acceptanceOrder) error) error { return yield(order) })
	if err != nil {
		t.Fatal(err)
	}
	workbook, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, part := range workbook.File {
		if part.Name == "xl/worksheets/sheet1.xml" {
			r, err := part.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			sheet = string(data)
		}
	}
	for _, want := range []string{"1.25 米 + 0.3 米", "1.550000 米", "100.01 元/米", "<v>155.02</v>"} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("metre workbook missing %q", want)
		}
	}
	for _, forbidden := range []string{"× 0", "工人工资", "人工单价"} {
		if strings.Contains(sheet, forbidden) {
			t.Fatalf("incorrect dimension or wage disclosure: %s", forbidden)
		}
	}
}
