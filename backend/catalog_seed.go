package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	catalogSeedFormat           = "tegu-catalog-seed-v1"
	catalogSeedMigrationVersion = 452
	maxCatalogSeedFileBytes     = 16 * 1024 * 1024
)

var catalogSeedIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var catalogSeedHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type catalogSeedManifest struct {
	Format               string                           `json:"format"`
	SeedID               string                           `json:"seed_id"`
	Sources              []catalogSeedSource              `json:"sources"`
	Companies            []catalogSeedCompany             `json:"companies"`
	Crafts               []catalogSeedCraft               `json:"crafts"`
	CompanyCrafts        []catalogSeedCompanyCraft        `json:"company_crafts"`
	CompanyWageOverrides []catalogSeedCompanyWageOverride `json:"company_wage_overrides,omitempty"`
	Products             []catalogSeedProduct             `json:"products,omitempty"`
	CompanyProductPrices []catalogSeedCompanyProductPrice `json:"company_product_prices,omitempty"`
	Packages             []catalogSeedPackage             `json:"packages,omitempty"`
	PackageCoverage      []catalogSeedPackageCoverage     `json:"package_coverage,omitempty"`
	BonusRules           []catalogSeedBonusRule           `json:"bonus_rules"`
}

type catalogSeedSource struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type catalogSeedReference struct {
	SourceID string `json:"source_id"`
	Sheet    string `json:"sheet"`
	Row      int    `json:"row"`
	Note     string `json:"note,omitempty"`
}

type catalogSeedValue struct {
	Cents          int64                `json:"cents"`
	Classification string               `json:"classification"`
	Source         catalogSeedReference `json:"source"`
}

type catalogSeedCompany struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	CustomerType string               `json:"customer_type,omitempty"`
	TicketMode   string               `json:"ticket_mode,omitempty"`
	Source       catalogSeedReference `json:"source"`
}

type catalogSeedCraft struct {
	ID       string               `json:"id"`
	Category string               `json:"category"`
	Name     string               `json:"name"`
	Mode     string               `json:"mode"`
	Spec     string               `json:"spec,omitempty"`
	Active   *bool                `json:"active"`
	Subitems []catalogSeedSubitem `json:"subitems"`
	Wage     *catalogSeedValue    `json:"wage"`
	Source   catalogSeedReference `json:"source"`
}

type catalogSeedSubitem struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Wage *catalogSeedValue `json:"wage,omitempty"`
}

type catalogSeedCompanyCraft struct {
	ID                string               `json:"id"`
	CompanyID         string               `json:"company_id"`
	CraftID           string               `json:"craft_id"`
	Price             catalogSeedValue     `json:"price"`
	PricingClass      string               `json:"pricing_class,omitempty"`
	TicketSlot        int                  `json:"ticket_slot,omitempty"`
	IncludedByCraftID string               `json:"included_by_craft_id,omitempty"`
	Note              string               `json:"note,omitempty"`
	Source            catalogSeedReference `json:"source"`
}

type catalogSeedCompanyWageOverride struct {
	CompanyID string               `json:"company_id"`
	CraftID   string               `json:"craft_id"`
	Wage      catalogSeedValue     `json:"wage"`
	Source    catalogSeedReference `json:"source"`
}

type catalogSeedCompanyProductPrice struct {
	ID        string               `json:"id"`
	CompanyID string               `json:"company_id"`
	ProductID string               `json:"product_id"`
	InputUnit string               `json:"input_unit"`
	Price     catalogSeedValue     `json:"price"`
	Source    catalogSeedReference `json:"source"`
	Note      string               `json:"note,omitempty"`
}

type catalogSeedProduct struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Unit     string               `json:"unit"`
	AuxUnit  string               `json:"aux_unit"`
	AuxRatio json.RawMessage      `json:"aux_ratio"`
	Active   json.RawMessage      `json:"active"`
	Color    string               `json:"color"`
	WeightKG json.RawMessage      `json:"weight_kg"`
	Brand    string               `json:"brand"`
	Spec     string               `json:"spec"`
	Note     string               `json:"note"`
	Source   catalogSeedReference `json:"source"`
}

type catalogSeedPackage struct {
	ID          string               `json:"id"`
	CompanyID   string               `json:"company_id"`
	Name        string               `json:"name"`
	PackageType string               `json:"package_type,omitempty"`
	RoomCount   int                  `json:"room_count"`
	Price       catalogSeedValue     `json:"price"`
	Source      catalogSeedReference `json:"source"`
	Note        string               `json:"note,omitempty"`
}

const catalogSeedPackageTypeMigrationVersion = 458

type catalogSeedPackageCoverage struct {
	PackageID string               `json:"package_id"`
	ItemName  string               `json:"item_name"`
	CraftID   string               `json:"craft_id,omitempty"`
	SubitemID string               `json:"subitem_id,omitempty"`
	Mapping   string               `json:"mapping"`
	Source    catalogSeedReference `json:"source"`
	Note      string               `json:"note,omitempty"`
}

type catalogSeedBonusRule struct {
	Key             string                 `json:"key"`
	Category        string                 `json:"category"`
	CraftName       string                 `json:"craft_name"`
	SubitemName     string                 `json:"subitem_name,omitempty"`
	Mode            string                 `json:"mode"`
	RateCents       int64                  `json:"rate_cents"`
	Unit            string                 `json:"unit"`
	OverrideReason  string                 `json:"override_reason"`
	Source          catalogSeedReference   `json:"source"`
	Supersedes      []catalogSeedReference `json:"supersedes,omitempty"`
	TargetCraftID   string                 `json:"target_craft_id"`
	TargetSubitemID string                 `json:"target_subitem_id,omitempty"`
}

type catalogSeedValueDetail struct {
	Cents          int64  `json:"cents"`
	Classification string `json:"classification"`
	Note           string `json:"note,omitempty"`
}

// migrateCatalogSeed is a schema-only migration. The caller registers version
// 452 from initSchema; importing catalog data is a separate explicit command.
func migrateCatalogSeed(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='crafts' AND COLUMN_NAME='wage_override_cents'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE crafts ADD COLUMN wage_override_cents BIGINT NULL"); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS catalog_seed_imports(
		seed_id VARCHAR(100) PRIMARY KEY,
		manifest_digest CHAR(64) NOT NULL,
		sources JSON NOT NULL,
		entry_counts JSON NOT NULL,
		imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS catalog_seed_provenance(
		seed_id VARCHAR(100) NOT NULL,
		target_kind VARCHAR(30) NOT NULL,
		target_id VARCHAR(100) NOT NULL,
		component VARCHAR(40) NOT NULL,
		classification VARCHAR(24) NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		source_file VARCHAR(255) NOT NULL DEFAULT '',
		source_sheet VARCHAR(100) NOT NULL,
		source_row INT NOT NULL DEFAULT 0,
		test_only BOOLEAN NOT NULL DEFAULT 0,
		detail JSON NOT NULL,
		PRIMARY KEY(seed_id,target_kind,target_id,component),
		INDEX idx_catalog_seed_target(target_kind,target_id),
		CONSTRAINT fk_catalog_seed_provenance_seed FOREIGN KEY(seed_id) REFERENCES catalog_seed_imports(seed_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS catalog_seed_packages(
		seed_id VARCHAR(100) NOT NULL,
		package_id VARCHAR(64) NOT NULL,
		company_id VARCHAR(64) NOT NULL,
		name VARCHAR(200) NOT NULL,
		package_type VARCHAR(20) NOT NULL DEFAULT 'room_count',
		room_count INT NOT NULL,
		price_cents BIGINT NOT NULL,
		classification VARCHAR(24) NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		source_sheet VARCHAR(100) NOT NULL,
		source_row INT NOT NULL,
		note VARCHAR(1000) NOT NULL DEFAULT '',
		PRIMARY KEY(seed_id,package_id),
		INDEX idx_catalog_seed_package_company(company_id,room_count),
		CONSTRAINT fk_catalog_seed_packages_seed FOREIGN KEY(seed_id) REFERENCES catalog_seed_imports(seed_id),
		CONSTRAINT fk_catalog_seed_packages_company FOREIGN KEY(company_id) REFERENCES companies(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var packageTypeColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='catalog_seed_packages' AND COLUMN_NAME='package_type'`).Scan(&packageTypeColumnCount); err != nil {
		return err
	}
	if packageTypeColumnCount == 0 {
		if _, err := db.Exec(`ALTER TABLE catalog_seed_packages ADD COLUMN package_type VARCHAR(20) NOT NULL DEFAULT 'room_count' AFTER name`); err != nil {
			return err
		}
	}
	if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", catalogSeedPackageTypeMigrationVersion); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS catalog_seed_package_coverage(
		seed_id VARCHAR(100) NOT NULL,
		package_id VARCHAR(64) NOT NULL,
		item_name VARCHAR(100) NOT NULL,
		craft_id VARCHAR(64) NOT NULL DEFAULT '',
		subitem_id VARCHAR(64) NOT NULL DEFAULT '',
		mapping VARCHAR(24) NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		source_sheet VARCHAR(100) NOT NULL,
		source_row INT NOT NULL,
		note VARCHAR(1000) NOT NULL DEFAULT '',
		PRIMARY KEY(seed_id,package_id,item_name),
		INDEX idx_catalog_seed_coverage_craft(craft_id,subitem_id),
		CONSTRAINT fk_catalog_seed_coverage_package FOREIGN KEY(seed_id,package_id) REFERENCES catalog_seed_packages(seed_id,package_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", catalogSeedMigrationVersion)
	return err
}

func parseCatalogSeed(data []byte) (catalogSeedManifest, string, error) {
	var manifest catalogSeedManifest
	if len(data) == 0 || len(data) > maxCatalogSeedFileBytes {
		return manifest, "", fail(413, "基础资料文件为空或超过16MB")
	}
	if !utf8.Valid(data) {
		return manifest, "", fail(400, "基础资料文件必须是有效UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", fail(400, "基础资料文件格式无效")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return manifest, "", fail(400, "基础资料文件包含多余内容")
	}
	if err := validateCatalogSeed(manifest); err != nil {
		return manifest, "", err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return manifest, "", fail(400, "基础资料文件无法规范化")
	}
	digest := sha256.Sum256(canonical)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func validateCatalogSeed(manifest catalogSeedManifest) error {
	if manifest.Format != catalogSeedFormat || !catalogSeedIDPattern.MatchString(manifest.SeedID) {
		return fail(400, "基础资料格式或版本编号无效")
	}
	if len(manifest.Sources) == 0 || len(manifest.Companies) == 0 || len(manifest.Crafts) == 0 {
		return fail(400, "基础资料缺少来源、公司或工艺")
	}
	sources := make(map[string]catalogSeedSource, len(manifest.Sources))
	for _, source := range manifest.Sources {
		if !catalogSeedIDPattern.MatchString(source.ID) {
			return fail(400, "基础资料来源编号无效")
		}
		if _, exists := sources[source.ID]; exists {
			return fail(400, "基础资料来源编号重复")
		}
		switch source.Kind {
		case "excel":
			if source.File == "" || filepath.Base(source.File) != source.File || strings.ContainsAny(source.File, `/\`) || !catalogSeedHashPattern.MatchString(source.SHA256) {
				return fail(400, "Excel来源文件名或摘要无效")
			}
		case "user":
			if source.File != "" || source.SHA256 != "" {
				return fail(400, "用户确认来源不能伪装成Excel文件")
			}
		default:
			return fail(400, "基础资料来源类型无效")
		}
		sources[source.ID] = source
	}
	validateRef := func(ref catalogSeedReference) error {
		source, ok := sources[ref.SourceID]
		if !ok || strings.TrimSpace(ref.Sheet) == "" || len([]rune(ref.Sheet)) > 100 || len([]rune(ref.Note)) > 1000 {
			return fail(400, "基础资料来源定位不完整")
		}
		if source.Kind == "excel" && ref.Row < 1 || source.Kind == "user" && ref.Row != 0 {
			return fail(400, "基础资料来源行号无效")
		}
		return nil
	}
	companyIDs, companyNames := map[string]bool{}, map[string]bool{}
	companyTicketModes := make(map[string]string, len(manifest.Companies))
	for _, company := range manifest.Companies {
		if !validCatalogSeedID(company.ID) || strings.TrimSpace(company.Name) != company.Name || company.Name == "" || len([]rune(company.Name)) > 200 || companyIDs[company.ID] || companyNames[company.Name] {
			return fail(400, "基础资料公司编号或名称无效、重复")
		}
		if company.TicketMode != "" && !contains([]string{"single", "split"}, company.TicketMode) {
			return fail(400, "公司交票方式无效")
		}
		if err := validateRef(company.Source); err != nil {
			return err
		}
		companyIDs[company.ID], companyNames[company.Name] = true, true
		ticketMode := company.TicketMode
		if ticketMode == "" {
			ticketMode = "single"
		}
		companyTicketModes[company.ID] = ticketMode
	}
	crafts := make(map[string]catalogSeedCraft, len(manifest.Crafts))
	for _, craft := range manifest.Crafts {
		if !validCatalogSeedID(craft.ID) || crafts[craft.ID].ID != "" || !contains(categories, craft.Category) || !validCraftMode(craft.Mode) || strings.TrimSpace(craft.Name) != craft.Name || craft.Name == "" || len([]rune(craft.Name)) > 200 || len([]rune(craft.Spec)) > 200 || craft.Active == nil {
			return fail(400, "基础资料工艺编号、分类、单位、状态或名称无效")
		}
		if err := validateRef(craft.Source); err != nil {
			return err
		}
		if len(craft.Subitems) > 100 {
			return fail(400, "基础资料工艺小项超过100个")
		}
		seenIDs, seenNames := map[string]bool{}, map[string]bool{}
		for _, item := range craft.Subitems {
			if !validCatalogSeedID(item.ID) || strings.TrimSpace(item.Name) != item.Name || item.Name == "" || len([]rune(item.Name)) > 100 || seenIDs[item.ID] || seenNames[item.Name] {
				return fail(400, "基础资料工艺小项编号或名称无效、重复")
			}
			if item.Wage != nil {
				if err := validateCatalogSeedValue(*item.Wage, "wage", validateRef); err != nil {
					return err
				}
			}
			seenIDs[item.ID], seenNames[item.Name] = true, true
		}
		if craft.Wage != nil {
			if err := validateCatalogSeedValue(*craft.Wage, "wage", validateRef); err != nil {
				return err
			}
			for _, item := range craft.Subitems {
				if item.Wage != nil {
					return fail(400, "整项工资与小项工资不能同时配置")
				}
			}
		}
		crafts[craft.ID] = craft
	}
	companyCraftIDs, companySources := map[string]bool{}, map[string]bool{}
	companyCraftByID := make(map[string]catalogSeedCompanyCraft, len(manifest.CompanyCrafts))
	for _, item := range manifest.CompanyCrafts {
		_, ok := crafts[item.CraftID]
		if !validCatalogSeedID(item.ID) || companyCraftIDs[item.ID] || !companyIDs[item.CompanyID] || !ok || item.Source.SourceID == "" {
			return fail(400, "公司固定工艺编号或关联无效")
		}
		if err := validateRef(item.Source); err != nil {
			return err
		}
		if err := validateCatalogSeedValue(item.Price, "customer_price", validateRef); err != nil {
			return err
		}
		if item.Price.Classification == "included" && item.IncludedByCraftID == "" || item.Price.Classification != "included" && item.IncludedByCraftID != "" {
			return fail(400, "含价工艺关联说明与价格类型不一致")
		}
		if item.TicketSlot < 0 || item.TicketSlot > 2 || len([]rune(item.Note)) > 1000 {
			return fail(400, "公司固定工艺交票分组或备注无效")
		}
		if companyTicketModes[item.CompanyID] == "single" && item.TicketSlot > 1 {
			return fail(400, "统一交票公司不能把固定工艺分入第二张票")
		}
		pair := item.CompanyID + "\x00" + item.CraftID
		if companySources[pair] {
			return fail(400, "同一公司不能重复导入同一来源工艺")
		}
		companyCraftIDs[item.ID], companySources[pair] = true, true
		companyCraftByID[item.ID] = item
	}
	for _, item := range manifest.CompanyCrafts {
		if item.IncludedByCraftID == "" {
			continue
		}
		parent, ok := companyCraftByID[item.IncludedByCraftID]
		if !ok || parent.CompanyID != item.CompanyID || parent.ID == item.ID || parent.IncludedByCraftID != "" {
			return fail(400, "含价工艺必须关联同公司的独立收费主工艺")
		}
		childSlot, parentSlot := item.TicketSlot, parent.TicketSlot
		if childSlot == 0 {
			childSlot = 1
		}
		if parentSlot == 0 {
			parentSlot = 1
		}
		if childSlot != parentSlot {
			return fail(400, "组合内工艺必须安排在同一张票")
		}
		if err := validateWaterproofCombination(crafts[item.CraftID].Category, crafts[parent.CraftID].Category); err != nil {
			return err
		}
	}
	if err := validateCatalogSeedCompanyWageOverrides(manifest, crafts, validateRef); err != nil {
		return err
	}
	products, err := validateCatalogSeedProducts(manifest.Products, validateRef)
	if err != nil {
		return err
	}
	if err := validateCatalogSeedCompanyProductPrices(manifest.CompanyProductPrices, companyIDs, products, validateRef); err != nil {
		return err
	}
	if err := validateCatalogSeedPackages(manifest, companyNamesByID(manifest.Companies), crafts, validateRef); err != nil {
		return err
	}
	for _, rule := range manifest.BonusRules {
		if !catalogSeedIDPattern.MatchString(rule.Key) || rule.Category == "" || rule.CraftName == "" || rule.Mode != "平方" || rule.Unit != "㎡" || rule.RateCents <= 0 || strings.TrimSpace(rule.OverrideReason) == "" || !validCatalogSeedID(rule.TargetCraftID) {
			return fail(400, "奖金规则配置不完整")
		}
		craft, ok := crafts[rule.TargetCraftID]
		if !ok || craft.Category != rule.Category || craft.Name != rule.CraftName || craft.Mode != rule.Mode || !*craft.Active {
			return fail(400, "奖金规则没有匹配启用的目标工艺")
		}
		if rule.TargetSubitemID == "" {
			if rule.SubitemName != "" || craft.Wage == nil {
				return fail(400, "整项奖金规则目标不完整")
			}
		} else {
			if !validCatalogSeedID(rule.TargetSubitemID) || rule.SubitemName == "" || craft.Wage != nil {
				return fail(400, "小项奖金规则目标不完整")
			}
			matched := false
			for _, item := range craft.Subitems {
				matched = matched || item.ID == rule.TargetSubitemID && item.Name == rule.SubitemName && item.Wage != nil
			}
			if !matched {
				return fail(400, "奖金规则未匹配唯一来源工艺小项")
			}
		}
		if err := validateRef(rule.Source); err != nil {
			return err
		}
		for _, source := range rule.Supersedes {
			if err := validateRef(source); err != nil {
				return err
			}
		}
	}
	if len(manifest.BonusRules) != 2 || !containsApprovedSeedBonus(manifest.BonusRules) {
		return fail(400, "本版本奖金配置须包含两条已确认的自动奖金规则")
	}
	return nil
}

func validateCatalogSeedValue(value catalogSeedValue, component string, validateRef func(catalogSeedReference) error) error {
	if err := validateRef(value.Source); err != nil {
		return err
	}
	switch value.Classification {
	case "confirmed":
		if value.Cents <= 0 || component == "customer_price" && value.Cents > 10000000000 {
			return fail(400, "已确认价格金额无效")
		}
	case "free":
		if value.Cents != 0 || component != "customer_price" {
			return fail(400, "免费标记只能用于零客户收费")
		}
	case "included":
		if value.Cents != 0 || component != "customer_price" {
			return fail(400, "套餐包含标记只能用于零客户收费")
		}
	case "package":
		if value.Cents <= 0 || component != "customer_price" {
			return fail(400, "套餐收费必须是有来源的客户价格")
		}
	case "test_only":
		if value.Cents < 100 || component != "customer_price" || !strings.Contains(strings.ToLower(value.Source.Note), "test_only") {
			return fail(400, "模拟值仅允许作为有明确标记的正数客户收费")
		}
	default:
		return fail(400, "基础资料价格分类无效")
	}
	return nil
}

func containsApprovedSeedBonus(rules []catalogSeedBonusRule) bool {
	want := map[string]catalogSeedBonusRule{
		"waterproof_split_two_coats":  {Key: "waterproof_split_two_coats", Category: "防水涂料", CraftName: "分开两遍防水工艺", Mode: "平方", RateCents: 50, Unit: "㎡", Source: catalogSeedReference{SourceID: "user-wage-rules", Sheet: "已确认工资规则", Row: 0}, TargetCraftID: "craft-v3-waterproof-split-two-coats"},
		"fine_start_wall_ground_wall": {Key: "fine_start_wall_ground_wall", Category: "开工", CraftName: "精开工·墙地固", SubitemName: "墙", Mode: "平方", RateCents: 15, Unit: "㎡", Source: catalogSeedReference{SourceID: "excel-wages", Sheet: "开工工资", Row: 15}, TargetCraftID: "craft-v3-fine-wall-ground", TargetSubitemID: "fine-wall"},
	}
	seen := map[string]bool{}
	for _, rule := range rules {
		expected, ok := want[rule.Key]
		if !ok || seen[rule.Key] || rule.Category != expected.Category || rule.CraftName != expected.CraftName || rule.SubitemName != expected.SubitemName || rule.Mode != expected.Mode || rule.RateCents != expected.RateCents || rule.Unit != expected.Unit || rule.Source.SourceID != expected.Source.SourceID || rule.Source.Sheet != expected.Source.Sheet || rule.Source.Row != expected.Source.Row || rule.TargetCraftID != expected.TargetCraftID || rule.TargetSubitemID != expected.TargetSubitemID || strings.TrimSpace(rule.OverrideReason) == "" {
			return false
		}
		seen[rule.Key] = true
	}
	return len(seen) == len(want)
}

func companyNamesByID(companies []catalogSeedCompany) map[string]string {
	names := make(map[string]string, len(companies))
	for _, company := range companies {
		names[company.ID] = company.Name
	}
	return names
}

func validateCatalogSeedCompanyWageOverrides(manifest catalogSeedManifest, crafts map[string]catalogSeedCraft, validateRef func(catalogSeedReference) error) error {
	companyNames := companyNamesByID(manifest.Companies)
	ordinaryPolypropylene := make(map[string]bool)
	for id, craft := range crafts {
		if *craft.Active && craft.Category == "丙纶" && craft.Mode == "平方" && !strings.Contains(craft.Name, "自粘") && !strings.Contains(craft.Name, "加厚") {
			ordinaryPolypropylene[id] = true
		}
	}
	seen := make(map[string]bool)
	for _, item := range manifest.CompanyWageOverrides {
		companyName := companyNames[item.CompanyID]
		if !companyIDsContain(manifest.Companies, item.CompanyID) || !ordinaryPolypropylene[item.CraftID] || (companyName != "东箭" && companyName != "宜仁") || item.Wage.Cents != 550 || item.Wage.Classification != "confirmed" || item.Wage.Source.SourceID != "user-wage-rules" || item.Wage.Source.Sheet != "已确认工资规则" || item.Wage.Source.Row != 0 || item.Source.SourceID != "user-wage-rules" || item.Source.Sheet != "已确认工资规则" || item.Source.Row != 0 {
			return fail(400, "公司工资覆盖仅允许东箭、宜仁普通丙纶550分/㎡，并须引用已确认规则")
		}
		if err := validateCatalogSeedValue(item.Wage, "wage", validateRef); err != nil {
			return err
		}
		if err := validateRef(item.Source); err != nil {
			return err
		}
		key := companyName + "\x00" + item.CraftID
		if seen[key] {
			return fail(400, "公司普通丙纶工资覆盖重复")
		}
		seen[key] = true
	}
	for _, companyName := range []string{"东箭", "宜仁"} {
		companyFound := false
		for _, name := range companyNames {
			companyFound = companyFound || name == companyName
		}
		if !companyFound {
			return fail(400, "基础资料缺少东箭或宜仁公司")
		}
		for craftID := range ordinaryPolypropylene {
			if !seen[companyName+"\x00"+craftID] {
				return fail(400, "东箭、宜仁全部普通丙纶须配置550分/㎡工资覆盖")
			}
		}
	}
	return nil
}

func companyIDsContain(companies []catalogSeedCompany, id string) bool {
	for _, company := range companies {
		if company.ID == id {
			return true
		}
	}
	return false
}

func validateCatalogSeedProducts(products []catalogSeedProduct, validateRef func(catalogSeedReference) error) ([]validatedProductCatalogEntry, error) {
	if len(products) == 0 {
		return nil, nil
	}
	input := productCatalogImport{Format: "tegu-product-catalog-v1", Products: make([]productCatalogImportEntry, 0, len(products))}
	for _, product := range products {
		if err := validateRef(product.Source); err != nil {
			return nil, err
		}
		input.Products = append(input.Products, productCatalogImportEntry{
			ID: product.ID, Name: product.Name, Unit: product.Unit, AuxUnit: product.AuxUnit,
			AuxRatio: product.AuxRatio, Active: product.Active, Color: product.Color,
			WeightKG: product.WeightKG, Brand: product.Brand, Spec: product.Spec, Note: product.Note,
		})
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fail(400, "商品基础资料无法规范化")
	}
	return parseProductCatalog(data)
}

func validateCatalogSeedCompanyProductPrices(prices []catalogSeedCompanyProductPrice, companyIDs map[string]bool, products []validatedProductCatalogEntry, validateRef func(catalogSeedReference) error) error {
	if len(prices) == 0 {
		return nil
	}
	productByID := make(map[string]productEntityInput, len(products))
	for _, product := range products {
		productByID[product.input.ID] = product.input
	}
	seenIDs := make(map[string]bool, len(prices))
	seenKeys := make(map[string]bool, len(prices))
	for _, item := range prices {
		if !validCatalogSeedID(item.ID) || seenIDs[item.ID] || !companyIDs[item.CompanyID] {
			return fail(400, "公司商品固定价编号或公司关联无效、重复")
		}
		product, ok := productByID[item.ProductID]
		if !ok || item.InputUnit != "base" && item.InputUnit != "aux" || item.InputUnit == "aux" && product.AuxUnit == "" {
			return fail(400, "公司商品固定价商品或计价单位无效")
		}
		if len([]rune(item.Note)) > 1000 {
			return fail(400, "公司商品固定价备注过长")
		}
		if err := validateRef(item.Source); err != nil {
			return err
		}
		if item.Source.SourceID != item.Price.Source.SourceID || item.Source.Sheet != item.Price.Source.Sheet || item.Source.Row != item.Price.Source.Row {
			return fail(400, "公司商品固定价与来源行不一致")
		}
		if item.Price.Source.Row <= 0 {
			return fail(400, "公司商品固定价须保留原始Excel行号")
		}
		if item.Price.Cents > 100000000 {
			return fail(400, "公司商品固定价超过数据库金额上限")
		}
		if err := validateCatalogSeedValue(item.Price, "customer_price", validateRef); err != nil {
			return err
		}
		key := item.CompanyID + "\x00" + item.ProductID + "\x00" + item.InputUnit
		if seenKeys[key] {
			return fail(400, "同一公司商品计价单位的固定价重复")
		}
		seenIDs[item.ID], seenKeys[key] = true, true
	}
	return nil
}

func validateCatalogSeedPackages(manifest catalogSeedManifest, companyNames map[string]string, crafts map[string]catalogSeedCraft, validateRef func(catalogSeedReference) error) error {
	if len(manifest.Packages) == 0 && len(manifest.PackageCoverage) == 0 {
		return nil
	}
	type approvedPackage struct {
		company string
		room    int
		cents   int64
		sheet   string
		row     int
	}
	approved := map[string]approvedPackage{}
	for _, item := range []struct {
		company string
		sheet   string
		rooms   []int
		prices  []int64
		rows    []int
	}{
		{company: "company-xuqiao", sheet: "旭桥装饰", rooms: []int{2, 3, 4, 5}, prices: []int64{35000, 50000, 65000, 75000}, rows: []int{3, 4, 5, 6}},
		{company: "company-opai", sheet: "欧派装饰", rooms: []int{2, 3, 4, 5}, prices: []int64{35000, 50000, 65000, 75000}, rows: []int{3, 4, 5, 6}},
		{company: "company-yuanchuang", sheet: "元创设计", rooms: []int{3, 4, 5}, prices: []int64{35000, 45000, 55000}, rows: []int{3, 4, 5}},
	} {
		for i, room := range item.rooms {
			id := fmt.Sprintf("package-%s-%d-room", strings.TrimPrefix(item.company, "company-"), room)
			approved[id] = approvedPackage{company: item.company, room: room, cents: item.prices[i], sheet: item.sheet, row: item.rows[i]}
		}
	}
	if len(manifest.Packages) != len(approved) {
		return fail(400, "套餐清单必须完整保留已确认的11个房型档价")
	}
	packages := make(map[string]catalogSeedPackage, len(manifest.Packages))
	for _, item := range manifest.Packages {
		want, ok := approved[item.ID]
		if !ok || !validCatalogSeedID(item.ID) || packages[item.ID].ID != "" || item.CompanyID != want.company || item.PackageType != "" && item.PackageType != "room_count" || item.RoomCount != want.room || item.Name != "墙固、地固普通工艺" || item.Price.Cents != want.cents || item.Price.Classification != "package" || item.Source.SourceID != "excel-prices" || item.Source.Sheet != want.sheet || item.Source.Row != want.row || item.Price.Source.SourceID != item.Source.SourceID || item.Price.Source.Sheet != item.Source.Sheet || item.Price.Source.Row != item.Source.Row || len([]rune(item.Note)) > 1000 {
			return fail(400, "套餐身份、来源或房型金额不符合原表")
		}
		if _, ok := companyNames[item.CompanyID]; !ok {
			return fail(400, "套餐公司不存在")
		}
		if err := validateRef(item.Source); err != nil {
			return err
		}
		if err := validateCatalogSeedValue(item.Price, "customer_price", validateRef); err != nil {
			return err
		}
		packages[item.ID] = item
	}
	wantCoverageCount := len(packages) * 7
	for _, pkg := range packages {
		if pkg.CompanyID == "company-yuanchuang" {
			wantCoverageCount--
		}
	}
	if len(manifest.PackageCoverage) != wantCoverageCount {
		return fail(400, "套餐覆盖项目数量必须与各公司原表一致")
	}
	expectedCoverage := map[string]struct {
		craftID   string
		subitemID string
		mapping   string
	}{
		"墙":    {craftID: "craft-v3-fine-wall-ground", subitemID: "fine-wall", mapping: "mapped"},
		"顶":    {craftID: "craft-v3-fine-wall-ground", subitemID: "fine-ceiling", mapping: "mapped"},
		"地":    {craftID: "craft-v3-fine-wall-ground", subitemID: "fine-floor", mapping: "mapped"},
		"弹线":   {craftID: "craft-v3-fine-marking", mapping: "mapped"},
		"喷字":   {craftID: "craft-v3-fine-marking", mapping: "mapped"},
		"门框改色": {mapping: "pending_mapping"},
		"地脚线":  {mapping: "pending_mapping"},
	}
	seen := make(map[string]bool, len(manifest.PackageCoverage))
	for _, coverage := range manifest.PackageCoverage {
		pkg, ok := packages[coverage.PackageID]
		want, itemOK := expectedCoverage[coverage.ItemName]
		if pkg.CompanyID == "company-yuanchuang" && coverage.ItemName == "地" {
			itemOK = false
		}
		key := coverage.PackageID + "\x00" + coverage.ItemName
		if !ok || !itemOK || seen[key] || coverage.CraftID != want.craftID || coverage.SubitemID != want.subitemID || coverage.Mapping != want.mapping || len([]rune(coverage.Note)) > 1000 {
			return fail(400, "套餐包含项目重复、缺失或映射不明确")
		}
		if err := validateRef(coverage.Source); err != nil {
			return err
		}
		if coverage.Source.SourceID != pkg.Source.SourceID || coverage.Source.Sheet != pkg.Source.Sheet || coverage.Source.Row != pkg.Source.Row {
			return fail(400, "套餐包含项目来源必须与套餐价格一致")
		}
		if want.mapping == "mapped" {
			craft, exists := crafts[want.craftID]
			if !exists {
				return fail(400, "套餐包含项目引用了不存在的工艺")
			}
			if want.subitemID != "" {
				found := false
				for _, subitem := range craft.Subitems {
					found = found || subitem.ID == want.subitemID
				}
				if !found {
					return fail(400, "套餐包含项目引用了不存在的小项")
				}
			}
		} else if coverage.CraftID != "" || coverage.SubitemID != "" {
			return fail(400, "未确认对应关系的套餐项目不能绑定到工艺")
		}
		seen[key] = true
	}
	for packageID := range packages {
		pkg := packages[packageID]
		for itemName := range expectedCoverage {
			if pkg.CompanyID == "company-yuanchuang" && itemName == "地" {
				continue
			}
			if !seen[packageID+"\x00"+itemName] {
				return fail(400, "套餐缺少原表包含项目")
			}
		}
	}
	return nil
}

func validCatalogSeedID(value string) bool {
	return catalogSeedIDPattern.MatchString(value)
}

func catalogSeedRuntimeSubitems(items []catalogSeedSubitem) []CraftSubitem {
	runtimeItems := make([]CraftSubitem, 0, len(items))
	for _, item := range items {
		runtimeItem := CraftSubitem{ID: item.ID, Name: item.Name}
		if item.Wage != nil {
			wage := item.Wage.Cents
			runtimeItem.WageCents = &wage
		}
		runtimeItems = append(runtimeItems, runtimeItem)
	}
	return runtimeItems
}

func (a *App) importCatalogSeed(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fail(400, "无法读取基础资料文件")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCatalogSeedFileBytes+1))
	if err != nil || len(data) > maxCatalogSeedFileBytes {
		return fail(413, "基础资料文件读取失败或超过16MB")
	}
	manifest, digest, err := parseCatalogSeed(data)
	if err != nil {
		return err
	}
	return importCatalogSeedData(context.Background(), a.db, manifest, digest)
}

func importCatalogSeedData(parent context.Context, db *sql.DB, manifest catalogSeedManifest, digest string) error {
	if err := validateCatalogSeed(manifest); err != nil {
		return err
	}
	if err := requireCatalogSeedSchema(parent, db); err != nil {
		return err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return fail(400, "基础资料无法规范化")
	}
	computed := sha256.Sum256(canonical)
	if digest == "" {
		digest = hex.EncodeToString(computed[:])
	} else if digest != hex.EncodeToString(computed[:]) {
		return fail(400, "基础资料摘要与内容不一致")
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(SUBSTRING(SHA2(LOWER(DATABASE()),256),1,32),':catalog-seed'),30)").Scan(&locked); err != nil {
		discardCatalogSeedConnection(conn)
		return fail(409, "基础资料导入锁获取失败，请核对目标数据库")
	}
	if !locked.Valid || locked.Int64 != 1 {
		return fail(409, "基础资料导入锁定失败，请稍后重试")
	}
	defer releaseCatalogSeedLock(conn, ctx)
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldDigest string
	err = tx.QueryRowContext(ctx, "SELECT manifest_digest FROM catalog_seed_imports WHERE seed_id=? FOR UPDATE", manifest.SeedID).Scan(&oldDigest)
	if err == nil {
		if oldDigest == digest {
			return tx.Commit()
		}
		return fail(409, "该基础资料编号已导入不同内容，拒绝覆盖")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = requireEmptyCatalogSeedTarget(ctx, tx); err != nil {
		return err
	}
	sourceJSON, _ := json.Marshal(manifest.Sources)
	countsJSON, _ := json.Marshal(map[string]int{
		"companies": len(manifest.Companies), "crafts": len(manifest.Crafts), "company_crafts": len(manifest.CompanyCrafts),
		"company_wage_overrides": len(manifest.CompanyWageOverrides), "products": len(manifest.Products),
		"company_product_prices": len(manifest.CompanyProductPrices),
		"packages":               len(manifest.Packages), "package_coverage": len(manifest.PackageCoverage), "bonus_rules": len(manifest.BonusRules),
	})
	if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_seed_imports(seed_id,manifest_digest,sources,entry_counts) VALUES(?,?,?,?)", manifest.SeedID, digest, string(sourceJSON), string(countsJSON)); err != nil {
		return err
	}
	for _, company := range manifest.Companies {
		ticketMode := company.TicketMode
		if ticketMode == "" {
			ticketMode = "single"
		}
		customerType := company.CustomerType
		if customerType == "" {
			customerType = "装修公司"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO companies(id,name,contact,phone,customer_type,note,ticket_mode,craft_category_order)
			VALUES(?,?, '', '', ?, '', ?, NULL)`, company.ID, company.Name, customerType, ticketMode); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company", company.ID, "identity", "confirmed", company.Source, false, map[string]any{"name": company.Name}); err != nil {
			return err
		}
	}
	for index, craft := range manifest.Crafts {
		active := *craft.Active
		runtimeSubitems := catalogSeedRuntimeSubitems(craft.Subitems)
		subitems, _ := json.Marshal(runtimeSubitems)
		wageCents := int64(0)
		if craft.Wage != nil {
			wageCents = craft.Wage.Cents
		}
		wageConfigured := craft.Wage != nil
		if _, err = tx.ExecContext(ctx, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents)
			VALUES(?,'',?,?,?,0,?,?,?,?,1,'',?,1,'',?,NULL)`, craft.ID, craft.Category, craft.Name, craft.Mode, wageCents, wageConfigured, active, craft.Spec, string(subitems), index); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "craft", craft.ID, "identity", "confirmed", craft.Source, false, map[string]any{"category": craft.Category, "name": craft.Name, "mode": craft.Mode, "spec": craft.Spec, "subitems": runtimeSubitems}); err != nil {
			return err
		}
		if craft.Wage != nil {
			if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "craft", craft.ID, "wage", craft.Wage.Classification, craft.Wage.Source, craft.Wage.Classification == "test_only", catalogSeedValueDetail{Cents: craft.Wage.Cents, Classification: craft.Wage.Classification, Note: craft.Wage.Source.Note}); err != nil {
				return err
			}
		}
		for _, item := range craft.Subitems {
			if item.Wage == nil {
				continue
			}
			key := sha256.Sum256([]byte(craft.ID + ":" + item.ID))
			detail := map[string]any{"craft_id": craft.ID, "subitem_id": item.ID, "cents": item.Wage.Cents, "classification": item.Wage.Classification, "note": item.Wage.Source.Note}
			if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "craft_subitem", hex.EncodeToString(key[:]), "wage", item.Wage.Classification, item.Wage.Source, item.Wage.Classification == "test_only", detail); err != nil {
				return err
			}
		}
	}
	craftByID := make(map[string]catalogSeedCraft, len(manifest.Crafts))
	for _, craft := range manifest.Crafts {
		craftByID[craft.ID] = craft
	}
	for index, item := range manifest.CompanyCrafts {
		craft := craftByID[item.CraftID]
		ticketSlot := item.TicketSlot
		if ticketSlot == 0 {
			ticketSlot = 1
		}
		spec := craft.Spec
		if item.Note != "" {
			spec = appendCatalogSeedNote(spec, item.Note)
		}
		subitems, _ := json.Marshal(catalogSeedRuntimeSubitems(craft.Subitems))
		wageCents := int64(0)
		wageConfigured := false
		if craft.Wage != nil {
			wageCents = craft.Wage.Cents
			wageConfigured = true
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents)
			VALUES(?,?,?,?,?,?,?,?,1,?,1,?,?,?, ?,?,NULL)`, item.ID, item.CompanyID, craft.Category, craft.Name, craft.Mode, item.Price.Cents, wageCents, wageConfigured, spec, craft.ID, string(subitems), ticketSlot, item.IncludedByCraftID, index); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company_craft", item.ID, "identity", "confirmed", item.Source, false, map[string]any{"company_id": item.CompanyID, "craft_id": item.CraftID, "category": craft.Category, "name": craft.Name, "mode": craft.Mode, "note": item.Note}); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company_craft", item.ID, "customer_price", item.Price.Classification, item.Price.Source, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}); err != nil {
			return err
		}
		if item.IncludedByCraftID != "" {
			if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company_craft", item.ID, "included_relation", "included", item.Source, false, map[string]string{"included_by_craft_id": item.IncludedByCraftID}); err != nil {
				return err
			}
		}
	}
	for _, item := range manifest.CompanyWageOverrides {
		if err = insertCompanyWageOverride(ctx, tx, manifest.SeedID, manifest.Sources, item); err != nil {
			return err
		}
	}
	seedProducts, err := validateCatalogSeedProducts(manifest.Products, func(catalogSeedReference) error { return nil })
	if err != nil {
		return err
	}
	for index, product := range seedProducts {
		input := product.input
		if _, err = tx.ExecContext(ctx, `INSERT INTO products(id,name,unit,aux_unit,aux_ratio,active,color,weight_kg,brand,spec,note,version)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, input.ID, input.Name, input.Unit, input.AuxUnit, product.ratio, bool(input.Active), input.Color, product.weight, input.Brand, input.Spec, input.Note); err != nil {
			return err
		}
		if err = syncProductUnitDefinition(tx, input.ID, input.Unit, input.AuxUnit, product.ratio); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "product", input.ID, "identity", "confirmed", manifest.Products[index].Source, false, input); err != nil {
			return err
		}
	}
	for _, item := range manifest.CompanyProductPrices {
		if _, err = tx.ExecContext(ctx, `INSERT INTO company_product_prices(id,seed_id,company_id,product_id,input_unit,price_cents,source_id,source_sheet,source_row,note,version)
			VALUES(?,?,?,?,?,?,?,?,?,?,1)`, item.ID, manifest.SeedID, item.CompanyID, item.ProductID, item.InputUnit, item.Price.Cents, item.Price.Source.SourceID, item.Price.Source.Sheet, item.Price.Source.Row, item.Note); err != nil {
			return err
		}
		detail := map[string]any{"company_id": item.CompanyID, "product_id": item.ProductID, "input_unit": item.InputUnit, "price_cents": item.Price.Cents, "note": item.Note}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company_product_price", item.ID, "identity", "confirmed", item.Source, false, detail); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "company_product_price", item.ID, "customer_price", item.Price.Classification, item.Price.Source, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}); err != nil {
			return err
		}
	}
	for _, item := range manifest.Packages {
		packageType := item.PackageType
		if packageType == "" {
			packageType = "room_count"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_seed_packages(seed_id,package_id,company_id,name,package_type,room_count,price_cents,classification,source_id,source_sheet,source_row,note)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, manifest.SeedID, item.ID, item.CompanyID, item.Name, packageType, item.RoomCount, item.Price.Cents, item.Price.Classification, item.Source.SourceID, item.Source.Sheet, item.Source.Row, item.Note); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "package", item.ID, "customer_price", item.Price.Classification, item.Price.Source, false, map[string]any{"company_id": item.CompanyID, "name": item.Name, "room_count": item.RoomCount, "price_cents": item.Price.Cents, "note": item.Note}); err != nil {
			return err
		}
	}
	for _, item := range manifest.PackageCoverage {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_seed_package_coverage(seed_id,package_id,item_name,craft_id,subitem_id,mapping,source_id,source_sheet,source_row,note)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, manifest.SeedID, item.PackageID, item.ItemName, item.CraftID, item.SubitemID, item.Mapping, item.Source.SourceID, item.Source.Sheet, item.Source.Row, item.Note); err != nil {
			return err
		}
		key := sha256.Sum256([]byte(item.PackageID + ":" + item.ItemName))
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "package_coverage", hex.EncodeToString(key[:]), "included_item", "included", item.Source, false, item); err != nil {
			return err
		}
	}
	for _, rule := range manifest.BonusRules {
		if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "bonus_rule", rule.Key, "automatic_rate", "confirmed", rule.Source, false, rule); err != nil {
			return err
		}
		for i, source := range rule.Supersedes {
			if err = insertCatalogSeedProvenance(ctx, tx, manifest.SeedID, manifest.Sources, "bonus_rule", rule.Key, fmt.Sprintf("superseded_source_%02d", i+1), "superseded", source, false, map[string]any{"reason": rule.OverrideReason}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func requireCatalogSeedSchema(ctx context.Context, db *sql.DB) error {
	var migrationCount, overrideMigrationCount, productPricesMigrationCount, wageStatusMigrationCount, packageTypeMigrationCount, columnCount, tableCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", catalogSeedMigrationVersion).Scan(&migrationCount); err != nil {
		return fail(409, "基础资料迁移未完成；请先执行 init-db")
	}
	if migrationCount != 1 {
		return fail(409, "基础资料迁移未完成；请先执行 init-db")
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", companyWageOverrideMigrationVersion).Scan(&overrideMigrationCount); err != nil {
		return fail(409, "公司工资覆盖迁移未完成；请先执行 init-db")
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", companyProductPricesMigrationVersion).Scan(&productPricesMigrationCount); err != nil {
		return fail(409, "公司商品固定价迁移未完成；请先执行 init-db")
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", craftWageStatusMigrationVersion).Scan(&wageStatusMigrationCount); err != nil {
		return fail(409, "工价配置状态迁移未完成；请先执行 init-db")
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", catalogSeedPackageTypeMigrationVersion).Scan(&packageTypeMigrationCount); err != nil {
		return fail(409, "套餐类型迁移未完成；请先执行 init-db")
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='crafts' AND COLUMN_NAME='wage_override_cents'").Scan(&columnCount); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('catalog_seed_imports','catalog_seed_provenance')").Scan(&tableCount); err != nil {
		return err
	}
	var auxiliaryTableCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('company_wage_overrides','catalog_seed_packages','catalog_seed_package_coverage','company_product_prices')").Scan(&auxiliaryTableCount); err != nil {
		return err
	}
	if overrideMigrationCount != 1 || productPricesMigrationCount != 1 || wageStatusMigrationCount != 1 || packageTypeMigrationCount != 1 || columnCount != 1 || tableCount != 2 || auxiliaryTableCount != 4 {
		return fail(409, "基础资料迁移结构不完整；请先执行 init-db")
	}
	return nil
}

func requireEmptyCatalogSeedTarget(ctx context.Context, tx *sql.Tx) error {
	for _, table := range backupTables {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fail(409, "目标数据库已有业务或基础资料，拒绝导入以避免覆盖")
		}
	}
	return nil
}

func insertCatalogSeedProvenance(ctx context.Context, tx *sql.Tx, seedID string, sources []catalogSeedSource, targetKind, targetID, component, classification string, ref catalogSeedReference, testOnly bool, detail any) error {
	var sourceFile string
	for _, source := range sources {
		if source.ID == ref.SourceID {
			sourceFile = source.File
			break
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_seed_provenance(seed_id,target_kind,target_id,component,classification,source_id,source_file,source_sheet,source_row,test_only,detail)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, seedID, targetKind, targetID, component, classification, ref.SourceID, sourceFile, ref.Sheet, ref.Row, testOnly, jsonText(detail))
	return err
}

func appendCatalogSeedNote(spec, note string) string {
	if spec == "" {
		return "客户收费说明：" + note
	}
	return spec + "；客户收费说明：" + note
}

func releaseCatalogSeedLock(conn *sql.Conn, parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(CONCAT(SUBSTRING(SHA2(LOWER(DATABASE()),256),1,32),':catalog-seed'))").Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
		discardCatalogSeedConnection(conn)
	}
}

func discardCatalogSeedConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
