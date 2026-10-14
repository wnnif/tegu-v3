package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestOrderFinanceFieldIsolationMySQL(t *testing.T) {
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
	adminDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	name := "tegu_go_test_perm_" + id()[:12]
	if _, err = adminDB.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer adminDB.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"INSERT INTO companies(id,name) VALUES('permission-company','权限测试公司')",
		"INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES('permission-project','PROJECT-1','permission-company','权限测试项目','项目地址','seed')",
		"INSERT INTO orders(id,number,company_id,project_id,project_name,project_number,address,work_date,note,status,total_cents,wage_cents,created_by,ticket_no,delivered_at,recipient,ticket_attachment,return_note) VALUES('permission-order','ORDER-1','permission-company','permission-project','权限测试项目','PROJECT-1','施工地址','2026-09-28','','已交单',10000,2000,'seed','SECRET-TICKET','2026-09-28','SECRET-RECIPIENT','SECRET-ATTACHMENT','SECRET-RETURN')",
		"INSERT INTO orders(id,number,company_id,project_id,address,work_date,note,status,wage_paid,created_by) VALUES('permission-editable','ORDER-2','permission-company','permission-project','可编辑施工单','2026-09-28','','进行中',0,'seed')",
		"INSERT INTO orders(id,number,company_id,project_id,address,work_date,note,status,wage_paid,created_by) VALUES('permission-wage-locked','ORDER-3','permission-company','permission-project','工资已发施工单','2026-09-28','','进行中',1,'seed')",
		"INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES('permission-line','permission-order','开工','测试施工','金额','[]',1000000,10000,2000,10000,2000,1)",
		"INSERT INTO payment_receipts(id,order_id,amount_cents,received_at,voucher_no,created_by) VALUES('permission-receipt','permission-order',3000,'2026-09-28','SECRET-VOUCHER','seed')",
	} {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{db: db}
	// Explicit price grant retains the original non-finance amount-view test;
	// wages-only below deliberately has no customer-price permission.
	ordersOnly := User{ID: "orders-only", Role: "employee", Permissions: []string{"orders", customerPricePermission}, Crafts: []string{"开工"}}
	wagesOnly := User{ID: "wages-only", Role: "employee", Permissions: []string{"wages"}}
	financeOnly := User{ID: "finance-only", Role: "employee", Permissions: []string{"finance", customerPricePermission}}
	operator := User{ID: "operator", Role: "operator"}
	admin := User{ID: "admin", Role: "admin"}

	getJSON := func(actor User, route string) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		if err := a.get(recorder, httptest.NewRequest("GET", route, nil), actor); err != nil {
			t.Fatal(route, actor.Role, err)
		}
		var result map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	containsField := func(record map[string]any, key string) bool {
		_, ok := record[key]
		return ok
	}
	for _, actor := range []User{ordersOnly, wagesOnly} {
		detail := getJSON(actor, "/api/order?id=permission-order")
		for _, field := range []string{"ticket_no", "delivered_at", "recipient", "ticket_attachment", "return_note", "payment_receipts"} {
			if containsField(detail, field) {
				t.Fatalf("%s order detail exposed %s", actor.Role, field)
			}
		}
		if containsField(detail, "wage_cents") != actor.can("wages") || containsField(detail, "total_cents") != actor.can(customerPricePermission) {
			t.Fatalf("%s order detail wage/construction amount isolation failed", actor.Role)
		}
		if containsField(detail, "wage_records") != actor.can("wages") {
			t.Fatal("worker wage accounts permission isolation failed")
		}
		if containsField(detail, "wage_paid") != actor.can("wages") {
			t.Fatalf("%s order detail wage payment state isolation failed", actor.Role)
		}
		view := "orders"
		if actor.can("wages") {
			view = "wages"
		}
		list := getJSON(actor, "/api/orders?view="+view+"&month=2026-09")
		rows := list["rows"].([]any)
		if len(rows) != 3 {
			t.Fatalf("%s order list size: %d", actor.Role, len(rows))
		}
		var listed map[string]any
		for _, row := range rows {
			item := row.(map[string]any)
			lookup := "id"
			if view == "wages" {
				lookup = "order_id"
			}
			if item[lookup] == "permission-order" {
				listed = item
			}
		}
		if listed == nil {
			t.Fatal("fixture order missing from list")
		}
		for _, field := range []string{"ticket_no", "delivered_at", "recipient", "ticket_attachment", "return_note"} {
			if containsField(listed, field) {
				t.Fatalf("%s order list exposed %s", actor.Role, field)
			}
		}
		if containsField(listed, "total_cents") != actor.can(customerPricePermission) || containsField(list, "total_cents") != actor.can(customerPricePermission) {
			t.Fatal("customer construction amount must follow its independent permission")
		}
		if containsField(listed, "wage_cents") != actor.can("wages") {
			t.Fatalf("%s order list wage isolation failed", actor.Role)
		}
		if containsField(listed, "wage_paid") != actor.can("wages") || containsField(list, "wage_cents") != actor.can("wages") {
			t.Fatalf("%s order list wage payment state/total isolation failed", actor.Role)
		}
		for _, example := range []struct {
			id   string
			want bool
		}{
			{"permission-order", false},
			{"permission-editable", actor.can("orders")},
			{"permission-wage-locked", false},
		} {
			order := getJSON(actor, "/api/order?id="+example.id)
			if order["editable"] != example.want {
				t.Fatalf("%s %s editable: %v, want %v", actor.Role, example.id, order["editable"], example.want)
			}
		}
	}
	for _, actor := range []User{financeOnly, operator, admin} {
		detail := getJSON(actor, "/api/order?id=permission-order")
		if !containsField(detail, "ticket_no") || !containsField(detail, "payment_receipts") ||
			containsField(detail, "wage_cents") != actor.can("wages") {
			t.Fatalf("%s finance/wage order detail missing required fields", actor.Role)
		}
	}

	export := func(actor User) [][]string {
		t.Helper()
		recorder := httptest.NewRecorder()
		route := "/api/export?view=project&project_id=permission-project"
		if err := a.get(recorder, httptest.NewRequest("GET", route, nil), actor); err != nil {
			t.Fatal(route, actor.Role, err)
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(recorder.Body.String(), "\ufeff"))).ReadAll()
		if err != nil || len(rows) != 4 {
			t.Fatalf("%s project export unreadable: %v", actor.Role, err)
		}
		return rows
	}
	findExportOrder := func(rows [][]string) []string {
		t.Helper()
		for _, row := range rows[1:] {
			if row[3] == "ORDER-1" {
				return row
			}
		}
		t.Fatal("fixture order missing from project export")
		return nil
	}
	limited := export(ordersOnly)
	if len(limited[0]) != 10 || limited[0][7] != "施工金额" || findExportOrder(limited)[7] != "100.00" ||
		strings.Contains(strings.Join(limited[0], ","), "已收金额") ||
		strings.Contains(strings.Join(limited[0], ","), "票据号") ||
		strings.Contains(strings.Join(findExportOrder(limited), ","), "SECRET-") {
		t.Fatal("orders-only project export exposed finance data or lost construction amount")
	}
	for _, actor := range []User{financeOnly, operator, admin} {
		full := export(actor)
		if len(full[0]) != 17 || full[0][8] != "已收金额" || full[0][11] != "票据号" || findExportOrder(full)[11] != "SECRET-TICKET" {
			t.Fatalf("%s finance project export lost expected fields", actor.Role)
		}
	}
	for _, route := range []string{"/api/orders?view=finance&month=2026-09&company_id=permission-company", "/api/orders?view=wages&month=2026-09", "/api/backups", "/api/backup-settings", "/api/backup-file?file=missing.ndjson", "/api/users"} {
		err := a.get(httptest.NewRecorder(), httptest.NewRequest("GET", route, nil), ordersOnly)
		f, ok := err.(failure)
		if !ok || f.Code != 403 {
			t.Fatalf("orders-only access to %s: %v", route, err)
		}
	}
	for _, route := range []string{"/api/orders?view=orders&month=2026-09&wage_paid=1", "/api/export?view=orders&month=2026-09&wage_paid=1"} {
		err := a.get(httptest.NewRecorder(), httptest.NewRequest("GET", route, nil), ordersOnly)
		f, ok := err.(failure)
		if !ok || f.Code != 403 {
			t.Fatalf("orders-only wage state filtering through %s: %v", route, err)
		}
	}
}
