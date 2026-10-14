package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestCraftConfigurationMySQL(t *testing.T) {
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("requires explicit isolated MySQL test environment")
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
	name := "tegu_go_test_craft_config_" + id()[:12]
	if _, err = control.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := control.Exec("DROP DATABASE `" + name + "`"); e != nil {
			t.Errorf("remove isolated craft test database: %v", e)
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
	admin := User{ID: "configuration-admin", Name: "合成配置管理员", Role: "admin"}
	save := func(payload map[string]any) (map[string]any, error) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		result, e := a.saveEntity(tx, admin, []byte(jsonText(payload)))
		if e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return result.(map[string]any), nil
	}
	public, err := save(map[string]any{"entity": "crafts", "name": "合成按个收费项", "category": "防水涂料", "mode": "个", "wage_cents": 3000, "active": true, "subitems": []CraftSubitem{{ID: "location-b", Name: "小项乙"}, {ID: "location-a", Name: "小项甲"}}})
	if err != nil {
		t.Fatal(err)
	}
	source := public["id"].(string)
	second, err := save(map[string]any{"entity": "crafts", "name": "合成含价工艺", "category": "丙纶", "mode": "平方", "wage_cents": 500, "active": true, "subitems": []CraftSubitem{}})
	if err != nil {
		t.Fatal(err)
	}
	secondSource := second["id"].(string)
	companyA, err := save(map[string]any{"entity": "companies", "name": "合成公司甲", "ticket_mode": "split", "fixed_crafts": []map[string]any{{"source_id": source, "price_cents": 10000, "wage_cents": 999999, "ticket_slot": 2}, {"source_id": secondSource, "price_cents": 2000, "ticket_slot": 2, "included_by_craft_id": "source:" + source}}})
	if err != nil {
		t.Fatal(err)
	}
	companyB, err := save(map[string]any{"entity": "companies", "name": "合成公司乙", "ticket_mode": "single", "fixed_crafts": []map[string]any{{"source_id": source, "price_cents": 8000, "ticket_slot": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	companyAID, companyBID := companyA["id"].(string), companyB["id"].(string)
	var fixedA, fixedB, included string
	if err = db.QueryRow("SELECT id FROM crafts WHERE company_id=? AND source_id=?", companyAID, source).Scan(&fixedA); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT id FROM crafts WHERE company_id=? AND source_id=?", companyBID, source).Scan(&fixedB); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT included_by_craft_id FROM crafts WHERE company_id=? AND source_id=?", companyAID, secondSource).Scan(&included); err != nil || included != fixedA {
		t.Fatal("new combination reference not resolved", included, err)
	}
	var includedPrice, includedWage int64
	if err = db.QueryRow("SELECT price_cents,wage_cents FROM crafts WHERE company_id=? AND source_id=?", companyAID, secondSource).Scan(&includedPrice, &includedWage); err != nil || includedPrice != 2000 || includedWage != 0 {
		t.Fatal("legacy link must retain independently configured customer price", includedPrice, includedWage, err)
	}
	get := func(company string, u User) catalogReferences {
		t.Helper()
		w := httptest.NewRecorder()
		if e := a.reference(w, httptest.NewRequest("GET", "/api/reference?tables=crafts&craft_scope=order&company_id="+company, nil), u); e != nil {
			t.Fatal(e)
		}
		var result catalogReferences
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	for _, pair := range []struct {
		company, craft string
		price          int64
	}{{companyAID, fixedA, 10000}, {companyBID, fixedB, 8000}} {
		rows := get(pair.company, admin).Crafts
		found := false
		for _, row := range rows {
			if row.ID == pair.craft {
				found = true
				if row.PriceCents != pair.price || row.WageCents == nil || *row.WageCents != 3000 || row.Mode != "个" || len(row.Subitems) != 2 || row.Subitems[0].ID != "location-b" {
					t.Fatalf("source wage or company charge/template wrong: %+v", row)
				}
			}
		}
		if !found {
			t.Fatal("fixed craft missing")
		}
	}
	employee := User{Role: "employee", Permissions: []string{"orders"}, Crafts: []string{source}}
	rows := get(companyAID, employee).Crafts
	if len(rows) != 2 {
		t.Fatal("ungranted fixed craft was hidden", len(rows))
	}
	for _, row := range rows {
		if row.Editable != (row.ID == fixedA) || row.WageCents != nil {
			t.Fatalf("write or wage access widened: %+v", row)
		}
	}
	if rows := get(companyBID, employee).Crafts; len(rows) != 1 || !rows[0].Editable {
		t.Fatal("public grant did not authorize the second company", rows)
	}
	if _, err = save(map[string]any{"entity": "crafts", "id": source, "version": 1, "name": "合成按个收费项新版", "category": "开工", "mode": "个", "wage_cents": 3500, "active": true, "subitems": []CraftSubitem{{ID: "location-a", Name: "小项甲新版"}}}); err != nil {
		t.Fatal(err)
	}
	for _, row := range get(companyAID, admin).Crafts {
		if row.ID == fixedA && (row.WageCents == nil || *row.WageCents != 3500 || row.Name != "合成按个收费项新版" || len(row.Subitems) != 1 || row.Subitems[0].Name != "小项甲新版") {
			t.Fatalf("public edits did not follow explicit source: %+v", row)
		}
	}
	if _, err = save(map[string]any{"entity": "companies", "id": companyAID, "version": 1, "name": "错误分组", "ticket_mode": "single"}); err == nil {
		t.Fatal("ticket mode change omitted fixed group validation")
	}
	var companyName string
	if err = db.QueryRow("SELECT name FROM companies WHERE id=?", companyAID).Scan(&companyName); err != nil || companyName != "合成公司甲" {
		t.Fatal("invalid save did not roll back company", companyName, err)
	}
	if _, err = db.Exec("INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('legacy-unlinked',?,'开工','合成按个收费项新版','个',10000,7777)", companyBID); err != nil {
		t.Fatal(err)
	}
	legacyFound := false
	for _, row := range get(companyBID, admin).Crafts {
		if row.ID == "legacy-unlinked" {
			legacyFound = true
			if !row.SourceMissing || row.Editable || row.WageCents == nil || *row.WageCents != 0 {
				t.Fatalf("unlinked craft was guessed or enabled: %+v", row)
			}
		}
	}
	if !legacyFound {
		t.Fatal("unlinked historical craft hidden")
	}
}
