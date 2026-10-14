package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestRestoreLegacyProjectSnapshotsMySQL(t *testing.T) {
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("set GO_MYSQL_TEST=1 on isolated MySQL")
	}
	if !strings.HasPrefix(os.Getenv("MYSQL_DATABASE"), "tegu_go_test_") {
		t.Fatal("requires isolated test database")
	}
	cfg := mysql.Config{
		User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"),
		Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"),
		DBName: os.Getenv("MYSQL_DATABASE"), AllowNativePasswords: true,
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "tegu_go_test_legacy_" + id()[:12]
	if _, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	var migrationCount int
	if err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=300").Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("empty database should record migration 300: %d %v", migrationCount, err)
	}

	// The first order predates snapshot columns. The second has only one of
	// them, while the third must keep both historical values after restore.
	order := func(orderID string) map[string]any {
		return map[string]any{
			"id": orderID, "number": orderID, "company_id": "legacy-company",
			"address": "原施工地址", "work_date": "2026-09-01", "note": "",
			"created_by": "legacy-user", "project_id": "legacy-project",
		}
	}
	partial := order("legacy-partial")
	partial["project_name"] = "已保存的旧项目名"
	withSnapshot := order("legacy-saved")
	withSnapshot["project_name"] = "更早的项目名"
	withSnapshot["project_number"] = "OLD-001"
	currentEmpty := order("current-empty")
	currentEmpty["project_name"] = ""
	currentEmpty["project_number"] = ""
	emptyName := order("present-empty-name")
	emptyName["project_name"] = ""
	emptyNumber := order("present-empty-number")
	emptyNumber["project_number"] = ""
	records := []snapshotRecord{
		{Format: "tegu-go-v1"},
		{Table: "companies", Row: map[string]any{"id": "legacy-company", "name": "测试公司"}},
		{Table: "projects", Row: map[string]any{"id": "legacy-project", "number": "CURRENT-001", "company_id": "legacy-company", "name": "当前项目名", "address": "项目地址", "created_by": "legacy-user"}},
		{Table: "orders", Row: order("legacy-empty")},
		{Table: "orders", Row: partial},
		{Table: "orders", Row: withSnapshot},
		{Table: "orders", Row: currentEmpty},
		{Table: "orders", Row: emptyName},
		{Table: "orders", Row: emptyNumber},
		{End: true, Counts: map[string]int{"companies": 1, "projects": 1, "orders": 6}},
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, record := range records {
		if err = encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "legacy-project-snapshot.ndjson")
	if err = os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sum, err := fileHash(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file+".sha256", []byte(sum), 0600); err != nil {
		t.Fatal(err)
	}
	if err = (&App{db: db}).restore(file); err != nil {
		t.Fatal("legacy snapshot restore failed", err)
	}
	check := func(orderID, wantName, wantNumber string) {
		t.Helper()
		var gotName, gotNumber string
		if err := db.QueryRow("SELECT project_name,project_number FROM orders WHERE id=?", orderID).Scan(&gotName, &gotNumber); err != nil {
			t.Fatal(err)
		}
		if gotName != wantName || gotNumber != wantNumber {
			t.Fatalf("%s project snapshot: got %q/%q, want %q/%q", orderID, gotName, gotNumber, wantName, wantNumber)
		}
	}
	check("legacy-empty", "当前项目名", "CURRENT-001")
	check("legacy-partial", "已保存的旧项目名", "CURRENT-001")
	check("legacy-saved", "更早的项目名", "OLD-001")
	check("current-empty", "", "")
	check("present-empty-name", "", "CURRENT-001")
	check("present-empty-number", "当前项目名", "")
	if _, err = db.Exec("UPDATE projects SET name='后来修改的项目名',number='NEW-002' WHERE id='legacy-project'"); err != nil {
		t.Fatal(err)
	}
	check("legacy-empty", "当前项目名", "CURRENT-001")
	check("legacy-partial", "已保存的旧项目名", "CURRENT-001")
	check("legacy-saved", "更早的项目名", "OLD-001")
	check("current-empty", "", "")
	check("present-empty-name", "", "CURRENT-001")
	check("present-empty-number", "当前项目名", "")
}
