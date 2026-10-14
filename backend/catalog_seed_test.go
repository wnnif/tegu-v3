package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

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
	if digest == "" || len(manifest.Companies) != 2 || len(manifest.Crafts) != 3 || len(manifest.BonusRules) != 2 {
		t.Fatalf("解析后的基础资料不完整: digest=%q companies=%d crafts=%d bonus=%d", digest, len(manifest.Companies), len(manifest.Crafts), len(manifest.BonusRules))
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
	if digest == "" || len(manifest.Companies) != 13 || len(manifest.Crafts) != 67 || len(manifest.CompanyCrafts) != 39 || len(manifest.CompanyWageOverrides) != 2 {
		t.Fatalf("基础资料数量不符: companies=%d crafts=%d company_crafts=%d wage_overrides=%d", len(manifest.Companies), len(manifest.Crafts), len(manifest.CompanyCrafts), len(manifest.CompanyWageOverrides))
	}
	if len(manifest.Products) != 0 || len(manifest.Packages) != 11 || len(manifest.PackageCoverage) != 74 || len(manifest.BonusRules) != 2 {
		t.Fatalf("商品、套餐或奖金数量不符: products=%d packages=%d coverage=%d bonuses=%d", len(manifest.Products), len(manifest.Packages), len(manifest.PackageCoverage), len(manifest.BonusRules))
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
	testOnlySources := map[string]struct {
		companyID string
		craftID   string
	}{
		"宅心:7":    {"company-zhai-xin", "a317b42faeb2ee6f317ffac535b43799"},
		"宅心:8":    {"company-zhai-xin", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"唐卡:7":    {"company-tangka", "a317b42faeb2ee6f317ffac535b43799"},
		"予里设计:7":  {"company-yuli", "a317b42faeb2ee6f317ffac535b43799"},
		"予里设计:8":  {"company-yuli", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"顾意设计:8":  {"company-guyi", "a317b42faeb2ee6f317ffac535b43799"},
		"顾意设计:9":  {"company-guyi", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"友所设计:8":  {"company-yousuo", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"友所设计:9":  {"company-yousuo", "c8e29907887b47448aa42972f2427a1a"},
		"旭桥装饰:9":  {"company-xuqiao", "a317b42faeb2ee6f317ffac535b43799"},
		"旭桥装饰:10": {"company-xuqiao", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"鼎格装饰:7":  {"company-dingge", "a317b42faeb2ee6f317ffac535b43799"},
		"鼎格装饰:8":  {"company-dingge", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"百姓家装饰:7": {"company-baixingjia", "a317b42faeb2ee6f317ffac535b43799"},
		"百姓家装饰:8": {"company-baixingjia", "12c57d091d3c9006acafe59a7b0b3f8a"},
		"百姓家装饰:9": {"company-baixingjia", "c8e29907887b47448aa42972f2427a1a"},
		"元创设计:8":  {"company-yuanchuang", "a317b42faeb2ee6f317ffac535b43799"},
		"元创设计:9":  {"company-yuanchuang", "12c57d091d3c9006acafe59a7b0b3f8a"},
	}
	seenTestOnly := make(map[string]bool, len(testOnlySources))
	for _, item := range manifest.CompanyCrafts {
		if item.Price.Classification != "test_only" {
			continue
		}
		key := item.Source.Sheet + ":" + strconv.Itoa(item.Source.Row)
		want, ok := testOnlySources[key]
		if !ok || seenTestOnly[key] || item.CompanyID != want.companyID || item.CraftID != want.craftID || item.Price.Cents != 100 || item.Price.Source.SourceID != "excel-prices" || item.Price.Source.Sheet != item.Source.Sheet || item.Price.Source.Row != item.Source.Row || !strings.Contains(item.Price.Source.Note, "test_only") || !strings.Contains(item.Source.Note, "㎡") {
			t.Fatalf("缺价模拟价来源/工艺/金额不符: key=%q item=%+v", key, item)
		}
		seenTestOnly[key] = true
	}
	if len(seenTestOnly) != len(testOnlySources) {
		t.Fatalf("缺价模拟价数量不符: got=%d want=%d", len(seenTestOnly), len(testOnlySources))
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
