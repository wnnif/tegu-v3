package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const catalogSeedBackfillPriorCommit = "050d99248a577120592719c21c9f92c52d56bcab"

func TestCatalogSeedBackfillSeedRevisionPair(t *testing.T) {
	oldPath, newPath, inputs := catalogSeedBackfillTestSeedFiles(t)
	if len(inputs.oldManifest.Companies) != 13 || len(inputs.oldManifest.Crafts) != 67 || len(inputs.oldManifest.CompanyCrafts) != 39 {
		t.Fatal("old V1 seed no longer matches the supported catalog baseline")
	}
	if len(inputs.newManifest.Companies) != 13 || len(inputs.newManifest.Crafts) != 95 || len(inputs.newManifest.CompanyCrafts) != 115 || len(inputs.newManifest.Products) != 2 || len(inputs.newManifest.CompanyProductPrices) != 2 {
		t.Fatal("current V1 seed counts no longer match the approved catalog")
	}
	if catalogSeedBackfillDeltaCount(inputs.oldByCraft, inputs.newByCraft) != 28 || catalogSeedBackfillDeltaCountCC(inputs.oldByCC, inputs.newByCC) != 94 {
		t.Fatal("old/current V1 seed deltas changed")
	}
	if len(inputs.newManifest.Packages) != 11 || len(inputs.newManifest.PackageCoverage) != 74 || len(inputs.newManifest.CompanyWageOverrides) != 2 {
		t.Fatal("preserved wage overrides, package tiers, or coverage changed")
	}
	if _, err := loadCatalogSeedBackfillInputs(oldPath, newPath); err != nil {
		t.Fatalf("approved V1 seed pair was rejected: %v", err)
	}

	mutated := inputs.newManifest
	mutated.CompanyProductPrices = append([]catalogSeedCompanyProductPrice(nil), inputs.newManifest.CompanyProductPrices...)
	mutated.CompanyProductPrices[0].Note += " modified"
	mutatedBytes, err := json.Marshal(mutated)
	if err != nil {
		t.Fatal(err)
	}
	mutatedPath := filepath.Join(t.TempDir(), "catalog-seed-mutated.json")
	if err = os.WriteFile(mutatedPath, mutatedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCatalogSeedBackfillInputs(oldPath, mutatedPath); err == nil {
		t.Fatal("modified seed with the same entry counts was accepted")
	}
}

func TestCatalogSeedBackfillMySQL(t *testing.T) {
	host := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_HOST")
	portText := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_PORT")
	user := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_USER")
	database := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_DATABASE")
	if host == "" && portText == "" && user == "" && database == "" {
		t.Skip("set TEGU_CATALOG_BACKFILL_TEST_* to an empty isolated loopback MySQL schema")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || host != "127.0.0.1" || port < 1024 || port == 3306 || !strings.HasPrefix(database, "tegu_backfill_test_") || user == "" {
		t.Fatal("MySQL backfill test requires loopback, a non-default port, a tegu_backfill_test_ database, and an explicit user")
	}
	cfg := mysql.Config{
		User: user, Passwd: os.Getenv("TEGU_CATALOG_BACKFILL_TEST_PASSWORD"), Net: "tcp",
		Addr: fmt.Sprintf("%s:%d", host, port), DBName: database, ParseTime: true,
		AllowNativePasswords: true, Params: map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"},
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open isolated MySQL test database:", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Fatal("connect to isolated MySQL test database:", err)
	}
	var actualDB, serverUUID string
	var actualPort int
	if err = db.QueryRowContext(ctx, "SELECT DATABASE(),@@server_uuid,@@port").Scan(&actualDB, &serverUUID, &actualPort); err != nil {
		t.Fatal("verify isolated MySQL target:", err)
	}
	if actualDB != database || serverUUID == "" || actualPort != port {
		t.Fatal("MySQL target identity does not match the explicitly isolated test target")
	}
	var tableCount int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&tableCount); err != nil {
		t.Fatal("inspect isolated test schema:", err)
	}
	if tableCount != 0 {
		t.Fatal("refusing to initialize a non-empty isolated test schema; it was left untouched")
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INT PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB"); err != nil {
		t.Fatal("prepare isolated historical migration marker:", err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations(version) VALUES(434)"); err != nil {
		t.Fatal("mark the empty synthetic schema as past legacy order backfill:", err)
	}

	if err = initSchema(db); err != nil {
		t.Fatal("initialize only the verified empty test schema:", err)
	}
	var migrated int
	if err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=459").Scan(&migrated); err != nil || migrated != 1 {
		t.Fatal("isolated MySQL schema did not reach migration 459")
	}
	oldPath, newPath, inputs := catalogSeedBackfillTestSeedFiles(t)
	app := &App{db: db}
	if err = assertCatalogSeedImportRejectsBonusHistory(t, db, app, oldPath); err != nil {
		t.Fatal("full seed importer accepted a populated bonus history table:", err)
	}
	if err = app.importCatalogSeed(oldPath); err != nil {
		t.Fatal("import historical V1 seed into empty synthetic database:", err)
	}
	placeholderIDs := make([]string, 0, catalogSeedBackfillPlaceholders)
	for id, item := range inputs.oldByCC {
		if item.Price.Classification == "test_only" {
			placeholderIDs = append(placeholderIDs, id)
		}
	}
	sort.Strings(placeholderIDs)
	if len(placeholderIDs) != catalogSeedBackfillPlaceholders {
		t.Fatal("historical V1 seed does not contain the supported placeholder set")
	}
	companyID := inputs.oldManifest.Companies[0].ID
	if err = insertCatalogSeedBackfillSyntheticBusiness(db, companyID, placeholderIDs[0], inputs); err != nil {
		t.Fatal("create synthetic admin, user craft, edited quote, and historical order:", err)
	}

	if err = insertBackfillCollision(db, inputs); err != nil {
		t.Fatal("create synthetic name collision:", err)
	}
	conflictPlan, err := app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || conflictPlan.Conflicts == 0 {
		t.Fatalf("dry-run did not identify the synthetic collision: summary=%+v err=%v", conflictPlan, err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, conflictPlan.ConfirmationToken); err == nil {
		t.Fatal("apply accepted a conflicting catalog")
	}
	if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM crafts WHERE id='synthetic-backfill-collision'"); count != 1 {
		t.Fatalf("rejected apply changed the synthetic conflict row: %d", count)
	}
	if _, err = db.Exec("DELETE FROM crafts WHERE id='synthetic-backfill-collision'"); err != nil {
		t.Fatal("remove only the synthetic collision fixture:", err)
	}
	assertCatalogSeedBackfillAbsent(t, db, inputs, placeholderIDs[0])

	if _, err = db.Exec("UPDATE crafts SET display_order=display_order+1000 WHERE id=?", placeholderIDs[0]); err != nil {
		t.Fatal(err)
	}
	conflictPlan, err = app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || conflictPlan.Conflicts == 0 {
		t.Fatalf("dry-run failed to detect a manually reordered test placeholder: summary=%+v err=%v", conflictPlan, err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, conflictPlan.ConfirmationToken); err == nil {
		t.Fatal("apply quarantined a manually modified test placeholder")
	}
	if _, err = db.Exec("UPDATE crafts SET display_order=? WHERE id=?", inputs.oldIndexCC[placeholderIDs[0]], placeholderIDs[0]); err != nil {
		t.Fatal("restore the synthetic fixture field:", err)
	}
	if _, err = db.Exec("UPDATE crafts SET wage_override_cents=1 WHERE id=?", placeholderIDs[0]); err != nil {
		t.Fatal(err)
	}
	conflictPlan, err = app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || conflictPlan.Conflicts == 0 {
		t.Fatalf("dry-run failed to detect a manually configured placeholder wage: summary=%+v err=%v", conflictPlan, err)
	}
	if _, err = db.Exec("UPDATE crafts SET wage_override_cents=NULL WHERE id=?", placeholderIDs[0]); err != nil {
		t.Fatal("restore the synthetic fixture field:", err)
	}

	plan, err := app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || plan.Conflicts != 0 || plan.Added != 126 || plan.Updated != 18 || plan.PlaceholdersDeactivated != 18 {
		t.Fatalf("unexpected clean dry-run result: summary=%+v err=%v", plan, err)
	}
	if plan.ConfirmationToken == "" {
		t.Fatal("clean dry-run did not provide an explicit confirmation token")
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, strings.Repeat("0", 64)); err == nil {
		t.Fatal("apply accepted a confirmation token that did not match the inspected database")
	}
	assertCatalogSeedBackfillAbsent(t, db, inputs, placeholderIDs[0])
	if _, err = db.Exec("UPDATE companies SET note=CONCAT(note,' synthetic'),version=version+1 WHERE id=?", companyID); err != nil {
		t.Fatal("simulate a concurrent manual catalog edit:", err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, plan.ConfirmationToken); err == nil {
		t.Fatal("apply accepted a confirmation token after the directory changed")
	}
	plan, err = app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || plan.Conflicts != 0 {
		t.Fatalf("dry-run after the synthetic manual edit was not clean: summary=%+v err=%v", plan, err)
	}

	if _, err = db.Exec(`CREATE TRIGGER synthetic_backfill_failure BEFORE INSERT ON audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic backfill failure'`); err != nil {
		t.Fatal("install synthetic transaction failure trigger:", err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, plan.ConfirmationToken); err == nil {
		t.Fatal("apply unexpectedly succeeded with a failure injected at the final audit write")
	}
	assertCatalogSeedBackfillAbsent(t, db, inputs, placeholderIDs[0])
	if _, err = db.Exec("DROP TRIGGER synthetic_backfill_failure"); err != nil {
		t.Fatal("remove synthetic test trigger:", err)
	}

	result, err := app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, plan.ConfirmationToken)
	if err != nil || result.Conflicts != 0 || result.Added != 126 || result.Updated != 18 || result.PlaceholdersDeactivated != 18 {
		t.Fatalf("apply failed after removing the test-only failure trigger: summary=%+v err=%v", result, err)
	}
	assertCatalogSeedBackfillApplied(t, db, inputs, placeholderIDs, companyID)

	repeat, err := app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || repeat.Conflicts != 0 || repeat.Added != 0 || repeat.Skipped != 126 || repeat.Updated != 0 {
		t.Fatalf("repeat dry-run was not idempotent: summary=%+v err=%v", repeat, err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, repeat.ConfirmationToken); err != nil {
		t.Fatal("idempotent apply failed:", err)
	}
	if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM audit WHERE action='V1客户报价增量回填'"); count != 1 {
		t.Fatalf("idempotent apply wrote an unexpected number of audit records: %d", count)
	}

	var manualCompanyID string
	var manualOriginalPrice int64
	for _, item := range inputs.newManifest.CompanyCrafts {
		if _, existed := inputs.oldByCC[item.ID]; existed {
			continue
		}
		manualCompanyID, manualOriginalPrice = item.ID, item.Price.Cents
		break
	}
	if manualCompanyID == "" {
		t.Fatal("current V1 seed has no new company charge to edit synthetically")
	}
	if _, err = db.Exec("UPDATE crafts SET price_cents=?,version=version+1 WHERE id=?", manualOriginalPrice+1, manualCompanyID); err != nil {
		t.Fatal(err)
	}
	manualPlan, err := app.planCatalogSeedBackfill(context.Background(), oldPath, newPath)
	if err != nil || manualPlan.Conflicts == 0 {
		t.Fatalf("repeat dry-run did not flag a manually edited company quote: summary=%+v err=%v", manualPlan, err)
	}
	if _, err = app.applyCatalogSeedBackfill(context.Background(), oldPath, newPath, manualPlan.ConfirmationToken); err == nil {
		t.Fatal("repeat apply accepted a manually edited company quote")
	}
	var manualPrice int64
	if err = db.QueryRow("SELECT price_cents FROM crafts WHERE id=?", manualCompanyID).Scan(&manualPrice); err != nil || manualPrice != manualOriginalPrice+1 {
		t.Fatal("manual company quote was overwritten", err)
	}
	if _, err = db.Exec("UPDATE crafts SET price_cents=?,version=1 WHERE id=?", manualOriginalPrice, manualCompanyID); err != nil {
		t.Fatal("restore the synthetic quote fixture after the conflict assertion:", err)
	}
	assertTestOnlyCompanyCraftResaveGuard(t, db, placeholderIDs[0])
}

func assertCatalogSeedImportRejectsBonusHistory(t *testing.T, db *sql.DB, app *App, seedPath string) error {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO payroll_bonus_settlements(id,worker_id,worker_name,bonus_year,settlement_date,amount_cents,status,actor_id,actor_name,operation_id)
		VALUES('synthetic-seed-import-bonus','synthetic-worker','合成工人',2026,'2026-10-15',100,'confirmed','synthetic-admin','合成管理员','synthetic-seed-import-bonus')`); err != nil {
		return fmt.Errorf("create synthetic bonus history row: %w", err)
	}
	err := app.importCatalogSeed(seedPath)
	if err == nil || !strings.Contains(err.Error(), "目标数据库已有业务或基础资料") {
		return fmt.Errorf("expected the empty-target guard to reject bonus history, got %v", err)
	}
	var companies, settlements int
	if err = db.QueryRow("SELECT COUNT(*) FROM companies").Scan(&companies); err != nil {
		return fmt.Errorf("verify no catalog was written: %w", err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM payroll_bonus_settlements WHERE id='synthetic-seed-import-bonus'").Scan(&settlements); err != nil {
		return fmt.Errorf("verify bonus history remains: %w", err)
	}
	if companies != 0 || settlements != 1 {
		return fmt.Errorf("empty-target rejection changed rows: companies=%d settlements=%d", companies, settlements)
	}
	if _, err = db.Exec("DELETE FROM payroll_bonus_settlements WHERE id='synthetic-seed-import-bonus'"); err != nil {
		return fmt.Errorf("remove only the synthetic bonus history fixture: %w", err)
	}
	return nil
}

func TestCatalogSeedBackfillReadOnlyDryRunMySQL(t *testing.T) {
	host := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_HOST")
	portText := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_PORT")
	user := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_USER")
	database := os.Getenv("TEGU_CATALOG_BACKFILL_TEST_DATABASE")
	if host == "" && portText == "" && user == "" && database == "" {
		t.Skip("set TEGU_CATALOG_BACKFILL_TEST_* to an isolated loopback MySQL schema")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || host != "127.0.0.1" || port < 1024 || port == 3306 || !strings.HasPrefix(database, "tegu_backfill_test_") || user == "" {
		t.Fatal("MySQL dry-run test requires loopback, a non-default port, a tegu_backfill_test_ database, and an explicit user")
	}
	cfg := mysql.Config{User: user, Passwd: os.Getenv("TEGU_CATALOG_BACKFILL_TEST_PASSWORD"), Net: "tcp", Addr: fmt.Sprintf("%s:%d", host, port), DBName: database, ParseTime: true, AllowNativePasswords: true, Params: map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"}}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open isolated MySQL test database:", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Fatal("connect to isolated MySQL test database:", err)
	}
	var actualDB, serverUUID string
	var actualPort int
	if err = db.QueryRowContext(ctx, "SELECT DATABASE(),@@server_uuid,@@port").Scan(&actualDB, &serverUUID, &actualPort); err != nil || actualDB != database || actualPort != port || serverUUID == "" {
		t.Fatal("MySQL target identity does not match the explicitly isolated test target", err)
	}
	oldPath, newPath, _ := catalogSeedBackfillTestSeedFiles(t)
	before := catalogSeedBackfillTestFingerprint(t, db)
	plan, err := (&App{db: db}).planCatalogSeedBackfill(ctx, oldPath, newPath)
	if err != nil {
		t.Fatal("read-only dry-run failed:", err)
	}
	after := catalogSeedBackfillTestFingerprint(t, db)
	if before != after {
		t.Fatal("dry-run changed the isolated database")
	}
	if plan.Conflicts != 0 || plan.Added != 0 || plan.Updated != 0 || plan.Skipped != 126 || plan.PlaceholdersDeactivated != 0 {
		t.Fatalf("repeat dry-run did not report the fully applied catalog as skipped: summary=%+v", plan)
	}
}

func catalogSeedBackfillTestFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal("begin read-only fingerprint transaction:", err)
	}
	defer tx.Rollback()
	fingerprint, err := catalogSeedBackfillFingerprint(context.Background(), tx)
	if err != nil {
		t.Fatal("read database fingerprint:", err)
	}
	return fingerprint
}

func catalogSeedBackfillTestSeedFiles(t *testing.T) (string, string, catalogSeedBackfillInputs) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "show", catalogSeedBackfillPriorCommit+":catalog/catalog-seed-v1.json")
	oldBytes, err := cmd.Output()
	if err != nil {
		t.Skipf("Git history containing %s is unavailable", catalogSeedBackfillPriorCommit[:7])
	}
	oldPath := filepath.Join(t.TempDir(), "catalog-seed-v1-050d992.json")
	if err = os.WriteFile(oldPath, oldBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	_, oldDigest, err := parsePriorCatalogSeedForBackfill(oldBytes)
	if err != nil {
		t.Fatal("parse prior V1 seed:", err)
	}
	newPath, err := filepath.Abs(filepath.Join("..", "catalog", "catalog-seed-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	newBytes, readErr := os.ReadFile(newPath)
	if readErr != nil {
		t.Fatal("read current V1 seed:", readErr)
	}
	_, newDigest, err := parseCatalogSeed(newBytes)
	if err != nil {
		t.Fatal("parse current V1 seed:", err)
	}
	if oldDigest != catalogSeedBackfillOldSeedDigest || newDigest != catalogSeedBackfillNewSeedDigest {
		t.Fatal("source seed digests do not match the approved V1 revisions")
	}
	inputs, err := loadCatalogSeedBackfillInputs(oldPath, newPath)
	if err != nil {
		t.Fatal("historical/current V1 seed pair:", err)
	}
	return oldPath, newPath, inputs
}

func insertCatalogSeedBackfillSyntheticBusiness(db *sql.DB, companyID, placeholderID string, inputs catalogSeedBackfillInputs) error {
	if _, err := db.Exec(`INSERT INTO users(id,username,name,password_hash,role,permissions,crafts,active) VALUES('synthetic-admin','synthetic-admin','合成管理员','synthetic-hash','admin',JSON_ARRAY(),JSON_ARRAY(),1)`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents) VALUES('synthetic-user-craft',?,?,?,'area',321,17,1,1,'人工新增的防水工艺',7,'',JSON_ARRAY(),1,'',999,NULL)`, companyID, "丙纶", "宜宾·连二遍防水工艺"); err != nil {
		return err
	}
	var oldReal catalogSeedCompanyCraft
	for _, row := range inputs.oldManifest.CompanyCrafts {
		if row.Price.Classification != "test_only" {
			oldReal = row
			break
		}
	}
	if oldReal.ID == "" {
		return fmt.Errorf("historical seed has no real company quote")
	}
	if _, err := db.Exec("UPDATE crafts SET price_cents=12345,version=version+1 WHERE id=?", oldReal.ID); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO orders(id,number,company_id,address,work_date,worker_id,warehouse_id,contact,note,status,wage_paid,total_cents,wage_cents,version,created_by,company_name,worker_name) VALUES('synthetic-history-order','synthetic-history-order',?,'合成历史地址','2026-10-15','','','','历史金额保留验收','进行中',0,100,0,1,'synthetic-admin','合成公司','')`, companyID); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO order_lines(id,order_id,craft_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES('synthetic-history-line','synthetic-history-order',?,'回填','合成历史施工明细','area',JSON_ARRAY(),1000000,100,0,100,0,0)`, placeholderID)
	return err
}

func insertBackfillCollision(db *sql.DB, inputs catalogSeedBackfillInputs) error {
	var collision *catalogSeedCraft
	for i := range inputs.newManifest.Crafts {
		if _, old := inputs.oldByCraft[inputs.newManifest.Crafts[i].ID]; !old {
			collision = &inputs.newManifest.Crafts[i]
			break
		}
	}
	if collision == nil {
		return fmt.Errorf("current seed has no newly added public craft")
	}
	_, err := db.Exec(`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,wage_configured,active,spec,version,source_id,subitems,ticket_slot,included_by_craft_id,display_order,wage_override_cents) VALUES('synthetic-backfill-collision','',?,?,?,0,0,0,1,'synthetic conflict',1,'',JSON_ARRAY(),1,'',999,NULL)`, collision.Category, collision.Name, collision.Mode)
	return err
}

func assertCatalogSeedBackfillAbsent(t *testing.T, db *sql.DB, inputs catalogSeedBackfillInputs, placeholderID string) {
	t.Helper()
	if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM catalog_seed_imports WHERE seed_id=?", inputs.ledgerID); count != 0 {
		t.Fatalf("backfill ledger exists despite rejection or rollback: %d", count)
	}
	if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM products"); count != 0 {
		t.Fatalf("product writes survived rejection or rollback: %d", count)
	}
	for _, queryWant := range []struct {
		query string
		want  int
	}{
		{"SELECT COUNT(*) FROM crafts WHERE company_id=''", len(inputs.oldManifest.Crafts)},
		{"SELECT COUNT(*) FROM crafts WHERE company_id<>'' AND source_id<>''", len(inputs.oldManifest.CompanyCrafts)},
		{"SELECT COUNT(*) FROM company_product_prices", 0},
		{"SELECT COUNT(*) FROM catalog_seed_provenance WHERE seed_id=?", 0},
		{"SELECT COUNT(*) FROM audit WHERE action='V1客户报价增量回填'", 0},
		{"SELECT COUNT(*) FROM catalog_seed_packages WHERE seed_id=?", len(inputs.oldManifest.Packages)},
		{"SELECT COUNT(*) FROM catalog_seed_package_coverage WHERE seed_id=?", len(inputs.oldManifest.PackageCoverage)},
		{"SELECT COUNT(*) FROM company_wage_overrides", len(inputs.oldManifest.CompanyWageOverrides)},
	} {
		var got int
		var err error
		if strings.Contains(queryWant.query, "seed_id=?") {
			seedID := inputs.ledgerID
			if strings.Contains(queryWant.query, "catalog_seed_packages") || strings.Contains(queryWant.query, "catalog_seed_package_coverage") {
				seedID = inputs.oldManifest.SeedID
			}
			err = db.QueryRow(queryWant.query, seedID).Scan(&got)
		} else {
			err = db.QueryRow(queryWant.query).Scan(&got)
		}
		if err != nil || got != queryWant.want {
			t.Fatalf("catalog changed despite rejection or rollback for %q: got=%d want=%d err=%v", queryWant.query, got, queryWant.want, err)
		}
	}
	for _, craft := range inputs.newManifest.Crafts {
		if _, exists := inputs.oldByCraft[craft.ID]; exists {
			continue
		}
		if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM crafts WHERE id=?", craft.ID); count != 0 {
			t.Fatalf("new public craft survived rejection or rollback: %s", craft.ID)
		}
	}
	for _, item := range inputs.newManifest.CompanyCrafts {
		if _, exists := inputs.oldByCC[item.ID]; exists {
			continue
		}
		if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM crafts WHERE id=?", item.ID); count != 0 {
			t.Fatalf("new company craft survived rejection or rollback: %s", item.ID)
		}
	}
	for _, id := range placeholderIDsFromSeed(inputs.oldByCC) {
		var active bool
		var version, price int64
		if err := db.QueryRow("SELECT active,version,price_cents FROM crafts WHERE id=?", id).Scan(&active, &version, &price); err != nil || !active || version != 1 || price != 100 {
			t.Fatalf("placeholder changed despite rejection or rollback: id=%s active=%v version=%d price=%d err=%v", id, active, version, price, err)
		}
	}
	var active bool
	if err := db.QueryRow("SELECT active FROM crafts WHERE id=?", placeholderID).Scan(&active); err != nil || !active {
		t.Fatal("referenced placeholder changed despite rejection or rollback", err)
	}
	var linePrice, lineTotal, orderTotal int64
	if err := db.QueryRow("SELECT price_cents,total_cents FROM order_lines WHERE id='synthetic-history-line'").Scan(&linePrice, &lineTotal); err != nil || linePrice != 100 || lineTotal != 100 {
		t.Fatalf("historical line changed despite rejection or rollback: price=%d total=%d err=%v", linePrice, lineTotal, err)
	}
	if err := db.QueryRow("SELECT total_cents FROM orders WHERE id='synthetic-history-order'").Scan(&orderTotal); err != nil || orderTotal != 100 {
		t.Fatalf("historical order changed despite rejection or rollback: total=%d err=%v", orderTotal, err)
	}
}

func placeholderIDsFromSeed(companyCrafts map[string]catalogSeedCompanyCraft) []string {
	ids := make([]string, 0, catalogSeedBackfillPlaceholders)
	for id, item := range companyCrafts {
		if item.Price.Classification == "test_only" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func assertTestOnlyCompanyCraftResaveGuard(t *testing.T, db *sql.DB, placeholderID string) {
	t.Helper()
	var companyID, sourceID, ticketMode string
	var version int64
	if err := db.QueryRow("SELECT company_id,source_id,version FROM crafts WHERE id=?", placeholderID).Scan(&companyID, &sourceID, &version); err != nil {
		t.Fatal("read quarantined placeholder for resave guard:", err)
	}
	if err := db.QueryRow("SELECT ticket_mode FROM companies WHERE id=?", companyID).Scan(&ticketMode); err != nil {
		t.Fatal("read synthetic company ticket mode:", err)
	}
	actor := User{ID: "synthetic-admin", Name: "合成管理员", Role: "admin"}
	request := func(price *int64) json.RawMessage {
		item := map[string]any{"id": placeholderID, "source_id": sourceID, "version": version, "ticket_slot": 1}
		if price != nil {
			item["price_cents"] = *price
		}
		data, err := json.Marshal([]map[string]any{item})
		if err != nil {
			t.Fatal("encode synthetic company craft update:", err)
		}
		return data
	}
	unchangedPrice := int64(100)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal("begin placeholder reactivation denial check:", err)
	}
	err = saveCompanyCrafts(tx, actor, companyID, ticketMode, request(&unchangedPrice))
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("company craft save accepted the quarantined one-yuan test price")
	}
	var active bool
	var price int64
	var classification string
	var testOnly bool
	if err = db.QueryRow(`SELECT c.active,c.price_cents,p.classification,p.test_only FROM crafts c JOIN catalog_seed_provenance p ON p.target_id=c.id AND p.target_kind='company_craft' AND p.component='customer_price' WHERE c.id=?`, placeholderID).Scan(&active, &price, &classification, &testOnly); err != nil || active || price != 100 || classification != "test_only" || !testOnly {
		t.Fatalf("denied reactivation changed the quarantined quote: active=%v price=%d provenance=%s/%v err=%v", active, price, classification, testOnly, err)
	}

	confirmedPrice := int64(350)
	tx, err = db.Begin()
	if err != nil {
		t.Fatal("begin explicit price-confirmation check:", err)
	}
	if err = saveCompanyCrafts(tx, actor, companyID, ticketMode, request(&confirmedPrice)); err != nil {
		_ = tx.Rollback()
		t.Fatal("authorized explicit replacement price was rejected:", err)
	}
	if err = tx.QueryRow(`SELECT c.active,c.price_cents,p.classification,p.test_only FROM crafts c JOIN catalog_seed_provenance p ON p.target_id=c.id AND p.target_kind='company_craft' AND p.component='customer_price' WHERE c.id=?`, placeholderID).Scan(&active, &price, &classification, &testOnly); err != nil || !active || price != confirmedPrice || classification != "confirmed" || testOnly {
		_ = tx.Rollback()
		t.Fatalf("explicit quote confirmation did not reclassify and enable the row: active=%v price=%d provenance=%s/%v err=%v", active, price, classification, testOnly, err)
	}
	var auditCount int
	if err = tx.QueryRow("SELECT COUNT(*) FROM audit WHERE action='确认公司测试占位报价'").Scan(&auditCount); err != nil || auditCount != 1 {
		_ = tx.Rollback()
		t.Fatalf("explicit quote confirmation did not create its transactional audit row: %d err=%v", auditCount, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal("roll back the successful reprice fixture:", err)
	}
}

func assertCatalogSeedBackfillApplied(t *testing.T, db *sql.DB, inputs catalogSeedBackfillInputs, placeholderIDs []string, companyID string) {
	t.Helper()
	for query, want := range map[string]int{
		"SELECT COUNT(*) FROM companies":                                                                                           13,
		"SELECT COUNT(*) FROM users":                                                                                               1,
		"SELECT COUNT(*) FROM crafts WHERE company_id=''":                                                                          95,
		"SELECT COUNT(*) FROM crafts WHERE company_id<>'' AND source_id<>''":                                                       133,
		"SELECT COUNT(*) FROM crafts WHERE company_id<>'' AND source_id<>'' AND active=1":                                          115,
		"SELECT COUNT(*) FROM products":                                                                                            2,
		"SELECT COUNT(*) FROM company_product_prices":                                                                              2,
		"SELECT COUNT(*) FROM company_wage_overrides":                                                                              2,
		"SELECT COUNT(*) FROM catalog_seed_packages WHERE seed_id=?":                                                               11,
		"SELECT COUNT(*) FROM catalog_seed_package_coverage WHERE seed_id=?":                                                       74,
		"SELECT COUNT(*) FROM catalog_seed_provenance WHERE seed_id=? AND target_kind='bonus_rule' AND component='automatic_rate'": 2,
	} {
		var got int
		var err error
		switch strings.Contains(query, "seed_id=?") {
		case true:
			err = db.QueryRow(query, inputs.oldManifest.SeedID).Scan(&got)
		default:
			err = db.QueryRow(query).Scan(&got)
		}
		if err != nil || got != want {
			t.Fatalf("post-backfill count mismatch for %q: got=%d want=%d err=%v", query, got, want, err)
		}
	}
	for _, id := range placeholderIDs {
		var active bool
		var version int64
		if err := db.QueryRow("SELECT active,version FROM crafts WHERE id=?", id).Scan(&active, &version); err != nil || active || version != 2 {
			t.Fatalf("test placeholder was not soft-disabled in place: id=%s active=%v version=%d err=%v", id, active, version, err)
		}
	}
	var linePrice, lineTotal, orderTotal int64
	if err := db.QueryRow("SELECT price_cents,total_cents FROM order_lines WHERE id='synthetic-history-line'").Scan(&linePrice, &lineTotal); err != nil || linePrice != 100 || lineTotal != 100 {
		t.Fatalf("historical line amount changed: price=%d total=%d err=%v", linePrice, lineTotal, err)
	}
	if err := db.QueryRow("SELECT total_cents FROM orders WHERE id='synthetic-history-order'").Scan(&orderTotal); err != nil || orderTotal != 100 {
		t.Fatalf("historical order amount changed: total=%d err=%v", orderTotal, err)
	}
	var customName, customSpec string
	var customPrice int64
	var customVersion int64
	if err := db.QueryRow("SELECT name,spec,price_cents,version FROM crafts WHERE id='synthetic-user-craft'").Scan(&customName, &customSpec, &customPrice, &customVersion); err != nil || customName != "宜宾·连二遍防水工艺" || customSpec != "人工新增的防水工艺" || customPrice != 321 || customVersion != 7 {
		t.Fatalf("user-created craft changed: name=%q spec=%q price=%d version=%d err=%v", customName, customSpec, customPrice, customVersion, err)
	}
	var editedPrice int64
	if err := db.QueryRow("SELECT price_cents FROM crafts WHERE company_id=? AND price_cents=12345", companyID).Scan(&editedPrice); err != nil || editedPrice != 12345 {
		t.Fatalf("manual edit to an existing real quote was overwritten: price=%d err=%v", editedPrice, err)
	}
	if count := catalogSeedBackfillCount(t, db, "SELECT COUNT(*) FROM audit WHERE action='V1客户报价增量回填'"); count != 1 {
		t.Fatalf("successful upgrade must write one audit record, got %d", count)
	}
	for _, craft := range inputs.newManifest.Crafts {
		if _, existed := inputs.oldByCraft[craft.ID]; existed {
			continue
		}
		var wage int64
		var configured bool
		if err := db.QueryRow("SELECT wage_cents,wage_configured FROM crafts WHERE id=?", craft.ID).Scan(&wage, &configured); err != nil {
			t.Fatal("read newly inserted public wage:", err)
		}
		if (craft.Wage == nil && (wage != 0 || configured)) || (craft.Wage != nil && (wage != craft.Wage.Cents || !configured)) {
			t.Fatalf("public wage state mismatch for %s: cents=%d configured=%v", craft.ID, wage, configured)
		}
	}
}

func catalogSeedBackfillCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
