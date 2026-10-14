package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func inventorySummaryTestList[T any](t *testing.T, a *App, query string, actor User) queryPageResult[T] {
	t.Helper()
	recorder := httptest.NewRecorder()
	if err := a.inventoryList(recorder, httptest.NewRequest("GET", "/api/inventory?"+query, nil), actor); err != nil {
		t.Fatal("inventory read", query, err)
	}
	var result queryPageResult[T]
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal("inventory response", err)
	}
	return result
}

func inventorySummaryTestExport(t *testing.T, a *App, query string) [][]string {
	t.Helper()
	recorder := httptest.NewRecorder()
	if err := a.exportFiltered(recorder, httptest.NewRequest("GET", "/api/export?view=inventory&"+query, nil), User{Role: "admin", ID: "inventory-admin"}); err != nil {
		t.Fatal("inventory export", query, err)
	}
	if !strings.HasPrefix(recorder.Body.String(), "\ufeff") {
		t.Fatal("inventory export lost UTF-8 BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(recorder.Body.String(), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal("inventory CSV", err)
	}
	return rows
}

func TestInventoryProductSummaryMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{Role: "admin", ID: "inventory-admin"}
	maintenanceFaultExec(t, a.db, `INSERT INTO warehouses(id,name,active) VALUES('inventory-a','合成甲仓库',1),('inventory-b','合成停用仓库',0),('inventory-empty','合成空仓库',1)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO products(id,name,brand,spec,unit,aux_unit,aux_ratio,active) VALUES
('inventory-fraction','合成涂料','合成品牌','防水一号','桶','公斤',3.5,1),
('inventory-big','@合成大数','','','桶','公斤',0.5,1),
('inventory-inactive','合成停用商品','','','个','',1,0),
('inventory-zero','合成零库存','','','个','',1,1),
('inventory-returned','合成已归零','','','个','',1,1)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor) VALUES
('inventory-fraction','桶','公斤',7,2),('inventory-big','桶','公斤',1,2),
('inventory-inactive','个','',1,1),('inventory-zero','个','',1,1),('inventory-returned','个','',1,1)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum,version) VALUES
('inventory-a','inventory-fraction',3333333,23333331,4),('inventory-b','inventory-fraction',1666667,11666669,5),
('inventory-a','inventory-big',9223372036854775807,9223372036854775807,1),('inventory-b','inventory-big',9223372036854775807,9223372036854775807,2),
('inventory-a','inventory-inactive',1000000,1000000,3),('inventory-a','inventory-returned',0,0,8),
('inventory-orphan','inventory-fraction',999000000,6993000000,9)`)

	readOne := func(query string) inventorySummaryRecord {
		t.Helper()
		result := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&"+query, admin)
		if result.Total != 1 || len(result.Rows) != 1 {
			t.Fatalf("one-product filter mismatch: %s %+v", query, result)
		}
		return result.Rows[0]
	}
	t.Run("exact cross-warehouse sum and inactive warehouse", func(t *testing.T) {
		row := readOne("product_id=inventory-fraction&state=active")
		if row.Quantity != "5.000000" || row.Qty != "5000000" || row.AuxiliaryQuantity != "17.500000" || row.WarehouseCount != 2 {
			t.Fatal("exact aggregation or warehouse count", row)
		}
		if row.WarehouseID != "" || row.WarehouseName != "" || row.Version != 0 || row.Name != "合成涂料" || row.AuxUnit != "公斤" {
			t.Fatal("summary metadata contract", row)
		}
		large := readOne("product_id=inventory-big")
		if large.Qty != "18446744073709551614" || large.Quantity != "18446744073709.551614" || large.AuxiliaryQuantity != "9223372036854.775807" {
			t.Fatal("sum overflowed or rounded through float/int64", large)
		}
	})
	t.Run("zero toggles and matching warehouse counts", func(t *testing.T) {
		hidden := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product", admin)
		shown := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&show_zero=1", admin)
		if hidden.Total != 3 || shown.Total != 5 {
			t.Fatal("zero products missing or repeated", hidden.Total, shown.Total)
		}
		for _, row := range shown.Rows {
			if row.WarehouseCount != 3 {
				t.Fatal("show-zero distribution count", row)
			}
		}
		zero := readOne("product_id=inventory-zero&show_zero=1")
		if zero.Qty != "0" || zero.Quantity != "0.000000" || zero.AuxiliaryQuantity != "0.000000" {
			t.Fatal("missing balance is not exact zero", zero)
		}
	})
	t.Run("product filters and single warehouse", func(t *testing.T) {
		for _, query := range []string{"合成涂料", "合成品牌", "防水一号"} {
			row := readOne("q=" + url.QueryEscape(query))
			if row.ProductID != "inventory-fraction" || row.Quantity != "5.000000" {
				t.Fatal("search changed aggregation", row)
			}
		}
		for _, query := range []string{"合成停用仓库", "' OR 1=1 --"} {
			result := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&show_zero=1&q="+url.QueryEscape(query), admin)
			if result.Total != 0 {
				t.Fatal("product search matched warehouse or injection", result)
			}
		}
		inactive := readOne("state=inactive")
		if inactive.ProductID != "inventory-inactive" {
			t.Fatal("product state included inactive warehouse", inactive)
		}
		selected := readOne("product_id=inventory-fraction&warehouse_id=inventory-b&state=active")
		if selected.Quantity != "1.666667" || selected.WarehouseCount != 1 {
			t.Fatal("selected inactive warehouse disappeared", selected)
		}
		missing := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&show_zero=1&warehouse_id=missing", admin)
		if missing.Total != 0 {
			t.Fatal("nonexistent warehouse created zero stock rows", missing)
		}
	})
	t.Run("warehouse distribution and old read compatibility", func(t *testing.T) {
		details := inventorySummaryTestList[inventoryRecord](t, a, "group_by=warehouse&product_id=inventory-fraction&state=active", admin)
		if details.Total != 2 || len(details.Rows) != 2 {
			t.Fatal("distribution omitted inactive warehouse", details)
		}
		var total int64
		for _, row := range details.Rows {
			total += row.Qty
		}
		if total != 5000000 {
			t.Fatal("distribution does not match summary", total)
		}
		withZero := inventorySummaryTestList[inventoryRecord](t, a, "group_by=warehouse&product_id=inventory-fraction&state=active&show_zero=1", admin)
		if withZero.Total != 3 {
			t.Fatal("distribution zero toggle", withZero.Total)
		}
		selected := inventorySummaryTestList[inventoryRecord](t, a, "group_by=warehouse&warehouse_id=inventory-b&state=active", admin)
		if selected.Total != 2 {
			t.Fatal("single-warehouse product state", selected.Total)
		}
		legacy := inventorySummaryTestList[inventoryRecord](t, a, "warehouse_id=inventory-b&state=active", admin)
		if legacy.Total != 0 {
			t.Fatal("legacy active warehouse scope changed", legacy)
		}
		legacySearch := inventorySummaryTestList[inventoryRecord](t, a, "q="+url.QueryEscape("合成停用仓库"), admin)
		if legacySearch.Total != 2 {
			t.Fatal("legacy warehouse name search changed", legacySearch)
		}
		legacyZero := inventorySummaryTestList[inventoryRecord](t, a, "show_zero=1", admin)
		if legacyZero.Total != 15 {
			t.Fatal("legacy warehouse-product zero rows changed", legacyZero.Total)
		}
	})
	t.Run("summary and single warehouse CSV", func(t *testing.T) {
		summary := inventorySummaryTestExport(t, a, "group_by=product&state=active")
		if len(summary) != 3 || summary[0][0] != "商品" || summary[0][7] != "仓库数" {
			t.Fatal("summary CSV contract", summary)
		}
		for _, row := range summary[1:] {
			if row[0] == "'@合成大数" && (row[3] != "18446744073709.551614" || row[5] != "9223372036854.775807") {
				t.Fatal("export overflowed exact quantity", row)
			}
			if row[0] == "合成涂料" && (row[3] != "5.000000" || row[5] != "17.500000" || row[7] != "2") {
				t.Fatal("export summary disagrees with page", row)
			}
		}
		if summary[1][0] != "'@合成大数" {
			t.Fatal("export formula prefix not escaped", summary[1])
		}
		single := inventorySummaryTestExport(t, a, "group_by=warehouse&warehouse_id=inventory-b&product_id=inventory-fraction&state=active")
		if len(single) != 2 || single[0][0] != "仓库" || single[1][0] != "合成停用仓库" || single[1][4] != "1.666667" {
			t.Fatal("single warehouse CSV mismatch", single)
		}
		withZero := inventorySummaryTestExport(t, a, "group_by=product&show_zero=1&product_id=inventory-zero")
		if len(withZero) != 2 || withZero[1][3] != "0.000000" || withZero[1][7] != "3" {
			t.Fatal("show-zero export count", withZero)
		}
	})
	t.Run("permissions and invalid modes", func(t *testing.T) {
		for _, query := range []string{"group_by=product", "group_by=warehouse&product_id=inventory-fraction", ""} {
			for _, actor := range []User{{Role: "employee", Permissions: []string{"finance"}}, {Role: "employee"}, {Role: "guest"}} {
				err := a.inventoryList(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/inventory?"+query, nil), actor)
				maintenanceFaultError(t, err, 403)
			}
			orders := User{Role: "employee", Permissions: []string{"orders"}}
			_ = inventorySummaryTestList[json.RawMessage](t, a, query, orders)
			maintenanceFaultError(t, a.exportFiltered(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/export?view=inventory&"+query, nil), orders), 403)
			warehouse := User{Role: "employee", Permissions: []string{"warehouse"}}
			_ = inventorySummaryTestList[json.RawMessage](t, a, query, warehouse)
		}
		for _, query := range []string{"group_by=bogus", "group_by=product&page=1000001", "group_by=warehouse&page=1000001"} {
			maintenanceFaultError(t, a.inventoryList(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/inventory?"+query, nil), admin), 400)
		}
		maintenanceFaultError(t, a.exportFiltered(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/export?view=inventory&group_by=bogus", nil), admin), 400)
	})
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances WHERE version IN (1,2,3,4,5,8,9)", 7)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_documents", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 0)
	t.Log("exact decimal sums beyond int64; inactive warehouse balances; product/warehouse/zero filters; legacy behavior; matching summary/detail CSV; permissions verified")
}

func TestInventorySummaryPaginationMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{Role: "admin", ID: "inventory-page-admin"}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 105; i++ {
		warehouseID := fmt.Sprintf("inventory-wh-%03d", i)
		productID := fmt.Sprintf("inventory-p-%03d", i)
		if _, err = tx.Exec("INSERT INTO warehouses(id,name,active) VALUES(?,?,?)", warehouseID, fmt.Sprintf("合成仓库%03d", i), i%2 == 0); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES(?,?,'桶','公斤',2.25)", productID, fmt.Sprintf("合成分页商品%03d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor) VALUES(?,'桶','公斤',9,4)", productID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 105; i++ {
		warehouseID := fmt.Sprintf("inventory-wh-%03d", i)
		if _, err = tx.Exec("INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum) VALUES(?,'inventory-p-000',?,?)", warehouseID, i+1, (i+1)*9); err != nil {
			t.Fatal(err)
		}
		if i != 0 {
			quantity := 1000000
			if i == 104 {
				quantity = 1000001
			}
			if _, err = tx.Exec("INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum) VALUES('inventory-wh-000',?,?,?)", fmt.Sprintf("inventory-p-%03d", i), quantity, quantity*9); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		result := inventorySummaryTestList[inventorySummaryRecord](t, a, fmt.Sprintf("group_by=product&state=active&page=%d", page), admin)
		wantRows := 100
		if page == 2 {
			wantRows = 5
		}
		if result.Total != 105 || result.Page != page || len(result.Rows) != wantRows {
			t.Fatal("summary pagination mismatch", result.Total, result.Page, len(result.Rows))
		}
		for _, row := range result.Rows {
			if seen[row.ProductID] {
				t.Fatal("product duplicated across pages", row.ProductID)
			}
			seen[row.ProductID] = true
			if row.ProductID == "inventory-p-000" && (row.Qty != "5565" || row.Quantity != "0.005565" || row.AuxiliaryQuantity != "0.012521" || row.WarehouseCount != 105) {
				t.Fatal("sum computed only from first warehouse page", row)
			}
			if row.ProductID == "inventory-p-104" && (row.Quantity != "1.000001" || row.AuxiliaryQuantity != "2.250002") {
				t.Fatal("summary lost last fractional digit", row)
			}
		}
	}
	if len(seen) != 105 {
		t.Fatal("summary omitted products", len(seen))
	}
	emptyPage := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&page=3", admin)
	if emptyPage.Total != 105 || len(emptyPage.Rows) != 0 {
		t.Fatal("out-of-range empty page contract", emptyPage)
	}
	var distributionSum int64
	detailSeen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		result := inventorySummaryTestList[inventoryRecord](t, a, fmt.Sprintf("group_by=warehouse&product_id=inventory-p-000&state=active&page=%d", page), admin)
		if result.Total != 105 {
			t.Fatal("detail pagination omitted warehouses", result.Total)
		}
		for _, row := range result.Rows {
			if detailSeen[row.WarehouseID] {
				t.Fatal("warehouse duplicated across detail pages", row.WarehouseID)
			}
			detailSeen[row.WarehouseID] = true
			distributionSum += row.Qty
			if row.Quantity != fmt.Sprintf("0.%06d", row.Qty) {
				t.Fatal("warehouse quantity lost fractional digits", row)
			}
		}
	}
	if distributionSum != 5565 || len(detailSeen) != 105 {
		t.Fatal("detail pages do not reconcile", distributionSum, len(detailSeen))
	}
	summaryCSV := inventorySummaryTestExport(t, a, "group_by=product&state=active&page=2")
	detailCSV := inventorySummaryTestExport(t, a, "group_by=warehouse&product_id=inventory-p-000&state=active&page=2")
	if len(summaryCSV) != 106 || len(detailCSV) != 106 {
		t.Fatal("exports truncated to current page", len(summaryCSV), len(detailCSV))
	}
	if summaryCSV[1][3] != "0.005565" || summaryCSV[1][5] != "0.012521" || summaryCSV[1][7] != "105" {
		t.Fatal("paginated summary export quantity", summaryCSV[1])
	}
	if summaryCSV[105][3] != "1.000001" || summaryCSV[105][5] != "2.250002" || detailCSV[1][4] != "0.000001" {
		t.Fatal("export lost last fractional digit", summaryCSV[105], detailCSV[1])
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_documents", 0)
	t.Log("105 product summaries and 105 warehouse distributions paginate without duplicates; complete CSV ignores page=2; exact aggregate includes all 105 warehouses")
}

func TestInventorySummaryNoWarehouseMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{Role: "admin"}
	maintenanceFaultExec(t, a.db, "INSERT INTO products(id,name,unit) VALUES('inventory-no-warehouse','合成未入库商品','个')")
	maintenanceFaultExec(t, a.db, "INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor) VALUES('inventory-no-warehouse','个','',1,1)")
	hidden := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product", admin)
	shown := inventorySummaryTestList[inventorySummaryRecord](t, a, "group_by=product&show_zero=1", admin)
	details := inventorySummaryTestList[inventoryRecord](t, a, "group_by=warehouse&product_id=inventory-no-warehouse&show_zero=1", admin)
	if hidden.Total != 0 || shown.Total != 1 || len(shown.Rows) != 1 || shown.Rows[0].WarehouseCount != 0 || shown.Rows[0].Quantity != "0.000000" || details.Total != 0 {
		t.Fatal("empty-warehouse state", hidden, shown, details)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM warehouses", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances", 0)
	t.Log("show-zero product directory works with no warehouses, while warehouse distribution is empty; read does not create warehouses or balances")
}
