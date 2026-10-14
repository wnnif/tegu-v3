package main

import (
	"strings"
	"testing"
)

func TestParseProductCatalogPreservesProductAndExactUnitConversion(t *testing.T) {
	data := []byte(`{"format":"tegu-product-catalog-v1","products":[{"id":"catalog-p1","name":"双单位商品","unit":"桶","aux_unit":"小组","aux_ratio":"6","active":true,"color":"白色","weight_kg":"1.250000","brand":"合成品牌","spec":"规格A","note":"目录资料"},{"id":"catalog-p2","name":"基本单位商品","unit":"包","aux_unit":"","aux_ratio":1,"active":0,"weight_kg":0}]}`)
	products, err := parseProductCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 {
		t.Fatalf("商品数量不符: %d", len(products))
	}
	first := products[0]
	definition, err := newProductUnitDefinition(first.input.Unit, first.input.AuxUnit, first.ratio)
	if err != nil {
		t.Fatal(err)
	}
	if first.input.ID != "catalog-p1" || first.input.Name != "双单位商品" || first.input.Brand != "合成品牌" || first.input.Spec != "规格A" || first.input.Note != "目录资料" || first.input.Color != "白色" {
		t.Fatalf("商品基础资料未保留: %+v", first.input)
	}
	if first.ratio != "6.000000" || definition.BaseFactor != 6 || definition.AuxFactor != 1 || first.weight != 1.25 || !bool(first.input.Active) {
		t.Fatalf("双单位配置或商品属性错误: %+v factor=%+v weight=%v", first, definition, first.weight)
	}
	second := products[1]
	if bool(second.input.Active) || second.input.AuxUnit != "" || second.ratio != "1.000000" {
		t.Fatalf("基本单位商品状态或单位配置错误: %+v", second)
	}
}

func TestParseProductCatalogRejectsMalformedOrUnsafeInput(t *testing.T) {
	valid := `{"format":"tegu-product-catalog-v1","products":[{"id":"p1","name":"商品","unit":"桶","aux_unit":"组","aux_ratio":"6","active":true,"weight_kg":0}]}`
	cases := []struct {
		name string
		data string
	}{
		{"invalid-json", `{`},
		{"wrong-version", strings.Replace(valid, "tegu-product-catalog-v1", "tegu-product-catalog-v2", 1)},
		{"unknown-field", strings.Replace(valid, `"weight_kg":0`, `"weight_kg":0,"stock":10`, 1)},
		{"trailing-document", valid + ` {}`},
		{"empty-catalog", `{"format":"tegu-product-catalog-v1","products":[]}`},
		{"missing-id", strings.Replace(valid, `"id":"p1",`, ``, 1)},
		{"blank-id", strings.Replace(valid, `"p1"`, `" "`, 1)},
		{"duplicate-id", strings.Replace(valid, `}]}`, `},{"id":"p1","name":"另一个商品","unit":"桶","aux_unit":"组","aux_ratio":6,"active":true,"weight_kg":0}]}`, 1)},
		{"missing-ratio", strings.Replace(valid, `,"aux_ratio":"6"`, ``, 1)},
		{"missing-active", strings.Replace(valid, `,"active":true`, ``, 1)},
		{"missing-weight", strings.Replace(valid, `,"weight_kg":0`, ``, 1)},
		{"bad-unit-ratio", strings.Replace(valid, `"aux_ratio":"6"`, `"aux_ratio":"1.0000001"`, 1)},
		{"zero-unit-ratio", strings.Replace(valid, `"aux_ratio":"6"`, `"aux_ratio":0`, 1)},
		{"null-active", strings.Replace(valid, `"active":true`, `"active":null`, 1)},
		{"non-boolean-active", strings.Replace(valid, `"active":true`, `"active":2`, 1)},
		{"bad-weight", strings.Replace(valid, `"weight_kg":0`, `"weight_kg":"NaN"`, 1)},
		{"null-weight", strings.Replace(valid, `"weight_kg":0`, `"weight_kg":null`, 1)},
		{"bool-weight", strings.Replace(valid, `"weight_kg":0`, `"weight_kg":true`, 1)},
		{"weight-rounding", strings.Replace(valid, `"weight_kg":0`, `"weight_kg":0.0000001`, 1)},
		{"ratio-without-aux-unit", strings.Replace(valid, `"aux_unit":"组"`, `"aux_unit":""`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseProductCatalog([]byte(tc.data)); err == nil {
				t.Fatal("非法商品目录被接受")
			}
		})
	}
}

func TestParseProductCatalogRejectsInvalidUTF8(t *testing.T) {
	data := []byte(`{"format":"tegu-product-catalog-v1","products":[{"id":"p1","name":"商品","unit":"桶","aux_unit":"组","aux_ratio":"6","active":true,"weight_kg":0}]}`)
	data = append(data, 0xff)
	if _, err := parseProductCatalog(data); err == nil {
		t.Fatal("非UTF-8商品目录被接受")
	}
}
