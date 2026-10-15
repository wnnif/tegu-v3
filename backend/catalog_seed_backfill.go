package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	catalogSeedBackfillMigrationVersion = 459
	catalogSeedBackfillOldCrafts        = 67
	catalogSeedBackfillNewCrafts        = 95
	catalogSeedBackfillOldCompanyCrafts = 39
	catalogSeedBackfillNewCompanyCrafts = 115
	catalogSeedBackfillPlaceholders     = 18
	catalogSeedBackfillOldSeedDigest    = "513c0e2a80931615c6209e53780a78fc5d7713a5646b99e6ec1ee1b9385d9705"
	catalogSeedBackfillNewSeedDigest    = "c44818a120067a764daaec4cc3ed06082a14ceb93d9a17193f930ddaf7d62a7a"
)

type catalogSeedBackfillInputs struct {
	oldManifest   catalogSeedManifest
	newManifest   catalogSeedManifest
	oldDigest     string
	newDigest     string
	ledgerID      string
	ledgerHash    string
	ledgerJSON    string
	countsJSON    string
	oldByCraft    map[string]catalogSeedCraft
	newByCraft    map[string]catalogSeedCraft
	newIndexCraft map[string]int
	oldByCC       map[string]catalogSeedCompanyCraft
	newByCC       map[string]catalogSeedCompanyCraft
	oldIndexCC    map[string]int
	newIndexCC    map[string]int
	oldProduct    map[string]bool
	newProduct    map[string]bool
}

type catalogSeedBackfillLedger struct {
	Operation       string              `json:"operation"`
	PreviousSeedID  string              `json:"previous_seed_id"`
	PreviousDigest  string              `json:"previous_digest"`
	TargetSeedID    string              `json:"target_seed_id"`
	TargetDigest    string              `json:"target_digest"`
	Companies       []catalogSeedSource `json:"sources"`
	PublicCrafts    int                 `json:"public_crafts"`
	CompanyCrafts   int                 `json:"company_crafts"`
	Products        int                 `json:"products"`
	ProductPrices   int                 `json:"company_product_prices"`
	QuarantinedRows int                 `json:"test_only_placeholders"`
}

type catalogSeedBackfillPlan struct {
	summary        catalogSeedBackfillSummary
	inputs         catalogSeedBackfillInputs
	newCrafts      []catalogSeedCraft
	newCompanyRows []catalogSeedCompanyCraft
	newProductRows []catalogSeedProduct
	newPriceRows   []catalogSeedCompanyProductPrice
	placeholderIDs []string
	applied        bool
}

type catalogSeedBackfillQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type catalogSeedBackfillCraftRow struct {
	ID                string
	CompanyID         string
	Category          string
	Name              string
	Mode              string
	PriceCents        int64
	WageCents         int64
	WageConfigured    bool
	Active            bool
	Spec              string
	Version           int64
	SourceID          string
	Subitems          []byte
	TicketSlot        int
	IncludedByCraftID string
	DisplayOrder      int
	WageOverrideCents sql.NullInt64
}

func (a *App) planCatalogSeedBackfill(parent context.Context, oldFile, newFile string) (catalogSeedBackfillSummary, error) {
	inputs, err := loadCatalogSeedBackfillInputs(oldFile, newFile)
	if err != nil {
		return catalogSeedBackfillSummary{}, err
	}
	return a.withCatalogSeedBackfillLock(parent, func(ctx context.Context, conn *sql.Conn) (catalogSeedBackfillSummary, error) {
		dbIdentity, err := catalogSeedBackfillDatabaseIdentity(ctx, conn)
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		defer tx.Rollback()
		plan, err := planCatalogSeedBackfillRows(ctx, tx, inputs, dbIdentity)
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		if err = tx.Commit(); err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		return plan.summary, nil
	})
}

func (a *App) applyCatalogSeedBackfill(parent context.Context, oldFile, newFile, confirmationToken string) (catalogSeedBackfillSummary, error) {
	inputs, err := loadCatalogSeedBackfillInputs(oldFile, newFile)
	if err != nil {
		return catalogSeedBackfillSummary{}, err
	}
	if !catalogSeedHashPattern.MatchString(confirmationToken) {
		return catalogSeedBackfillSummary{}, fail(400, "确认码格式无效；未写入数据库")
	}
	return a.withCatalogSeedBackfillLock(parent, func(ctx context.Context, conn *sql.Conn) (catalogSeedBackfillSummary, error) {
		dbIdentity, err := catalogSeedBackfillDatabaseIdentity(ctx, conn)
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		defer tx.Rollback()
		plan, err := planCatalogSeedBackfillRows(ctx, tx, inputs, dbIdentity)
		if err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		if plan.summary.Conflicts != 0 {
			return plan.summary, fail(409, "检测到目录来源、稳定编号或人工修改冲突；整批回填已拒绝")
		}
		if !constantTimeCatalogSeedTokenEqual(plan.summary.ConfirmationToken, confirmationToken) {
			return plan.summary, fail(409, "数据库目录自 dry-run 后已有变化或确认码不匹配；未写入数据库，请重新 dry-run")
		}
		if plan.applied {
			if err := tx.Commit(); err != nil {
				return catalogSeedBackfillSummary{}, err
			}
			return plan.summary, nil
		}
		if err := writeCatalogSeedBackfill(ctx, tx, plan); err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		if err := tx.Commit(); err != nil {
			return catalogSeedBackfillSummary{}, err
		}
		return plan.summary, nil
	})
}

func (a *App) withCatalogSeedBackfillLock(parent context.Context, run func(context.Context, *sql.Conn) (catalogSeedBackfillSummary, error)) (catalogSeedBackfillSummary, error) {
	if err := requireCatalogSeedBackfillSchema(parent, a.db); err != nil {
		return catalogSeedBackfillSummary{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return catalogSeedBackfillSummary{}, err
	}
	defer conn.Close()
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(SUBSTRING(SHA2(LOWER(DATABASE()),256),1,32),':catalog-seed'),30)").Scan(&locked); err != nil {
		discardCatalogSeedConnection(conn)
		return catalogSeedBackfillSummary{}, fail(409, "基础资料导入锁获取失败；未写入数据库")
	}
	if !locked.Valid || locked.Int64 != 1 {
		return catalogSeedBackfillSummary{}, fail(409, "基础资料导入锁定失败；请稍后重试")
	}
	defer releaseCatalogSeedLock(conn, ctx)
	return run(ctx, conn)
}

func loadCatalogSeedBackfillInputs(oldFile, newFile string) (catalogSeedBackfillInputs, error) {
	oldBytes, err := readCatalogSeedBackfillFile(oldFile)
	if err != nil {
		return catalogSeedBackfillInputs{}, err
	}
	newBytes, err := readCatalogSeedBackfillFile(newFile)
	if err != nil {
		return catalogSeedBackfillInputs{}, err
	}
	oldManifest, oldDigest, err := parsePriorCatalogSeedForBackfill(oldBytes)
	if err != nil {
		return catalogSeedBackfillInputs{}, err
	}
	newManifest, newDigest, err := parseCatalogSeed(newBytes)
	if err != nil {
		return catalogSeedBackfillInputs{}, err
	}
	if oldManifest.SeedID != newManifest.SeedID {
		return catalogSeedBackfillInputs{}, fail(400, "旧版与当前 seed 编号不一致")
	}
	if oldDigest != catalogSeedBackfillOldSeedDigest || newDigest != catalogSeedBackfillNewSeedDigest {
		return catalogSeedBackfillInputs{}, fail(409, "seed 摘要不属于获准的050d992与a294333版本；未写入数据库")
	}
	inputs := catalogSeedBackfillInputs{
		oldManifest: oldManifest, newManifest: newManifest, oldDigest: oldDigest, newDigest: newDigest,
		oldByCraft: map[string]catalogSeedCraft{}, newByCraft: map[string]catalogSeedCraft{},
		newIndexCraft: map[string]int{},
		oldByCC:       map[string]catalogSeedCompanyCraft{}, newByCC: map[string]catalogSeedCompanyCraft{},
		oldIndexCC: map[string]int{}, newIndexCC: map[string]int{}, oldProduct: map[string]bool{}, newProduct: map[string]bool{},
	}
	for _, craft := range oldManifest.Crafts {
		inputs.oldByCraft[craft.ID] = craft
	}
	for index, craft := range newManifest.Crafts {
		inputs.newByCraft[craft.ID] = craft
		inputs.newIndexCraft[craft.ID] = index
	}
	for index, item := range oldManifest.CompanyCrafts {
		inputs.oldByCC[item.ID] = item
		inputs.oldIndexCC[item.ID] = index
	}
	for index, item := range newManifest.CompanyCrafts {
		inputs.newByCC[item.ID] = item
		inputs.newIndexCC[item.ID] = index
	}
	for _, product := range oldManifest.Products {
		inputs.oldProduct[product.ID] = true
	}
	for _, product := range newManifest.Products {
		inputs.newProduct[product.ID] = true
	}
	if err := validateCatalogSeedBackfillPair(inputs); err != nil {
		return catalogSeedBackfillInputs{}, err
	}
	ledger := catalogSeedBackfillLedger{
		Operation: "v1-customer-price-backfill-v1", PreviousSeedID: oldManifest.SeedID, PreviousDigest: oldDigest,
		TargetSeedID: newManifest.SeedID, TargetDigest: newDigest, Companies: newManifest.Sources,
		PublicCrafts: 28, CompanyCrafts: 94, Products: 2, ProductPrices: 2, QuarantinedRows: catalogSeedBackfillPlaceholders,
	}
	ledgerJSON, err := json.Marshal(ledger)
	if err != nil {
		return catalogSeedBackfillInputs{}, fail(400, "增量回填账本无法规范化")
	}
	ledgerSum := sha256.Sum256(ledgerJSON)
	inputs.ledgerID = "catalog-backfill-v1-" + oldDigest[:16] + "-" + newDigest[:16]
	inputs.ledgerHash = hex.EncodeToString(ledgerSum[:])
	inputs.ledgerJSON = string(ledgerJSON)
	countsJSON, err := json.Marshal(map[string]int{
		"public_crafts": 28, "company_crafts": 94, "products": 2, "company_product_prices": 2,
		"test_only_placeholders": catalogSeedBackfillPlaceholders,
	})
	if err != nil {
		return catalogSeedBackfillInputs{}, fail(400, "增量回填计数无法规范化")
	}
	inputs.countsJSON = string(countsJSON)
	return inputs, nil
}

func readCatalogSeedBackfillFile(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fail(400, "无法读取指定的基础资料文件")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCatalogSeedFileBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxCatalogSeedFileBytes {
		return nil, fail(413, "基础资料文件为空、读取失败或超过16MB")
	}
	return data, nil
}

func parsePriorCatalogSeedForBackfill(data []byte) (catalogSeedManifest, string, error) {
	var manifest catalogSeedManifest
	if len(data) == 0 || len(data) > maxCatalogSeedFileBytes || !utf8.Valid(data) {
		return manifest, "", fail(400, "旧版基础资料文件为空、过大或不是有效UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", fail(400, "旧版基础资料文件格式无效")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return manifest, "", fail(400, "旧版基础资料文件包含多余内容")
	}
	if err := validatePriorCatalogSeedBackfillShape(manifest); err != nil {
		return manifest, "", err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return manifest, "", fail(400, "旧版基础资料无法规范化")
	}
	digest := sha256.Sum256(canonical)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func validatePriorCatalogSeedBackfillShape(manifest catalogSeedManifest) error {
	if manifest.Format != catalogSeedFormat || !catalogSeedIDPattern.MatchString(manifest.SeedID) || len(manifest.Companies) != 13 || len(manifest.Crafts) != catalogSeedBackfillOldCrafts || len(manifest.CompanyCrafts) != catalogSeedBackfillOldCompanyCrafts || len(manifest.CompanyWageOverrides) != 2 || len(manifest.Products) != 0 || len(manifest.CompanyProductPrices) != 0 || len(manifest.Packages) != 11 || len(manifest.PackageCoverage) != 74 || len(manifest.BonusRules) != 2 {
		return fail(400, "旧 seed 不是已支持的 V1 基线版本")
	}
	sources := make(map[string]catalogSeedSource, len(manifest.Sources))
	for _, source := range manifest.Sources {
		if !validCatalogSeedID(source.ID) {
			return fail(400, "旧 seed 来源编号无效")
		}
		if _, ok := sources[source.ID]; ok {
			return fail(400, "旧 seed 来源编号重复")
		}
		sources[source.ID] = source
	}
	validateRef := func(ref catalogSeedReference) error {
		source, ok := sources[ref.SourceID]
		if !ok || strings.TrimSpace(ref.Sheet) == "" || len([]rune(ref.Sheet)) > 100 || len([]rune(ref.Note)) > 1000 {
			return fail(400, "旧 seed 来源定位不完整")
		}
		if source.Kind == "excel" && (source.File == "" || !catalogSeedHashPattern.MatchString(source.SHA256) || ref.Row < 1) || source.Kind == "user" && (source.File != "" || source.SHA256 != "" || ref.Row != 0) || source.Kind != "excel" && source.Kind != "user" {
			return fail(400, "旧 seed 来源类型或行号无效")
		}
		return nil
	}
	companies, names := map[string]bool{}, map[string]bool{}
	for _, company := range manifest.Companies {
		if !validCatalogSeedID(company.ID) || strings.TrimSpace(company.Name) != company.Name || company.Name == "" || companies[company.ID] || names[company.Name] {
			return fail(400, "旧 seed 公司编号或名称无效、重复")
		}
		if err := validateRef(company.Source); err != nil {
			return err
		}
		companies[company.ID], names[company.Name] = true, true
	}
	crafts, craftNames := map[string]catalogSeedCraft{}, map[string]bool{}
	for _, craft := range manifest.Crafts {
		if !validCatalogSeedID(craft.ID) || craftNames[craft.ID] || strings.TrimSpace(craft.Name) != craft.Name || craft.Name == "" || !validCraftMode(craft.Mode) || craft.Category == "" || craft.Active == nil {
			return fail(400, "旧 seed 公共工艺编号或字段无效、重复")
		}
		if err := validateRef(craft.Source); err != nil {
			return err
		}
		if craft.Wage != nil {
			if err := validateCatalogSeedValue(*craft.Wage, "wage", validateRef); err != nil {
				return err
			}
		}
		for _, subitem := range craft.Subitems {
			if !validCatalogSeedID(subitem.ID) || strings.TrimSpace(subitem.Name) != subitem.Name || subitem.Name == "" {
				return fail(400, "旧 seed 工艺小项编号或名称无效")
			}
			if subitem.Wage != nil {
				if err := validateCatalogSeedValue(*subitem.Wage, "wage", validateRef); err != nil {
					return err
				}
			}
		}
		crafts[craft.ID], craftNames[craft.ID] = craft, true
	}
	companyCraftIDs, companyCraftPairs := map[string]bool{}, map[string]bool{}
	placeholders := 0
	for _, item := range manifest.CompanyCrafts {
		if !validCatalogSeedID(item.ID) || companyCraftIDs[item.ID] || !companies[item.CompanyID] || crafts[item.CraftID].ID == "" {
			return fail(400, "旧 seed 公司工艺编号或关联无效、重复")
		}
		if err := validateRef(item.Source); err != nil {
			return err
		}
		if err := validateCatalogSeedValue(item.Price, "customer_price", validateRef); err != nil {
			return err
		}
		pair := item.CompanyID + "\x00" + item.CraftID
		if companyCraftPairs[pair] {
			return fail(400, "旧 seed 同一公司存在重复来源工艺")
		}
		companyCraftIDs[item.ID], companyCraftPairs[pair] = true, true
		if item.Price.Classification == "test_only" {
			if item.Price.Cents != 100 || !strings.HasPrefix(item.ID, "cc-test-") {
				return fail(400, "旧 seed 的 test_only 占位不符合受支持基线")
			}
			placeholders++
		}
	}
	if placeholders != catalogSeedBackfillPlaceholders {
		return fail(400, "旧 seed 的 1 元 test_only 占位数量不符合受支持基线")
	}
	for _, item := range manifest.CompanyWageOverrides {
		if !companies[item.CompanyID] || crafts[item.CraftID].ID == "" || validateCatalogSeedValue(item.Wage, "wage", validateRef) != nil || validateRef(item.Source) != nil {
			return fail(400, "旧 seed 工资覆盖来源无效")
		}
	}
	for _, item := range manifest.Packages {
		if !companies[item.CompanyID] || !validCatalogSeedID(item.ID) || validateCatalogSeedValue(item.Price, "customer_price", validateRef) != nil || validateRef(item.Source) != nil {
			return fail(400, "旧 seed 套餐来源无效")
		}
	}
	for _, item := range manifest.PackageCoverage {
		if validateRef(item.Source) != nil {
			return fail(400, "旧 seed 套餐覆盖来源无效")
		}
	}
	for _, rule := range manifest.BonusRules {
		if !validCatalogSeedID(rule.Key) || crafts[rule.TargetCraftID].ID == "" || validateRef(rule.Source) != nil {
			return fail(400, "旧 seed 奖金规则来源无效")
		}
	}
	return nil
}

func validateCatalogSeedBackfillPair(inputs catalogSeedBackfillInputs) error {
	old, current := inputs.oldManifest, inputs.newManifest
	if len(old.Companies) != 13 || len(current.Companies) != 13 || len(old.Crafts) != catalogSeedBackfillOldCrafts || len(current.Crafts) != catalogSeedBackfillNewCrafts || len(old.CompanyCrafts) != catalogSeedBackfillOldCompanyCrafts || len(current.CompanyCrafts) != catalogSeedBackfillNewCompanyCrafts || len(old.Products) != 0 || len(current.Products) != 2 || len(old.CompanyProductPrices) != 0 || len(current.CompanyProductPrices) != 2 || len(old.Packages) != 11 || len(current.Packages) != 11 || len(old.PackageCoverage) != 74 || len(current.PackageCoverage) != 74 {
		return fail(400, "旧/新 seed 条目数量不属于已支持的 V1 增量")
	}
	if !catalogSeedJSONEqual(old.Companies, current.Companies) || !catalogSeedJSONEqual(old.CompanyWageOverrides, current.CompanyWageOverrides) || !catalogSeedJSONEqual(old.BonusRules, current.BonusRules) || !catalogSeedBackfillPackagesEqual(old.Packages, current.Packages) || !catalogSeedJSONEqual(old.PackageCoverage, current.PackageCoverage) {
		return fail(409, "公司、工资覆盖、奖金、套餐或覆盖关系在新旧 seed 间发生变化；本命令只处理目录增量")
	}
	for id, previous := range inputs.oldByCraft {
		currentCraft, ok := inputs.newByCraft[id]
		if !ok || previous.ID != currentCraft.ID || previous.Category != currentCraft.Category || previous.Name != currentCraft.Name || previous.Mode != currentCraft.Mode {
			return fail(409, "旧公共工艺稳定编号或身份在当前 seed 中发生变化")
		}
	}
	placeholders := 0
	for id, previous := range inputs.oldByCC {
		currentRow, ok := inputs.newByCC[id]
		if previous.Price.Classification == "test_only" {
			placeholders++
			if ok {
				return fail(409, "旧 test_only 占位仍出现在当前 seed，拒绝视为真实报价")
			}
			continue
		}
		if !ok || previous.CompanyID != currentRow.CompanyID || previous.CraftID != currentRow.CraftID || previous.Price.Cents != currentRow.Price.Cents || previous.Price.Classification != currentRow.Price.Classification {
			return fail(409, "原有公司收费身份或金额在当前 seed 中发生变化")
		}
	}
	for id, item := range inputs.newByCC {
		if _, existed := inputs.oldByCC[id]; existed {
			continue
		}
		if item.Price.Classification == "test_only" || item.Price.Cents == 100 {
			return fail(409, "新增公司收费不能包含1元或test_only测试占位")
		}
	}
	if placeholders != catalogSeedBackfillPlaceholders {
		return fail(400, "旧 seed test_only 占位数量不正确")
	}
	if catalogSeedBackfillDeltaCount(inputs.oldByCraft, inputs.newByCraft) != 28 || catalogSeedBackfillDeltaCountCC(inputs.oldByCC, inputs.newByCC) != 94 {
		return fail(400, "旧/新 seed 稳定编号差异不是预期的28条工艺和94条公司收费")
	}
	return nil
}

func catalogSeedBackfillPackagesEqual(old, current []catalogSeedPackage) bool {
	normalize := func(packages []catalogSeedPackage) []catalogSeedPackage {
		result := append([]catalogSeedPackage(nil), packages...)
		for i := range result {
			if result[i].PackageType == "" {
				result[i].PackageType = "room_count"
			}
		}
		return result
	}
	return catalogSeedJSONEqual(normalize(old), normalize(current))
}

func catalogSeedBackfillDeltaCount[T any](old, current map[string]T) int {
	count := 0
	for id := range current {
		if _, ok := old[id]; !ok {
			count++
		}
	}
	return count
}

func catalogSeedBackfillDeltaCountCC(old, current map[string]catalogSeedCompanyCraft) int {
	return catalogSeedBackfillDeltaCount(old, current)
}

func catalogSeedJSONEqual(a, b any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func requireCatalogSeedBackfillSchema(ctx context.Context, db *sql.DB) error {
	if err := requireCatalogSeedSchema(ctx, db); err != nil {
		return err
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", catalogSeedBackfillMigrationVersion).Scan(&count); err != nil || count != 1 {
		return fail(409, "增量报价回填仅支持已完成459迁移的数据库；命令不会执行迁移")
	}
	return nil
}

func catalogSeedBackfillDatabaseIdentity(ctx context.Context, conn *sql.Conn) (string, error) {
	var database, serverUUID string
	var port int
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE(),@@server_uuid,@@port").Scan(&database, &serverUUID, &port); err != nil {
		return "", fail(409, "无法确认当前数据库身份；未执行增量回填")
	}
	if strings.TrimSpace(database) == "" || strings.TrimSpace(serverUUID) == "" || port < 1 {
		return "", fail(409, "数据库身份不完整；未执行增量回填")
	}
	return fmt.Sprintf("%s|%s|%d", serverUUID, database, port), nil
}

func planCatalogSeedBackfillRows(ctx context.Context, tx *sql.Tx, inputs catalogSeedBackfillInputs, dbIdentity string) (catalogSeedBackfillPlan, error) {
	plan := catalogSeedBackfillPlan{inputs: inputs}
	var err error
	plan.applied, err = verifyCatalogSeedBackfillLedger(ctx, tx, inputs)
	if err != nil {
		addCatalogSeedBackfillConflict(&plan, "回填审计账本与seed摘要不一致")
		return plan, nil
	}
	if err := verifyPriorCatalogSeedLedger(ctx, tx, inputs); err != nil {
		addCatalogSeedBackfillConflict(&plan, "旧版seed导入记录校验失败")
		return plan, nil
	}
	if err := verifyCatalogSeedBackfillUntouchedData(ctx, tx, inputs, plan.applied); err != nil {
		addCatalogSeedBackfillConflict(&plan, err.Error())
		return plan, nil
	}
	for _, craft := range inputs.newManifest.Crafts {
		if _, existed := inputs.oldByCraft[craft.ID]; existed {
			continue
		}
		row, found, err := getCatalogSeedBackfillCraft(ctx, tx, craft.ID)
		if err != nil {
			return plan, err
		}
		if found {
			if !plan.applied || !catalogSeedBackfillCraftOwned(ctx, tx, inputs, craft, row, inputs.newIndexCraft[craft.ID]) {
				addCatalogSeedBackfillConflict(&plan, "新增公共工艺编号已被其他资料占用")
				continue
			}
			plan.summary.Skipped++
			continue
		}
		var duplicateCount int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM crafts WHERE company_id='' AND id<>? AND category=? AND name=? AND mode=?", craft.ID, craft.Category, craft.Name, craft.Mode).Scan(&duplicateCount); err != nil {
			return plan, err
		}
		if duplicateCount != 0 || plan.applied {
			addCatalogSeedBackfillConflict(&plan, "新增公共工艺与现有名称发生冲突")
			continue
		}
		plan.newCrafts = append(plan.newCrafts, craft)
	}
	plannedCompanyNames := map[string]bool{}
	for index, item := range inputs.newManifest.CompanyCrafts {
		if _, existed := inputs.oldByCC[item.ID]; existed {
			continue
		}
		row, found, err := getCatalogSeedBackfillCraft(ctx, tx, item.ID)
		if err != nil {
			return plan, err
		}
		craft, ok := inputs.newByCraft[item.CraftID]
		if !ok {
			addCatalogSeedBackfillConflict(&plan, "新增公司收费缺少公共工艺来源")
			continue
		}
		publicRow, publicFound, err := getCatalogSeedBackfillCraft(ctx, tx, item.CraftID)
		if err != nil {
			return plan, err
		}
		if !publicFound {
			if planned, ok := plannedCatalogSeedBackfillCraftRow(plan.newCrafts, inputs.newIndexCraft, item.CraftID); ok {
				publicRow, publicFound = planned, true
			}
		}
		if !publicFound || publicRow.CompanyID != "" || !publicRow.Active {
			addCatalogSeedBackfillConflict(&plan, "公司收费对应的公共工艺不存在或不可用")
			continue
		}
		if found {
			if !plan.applied || !catalogSeedBackfillCompanyCraftOwned(ctx, tx, inputs, item, craft, row, index) {
				addCatalogSeedBackfillConflict(&plan, "新增公司工艺稳定ID已被其他资料占用")
				continue
			}
			plan.summary.Skipped++
			continue
		}
		var sourceCount, nameCount int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM crafts WHERE company_id=? AND source_id=? AND id<>?", item.CompanyID, item.CraftID, item.ID).Scan(&sourceCount); err != nil {
			return plan, err
		}
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM crafts WHERE company_id=? AND category=? AND name=? AND mode=? AND id<>?", item.CompanyID, publicRow.Category, publicRow.Name, publicRow.Mode, item.ID).Scan(&nameCount); err != nil {
			return plan, err
		}
		if sourceCount != 0 || nameCount != 0 || plan.applied {
			addCatalogSeedBackfillConflict(&plan, "新增公司工艺与现有收费名称冲突")
			continue
		}
		naturalName := item.CompanyID + "\x00" + publicRow.Category + "\x00" + publicRow.Name + "\x00" + publicRow.Mode
		if plannedCompanyNames[naturalName] {
			addCatalogSeedBackfillConflict(&plan, "本次新增公司工艺之间存在重名")
			continue
		}
		plannedCompanyNames[naturalName] = true
		plan.newCompanyRows = append(plan.newCompanyRows, item)
	}
	for _, product := range inputs.newManifest.Products {
		if inputs.oldProduct[product.ID] {
			continue
		}
		var found int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE id=?", product.ID).Scan(&found); err != nil {
			return plan, err
		}
		var duplicateCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE name=?", product.Name).Scan(&duplicateCount); err != nil {
			return plan, err
		}
		if found != 0 {
			if !plan.applied || !catalogSeedBackfillProductOwned(ctx, tx, inputs, product) {
				addCatalogSeedBackfillConflict(&plan, "新增商品编号已被其他资料占用")
				continue
			}
			plan.summary.Skipped++
			continue
		}
		if duplicateCount != 0 || plan.applied {
			addCatalogSeedBackfillConflict(&plan, "新增商品名称与现有商品冲突")
			continue
		}
		plan.newProductRows = append(plan.newProductRows, product)
	}
	for _, item := range inputs.newManifest.CompanyProductPrices {
		if _, existed := inputs.oldByCC[item.ID]; existed {
			continue
		}
		var idFound, naturalFound int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_product_prices WHERE id=?", item.ID).Scan(&idFound); err != nil {
			return plan, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_product_prices WHERE company_id=? AND product_id=? AND input_unit=?", item.CompanyID, item.ProductID, item.InputUnit).Scan(&naturalFound); err != nil {
			return plan, err
		}
		if idFound != 0 || naturalFound != 0 {
			if !plan.applied || !catalogSeedBackfillCompanyProductPriceOwned(ctx, tx, inputs, item) {
				addCatalogSeedBackfillConflict(&plan, "新增公司商品售价编号或公司商品键已被占用")
				continue
			}
			plan.summary.Skipped++
			continue
		}
		if plan.applied {
			addCatalogSeedBackfillConflict(&plan, "已回填数据库出现缺失的新目录项目")
			continue
		}
		plan.newPriceRows = append(plan.newPriceRows, item)
	}
	if err := planCatalogSeedBackfillPlaceholders(ctx, tx, &plan); err != nil {
		return plan, err
	}
	plan.summary.Added = len(plan.newCrafts) + len(plan.newCompanyRows) + len(plan.newProductRows) + len(plan.newPriceRows)
	if !plan.applied {
		plan.summary.Updated = len(plan.placeholderIDs)
		plan.summary.PlaceholdersDeactivated = len(plan.placeholderIDs)
	} else {
		plan.summary.Added = 0
	}
	fingerprint, err := catalogSeedBackfillFingerprint(ctx, tx)
	if err != nil {
		return plan, err
	}
	tokenData, err := json.Marshal(struct {
		DatabaseIdentity string                     `json:"database_identity"`
		OldDigest        string                     `json:"old_digest"`
		NewDigest        string                     `json:"new_digest"`
		LedgerDigest     string                     `json:"ledger_digest"`
		Fingerprint      string                     `json:"fingerprint"`
		Summary          catalogSeedBackfillSummary `json:"summary"`
	}{dbIdentity, inputs.oldDigest, inputs.newDigest, inputs.ledgerHash, fingerprint, plan.summary})
	if err != nil {
		return plan, err
	}
	token := sha256.Sum256(tokenData)
	plan.summary.ConfirmationToken = hex.EncodeToString(token[:])
	return plan, nil
}

func addCatalogSeedBackfillConflict(plan *catalogSeedBackfillPlan, reason string) {
	plan.summary.Conflicts++
	plan.summary.ConflictReasons = append(plan.summary.ConflictReasons, reason)
}

func verifyPriorCatalogSeedLedger(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs) error {
	var digest string
	var countsJSON []byte
	if err := tx.QueryRowContext(ctx, "SELECT manifest_digest,entry_counts FROM catalog_seed_imports WHERE seed_id=?", inputs.oldManifest.SeedID).Scan(&digest, &countsJSON); err != nil {
		return err
	}
	if digest != inputs.oldDigest {
		return errors.New("prior seed digest mismatch")
	}
	want, err := json.Marshal(catalogSeedEntryCounts(inputs.oldManifest))
	if err != nil {
		return err
	}
	legacyShapeMatches := catalogSeedJSONEqual(json.RawMessage(countsJSON), json.RawMessage(want))
	compatibleShape := catalogSeedEntryCounts(inputs.oldManifest)
	compatibleShape["company_product_prices"] = 0
	compatible, err := json.Marshal(compatibleShape)
	if err != nil {
		return err
	}
	if !legacyShapeMatches && !catalogSeedJSONEqual(json.RawMessage(countsJSON), json.RawMessage(compatible)) {
		return errors.New("prior seed counts mismatch")
	}
	return nil
}

func catalogSeedEntryCounts(manifest catalogSeedManifest) map[string]int {
	counts := map[string]int{
		"companies": len(manifest.Companies), "crafts": len(manifest.Crafts), "company_crafts": len(manifest.CompanyCrafts),
		"company_wage_overrides": len(manifest.CompanyWageOverrides), "products": len(manifest.Products),
		"packages":         len(manifest.Packages),
		"package_coverage": len(manifest.PackageCoverage), "bonus_rules": len(manifest.BonusRules),
	}
	if len(manifest.CompanyProductPrices) > 0 {
		counts["company_product_prices"] = len(manifest.CompanyProductPrices)
	}
	return counts
}

func verifyCatalogSeedBackfillUntouchedData(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, backfillApplied bool) error {
	for _, company := range inputs.oldManifest.Companies {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM companies WHERE id=?", company.ID).Scan(&id); err != nil {
			return err
		}
		if err := requireCatalogSeedBackfillProvenance(ctx, tx, inputs.oldManifest.SeedID, "company", company.ID, "identity", company.Source, "confirmed", false, map[string]any{"name": company.Name}); err != nil {
			return fmt.Errorf("company %s provenance: %w", company.ID, err)
		}
	}
	for _, craft := range inputs.oldManifest.Crafts {
		row, found, err := getCatalogSeedBackfillCraft(ctx, tx, craft.ID)
		if err != nil {
			return err
		}
		if !found || row.CompanyID != "" || row.SourceID != "" {
			return errors.New("legacy public craft identity mismatch")
		}
		if err := requireCatalogSeedBackfillProvenance(ctx, tx, inputs.oldManifest.SeedID, "craft", craft.ID, "identity", craft.Source, "confirmed", false, map[string]any{"category": craft.Category, "name": craft.Name, "mode": craft.Mode, "spec": craft.Spec, "subitems": catalogSeedRuntimeSubitems(craft.Subitems)}); err != nil {
			return fmt.Errorf("public craft %s provenance: %w", craft.ID, err)
		}
	}
	for _, item := range inputs.oldManifest.CompanyCrafts {
		row, found, err := getCatalogSeedBackfillCraft(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		if !found || row.CompanyID != item.CompanyID || row.SourceID != item.CraftID {
			return errors.New("legacy company craft identity mismatch")
		}
		craft := inputs.oldByCraft[item.CraftID]
		if err := requireCatalogSeedBackfillProvenance(ctx, tx, inputs.oldManifest.SeedID, "company_craft", item.ID, "identity", item.Source, "confirmed", false, map[string]any{"company_id": item.CompanyID, "craft_id": item.CraftID, "category": craft.Category, "name": craft.Name, "mode": craft.Mode, "note": item.Note}); err != nil {
			return fmt.Errorf("company craft %s identity provenance: %w", item.ID, err)
		}
		if err := requireCatalogSeedBackfillProvenance(ctx, tx, inputs.oldManifest.SeedID, "company_craft", item.ID, "customer_price", item.Price.Source, item.Price.Classification, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}); err != nil {
			return fmt.Errorf("company craft %s customer-price provenance: %w", item.ID, err)
		}
		if item.Price.Classification == "test_only" {
			if err := verifyCatalogSeedBackfillPlaceholder(ctx, tx, inputs, item, craft, row, backfillApplied); err != nil {
				return err
			}
		}
	}
	for _, item := range inputs.oldManifest.CompanyWageOverrides {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_wage_overrides WHERE company_id=? AND craft_id=?", item.CompanyID, item.CraftID).Scan(&count); err != nil || count != 1 {
			return errors.New("legacy wage override is missing")
		}
	}
	var wageOverrideCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_wage_overrides").Scan(&wageOverrideCount); err != nil {
		return err
	}
	if wageOverrideCount != len(inputs.oldManifest.CompanyWageOverrides) {
		return errors.New("legacy wage override rows changed")
	}
	var packageCount, coverageCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_seed_packages WHERE seed_id=?", inputs.oldManifest.SeedID).Scan(&packageCount); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_seed_package_coverage WHERE seed_id=?", inputs.oldManifest.SeedID).Scan(&coverageCount); err != nil {
		return err
	}
	if packageCount != len(inputs.oldManifest.Packages) || coverageCount != len(inputs.oldManifest.PackageCoverage) {
		return errors.New("legacy package rows changed")
	}
	var bonusCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_seed_provenance WHERE seed_id=? AND target_kind='bonus_rule' AND component LIKE 'automatic_rate%'", inputs.oldManifest.SeedID).Scan(&bonusCount); err != nil {
		return err
	}
	if bonusCount != len(inputs.oldManifest.BonusRules) {
		return errors.New("legacy bonus provenance changed")
	}
	return nil
}

func verifyCatalogSeedBackfillLedger(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs) (bool, error) {
	var digest string
	var sources, counts []byte
	err := tx.QueryRowContext(ctx, "SELECT manifest_digest,sources,entry_counts FROM catalog_seed_imports WHERE seed_id=?", inputs.ledgerID).Scan(&digest, &sources, &counts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != inputs.ledgerHash || !catalogSeedJSONEqual(json.RawMessage(sources), json.RawMessage(inputs.ledgerJSON)) || !catalogSeedJSONEqual(json.RawMessage(counts), json.RawMessage(inputs.countsJSON)) {
		return false, errors.New("backfill ledger digest mismatch")
	}
	return true, nil
}

func planCatalogSeedBackfillPlaceholders(ctx context.Context, tx catalogSeedBackfillQueryer, plan *catalogSeedBackfillPlan) error {
	for id, item := range plan.inputs.oldByCC {
		if item.Price.Classification != "test_only" {
			continue
		}
		craft := plan.inputs.oldByCraft[item.CraftID]
		row, found, err := getCatalogSeedBackfillCraft(ctx, tx, id)
		if err != nil {
			return err
		}
		if !found {
			addCatalogSeedBackfillConflict(plan, "1元测试占位工艺不存在")
			continue
		}
		if plan.applied {
			if err := verifyCatalogSeedBackfillPlaceholder(ctx, tx, plan.inputs, item, craft, row, true); err != nil {
				addCatalogSeedBackfillConflict(plan, "已隔离的1元测试占位被人工修改")
				continue
			}
			var markerCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_seed_provenance WHERE seed_id=? AND target_kind='company_craft' AND target_id=? AND component='placeholder_quarantine' AND classification='test_only_quarantined' AND test_only=1`, plan.inputs.ledgerID, id).Scan(&markerCount); err != nil {
				return err
			}
			if markerCount != 1 {
				addCatalogSeedBackfillConflict(plan, "1元测试占位隔离审计标记不唯一")
			}
			continue
		}
		if err := verifyCatalogSeedBackfillPlaceholder(ctx, tx, plan.inputs, item, craft, row, false); err != nil {
			addCatalogSeedBackfillConflict(plan, "1元测试占位来源或字段已被人工修改")
			continue
		}
		plan.placeholderIDs = append(plan.placeholderIDs, id)
	}
	return nil
}

func verifyCatalogSeedBackfillPlaceholder(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, item catalogSeedCompanyCraft, craft catalogSeedCraft, row catalogSeedBackfillCraftRow, after bool) error {
	wantActive, wantVersion := true, int64(1)
	if after {
		wantActive, wantVersion = false, 2
	}
	wantSpec := craft.Spec
	if item.Note != "" {
		wantSpec = appendCatalogSeedNote(wantSpec, item.Note)
	}
	wantSlot := item.TicketSlot
	if wantSlot == 0 {
		wantSlot = 1
	}
	wantWage, wageConfigured := int64(0), false
	if craft.Wage != nil {
		wantWage, wageConfigured = craft.Wage.Cents, true
	}
	wantSubitems, _ := json.Marshal(catalogSeedRuntimeSubitems(craft.Subitems))
	if row.ID != item.ID || row.CompanyID != item.CompanyID || row.Category != craft.Category || row.Name != craft.Name || row.Mode != craft.Mode || row.PriceCents != 100 || row.WageCents != wantWage || row.WageConfigured != wageConfigured || row.Active != wantActive || row.Spec != wantSpec || row.Version != wantVersion || row.SourceID != craft.ID || !catalogSeedJSONEqual(json.RawMessage(row.Subitems), json.RawMessage(wantSubitems)) || row.TicketSlot != wantSlot || row.IncludedByCraftID != item.IncludedByCraftID || row.DisplayOrder != inputs.oldIndexCC[item.ID] || row.WageOverrideCents.Valid {
		return errors.New("legacy test placeholder was modified")
	}
	if !after {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_seed_provenance WHERE seed_id=? AND target_kind='company_craft' AND target_id=? AND component='placeholder_quarantine' AND classification='test_only_quarantined' AND test_only=1 AND source_id=? AND source_sheet=? AND source_row=? AND detail=CAST(? AS JSON)`, inputs.ledgerID, item.ID, item.Price.Source.SourceID, item.Price.Source.Sheet, item.Price.Source.Row, jsonText(map[string]any{"previous_seed_id": inputs.oldManifest.SeedID, "original_price_cents": 100, "active": false, "version": 2})).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("placeholder quarantine marker mismatch")
	}
	return nil
}

func getCatalogSeedBackfillCraft(ctx context.Context, tx catalogSeedBackfillQueryer, id string) (catalogSeedBackfillCraftRow, bool, error) {
	var row catalogSeedBackfillCraftRow
	err := tx.QueryRowContext(ctx, `SELECT id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents FROM crafts WHERE id=?`, id).Scan(
		&row.ID, &row.CompanyID, &row.Category, &row.Name, &row.Mode, &row.PriceCents, &row.WageCents, &row.WageConfigured, &row.Active, &row.Spec, &row.Version, &row.SourceID, &row.Subitems, &row.TicketSlot, &row.IncludedByCraftID, &row.DisplayOrder, &row.WageOverrideCents,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	return row, err == nil, err
}

func catalogSeedBackfillCraftOwned(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, craft catalogSeedCraft, row catalogSeedBackfillCraftRow, index int) bool {
	wage, wageConfigured := int64(0), false
	if craft.Wage != nil {
		wage, wageConfigured = craft.Wage.Cents, true
	}
	if row.CompanyID != "" || row.Category != craft.Category || row.Name != craft.Name || row.Mode != craft.Mode || row.PriceCents != 0 || row.WageCents != wage || row.WageConfigured != wageConfigured || row.Active != *craft.Active || row.Spec != craft.Spec || row.SourceID != "" || row.Version != 1 || row.TicketSlot != 1 || row.IncludedByCraftID != "" || row.DisplayOrder != index || row.WageOverrideCents.Valid {
		return false
	}
	if requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "craft", craft.ID, "identity", craft.Source, "confirmed", false, map[string]any{"category": craft.Category, "name": craft.Name, "mode": craft.Mode, "spec": craft.Spec, "subitems": catalogSeedRuntimeSubitems(craft.Subitems)}) != nil {
		return false
	}
	if craft.Wage != nil && requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "craft", craft.ID, "wage", craft.Wage.Source, craft.Wage.Classification, craft.Wage.Classification == "test_only", catalogSeedValueDetail{Cents: craft.Wage.Cents, Classification: craft.Wage.Classification, Note: craft.Wage.Source.Note}) != nil {
		return false
	}
	return catalogSeedJSONEqual(json.RawMessage(row.Subitems), mustCatalogSeedJSON(catalogSeedRuntimeSubitems(craft.Subitems)))
}

func catalogSeedBackfillCompanyCraftOwned(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, item catalogSeedCompanyCraft, craft catalogSeedCraft, row catalogSeedBackfillCraftRow, index int) bool {
	wantSpec := craft.Spec
	if item.Note != "" {
		wantSpec = appendCatalogSeedNote(wantSpec, item.Note)
	}
	wantWage, configured := int64(0), false
	if craft.Wage != nil {
		wantWage, configured = craft.Wage.Cents, true
	}
	wantSlot := item.TicketSlot
	if wantSlot == 0 {
		wantSlot = 1
	}
	if row.ID != item.ID || row.CompanyID != item.CompanyID || row.Category != craft.Category || row.Name != craft.Name || row.Mode != craft.Mode || row.PriceCents != item.Price.Cents || row.WageCents != wantWage || row.WageConfigured != configured || !row.Active || row.Spec != wantSpec || row.Version != 1 || row.SourceID != craft.ID || row.TicketSlot != wantSlot || row.IncludedByCraftID != item.IncludedByCraftID || row.DisplayOrder != index || row.WageOverrideCents.Valid {
		return false
	}
	if !catalogSeedJSONEqual(json.RawMessage(row.Subitems), mustCatalogSeedJSON(catalogSeedRuntimeSubitems(craft.Subitems))) {
		return false
	}
	if requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "company_craft", item.ID, "identity", item.Source, "confirmed", false, map[string]any{"company_id": item.CompanyID, "craft_id": item.CraftID, "category": craft.Category, "name": craft.Name, "mode": craft.Mode, "note": item.Note}) != nil {
		return false
	}
	if requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "company_craft", item.ID, "customer_price", item.Price.Source, item.Price.Classification, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}) != nil {
		return false
	}
	return true
}

func catalogSeedBackfillProductOwned(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, product catalogSeedProduct) bool {
	validated, err := validateCatalogSeedProducts([]catalogSeedProduct{product}, func(catalogSeedReference) error { return nil })
	if err != nil || len(validated) != 1 {
		return false
	}
	expected := validated[0]
	var id, name, unit, auxUnit, ratio, color, weight, brand, spec, note string
	var active bool
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT id,name,unit,aux_unit,CAST(aux_ratio AS CHAR),active,color,CAST(weight_kg AS CHAR),brand,spec,note,version FROM products WHERE id=?`, product.ID).Scan(&id, &name, &unit, &auxUnit, &ratio, &active, &color, &weight, &brand, &spec, &note, &version); err != nil {
		return false
	}
	wantWeight := strconv.FormatFloat(expected.weight, 'f', 6, 64)
	if id != expected.input.ID || name != expected.input.Name || unit != expected.input.Unit || auxUnit != expected.input.AuxUnit || ratio != expected.ratio || active != bool(expected.input.Active) || color != expected.input.Color || weight != wantWeight || brand != expected.input.Brand || spec != expected.input.Spec || note != expected.input.Note || version != 1 {
		return false
	}
	definition, err := newProductUnitDefinition(expected.input.Unit, expected.input.AuxUnit, expected.ratio)
	if err != nil {
		return false
	}
	var productID, baseUnit, auxDefinition string
	var baseFactor, auxFactor int64
	var quantityScale int
	var definitionVersion int64
	if err = tx.QueryRowContext(ctx, "SELECT product_id,base_unit,aux_unit,base_factor,aux_factor,quantity_scale,version FROM product_unit_definitions WHERE product_id=?", product.ID).Scan(&productID, &baseUnit, &auxDefinition, &baseFactor, &auxFactor, &quantityScale, &definitionVersion); err != nil {
		return false
	}
	if productID != product.ID || baseUnit != definition.BaseUnit || auxDefinition != definition.AuxUnit || baseFactor != definition.BaseFactor || auxFactor != definition.AuxFactor || quantityScale != 6 || definitionVersion != 1 {
		return false
	}
	return requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "product", product.ID, "identity", product.Source, "confirmed", false, expected.input) == nil
}

func plannedCatalogSeedBackfillCraftRow(planned []catalogSeedCraft, indexes map[string]int, id string) (catalogSeedBackfillCraftRow, bool) {
	for _, craft := range planned {
		if craft.ID != id {
			continue
		}
		wage, configured, subitems, err := catalogSeedBackfillCraftValues(craft)
		if err != nil {
			return catalogSeedBackfillCraftRow{}, false
		}
		return catalogSeedBackfillCraftRow{
			ID: craft.ID, Category: craft.Category, Name: craft.Name, Mode: craft.Mode,
			WageCents: wage, WageConfigured: configured, Active: *craft.Active, Spec: craft.Spec,
			Version: 1, Subitems: []byte(subitems), TicketSlot: 1, DisplayOrder: indexes[craft.ID],
		}, true
	}
	return catalogSeedBackfillCraftRow{}, false
}

func catalogSeedBackfillCompanyProductPriceOwned(ctx context.Context, tx catalogSeedBackfillQueryer, inputs catalogSeedBackfillInputs, item catalogSeedCompanyProductPrice) bool {
	var seedID, companyID, productID, inputUnit, sourceID, sourceSheet, note string
	var cents int64
	var sourceRow int
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT seed_id,company_id,product_id,input_unit,price_cents,source_id,source_sheet,source_row,note,version FROM company_product_prices WHERE id=?", item.ID).Scan(&seedID, &companyID, &productID, &inputUnit, &cents, &sourceID, &sourceSheet, &sourceRow, &note, &version); err != nil {
		return false
	}
	if seedID != inputs.ledgerID || companyID != item.CompanyID || productID != item.ProductID || inputUnit != item.InputUnit || cents != item.Price.Cents || sourceID != item.Price.Source.SourceID || sourceSheet != item.Price.Source.Sheet || sourceRow != item.Price.Source.Row || note != item.Note || version != 1 {
		return false
	}
	identity := map[string]any{"company_id": item.CompanyID, "product_id": item.ProductID, "input_unit": item.InputUnit, "price_cents": item.Price.Cents, "note": item.Note}
	return requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "company_product_price", item.ID, "identity", item.Source, "confirmed", false, identity) == nil && requireCatalogSeedBackfillProvenance(ctx, tx, inputs.ledgerID, "company_product_price", item.ID, "customer_price", item.Price.Source, item.Price.Classification, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}) == nil
}

func mustCatalogSeedJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return json.RawMessage(data)
}

func requireCatalogSeedBackfillProvenance(ctx context.Context, tx catalogSeedBackfillQueryer, seedID, targetKind, targetID, component string, source catalogSeedReference, classification string, testOnly bool, detail any) error {
	var sourceID, sourceFile, sourceSheet, storedClassification string
	var sourceRow int
	var storedTestOnly bool
	var storedDetail []byte
	if err := tx.QueryRowContext(ctx, `SELECT source_id,source_file,source_sheet,source_row,classification,test_only,detail FROM catalog_seed_provenance WHERE seed_id=? AND target_kind=? AND target_id=? AND component=?`, seedID, targetKind, targetID, component).Scan(&sourceID, &sourceFile, &sourceSheet, &sourceRow, &storedClassification, &storedTestOnly, &storedDetail); err != nil {
		return err
	}
	wantFile := ""
	for _, candidate := range provenanceSourcesForSeed(ctx, tx, seedID) {
		if candidate.ID == source.SourceID {
			wantFile = candidate.File
			break
		}
	}
	var mismatches []string
	if sourceID != source.SourceID {
		mismatches = append(mismatches, "source_id")
	}
	if sourceFile != wantFile {
		mismatches = append(mismatches, "source_file")
	}
	if sourceSheet != source.Sheet || sourceRow != source.Row {
		mismatches = append(mismatches, "source_location")
	}
	if storedClassification != classification || storedTestOnly != testOnly {
		mismatches = append(mismatches, "classification")
	}
	if !catalogSeedJSONEqual(json.RawMessage(storedDetail), mustCatalogSeedJSON(detail)) {
		mismatches = append(mismatches, "detail")
	}
	if len(mismatches) != 0 {
		return fmt.Errorf("catalog provenance mismatch: %s", strings.Join(mismatches, ","))
	}
	return nil
}

func provenanceSourcesForSeed(ctx context.Context, tx catalogSeedBackfillQueryer, seedID string) []catalogSeedSource {
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT sources FROM catalog_seed_imports WHERE seed_id=?", seedID).Scan(&raw); err != nil {
		return nil
	}
	var sources []catalogSeedSource
	if err := json.Unmarshal(raw, &sources); err == nil {
		return sources
	}
	var ledger catalogSeedBackfillLedger
	if err := json.Unmarshal(raw, &ledger); err == nil {
		return ledger.Companies
	}
	return sources
}

func catalogSeedBackfillFingerprint(ctx context.Context, tx catalogSeedBackfillQueryer) (string, error) {
	tables := []struct {
		name  string
		order string
	}{
		{"schema_migrations", "version"}, {"companies", "id"}, {"crafts", "id"}, {"products", "id"},
		{"product_unit_definitions", "product_id"}, {"company_product_prices", "id"}, {"company_wage_overrides", "company_id,craft_id"},
		{"catalog_seed_imports", "seed_id"}, {"catalog_seed_provenance", "seed_id,target_kind,target_id,component"},
		{"catalog_seed_packages", "seed_id,package_id"}, {"catalog_seed_package_coverage", "seed_id,package_id,item_name"},
	}
	var all bytes.Buffer
	encoder := json.NewEncoder(&all)
	for _, table := range tables {
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+table.name+" ORDER BY "+table.order)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		if _, err = fmt.Fprint(&all, table.name, "\n"); err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err = rows.Scan(destinations...); err != nil {
				rows.Close()
				return "", err
			}
			for i, value := range values {
				if bytesValue, ok := value.([]byte); ok {
					values[i] = string(bytesValue)
				}
			}
			if err = encoder.Encode(values); err != nil {
				rows.Close()
				return "", err
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		if err = rows.Close(); err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(all.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

func catalogSeedBackfillCraftValues(craft catalogSeedCraft) (wage int64, configured bool, subitems string, err error) {
	if craft.Wage != nil {
		wage, configured = craft.Wage.Cents, true
	}
	data, err := json.Marshal(catalogSeedRuntimeSubitems(craft.Subitems))
	return wage, configured, string(data), err
}

func writeCatalogSeedBackfill(ctx context.Context, tx *sql.Tx, plan catalogSeedBackfillPlan) error {
	inputs := plan.inputs
	if _, err := tx.ExecContext(ctx, "INSERT INTO catalog_seed_imports(seed_id,manifest_digest,sources,entry_counts) VALUES(?,?,?,?)", inputs.ledgerID, inputs.ledgerHash, inputs.ledgerJSON, inputs.countsJSON); err != nil {
		return err
	}
	for index, craft := range inputs.newManifest.Crafts {
		if _, existed := inputs.oldByCraft[craft.ID]; existed {
			continue
		}
		wage, configured, subitems, err := catalogSeedBackfillCraftValues(craft)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents)
			VALUES(?,'',?,?,?,0,?,?,?,?,1,'',?,1,'',?,NULL)`, craft.ID, craft.Category, craft.Name, craft.Mode, wage, configured, *craft.Active, craft.Spec, subitems, index); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "craft", craft.ID, "identity", "confirmed", craft.Source, false, map[string]any{"category": craft.Category, "name": craft.Name, "mode": craft.Mode, "spec": craft.Spec, "subitems": catalogSeedRuntimeSubitems(craft.Subitems)}); err != nil {
			return err
		}
		if craft.Wage != nil {
			if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "craft", craft.ID, "wage", craft.Wage.Classification, craft.Wage.Source, craft.Wage.Classification == "test_only", catalogSeedValueDetail{Cents: craft.Wage.Cents, Classification: craft.Wage.Classification, Note: craft.Wage.Source.Note}); err != nil {
				return err
			}
		}
		for _, item := range craft.Subitems {
			if item.Wage == nil {
				continue
			}
			key := sha256.Sum256([]byte(craft.ID + ":" + item.ID))
			detail := map[string]any{"craft_id": craft.ID, "subitem_id": item.ID, "cents": item.Wage.Cents, "classification": item.Wage.Classification, "note": item.Wage.Source.Note}
			if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "craft_subitem", hex.EncodeToString(key[:]), "wage", item.Wage.Classification, item.Wage.Source, item.Wage.Classification == "test_only", detail); err != nil {
				return err
			}
		}
	}
	for index, item := range inputs.newManifest.CompanyCrafts {
		if _, existed := inputs.oldByCC[item.ID]; existed {
			continue
		}
		craft := inputs.newByCraft[item.CraftID]
		publicRow, found, err := getCatalogSeedBackfillCraft(ctx, tx, item.CraftID)
		if err != nil || !found {
			if err != nil {
				return err
			}
			return errors.New("public craft disappeared during backfill")
		}
		wageConfigured := publicRow.WageConfigured
		wage := publicRow.WageCents
		if publicRow.CompanyID != "" {
			return errors.New("company craft source is not public")
		}
		spec := publicRow.Spec
		if item.Note != "" {
			spec = appendCatalogSeedNote(spec, item.Note)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents)
			VALUES(?,?,?,?,?,?,?,?,1,?,1,?,?,?, ?,?,NULL)`, item.ID, item.CompanyID, publicRow.Category, publicRow.Name, publicRow.Mode, item.Price.Cents, wage, wageConfigured, spec, item.CraftID, string(publicRow.Subitems), defaultCatalogSeedTicketSlot(item.TicketSlot), item.IncludedByCraftID, index); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_craft", item.ID, "identity", "confirmed", item.Source, false, map[string]any{"company_id": item.CompanyID, "craft_id": item.CraftID, "category": craft.Category, "name": craft.Name, "mode": craft.Mode, "note": item.Note}); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_craft", item.ID, "customer_price", item.Price.Classification, item.Price.Source, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}); err != nil {
			return err
		}
		if item.IncludedByCraftID != "" {
			if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_craft", item.ID, "included_relation", "included", item.Source, false, map[string]string{"included_by_craft_id": item.IncludedByCraftID}); err != nil {
				return err
			}
		}
	}
	products, err := validateCatalogSeedProducts(inputs.newManifest.Products, func(catalogSeedReference) error { return nil })
	if err != nil {
		return err
	}
	productByID := make(map[string]catalogSeedProduct, len(inputs.newManifest.Products))
	for _, product := range inputs.newManifest.Products {
		productByID[product.ID] = product
	}
	for _, product := range products {
		input := product.input
		if _, err = tx.ExecContext(ctx, `INSERT INTO products(id,name,unit,aux_unit,aux_ratio,active,color,weight_kg,brand,spec,note,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, input.ID, input.Name, input.Unit, input.AuxUnit, product.ratio, bool(input.Active), input.Color, product.weight, input.Brand, input.Spec, input.Note); err != nil {
			return err
		}
		if err = syncProductUnitDefinition(tx, input.ID, input.Unit, input.AuxUnit, product.ratio); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "product", input.ID, "identity", "confirmed", productByID[input.ID].Source, false, input); err != nil {
			return err
		}
	}
	for _, item := range inputs.newManifest.CompanyProductPrices {
		if _, err = tx.ExecContext(ctx, `INSERT INTO company_product_prices(id,seed_id,company_id,product_id,input_unit,price_cents,source_id,source_sheet,source_row,note,version) VALUES(?,?,?,?,?,?,?,?,?,?,1)`, item.ID, inputs.ledgerID, item.CompanyID, item.ProductID, item.InputUnit, item.Price.Cents, item.Price.Source.SourceID, item.Price.Source.Sheet, item.Price.Source.Row, item.Note); err != nil {
			return err
		}
		identity := map[string]any{"company_id": item.CompanyID, "product_id": item.ProductID, "input_unit": item.InputUnit, "price_cents": item.Price.Cents, "note": item.Note}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_product_price", item.ID, "identity", "confirmed", item.Source, false, identity); err != nil {
			return err
		}
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_product_price", item.ID, "customer_price", item.Price.Classification, item.Price.Source, item.Price.Classification == "test_only", catalogSeedValueDetail{Cents: item.Price.Cents, Classification: item.Price.Classification, Note: item.Price.Source.Note}); err != nil {
			return err
		}
	}
	for _, id := range plan.placeholderIDs {
		result, err := tx.ExecContext(ctx, "UPDATE crafts SET active=0,version=version+1 WHERE id=? AND company_id=? AND price_cents=100 AND active=1 AND version=1", id, inputs.oldByCC[id].CompanyID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return fail(409, "旧 1 元占位在写入前发生变化；整批回填已回滚")
		}
		item := inputs.oldByCC[id]
		if err = insertCatalogSeedProvenance(ctx, tx, inputs.ledgerID, inputs.newManifest.Sources, "company_craft", id, "placeholder_quarantine", "test_only_quarantined", item.Price.Source, true, map[string]any{"previous_seed_id": inputs.oldManifest.SeedID, "original_price_cents": 100, "active": false, "version": 2}); err != nil {
			return err
		}
	}
	user := User{ID: "catalog-seed-backfill", Name: "基础资料增量回填"}
	detail := map[string]any{
		"previous_seed_id": inputs.oldManifest.SeedID, "previous_seed_digest": inputs.oldDigest, "target_seed_digest": inputs.newDigest,
		"added":                       map[string]int{"public_crafts": 28, "company_crafts": 94, "products": 2, "company_product_prices": 2},
		"quarantined_placeholder_ids": plan.placeholderIDs,
		"preserved":                   map[string]int{"companies": 13, "company_wage_overrides": 2, "packages": 11, "package_coverage": 74, "bonus_rules": 2},
	}
	if err = audit(tx, user, "V1客户报价增量回填", inputs.ledgerID, detail); err != nil {
		return err
	}
	return nil
}

func defaultCatalogSeedTicketSlot(slot int) int {
	if slot == 0 {
		return 1
	}
	return slot
}

func constantTimeCatalogSeedTokenEqual(left, right string) bool {
	leftBytes, errLeft := hex.DecodeString(left)
	rightBytes, errRight := hex.DecodeString(right)
	return errLeft == nil && errRight == nil && len(leftBytes) == len(rightBytes) && subtleConstantTimeCompare(leftBytes, rightBytes)
}

func subtleConstantTimeCompare(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for i := range left {
		diff |= left[i] ^ right[i]
	}
	return diff == 0
}

func catalogSeedBackfillDriverValue(value any) any {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	if tm, ok := value.(time.Time); ok {
		return tm.UTC().Format(time.RFC3339Nano)
	}
	return value
}
