package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestDimensions(t *testing.T) {
	l := Line{Category: "开工", Name: "墙面", Mode: "平方", Price: 1200, Wage: 300, Segments: []Segment{{Length: 2, Width: 3}, {Length: 4, Width: 5}}}
	if e := calcLine(&l); e != nil || l.Total != 31200 || l.Labor != 7800 {
		t.Fatalf("area %+v %v", l, e)
	}
	l.Mode = "立方"
	l.Segments = []Segment{{Length: 2, Width: 3, Height: 4}}
	if e := calcLine(&l); e != nil || l.Qty != 24000000 {
		t.Fatal(l, e)
	}
	l.Segments[0].Width = 0
	if calcLine(&l) == nil {
		t.Fatal("invalid dimensions accepted")
	}
}

func TestSegmentModes(t *testing.T) {
	mixed := Line{Category: "开工", Name: "混合", Mode: "平方", Price: 1000, Wage: 200, Segments: []Segment{
		{Mode: "平方", Length: 2, Width: 3},
		{Mode: "金额", Amount: 1250},
	}}
	if e := calcLine(&mixed); e == nil {
		t.Fatal("mixed square and amount accepted")
	}
	mixedCube := Line{Category: "开工", Name: "平方立方混合", Mode: "平方", Price: 1000, Segments: []Segment{
		{Mode: "平方", Length: 2, Width: 3},
		{Mode: "立方", Length: 2, Width: 3, Height: 4},
	}}
	if e := calcLine(&mixedCube); e == nil {
		t.Fatal("mixed square and cube accepted")
	}
	if got := segmentQuantityLabel(mixedCube.Mode, jsonText(mixedCube.Segments), mixedCube.Qty); got != "6.000000 ㎡ + 24.000000 m³" {
		t.Fatal("mixed units", got)
	}
	mixedCube.Segments[1].Height = 0
	if calcLine(&mixedCube) == nil {
		t.Fatal("cube without height accepted")
	}
	legacy := Line{Category: "开工", Name: "旧单", Mode: "平方", Price: 1000, Segments: []Segment{{Length: 2, Width: 3}}}
	if e := calcLine(&legacy); e != nil || legacy.Total != 6000 {
		t.Fatalf("legacy segment %+v %v", legacy, e)
	}
	legacy.Mode = "立方"
	legacy.Segments = []Segment{{Mode: "立方", Length: 2, Width: 3, Height: 4}}
	if e := calcLine(&legacy); e != nil || legacy.Qty != 24000000 {
		t.Fatalf("volume roundtrip %+v %v", legacy, e)
	}
	legacy.Mode = "平方"
	legacy.Segments = []Segment{{Mode: "金额", Amount: 1234}}
	if e := calcLine(&legacy); e == nil {
		t.Fatal("amount segment in dimensional item accepted")
	}
	for _, amount := range []int64{0, 100000001} {
		invalid := Line{Category: "开工", Name: "金额校验", Mode: "平方", Segments: []Segment{{Mode: "金额", Amount: amount}}}
		if calcLine(&invalid) == nil {
			t.Fatalf("accepted invalid segment amount %d", amount)
		}
	}
}
func TestTransactionsMySQL(t *testing.T) {
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("set GO_MYSQL_TEST=1 on isolated MySQL")
	}
	dbName := os.Getenv("MYSQL_DATABASE")
	if !strings.HasPrefix(dbName, "tegu_go_test_") {
		t.Fatal("requires isolated test database")
	}
	// This disposable fixture owns its administrator secret. Never depend on
	// the runner's credentials or revive the removed admin/admin default.
	adminPassword := "synthetic-transactions-" + id()
	t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", "")
	t.Setenv("ADMIN_INITIAL_PASSWORD", adminPassword)
	cfg := mysql.Config{User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"), DBName: dbName, AllowNativePasswords: true}
	control, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer control.Close()
	dbName = "tegu_go_test_transactions_" + id()[:12]
	if _, e = control.Exec("CREATE DATABASE `" + dbName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, err := control.Exec("DROP DATABASE `" + dbName + "`"); err != nil {
			t.Error("cleanup isolated transactions test database", err)
		}
	}()
	cfg.DBName = dbName
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	if e = initSchema(db); e != nil {
		t.Fatal(e)
	}
	a := &App{db: db}
	if e = a.bootstrap(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES('worker-account','worker-account','施工测试账号','','employee',?,?)", jsonText([]string{"orders"}), jsonText([]string{"public-r"})); e != nil {
		t.Fatal(e)
	}
	u := User{ID: "admin", Name: "test", Role: "admin"}
	ctx := context.Background()
	_, e = db.Exec("INSERT INTO companies(id,name) VALUES('c','公司')")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO workers(id,name,user_id,crafts) VALUES('w','师傅','worker-account',?)", jsonText(categories)); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{
		"INSERT INTO warehouses(id,name,worker_id) VALUES('a','师傅仓库','w'),('b','仓库B',NULL)",
		"INSERT INTO products(id,name,unit) VALUES('p','材料','桶')",
		"INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('public-r','','开工','开工','平方',0,100)",
		"INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,source_id) VALUES('r','c','开工','开工','平方',600,0,'public-r')",
	} {
		if _, e = db.Exec(statement); e != nil {
			t.Fatal(e)
		}
	}
	call := func(path, key string, input any) (any, error) {
		b := []byte(jsonText(input))
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b))).WithContext(ctx)
		r.Header.Set("Idempotency-Key", key)
		return a.mutate(r, u, b, func(tx *sql.Tx, k string) (any, error) { return a.post(tx, path, u, b, k) })
	}
	stock := map[string]any{"kind": "进货", "date": "2026-09-19", "handler": "测试经手人", "to": "a", "lines": []Material{{Product: "p", Qty: 10000000}}}
	const key = "duplicate-stock-operation"
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := call("/api/stock", key, stock); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var balance, count int64
	db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance)
	db.QueryRow("SELECT COUNT(*) FROM stock_documents").Scan(&count)
	if balance != 10000000 || count != 1 {
		t.Fatal("duplicate receipt", balance, count)
	}
	stock["note"] = "different"
	if _, e = call("/api/stock", key, stock); e == nil {
		t.Fatal("same key accepted different body")
	}
	errs = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := call("/api/stock", id(), map[string]any{"kind": "调拨", "date": "2026-09-19", "handler": "测试经手人", "from": "a", "to": "b", "lines": []Material{{Product: "p", Qty: 7000000}}})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("oversell", success)
	}
	db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance)
	if balance != 3000000 {
		t.Fatal(balance)
	}
	projectResult, e := call("/api/project", id(), Project{Company: "c", Name: "核心流程项目", Address: "工地"})
	if e != nil {
		t.Fatal(e)
	}
	o := Order{Company: "c", Project: projectResult.(map[string]any)["id"].(string), Address: "工地", Date: "2026-09-19", Worker: "w", Lines: []Line{{CraftID: "r", Category: "开工", Segments: []Segment{{Length: 2, Width: 3}}}}, Materials: []Material{{Product: "p", Qty: 1000000}}}
	result, e := call("/api/order", id(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.ID = result.(map[string]any)["id"].(string)
	o.Version = 1
	db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance)
	if balance != 2000000 {
		t.Fatal(balance)
	}
	o.Materials[0].Qty = 2000000
	_, e = call("/api/order", id(), o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = call("/api/order", id(), o); e == nil {
		t.Fatal("stale edit accepted")
	}
	o.Version = 2
	o.Materials[0].Qty = 999000000
	if _, e = call("/api/order", id(), o); e == nil {
		t.Fatal("excess material accepted")
	}
	var version int64
	db.QueryRow("SELECT version FROM orders WHERE id=?", o.ID).Scan(&version)
	if version != 2 {
		t.Fatal("failed transaction changed order", version)
	}
	db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance)
	if balance != 1000000 {
		t.Fatal("failed transaction changed stock", balance)
	}
	o.Materials = nil
	o.Delete = true
	u = User{ID: "worker-account", Name: "施工测试账号", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"public-r"}}
	if _, e = call("/api/order", id(), o); e == nil {
		t.Fatal("employee without delete permission cancelled order")
	} else if f, ok := e.(failure); !ok || f.Code != 403 {
		t.Fatalf("expected 403 for missing delete permission, got %v", e)
	}
	db.QueryRow("SELECT version FROM orders WHERE id=?", o.ID).Scan(&version)
	if version != 2 {
		t.Fatal("denied cancellation changed order", version)
	}
	u.Permissions = append(u.Permissions, "order_delete")
	if _, e = call("/api/order", id(), o); e != nil {
		t.Fatal(e)
	}
	u = User{ID: "admin", Name: "test", Role: "admin"}
	db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance)
	if balance != 3000000 {
		t.Fatal("cancel did not return material", balance)
	}
	var status string
	if e = db.QueryRow("SELECT status FROM orders WHERE id=?", o.ID).Scan(&status); e != nil || status != "已取消" {
		t.Fatal("cancel did not retain historical order", status, e)
	}
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	file, e := a.backup()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.restore(filepath.Join(dir, file)); e == nil {
		t.Fatal("restore overwrote occupied database")
	}
	// Restore into a separately created empty database, then compare business data.
	restoredName := dbName + "_restore"
	if _, e = db.Exec("CREATE DATABASE `" + restoredName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); e != nil {
		t.Fatal(e)
	}
	defer db.Exec("DROP DATABASE `" + restoredName + "`")
	cfg.DBName = restoredName
	restoredDB, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer restoredDB.Close()
	if e = initSchema(restoredDB); e != nil {
		t.Fatal(e)
	}
	restored := &App{db: restoredDB}
	if e = restored.restore(filepath.Join(dir, file)); e != nil {
		t.Fatal("empty restore failed", e)
	}
	for _, table := range []string{"orders", "order_lines", "order_materials", "stock_documents", "stock_movements", "requests", "audit", "crafts"} {
		var originalCount, restoredCount int
		if e = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&originalCount); e != nil {
			t.Fatal(e)
		}
		if e = restoredDB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&restoredCount); e != nil {
			t.Fatal(e)
		}
		if originalCount != restoredCount {
			t.Fatalf("restore mismatch %s: %d/%d", table, originalCount, restoredCount)
		}
	}
	if e = restoredDB.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='a' AND product_id='p'").Scan(&balance); e != nil || balance != 3000000 {
		t.Fatal("restored balance mismatch", balance, e)
	}
	f, _ := os.OpenFile(filepath.Join(dir, file), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("corrupt")
	f.Close()
	if e = a.restore(filepath.Join(dir, file)); e == nil {
		t.Fatal("corrupt backup accepted")
	}
	t.Log("8 duplicate requests: one receipt; concurrent transfer no oversell; order/stock atomicity; stale edit rejection; cancellation returns stock; occupied and corrupt restore refused")
	projectAcceptance(t, a)
	workflowAcceptance(t, a)
	financialReportingAcceptance(t, a)
	backupCloudFailureRetentionAcceptance(t, a)
	settlementAcceptance(t, a)
	acceptanceAPI(t, a, adminPassword)
	largeRestoreAcceptance(t, a, cfg, dbName)
}

func largeRestoreAcceptance(t *testing.T, a *App, cfg mysql.Config, dbName string) {
	t.Helper()
	// Earlier finance/settlement checks intentionally seed old no-account rows.
	// After those compatibility checks, prepare a complete current-schema source
	// snapshot independently of the restore/backfill functions being tested.
	completeLargeRestoreWageFixture(t, a.db)
	file, e := a.backup()
	if e != nil {
		t.Fatal(e)
	}
	targetName := dbName + "_full_restore"
	if _, e = a.db.Exec("CREATE DATABASE `" + targetName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); e != nil {
		t.Fatal(e)
	}
	defer a.db.Exec("DROP DATABASE `" + targetName + "`")
	cfg.DBName = targetName
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = initSchema(db); e != nil {
		t.Fatal(e)
	}
	restored := &App{db: db}
	if e = restored.restore(filepath.Join(os.Getenv("BACKUP_DIR"), file)); e != nil {
		t.Fatal(e)
	}
	// Compare every field, not merely row counts: balances, names, amounts, roles, logs and manifests.
	digest := func(db *sql.DB, table string) string {
		t.Helper()
		rs, e := rows(db, "SELECT * FROM "+table)
		if e != nil {
			t.Fatal(e)
		}
		encoded := make([]string, 0, len(rs))
		for _, r := range rs {
			b, e := json.Marshal(r)
			if e != nil {
				t.Fatal(e)
			}
			encoded = append(encoded, string(b))
		}
		sort.Strings(encoded)
		h := sha256.New()
		for _, r := range encoded {
			h.Write([]byte(r))
			h.Write([]byte{10})
		}
		return string(h.Sum(nil))
	}
	for _, table := range backupTables {
		if table == "backup_runs" {
			continue
		}
		sourceDigest, restoredDigest := digest(a.db, table), digest(db, table)
		if sourceDigest != restoredDigest {
			t.Fatalf("full restore field mismatch %s: source=%x restored=%x", table, sourceDigest, restoredDigest)
		}
	}
	t.Log("109500+ order database restored into independent DB; all fields in business, permission, inventory, audit, idempotency and archive-manifest tables match (backup_runs excluded because backup adds its own record)")
}

func completeLargeRestoreWageFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	var database string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&database); err != nil || !strings.HasPrefix(database, "tegu_go_test_") {
		t.Fatal("large restore fixture requires an isolated test database", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = validateWorkerAccounts(tx); err != nil {
		t.Fatal("invalid source wage fixture", err)
	}
	// These are only deliberate pre-account single-person fixture records. Use
	// distinct fixture IDs, exact header snapshots and legacy semantics; do not
	// invoke restore or backfillWorkerWages to manufacture the comparison source.
	result, err := tx.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version,legacy)
		SELECT MD5(CONCAT('fixture-legacy:',o.id)),o.id,o.worker_id,o.worker_name,o.wage_cents,o.delivery_wage_cents,o.wage_paid,o.version,1
		FROM orders o WHERE NOT EXISTS(SELECT 1 FROM order_worker_wages w WHERE w.order_id=o.id)`)
	if err != nil {
		t.Fatal("complete source wage fixture", err)
	}
	if err = validateWorkerAccounts(tx); err != nil {
		t.Fatal("completed source wage fixture invalid", err)
	}
	var missing int
	if err = tx.QueryRow("SELECT COUNT(*) FROM orders o WHERE NOT EXISTS(SELECT 1 FROM order_worker_wages w WHERE w.order_id=o.id)").Scan(&missing); err != nil || missing != 0 {
		t.Fatal("full snapshot source still lacks wage accounts", missing, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	added, _ := result.RowsAffected()
	t.Logf("complete current-schema snapshot fixture prepared: %d intentional old wage accounts added independently; existing accounts retained", added)
}
