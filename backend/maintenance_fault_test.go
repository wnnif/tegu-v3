package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func maintenanceFaultError(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatal("fault unexpectedly succeeded")
	}
	if code != 0 {
		var f failure
		if !errors.As(err, &f) || f.Code != code {
			t.Fatalf("expected status %d, got %v", code, err)
		}
	}
}

func maintenanceFaultSnapshot(t *testing.T, dir string, records ...snapshotRecord) string {
	t.Helper()
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, "tegu-go-"+id()+".ndjson")
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+".sha256", []byte(hash(data.Bytes())), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func maintenanceFaultFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		key, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[key] = "directory"
			return nil
		}
		sum, err := fileHash(path)
		files[key] = sum
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func maintenanceFaultDB(t *testing.T) (*App, mysql.Config) {
	t.Helper()
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("set GO_MYSQL_TEST=1 on isolated MySQL")
	}
	if !strings.HasPrefix(os.Getenv("MYSQL_DATABASE"), "tegu_go_test_") {
		t.Fatal("maintenance faults require an isolated test database")
	}
	cfg := mysql.Config{User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"), DBName: os.Getenv("MYSQL_DATABASE"), AllowNativePasswords: true}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	name := "tegu_go_test_fault_" + id()[:12]
	if _, err = control.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := control.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error("isolated maintenance database cleanup", err)
		}
		control.Close()
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	return &App{db: db}, cfg
}

func maintenanceFaultExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
	stockFixSyncLegacyBalances(t, db)
}

func maintenanceFaultCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil || count != want {
		t.Fatalf("count mismatch: got %d want %d: %v", count, want, err)
	}
}

func maintenanceFaultWaitJob(t *testing.T, a *App, recorder *httptest.ResponseRecorder) maintenanceJob {
	t.Helper()
	if recorder.Code != 202 {
		t.Fatalf("maintenance request status %d: %s", recorder.Code, recorder.Body.String())
	}
	var accepted maintenanceJob
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := a.maintenanceJob(accepted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "运行中" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("maintenance job did not finish")
	return maintenanceJob{}
}

func TestLocalMaintenanceSnapshotGuards(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	head := snapshotRecord{Format: "tegu-go-v1"}
	end := snapshotRecord{End: true, Counts: map[string]int{}}
	valid := maintenanceFaultSnapshot(t, dir, head, end)
	if _, err := inspectSnapshot(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		records []snapshotRecord
	}{
		{"unsupported_format", []snapshotRecord{{Format: "other"}, end}},
		{"missing_end", []snapshotRecord{head}},
		{"trailing_data", []snapshotRecord{head, end, end}},
		{"wrong_count", []snapshotRecord{head, {End: true, Counts: map[string]int{"companies": 1}}}},
		{"unknown_table", []snapshotRecord{head, {Table: "unexpected", Row: map[string]any{"id": "x"}}, end}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inspectSnapshot(maintenanceFaultSnapshot(t, dir, tc.records...))
			maintenanceFaultError(t, err, 400)
		})
	}
	t.Run("missing_checksum", func(t *testing.T) {
		file := maintenanceFaultSnapshot(t, dir, head, end)
		if err := os.Remove(file + ".sha256"); err != nil {
			t.Fatal(err)
		}
		_, err := inspectSnapshot(file)
		maintenanceFaultError(t, err, 400)
	})
	t.Run("changed_content", func(t *testing.T) {
		file := maintenanceFaultSnapshot(t, dir, head, end)
		if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := inspectSnapshot(file)
		maintenanceFaultError(t, err, 400)
	})
	admin := User{ID: "admin", Name: "test", Role: "admin"}
	app := &App{}
	body := []byte(jsonText(map[string]string{"file": filepath.Base(valid)}))
	for _, user := range []User{{Role: "operator"}, {Role: "employee", Permissions: []string{"backups"}}} {
		maintenanceFaultError(t, app.backupConsole(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/maintenance/verify", nil), user, body), 403)
		maintenanceFaultError(t, app.backupFile(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/backup-file?file="+filepath.Base(valid), nil), user), 403)
		maintenanceFaultError(t, app.maintenance(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/maintenance/backup", nil), user, nil), 403)
	}
	for _, file := range []string{"../tegu-go-x.ndjson", "tegu-go-x.ndjson.partial"} {
		_, err := backupPath(file)
		maintenanceFaultError(t, err, 400)
	}
	if err := os.Mkdir(filepath.Join(dir, "tegu-go-directory.ndjson"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := backupPath("tegu-go-directory.ndjson")
	maintenanceFaultError(t, err, 400)
	t.Setenv("MYSQL_DATABASE", "tegu_restore_fault_current")
	currentDB := os.Getenv("MYSQL_DATABASE")
	for _, tc := range []struct{ target, confirm string }{{"production", "production"}, {"tegu_restore_fault_other", "different"}, {currentDB, currentDB}} {
		body = []byte(jsonText(map[string]string{"file": filepath.Base(valid), "target": tc.target, "confirm": tc.confirm}))
		maintenanceFaultError(t, app.backupConsole(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/maintenance/restore", nil), admin, body), 400)
	}
	app.backupMu.Lock()
	body = []byte(jsonText(map[string]string{"file": filepath.Base(valid), "target": "tegu_restore_fault_other", "confirm": "tegu_restore_fault_other"}))
	err = app.backupConsole(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/maintenance/restore", nil), admin, body)
	app.backupMu.Unlock()
	maintenanceFaultError(t, err, 409)
	t.Log("snapshot corruption, permissions, file paths, confirmation/current-target and process-lock guards passed without database access")
}

func TestLocalMaintenanceBackupFailuresMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	t.Setenv("DATA_DIR", t.TempDir())
	name, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inspectSnapshot(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "previous-unfinished.partial"), []byte("must remain untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	before := maintenanceFaultFiles(t, dir)
	admin := User{ID: "admin", Name: "test", Role: "admin"}
	t.Run("unwritable_path_records_failure", func(t *testing.T) {
		block := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(block, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BACKUP_DIR", filepath.Join(block, "backup"))
		w := httptest.NewRecorder()
		err := a.maintenance(w, httptest.NewRequest("POST", "/api/maintenance/backup", nil), admin, nil)
		if err != nil {
			t.Fatal(err)
		}
		if job := maintenanceFaultWaitJob(t, a, w); job.Status != "失败" {
			t.Fatalf("failed backup reported %s", job.Status)
		}
	})
	t.Run("read_failure_cleans_current_partial", func(t *testing.T) {
		maintenanceFaultExec(t, a.db, "RENAME TABLE users TO fault_users")
		defer maintenanceFaultExec(t, a.db, "RENAME TABLE fault_users TO users")
		w := httptest.NewRecorder()
		err := a.maintenance(w, httptest.NewRequest("POST", "/api/maintenance/backup", nil), admin, nil)
		if err != nil {
			t.Fatal(err)
		}
		if job := maintenanceFaultWaitJob(t, a, w); job.Status != "失败" {
			t.Fatalf("failed backup reported %s", job.Status)
		}
		if after := maintenanceFaultFiles(t, dir); !reflect.DeepEqual(before, after) {
			t.Fatalf("failed backup changed files: before=%v after=%v", before, after)
		}
	})
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM backup_runs WHERE status='失败'", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM backup_runs WHERE status='成功'", 1)
	name, err = a.backup()
	if err != nil {
		t.Fatal("retry after faults", err)
	}
	if _, err = inspectSnapshot(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM backup_runs WHERE status='成功'", 2)
	t.Log("blocked storage path and failed database read report failure; current partial removed; earlier snapshot and unrelated partial preserved; retry verified")
}

func TestLocalMaintenanceArchiveFailuresMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	maintenanceFaultExec(t, a.db, "INSERT INTO companies(id,name) VALUES('fault-company','隔离维护测试公司')")
	maintenanceFaultExec(t, a.db, "INSERT INTO orders(id,number,company_id,address,work_date,note,created_by,total_cents) VALUES('fault-order','FAULT-001','fault-company','隔离工地','2026-09-01','','admin',1000)")
	maintenanceFaultExec(t, a.db, "INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES('fault-line','fault-order','维修','测试','金额',JSON_ARRAY(),1000000,1000,0,1000,0,1)")
	if files, err := a.archive("2026-09", "fault-company", false); err != nil || len(files) != 5 {
		t.Fatal("initial archive", len(files), err)
	}
	before := maintenanceFaultFiles(t, dir)
	checkFailed := func(t *testing.T, err error) {
		t.Helper()
		maintenanceFaultError(t, err, 0)
		maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM archive_runs", 1)
		var raw string
		if err := a.db.QueryRow("SELECT value FROM settings WHERE setting_key='archive_status'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var status map[string]any
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			t.Fatal(err)
		}
		if text(status, "error") == "" {
			t.Fatal("archive failure status missing")
		}
		if after := maintenanceFaultFiles(t, dir); !reflect.DeepEqual(before, after) {
			t.Fatalf("failed archive left or changed files: before=%v after=%v", before, after)
		}
	}
	t.Run("unwritable_directory", func(t *testing.T) {
		block := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(block, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BACKUP_DIR", block)
		_, err := a.archive("2026-09", "fault-company", false)
		checkFailed(t, err)
	})
	t.Run("partial_output_failure", func(t *testing.T) {
		maintenanceFaultExec(t, a.db, "RENAME TABLE order_lines TO fault_order_lines")
		defer maintenanceFaultExec(t, a.db, "RENAME TABLE fault_order_lines TO order_lines")
		_, err := a.archive("2026-09", "fault-company", false)
		checkFailed(t, err)
	})
	t.Run("manifest_insert_failure", func(t *testing.T) {
		maintenanceFaultExec(t, a.db, "CREATE TRIGGER fault_archive_insert BEFORE INSERT ON archive_runs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='isolated archive manifest failure'")
		defer maintenanceFaultExec(t, a.db, "DROP TRIGGER fault_archive_insert")
		_, err := a.archive("2026-09", "fault-company", false)
		checkFailed(t, err)
	})
	if _, err := a.archive("2026-09", "fault-company", false); err != nil {
		t.Fatal("archive retry", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM archive_runs", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit WHERE action='生成月度归档'", 2)
	var message string
	if err := a.db.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(value,'$.error')) FROM settings WHERE setting_key='archive_status'").Scan(&message); err != nil || message != "" {
		t.Fatal("retry did not clear current error", message, err)
	}
	t.Log("unwritable storage, partial CSV failure and manifest failure remain visible; no failed-run files/manifests/audits published; earlier archive preserved; retry passed")
}

func TestLocalMaintenanceRestoreFailuresMySQL(t *testing.T) {
	a, cfg := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, "INSERT INTO companies(id,name) VALUES('source-sentinel','当前隔离库必须保留')")
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	admin := User{ID: "admin", Name: "test", Role: "admin"}
	// The restore API intentionally accepts only tegu_restore_ targets. An isolated
	// runner can pre-authorize four exact database names, instead of gaining a
	// global/wildcard restoration grant or weakening that production safeguard.
	var restoreTargets []string
	if configured := os.Getenv("GO_MYSQL_RESTORE_TARGETS"); configured != "" {
		restoreTargets = strings.Split(configured, ",")
		if len(restoreTargets) != 4 {
			t.Fatal("restore fault tests require four explicitly authorized target databases")
		}
		seen := map[string]bool{}
		for _, name := range restoreTargets {
			if !strings.HasPrefix(name, "tegu_restore_fault_") || len(name) > 64 || seen[name] {
				t.Fatal("unsafe or duplicate isolated restore target")
			}
			for _, c := range name {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
					t.Fatal("restore target must be a safe lowercase SQL identifier")
				}
			}
			seen[name] = true
		}
	}
	targetIndex := 0
	newTarget := func(t *testing.T) (string, *sql.DB) {
		t.Helper()
		name := "tegu_restore_fault_" + id()[:12]
		if len(restoreTargets) > 0 {
			if targetIndex >= len(restoreTargets) {
				t.Fatal("no authorized restore target remaining")
			}
			name = restoreTargets[targetIndex]
			targetIndex++
		}
		maintenanceFaultExec(t, a.db, "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin")
		t.Cleanup(func() { maintenanceFaultExec(t, a.db, "DROP DATABASE `"+name+"`") })
		copyCfg := cfg
		copyCfg.DBName = name
		db, err := sql.Open("mysql", copyCfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return name, db
	}
	call := func(file, target string) (*httptest.ResponseRecorder, error) {
		w := httptest.NewRecorder()
		body := []byte(jsonText(map[string]string{"file": filepath.Base(file), "target": target, "confirm": target}))
		return w, a.backupConsole(w, httptest.NewRequest("POST", "/api/maintenance/restore", nil), admin, body)
	}
	head := snapshotRecord{Format: "tegu-go-v1"}
	row := snapshotRecord{Table: "companies", Row: map[string]any{"id": "restored", "name": "隔离恢复公司"}}
	valid := maintenanceFaultSnapshot(t, dir, head, row, snapshotRecord{End: true, Counts: map[string]int{"companies": 1}})
	t.Run("missing_target", func(t *testing.T) {
		_, err := call(valid, "tegu_restore_fault_missing_"+id()[:8])
		maintenanceFaultError(t, err, 400)
	})
	t.Run("existing_table_preserved", func(t *testing.T) {
		name, db := newTarget(t)
		maintenanceFaultExec(t, db, "CREATE TABLE sentinel(id INT PRIMARY KEY)")
		maintenanceFaultExec(t, db, "INSERT INTO sentinel VALUES(7)")
		_, err := call(valid, name)
		maintenanceFaultError(t, err, 409)
		maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM sentinel WHERE id=7", 1)
	})
	t.Run("database_lock", func(t *testing.T) {
		name, db := newTarget(t)
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var locked int
		if err = conn.QueryRowContext(context.Background(), "SELECT GET_LOCK(CONCAT(DATABASE(),':restore'),0)").Scan(&locked); err != nil || locked != 1 {
			t.Fatal(locked, err)
		}
		defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':restore'))")
		_, err = call(valid, name)
		maintenanceFaultError(t, err, 409)
		maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()", 0)
	})
	t.Run("failed_insert_rolls_back_and_blocks_reuse", func(t *testing.T) {
		name, db := newTarget(t)
		invalid := maintenanceFaultSnapshot(t, dir, head, row, row, snapshotRecord{End: true, Counts: map[string]int{"companies": 2}})
		if _, err := inspectSnapshot(invalid); err != nil {
			t.Fatal("fixture must pass structure validation", err)
		}
		_, err := call(invalid, name)
		maintenanceFaultError(t, err, 400)
		if !strings.Contains(err.Error(), "目标库不可投入使用") {
			t.Fatal("missing failed-target warning", err)
		}
		maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM companies", 0)
		_, err = call(valid, name)
		maintenanceFaultError(t, err, 409)
	})
	t.Run("fresh_target_success", func(t *testing.T) {
		name, db := newTarget(t)
		w, err := call(valid, name)
		if err != nil || w.Code != 200 {
			t.Fatal("restore success", w.Code, err)
		}
		maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM companies WHERE id='restored'", 1)
	})
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM companies WHERE id='source-sentinel' AND name='当前隔离库必须保留'", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM companies", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit WHERE action='恢复到独立数据库'", 1)
	t.Log("missing/nonempty/locked targets rejected; malformed data transaction rolled back and target reuse blocked; fresh target succeeds; current isolated source unchanged")
}
