package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestTypedWorkerWarehouseSaveMySQL(t *testing.T) {
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("set GO_MYSQL_TEST=1 on isolated MySQL")
	}
	if !strings.HasPrefix(os.Getenv("MYSQL_DATABASE"), "tegu_go_test_") {
		t.Fatal("requires isolated test database")
	}
	cfg := mysql.Config{User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"), DBName: os.Getenv("MYSQL_DATABASE"), AllowNativePasswords: true}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	name := "tegu_go_test_typed_" + id()[:12]
	if _, err = control.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, cleanupErr := control.Exec("DROP DATABASE `" + name + "`"); cleanupErr != nil {
			t.Errorf("remove isolated test database: %v", cleanupErr)
		}
	}()
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db}
	admin := User{ID: "admin", Name: "系统管理员", Role: "admin"}
	save := func(payload map[string]any) (map[string]any, error) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.saveEntity(tx, admin, body)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return result.(map[string]any), nil
	}
	worker, err := save(map[string]any{"entity": "workers", "name": "定向验收施工人", "phone": "123", "crafts": []string{"开工"}})
	if err != nil {
		t.Fatal(err)
	}
	workerID := worker["id"].(string)
	var warehouseID, workerName string
	if err = db.QueryRow("SELECT id,name FROM warehouses WHERE worker_id=?", workerID).Scan(&warehouseID, &workerName); err != nil || workerName != "定向验收施工人" {
		t.Fatal("worker and linked warehouse not committed together", err, workerName)
	}
	if _, err = save(map[string]any{"entity": "workers", "id": workerID, "version": 0, "name": "已过期", "crafts": []string{"开工"}}); err == nil {
		t.Fatal("stale worker version accepted")
	}
	var nameAfter string
	if err = db.QueryRow("SELECT name FROM workers WHERE id=?", workerID).Scan(&nameAfter); err != nil || nameAfter != "定向验收施工人" {
		t.Fatal("stale worker edit changed data", err, nameAfter)
	}
	if _, err = save(map[string]any{"entity": "warehouses", "id": warehouseID, "version": 1, "name": "改名仓库", "worker_id": workerID, "active": 1, "category": "施工人仓库", "warehouse_type": "普通仓库", "note": "核对"}); err != nil {
		t.Fatal(err)
	}
	var active, version int
	if err = db.QueryRow("SELECT name,active,version FROM warehouses WHERE id=?", warehouseID).Scan(&nameAfter, &active, &version); err != nil || nameAfter != "改名仓库" || active != 1 || version != 2 {
		t.Fatal("warehouse edit did not persist", err, nameAfter, active, version)
	}
	var audits int
	if err = db.QueryRow("SELECT COUNT(*) FROM audit WHERE target_id IN (?,?)", workerID, warehouseID).Scan(&audits); err != nil || audits != 3 {
		t.Fatal("worker, automatic warehouse and warehouse edit audits missing", err, audits)
	}
}
