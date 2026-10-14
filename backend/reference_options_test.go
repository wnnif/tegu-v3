package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestReferenceOptionsMySQL(t *testing.T) {
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
	name := "tegu_go_test_refs_" + id()[:12]
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
	for i := 0; i < 61; i++ {
		if _, err = db.Exec("INSERT INTO companies(id,name) VALUES(?,?)", fmt.Sprintf("company-%02d", i), fmt.Sprintf("公司%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO workers(id,name,crafts) VALUES('worker-open','开工师傅','[\"开工\"]')",
		"INSERT INTO workers(id,name,crafts) VALUES('worker-repair','维修师傅','[\"维修\"]')",
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{db: db}
	admin := User{ID: "admin", Role: "admin"}
	get := func(route string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		if err := a.get(w, httptest.NewRequest("GET", route, nil), admin); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := get("/api/reference-options?entity=companies&page=1")
	second := get("/api/reference-options?entity=companies&page=2")
	if first["total"] != float64(61) || len(first["rows"].([]any)) != 50 || len(second["rows"].([]any)) != 11 {
		t.Fatal("reference options pagination", first["total"], len(first["rows"].([]any)), len(second["rows"].([]any)))
	}
	selected := get("/api/reference-options?entity=companies&id=company-60")
	if len(selected["rows"].([]any)) != 1 {
		t.Fatal("selected company outside first page was not returned")
	}
	craft := get("/api/reference-options?entity=workers&craft=%E5%BC%80%E5%B7%A5")
	if craft["total"] != float64(1) || craft["rows"].([]any)[0].(map[string]any)["id"] != "worker-open" {
		t.Fatal("worker craft filter", craft)
	}
	search := get("/api/catalog?entity=companies&q=%E5%85%AC%E5%8F%B860")
	if search["total"] != float64(1) {
		t.Fatal("catalog search", search)
	}
	ordersOnly := User{ID: "orders-only", Role: "employee", Permissions: []string{"orders"}}
	if err := a.listCatalog(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/catalog?entity=companies", nil), ordersOnly); err == nil || err.(failure).Code != 403 {
		t.Fatal("catalog permission bypass", err)
	}
}

func TestWarehouseReferenceWorkerNameMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	maintenanceFaultExec(t, a.db, `INSERT INTO workers(id,name,phone) VALUES('warehouse-worker-search','合成张师傅','合成电话123456')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO warehouses(id,name,worker_id,category) VALUES('warehouse-search-linked','合成固定仓名','warehouse-worker-search','合成施工人仓类'),('warehouse-search-unlinked','合成无关联仓',NULL,'合成普通仓类')`)
	get := func(actor User, query string) []warehousesRecord {
		t.Helper()
		recorder := httptest.NewRecorder()
		path := "/api/reference-options?entity=warehouses&q=" + url.QueryEscape(query)
		if err := a.referenceOptions(recorder, httptest.NewRequest("GET", path, nil), actor); err != nil {
			t.Fatal("warehouse reference search", err)
		}
		var result struct {
			Rows     []warehousesRecord `json:"rows"`
			Total    int64              `json:"total"`
			Page     int                `json:"page"`
			PageSize int                `json:"page_size"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Total != int64(len(result.Rows)) || result.Page != 1 || result.PageSize != 50 {
			t.Fatal("warehouse search pagination contract changed", result)
		}
		if strings.Contains(recorder.Body.String(), "合成电话123456") || strings.Contains(recorder.Body.String(), `"phone"`) {
			t.Fatal("warehouse search exposed worker phone")
		}
		return result.Rows
	}
	actors := []User{{Role: "admin"}, {Role: "employee", Permissions: []string{"warehouse"}}, {Role: "employee", Permissions: []string{"orders"}}}
	for _, actor := range actors {
		rows := get(actor, "合成张师傅")
		if len(rows) != 1 || rows[0].ID != "warehouse-search-linked" || rows[0].Name != "合成固定仓名" || rows[0].WorkerName != "合成张师傅" {
			t.Fatal("different warehouse/worker name did not match", actor.Role, rows)
		}
		if len(get(actor, "合成电话123456")) != 0 {
			t.Fatal("warehouse search matched private worker phone", actor.Role)
		}
	}
	maintenanceFaultExec(t, a.db, `UPDATE workers SET name='合成李师傅',version=version+1 WHERE id='warehouse-worker-search'`)
	for _, actor := range actors {
		current := get(actor, "合成李师傅")
		if len(current) != 1 || current[0].WorkerName != "合成李师傅" || current[0].Name != "合成固定仓名" {
			t.Fatal("renamed worker search did not use current name", current)
		}
		if len(get(actor, "合成张师傅")) != 0 {
			t.Fatal("worker search retained stale worker name")
		}
		for _, query := range []string{"合成固定仓名", "合成施工人仓类"} {
			if rows := get(actor, query); len(rows) != 1 || rows[0].ID != "warehouse-search-linked" {
				t.Fatal("warehouse name/category search lost compatibility", query, rows)
			}
		}
	}
	if rows := get(actors[0], "合成无关联仓"); len(rows) != 1 || rows[0].WorkerName != "" {
		t.Fatal("unlinked warehouse search failed", rows)
	}
	for _, actor := range []User{{Role: "employee", Permissions: []string{"workers"}}, {Role: "employee", Permissions: []string{"wages"}}, {Role: "employee", Permissions: []string{"finance"}}, {Role: "guest"}} {
		maintenanceFaultError(t, a.referenceOptions(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/reference-options?entity=warehouses&q="+url.QueryEscape("合成李师傅"), nil), actor), 403)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM warehouses WHERE name='合成固定仓名' AND version=1 AND worker_id='warehouse-worker-search'", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances", 0)
	t.Log("warehouse selector searches current associated worker name across renames, retains warehouse name/category and unlinked warehouses, does not search/expose phone or broaden read permissions")
}
