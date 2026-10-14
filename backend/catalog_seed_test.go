package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestParseCatalogSeedDigestIgnoresLineEndings(t *testing.T) {
	data, err := os.ReadFile("../catalog/catalog-seed-v1.json")
	if err != nil {
		t.Fatal(err)
	}

	lf := bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	if bytes.Equal(lf, crlf) {
		t.Fatal("LF and CRLF fixtures must contain different raw bytes")
	}
	if sha256.Sum256(lf) == sha256.Sum256(crlf) {
		t.Fatal("raw seed SHA-256 should reflect the line-ending bytes")
	}

	_, lfDigest, err := parseCatalogSeed(lf)
	if err != nil {
		t.Fatalf("parse LF seed: %v", err)
	}
	_, crlfDigest, err := parseCatalogSeed(crlf)
	if err != nil {
		t.Fatalf("parse CRLF seed: %v", err)
	}
	if lfDigest != crlfDigest {
		t.Fatalf("canonical seed digest changed with line endings: LF=%s CRLF=%s", lfDigest, crlfDigest)
	}
}

func validCatalogSeedFixture() catalogSeedManifest {
	wall, poly, waterproof := true, true, true
	return catalogSeedManifest{
		Format: "tegu-catalog-seed-v1",
		SeedID: "v3-seed-test",
		Sources: []catalogSeedSource{
			{ID: "excel-wages", Kind: "excel", File: "最新工价标准.xlsx", SHA256: strings.Repeat("a", 64)},
			{ID: "excel-prices", Kind: "excel", File: "开工、包管、回填、防水最新汇总价格.xlsx", SHA256: strings.Repeat("b", 64)},
			{ID: "user-wage-rules", Kind: "user"},
		},
		Companies: []catalogSeedCompany{
			{ID: "company-dongjian", Name: "东箭", Source: catalogSeedReference{SourceID: "user-wage-rules", Sheet: "已确认工资规则"}},
			{ID: "company-yiren", Name: "宜仁", Source: catalogSeedReference{SourceID: "user-wage-rules", Sheet: "已确认工资规则"}},
		},
		Crafts: []catalogSeedCraft{
			{ID: "craft-v3-fine-wall-ground", Category: "开工", Name: "精开工·墙地固", Mode: "平方", Active: &wall, Subitems: []catalogSeedSubitem{{ID: "fine-wall", Name: "墙", Wage: seedValue(50, "confirmed", "excel-wages", "开工工资", 15)}}, Source: seedRef("excel-wages", "开工工资", 15)},
			{ID: "craft-polypropylene", Category: "丙纶", Name: "普通丙纶", Mode: "平方", Active: &poly, Subitems: []catalogSeedSubitem{}, Wage: seedValue(500, "confirmed", "excel-wages", "防水工资", 10), Source: seedRef("excel-wages", "防水工资", 10)},
			{ID: "craft-v3-waterproof-split-two-coats", Category: "防水涂料", Name: "分开两遍防水工艺", Mode: "平方", Active: &waterproof, Subitems: []catalogSeedSubitem{}, Wage: seedValue(450, "confirmed", "excel-wages", "防水工资", 4), Source: seedRef("excel-wages", "防水工资", 4)},
		},
		CompanyCrafts: []catalogSeedCompanyCraft{
			{ID: "company-poly-dongjian", CompanyID: "company-dongjian", CraftID: "craft-polypropylene", Price: *seedValue(100, "test_only", "excel-prices", "东箭缺价测试", 3), Source: seedRef("excel-prices", "东箭缺价测试", 3)},
			{ID: "company-poly-yiren", CompanyID: "company-yiren", CraftID: "craft-polypropylene", Price: *seedValue(100, "test_only", "excel-prices", "宜仁缺价测试", 4), Source: seedRef("excel-prices", "宜仁缺价测试", 4)},
		},
		CompanyWageOverrides: []catalogSeedCompanyWageOverride{
			{CompanyID: "company-dongjian", CraftID: "craft-polypropylene", Wage: *seedValue(550, "confirmed", "user-wage-rules", "已确认工资规则", 0), Source: seedRef("user-wage-rules", "已确认工资规则", 0)},
			{CompanyID: "company-yiren", CraftID: "craft-polypropylene", Wage: *seedValue(550, "confirmed", "user-wage-rules", "已确认工资规则", 0), Source: seedRef("user-wage-rules", "已确认工资规则", 0)},
		},
		Products: []catalogSeedProduct{{
			ID: "temporary-toilet", Name: "临时马桶", Unit: "个", AuxUnit: "", AuxRatio: json.RawMessage("1"),
			Active: json.RawMessage("true"), WeightKG: json.RawMessage("0"), Source: seedRef("excel-prices", "一半设计", 20),
		}},
		CompanyProductPrices: []catalogSeedCompanyProductPrice{{
			ID: "company-toilet-dongjian", CompanyID: "company-dongjian", ProductID: "temporary-toilet", InputUnit: "base",
			Price: *seedValue(1500, "confirmed", "excel-prices", "一半设计", 20), Source: seedRef("excel-prices", "一半设计", 20), Note: "代购售价，不含工人工资",
		}},
		BonusRules: []catalogSeedBonusRule{
			{Key: "waterproof_split_two_coats", Category: "防水涂料", CraftName: "分开两遍防水工艺", Mode: "平方", RateCents: 50, Unit: "㎡", OverrideReason: "按已确认工资规则", Source: seedRef("user-wage-rules", "已确认工资规则", 0), TargetCraftID: "craft-v3-waterproof-split-two-coats"},
			{Key: "fine_start_wall_ground_wall", Category: "开工", CraftName: "精开工·墙地固", SubitemName: "墙", Mode: "平方", RateCents: 15, Unit: "㎡", OverrideReason: "按已确认工资规则", Source: seedRef("excel-wages", "开工工资", 15), TargetCraftID: "craft-v3-fine-wall-ground", TargetSubitemID: "fine-wall"},
		},
	}
}

func seedRef(source, sheet string, row int) catalogSeedReference {
	return catalogSeedReference{SourceID: source, Sheet: sheet, Row: row}
}

func seedValue(cents int64, classification, source, sheet string, row int) *catalogSeedValue {
	note := ""
	if classification == "test_only" {
		note = "test_only：仅用于测试缺失价格"
	}
	return &catalogSeedValue{Cents: cents, Classification: classification, Source: catalogSeedReference{SourceID: source, Sheet: sheet, Row: row, Note: note}}
}

func seedBool(value bool) *bool { return &value }

func TestParseCatalogSeedValidatesSourcesBonusRulesAndOverrides(t *testing.T) {
	data, err := json.Marshal(validCatalogSeedFixture())
	if err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := parseCatalogSeed(data)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || len(manifest.Companies) != 2 || len(manifest.Crafts) != 3 || len(manifest.CompanyProductPrices) != 1 || len(manifest.BonusRules) != 2 {
		t.Fatalf("解析后的基础资料不完整: digest=%q companies=%d crafts=%d company_product_prices=%d bonus=%d", digest, len(manifest.Companies), len(manifest.Crafts), len(manifest.CompanyProductPrices), len(manifest.BonusRules))
	}
}

func TestProjectCatalogSeedParsesWithoutDatabaseWrites(t *testing.T) {
	data, err := os.ReadFile("../catalog/catalog-seed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := parseCatalogSeed(data)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || len(manifest.Companies) != 13 || len(manifest.Crafts) < 67 || len(manifest.CompanyCrafts) < 21 || len(manifest.CompanyWageOverrides) != 2 {
		t.Fatalf("基础资料数量不符: companies=%d crafts=%d company_crafts=%d wage_overrides=%d", len(manifest.Companies), len(manifest.Crafts), len(manifest.CompanyCrafts), len(manifest.CompanyWageOverrides))
	}
	if len(manifest.Products) == 0 || len(manifest.CompanyProductPrices) == 0 || len(manifest.Packages) != 11 || len(manifest.PackageCoverage) != 74 || len(manifest.BonusRules) != 2 {
		t.Fatalf("商品、公司售价、套餐或奖金数量不符: products=%d company_product_prices=%d packages=%d coverage=%d bonuses=%d", len(manifest.Products), len(manifest.CompanyProductPrices), len(manifest.Packages), len(manifest.PackageCoverage), len(manifest.BonusRules))
	}
	yuanchuangCoverage := 0
	for _, item := range manifest.PackageCoverage {
		if strings.HasPrefix(item.PackageID, "package-yuanchuang-") {
			yuanchuangCoverage++
			if item.ItemName == "地" {
				t.Fatal("元创套餐原文不含地面，不能免收地面施工")
			}
		}
	}
	if yuanchuangCoverage != 18 {
		t.Fatalf("元创3档套餐应各有6项来源含项，got %d", yuanchuangCoverage)
	}
	var fineStart catalogSeedCraft
	for _, craft := range manifest.Crafts {
		if craft.ID == "craft-v3-fine-wall-ground" {
			fineStart = craft
			break
		}
	}
	if fineStart.Wage != nil || len(fineStart.Subitems) != 3 || fineStart.Subitems[0].Wage == nil {
		t.Fatal("精开工工资应按小项保存，不能折成整项工资")
	}
	runtimeSubitems := catalogSeedRuntimeSubitems(fineStart.Subitems)
	if runtimeSubitems[0].WageCents == nil || *runtimeSubitems[0].WageCents != 50 {
		t.Fatal("小项工资没有安全映射到运行时 CraftSubitem.WageCents")
	}
	selfAdhesiveSources := map[string]int{
		"唐卡": 18, "予里设计": 19, "顾意设计": 21, "友所设计": 20, "旭桥装饰": 20,
		"鼎格装饰": 15, "百姓家装饰": 20, "一半设计": 26, "元创设计": 18, "欧派装饰": 26,
	}
	for sheet, row := range selfAdhesiveSources {
		found := false
		for _, item := range manifest.CompanyCrafts {
			if item.CraftID == "craft-v3-membrane-self-adhesive" && item.Source.Sheet == sheet && item.Source.Row == row {
				if item.Price.Cents != 5500 || item.Price.Classification != "confirmed" || item.Price.Source.SourceID != "excel-prices" {
					t.Fatalf("自粘卷材公司收费金额/分类错误: %s row %d", sheet, row)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("遗漏有明确映射的自粘卷材客户价: %s row %d", sheet, row)
		}
	}
	ordinaryPolypropyleneRows := map[string]int{
		"宅心": 16, "唐卡": 16, "予里设计": 17, "顾意设计": 19, "友所设计": 18,
		"旭桥装饰": 18, "鼎格装饰": 13, "百姓家装饰": 18, "一半设计": 24, "元创设计": 16, "欧派装饰": 24,
	}
	seenOrdinaryPolypropylene := make(map[string]bool, len(ordinaryPolypropyleneRows))
	for _, item := range manifest.CompanyCrafts {
		if item.Price.Classification == "test_only" {
			t.Fatalf("V1初始化资料不能包含测试客户价: %s!%d", item.Source.Sheet, item.Source.Row)
		}
		if item.CraftID != "1ff20429655bace0933b240d1f5f9d59" {
			continue
		}
		row, ok := ordinaryPolypropyleneRows[item.Source.Sheet]
		if !ok || seenOrdinaryPolypropylene[item.Source.Sheet] || item.Source.Row != row || item.Price.Cents != 1200 || item.Price.Classification != "confirmed" || item.Price.Source.SourceID != "excel-prices" {
			t.Fatalf("原有丙纶客户价来源/工艺/金额不符: item=%+v", item)
		}
		seenOrdinaryPolypropylene[item.Source.Sheet] = true
	}
	if len(seenOrdinaryPolypropylene) != len(ordinaryPolypropyleneRows) {
		t.Fatalf("原有丙纶真实客户价数量不符: got=%d want=%d", len(seenOrdinaryPolypropylene), len(ordinaryPolypropyleneRows))
	}
	for _, item := range manifest.CompanyProductPrices {
		if item.Price.Classification == "test_only" {
			t.Fatalf("V1初始化资料不能包含测试商品价: %s!%d", item.Source.Sheet, item.Source.Row)
		}
	}
}

func TestProjectCatalogSeedCustomerPriceBackfill(t *testing.T) {
	data, err := os.ReadFile("../catalog/catalog-seed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := parseCatalogSeed(data)
	if err != nil {
		t.Fatalf("V1报价种子解析失败: %v", err)
	}
	if len(manifest.CompanyCrafts) != 115 {
		t.Fatalf("公司固定收费数量不符: got=%d want=115", len(manifest.CompanyCrafts))
	}
	if len(manifest.Products) != 2 || len(manifest.CompanyProductPrices) != 2 {
		t.Fatalf("一半设计材料/代购固定价不完整: products=%d prices=%d", len(manifest.Products), len(manifest.CompanyProductPrices))
	}
	if len(manifest.Packages) != 11 || len(manifest.PackageCoverage) != 74 {
		t.Fatalf("既有墙地固套餐被改动: packages=%d coverage=%d", len(manifest.Packages), len(manifest.PackageCoverage))
	}

	legacyCrafts := map[string]bool{
		"1ff20429655bace0933b240d1f5f9d59": true,
		"craft-v3-membrane-self-adhesive":   true,
	}
	newSourceRows := make(map[string]bool)
	for _, item := range manifest.CompanyCrafts {
		if item.Price.Classification == "test_only" {
			t.Fatalf("V1种子仍有测试客户价: %s!%d", item.Source.Sheet, item.Source.Row)
		}
		if !legacyCrafts[item.CraftID] {
			newSourceRows[item.Source.Sheet+"!"+strconv.Itoa(item.Source.Row)] = true
		}
	}
	if len(newSourceRows) != 92 {
		t.Fatalf("固定报价来源行数量不符: got=%d want=92", len(newSourceRows))
	}
	for _, unresolved := range []string{"宅心!6", "唐卡!6", "旭桥装饰!8", "元创设计!6"} {
		if newSourceRows[unresolved] {
			t.Fatalf("未决歧义行被错误导入固定收费: %s", unresolved)
		}
	}

	wantYiban := map[string]int64{
		"craft-v3-marking-house":                    10000,
		"craft-v3-door-protection-unit":             80000,
		"craft-v3-tile-protection":                    350,
		"craft-v3-safety-install-transport":         10000,
		"craft-v3-safety-frame-a":                    1500,
		"craft-v3-safety-frame-b":                    1500,
		"craft-v3-safety-net-large":                  5000,
		"craft-v3-safety-net-small":                  3000,
		"craft-v3-backfill-area":                     9000,
		"craft-v3-waterproof-combo-tooth-two-coats":  2600,
		"craft-v3-waterproof-extra-two-coats":        1500,
		"1ff20429655bace0933b240d1f5f9d59":           1200,
		"craft-v3-membrane-self-adhesive":            5500,
	}
	seenYiban := make(map[string]bool)
	for _, item := range manifest.CompanyCrafts {
		if item.CompanyID != "company-yiban" {
			continue
		}
		if want, ok := wantYiban[item.CraftID]; ok {
			if item.Price.Cents != want || item.Price.Classification != "confirmed" {
				t.Fatalf("一半设计客户价错误: craft=%s cents=%d want=%d", item.CraftID, item.Price.Cents, want)
			}
			seenYiban[item.CraftID] = true
		}
		if item.ID == "cc-v3-yiban-door" && !strings.Contains(item.Price.Source.Note, "包含人工") {
			t.Fatal("一半设计门保护未保留原表包含人工说明")
		}
	}
	if len(seenYiban) != len(wantYiban) {
		t.Fatalf("一半设计重点收费映射不完整: got=%d want=%d", len(seenYiban), len(wantYiban))
	}

	productPrices := make(map[string]int64)
	for _, item := range manifest.CompanyProductPrices {
		if item.CompanyID != "company-yiban" || item.InputUnit != "base" || item.Price.Classification != "confirmed" {
			t.Fatalf("材料/代购固定价身份错误: %+v", item)
		}
		productPrices[item.ProductID] = item.Price.Cents
	}
	if productPrices["product-v3-temporary-toilet"] != 1500 || productPrices["product-v3-electrical-box"] != 10000 {
		t.Fatalf("材料/代购固定价金额错误: %+v", productPrices)
	}

}

func TestParseCatalogSeedRejectsUnmarkedTestOnlyOrAlteredBonus(t *testing.T) {
	t.Run("test-only-not-marked", func(t *testing.T) {
		manifest := validCatalogSeedFixture()
		manifest.CompanyCrafts[0].Price.Source.Note = "缺失价格"
		if err := validateCatalogSeed(manifest); err == nil {
			t.Fatal("未标记 test_only 的模拟价格被接受")
		}
	})
	t.Run("simulated-wage", func(t *testing.T) {
		value := *seedValue(100, "test_only", "excel-wages", "开工工资", 15)
		if err := validateCatalogSeedValue(value, "wage", func(catalogSeedReference) error { return nil }); err == nil {
			t.Fatal("模拟工资标记不合规时应被拒绝")
		}
	})
	t.Run("changed-confirmed-bonus", func(t *testing.T) {
		manifest := validCatalogSeedFixture()
		manifest.BonusRules[0].RateCents = 15
		if err := validateCatalogSeed(manifest); err == nil {
			t.Fatal("与确认口径不同的自动奖金规则被接受")
		}
	})
	t.Run("extra-automatic-bonus", func(t *testing.T) {
		manifest := validCatalogSeedFixture()
		manifest.BonusRules = append(manifest.BonusRules, manifest.BonusRules[1])
		if err := validateCatalogSeed(manifest); err == nil {
			t.Fatal("未确认的自动奖金规则被接受")
		}
	})
	t.Run("independent-company-wage-override", func(t *testing.T) {
		manifest := validCatalogSeedFixture()
		manifest.CompanyCrafts[0].Price.Cents = 0
		manifest.CompanyCrafts[0].Price.Classification = "free"
		if err := validateCatalogSeed(manifest); err != nil {
			t.Fatalf("工资覆盖不应依赖公司客户收费关系: %v", err)
		}
	})
}

func TestCatalogSeedCompanyProductPriceValidation(t *testing.T) {
	valid := validCatalogSeedFixture()
	if err := validateCatalogSeed(valid); err != nil {
		t.Fatalf("有效公司商品固定价应通过校验: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*catalogSeedManifest)
	}{
		{name: "unknown-company", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].CompanyID = "missing-company" }},
		{name: "unknown-product", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].ProductID = "missing-product" }},
		{name: "unsupported-unit", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].InputUnit = "aux" }},
		{name: "source-row-mismatch", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].Price.Source.Row++ }},
		{name: "zero-confirmed-price", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].Price.Cents = 0 }},
		{name: "database-price-limit", mutate: func(m *catalogSeedManifest) { m.CompanyProductPrices[0].Price.Cents = 100000001 }},
		{name: "duplicate-company-product-unit", mutate: func(m *catalogSeedManifest) {
			duplicate := m.CompanyProductPrices[0]
			duplicate.ID = "duplicate-product-price"
			m.CompanyProductPrices = append(m.CompanyProductPrices, duplicate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validCatalogSeedFixture()
			test.mutate(&manifest)
			if err := validateCatalogSeed(manifest); err == nil {
				t.Fatal("非法公司商品固定价被接受")
			}
		})
	}
}

func TestCatalogSeedAllowsActiveCraftWithPendingWage(t *testing.T) {
	manifest := validCatalogSeedFixture()
	active := true
	manifest.Crafts = append(manifest.Crafts, catalogSeedCraft{
		ID: "craft-pending-wage", Category: "开工", Name: "待配置工价施工", Mode: "套", Active: &active,
		Subitems: []catalogSeedSubitem{{ID: "pending-subitem", Name: "待配置小项"}}, Source: seedRef("excel-prices", "一半设计", 15),
	})
	if err := validateCatalogSeed(manifest); err != nil {
		t.Fatalf("客户收费已知但工资待配置的启用工艺应可导入: %v", err)
	}
	runtime := catalogSeedRuntimeSubitems(manifest.Crafts[len(manifest.Crafts)-1].Subitems)
	if runtime[0].WageCents != nil {
		t.Fatalf("待配置小项工资不能转换成零工资: %v", *runtime[0].WageCents)
	}
}

func TestCatalogSeedPackageRelationCanReferForwardWithinSameCompany(t *testing.T) {
	manifest := validCatalogSeedFixture()
	manifest.Crafts = append(manifest.Crafts, catalogSeedCraft{
		ID: "craft-waterproof", Category: "防水涂料", Name: "连二遍防水", Mode: "平方", Active: boolPointer(true),
		Subitems: []catalogSeedSubitem{}, Wage: seedValue(350, "confirmed", "excel-wages", "防水工资", 3), Source: seedRef("excel-wages", "防水工资", 3),
	})
	manifest.CompanyCrafts = []catalogSeedCompanyCraft{
		{ID: "included-poly", CompanyID: "company-dongjian", CraftID: "craft-polypropylene", IncludedByCraftID: "waterproof-main", Price: *seedValue(0, "included", "excel-prices", "组合", 7), Source: seedRef("excel-prices", "组合", 7)},
		{ID: "waterproof-main", CompanyID: "company-dongjian", CraftID: "craft-waterproof", Price: *seedValue(2500, "confirmed", "excel-prices", "组合", 6), Source: seedRef("excel-prices", "组合", 6)},
	}
	if err := validateCatalogSeed(manifest); err != nil {
		t.Fatalf("有效的前向套餐关联应通过校验: %v", err)
	}
}

func TestParseCatalogSeedRejectsUnknownFieldsTrailingJSONAndInvalidUTF8(t *testing.T) {
	data, err := json.Marshal(validCatalogSeedFixture())
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		append(append([]byte(nil), data...), []byte(` {}`)...),
		[]byte(strings.Replace(string(data), `"seed_id"`, `"unknown":"x","seed_id"`, 1)),
		append(append([]byte(nil), data...), 0xff),
	}
	for index, input := range cases {
		if _, _, err := parseCatalogSeed(input); err == nil {
			t.Fatalf("非法输入 %d 被接受", index)
		}
	}
}

func boolPointer(value bool) *bool { return &value }
