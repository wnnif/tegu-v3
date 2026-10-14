package main

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
)

func quantumFixture(t *testing.T) *App {
	t.Helper()
	a := stockFixDB(t)
	stockFixExec(t, a.db, "INSERT INTO workers(id,name,crafts) VALUES('stock-worker','合成工人',JSON_ARRAY())")
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('boss','合成总仓')")
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name,worker_id) VALUES('worker','合成个人结余','stock-worker')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('material','合成防水材料','桶','小组',6)")
	// stockFixExec synchronises the phase-2 unit definition for synthetic
	// products.  Keep the fixture to one source of truth so this does not
	// race with or duplicate that helper insert.
	return a
}

func quantumStockCall(t *testing.T, a *App, input map[string]any) string {
	t.Helper()
	result, err := stockFixCall(a, stockFixActor(), "/api/stock", id(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["id"].(string)
}

func quantumStockDocument(kind, source, destination, quantity, unit string, amount int64) map[string]any {
	return map[string]any{
		"kind": kind, "date": "2026-10-11", "from": source, "to": destination, "handler": "合成经手人",
		"lines": []map[string]any{{"product_id": "material", "input_quantity": quantity, "input_unit": unit, "total_cents": amount}},
	}
}

func quantumBalance(t *testing.T, a *App, warehouse string) int64 {
	t.Helper()
	var quantity int64
	if err := a.db.QueryRow("SELECT qty_quantum FROM stock_balances WHERE warehouse_id=? AND product_id='material'", warehouse).Scan(&quantity); err != nil {
		t.Fatal(err)
	}
	return quantity
}

func TestInventoryQuantumSixAuxiliaryUsesMySQL(t *testing.T) {
	a := quantumFixture(t)
	quantumStockCall(t, a, quantumStockDocument("进货", "", "boss", "1", "base", 24000))
	if got := quantumBalance(t, a, "boss"); got != 6000000 {
		t.Fatalf("one bucket should be six auxiliary units, got %d quantum", got)
	}
	for i := 0; i < 6; i++ {
		quantumStockCall(t, a, quantumStockDocument("报损", "boss", "", "1", "aux", 0))
	}
	if got := quantumBalance(t, a, "boss"); got != 0 {
		t.Fatalf("six one-auxiliary decrements must leave exactly zero, got %d", got)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 7)
	response := httptest.NewRecorder()
	if err := a.listStockMovements(response, httptest.NewRequest("GET", "/api/stock-movements?product_id=material", nil), stockFixActor()); err != nil {
		t.Fatal(err)
	}
	var movements queryPageResult[stockMovementRecord]
	if err := json.Unmarshal(response.Body.Bytes(), &movements); err != nil || movements.Total != 7 || movements.Rows[0].DeltaQuantum != "-1000000" || movements.Rows[0].BaseFactor != "6" {
		t.Fatalf("ledger lost exact auxiliary decrement: %+v, %v", movements, err)
	}
}

func TestInventoryQuantumOrderMaterialAndSaleMySQL(t *testing.T) {
	a := quantumFixture(t)
	stockFixExec(t, a.db, "UPDATE workers SET crafts=JSON_ARRAY('开工') WHERE id='stock-worker'")
	stockFixExec(t, a.db, "INSERT INTO companies(id,name) VALUES('order-company','合成库存公司')")
	quantumStockCall(t, a, quantumStockDocument("进货", "", "boss", "1", "base", 24000))
	read := func(orderID string) Order {
		t.Helper()
		response := httptest.NewRecorder()
		if err := a.readOrder(response, httptest.NewRequest("GET", "/api/order?id="+orderID, nil), stockFixActor()); err != nil {
			t.Fatal(err)
		}
		var order Order
		if err := json.Unmarshal(response.Body.Bytes(), &order); err != nil {
			t.Fatal(err)
		}
		return order
	}
	var last string
	var project string
	for i := 0; i < 6; i++ {
		order := Order{Company: "order-company", Project: project, Address: "合成六次耗料地址", Date: "2026-09-02", Worker: "stock-worker",
			Lines:     []Line{{Temporary: true, Category: "开工", Name: "合成施工", Mode: "金额", Price: 1000}},
			Materials: []Material{{Product: "material", Warehouse: "boss", InputQty: "1", InputUnit: "aux"}}}
		key := id()
		result, err := stockFixCall(a, stockFixActor(), "/api/order", key, order)
		if err != nil {
			t.Fatal(err)
		}
		last = result.(map[string]any)["id"].(string)
		if _, err = stockFixCall(a, stockFixActor(), "/api/order", key, order); err != nil {
			t.Fatal(err)
		}
		saved := read(last)
		project = saved.Project
		if saved.Materials[0].InputQty != "1" || saved.Materials[0].InputUnit != "aux" || saved.Materials[0].QtyQuantum != "1000000" {
			t.Fatalf("reopen changed material: %+v", saved.Materials)
		}
		saved.Note = "合成重开保存"
		if _, err = stockFixCall(a, stockFixActor(), "/api/order", id(), saved); err != nil {
			t.Fatal(err)
		}
		if got := quantumBalance(t, a, "boss"); got != int64(5-i)*1000000 {
			t.Fatalf("use %d balance=%d", i+1, got)
		}
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 7)
	// Returning material and adding a sale of the same amount is a net-zero
	// balance edit, but both distinct business events must remain in the ledger.
	saved := read(last)
	saved.Materials = nil
	saved.Sales = []Sale{{Product: "material", Warehouse: "boss", InputQty: "1", InputUnit: "aux", Price: 1500}}
	if _, err := stockFixCall(a, stockFixActor(), "/api/order", id(), saved); err != nil {
		t.Fatal(err)
	}
	if got := quantumBalance(t, a, "boss"); got != 0 {
		t.Fatalf("offset edit changed balance: %d", got)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements WHERE source_type='施工耗材'", 7)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements WHERE source_type='商品销售'", 1)
	saved = read(last)
	if saved.Sales[0].InputQty != "1" || saved.Sales[0].QtyQuantum != "1000000" {
		t.Fatal("sale reopen changed quantity")
	}
	maintenanceFaultCount(t, a.db, "SELECT total_cents FROM order_sales WHERE order_id='"+last+"'", 1500)
	saved.Delete = true
	saved.Combinations = nil
	key := id()
	if _, err := stockFixCall(a, stockFixActor(), "/api/order", key, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := stockFixCall(a, stockFixActor(), "/api/order", key, saved); err != nil {
		t.Fatal(err)
	}
	if got := quantumBalance(t, a, "boss"); got != 1000000 {
		t.Fatalf("cancel did not return one small group: %d", got)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 10)
	response := httptest.NewRecorder()
	if err := a.listStockMovements(response, httptest.NewRequest("GET", "/api/stock-movements?from=2026-09-02&to=2026-09-02", nil), stockFixActor()); err != nil {
		t.Fatal(err)
	}
	var page queryPageResult[stockMovementRecord]
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Total != 9 || page.Rows[0].ProjectAddress != "合成六次耗料地址" {
		t.Fatalf("order ledger date/source lost: %+v err=%v", page, err)
	}
}

func TestInventoryQuantumWorkerAndAverageValueMySQL(t *testing.T) {
	a := quantumFixture(t)
	quantumStockCall(t, a, quantumStockDocument("进货", "", "boss", "20", "base", 240000))
	quantumStockCall(t, a, quantumStockDocument("领料", "boss", "worker", "5", "base", 0))
	if boss, worker := quantumBalance(t, a, "boss"), quantumBalance(t, a, "worker"); boss != 90000000 || worker != 30000000 {
		t.Fatalf("20-5 transfer changed company total: boss=%d worker=%d", boss, worker)
	}
	quantumStockCall(t, a, quantumStockDocument("报损", "worker", "", "2", "base", 0))
	quantumStockCall(t, a, quantumStockDocument("退料", "worker", "boss", "1", "base", 0))
	if boss, worker := quantumBalance(t, a, "boss"), quantumBalance(t, a, "worker"); boss != 96000000 || worker != 12000000 {
		t.Fatalf("use and return balances: boss=%d worker=%d", boss, worker)
	}
	quantumStockCall(t, a, quantumStockDocument("进货", "", "boss", "10", "base", 180000))
	response := httptest.NewRecorder()
	if err := a.inventoryList(response, httptest.NewRequest("GET", "/api/inventory?group_by=product&product_id=material", nil), stockFixActor()); err != nil {
		t.Fatal(err)
	}
	var inventory queryPageResult[inventorySummaryRecord]
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil || inventory.Total != 1 || len(inventory.Rows) != 1 {
		t.Fatalf("inventory summary failed: %+v, %v", inventory, err)
	}
	item := inventory.Rows[0]
	if item.QuantityQuantum != "168000000" || item.Quantity != "28.000000" || item.ReferenceValue != "392000" {
		t.Fatalf("expected 28 buckets at 14000 cents average, got %+v", item)
	}
}

func TestInventoryQuantumConcurrentAndReversalMySQL(t *testing.T) {
	a := quantumFixture(t)
	quantumStockCall(t, a, quantumStockDocument("进货", "", "boss", "5", "base", 60000))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := stockFixCall(a, stockFixActor(), "/api/stock", id(), quantumStockDocument("报损", "boss", "", "4", "base", 0))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 || quantumBalance(t, a, "boss") != 6000000 {
		t.Fatalf("concurrent decrements overspent stock: success=%d failure=%d", successes, failures)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 2)
	if _, err := stockFixCall(a, stockFixActor(), "/api/stock", id(), quantumStockDocument("报损", "boss", "", "2", "base", 0)); err == nil {
		t.Fatal("insufficient balance was accepted")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 2)
}

func TestInventoryQuantumEditVoidRestoreMySQL(t *testing.T) {
	a := quantumFixture(t)
	input := quantumStockDocument("进货", "", "boss", "2", "base", 24000)
	key := id()
	result, err := stockFixCall(a, stockFixActor(), "/api/stock", key, input)
	if err != nil {
		t.Fatal(err)
	}
	documentID := result.(map[string]any)["id"].(string)
	if replay, err := stockFixCall(a, stockFixActor(), "/api/stock", key, input); err != nil || replay.(map[string]any)["id"] != documentID {
		t.Fatalf("idempotent retry created a second document: %v", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 1)
	var saved string
	if err := a.db.QueryRow("SELECT line_items FROM stock_documents WHERE id=?", documentID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	edit := quantumStockDocument("进货", "", "boss", "2", "base", 24000)
	edit["id"], edit["version"], edit["lines"], edit["note"] = documentID, 1, json.RawMessage(saved), "合成备注变更"
	quantumStockCall(t, a, edit)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 1)
	edit = quantumStockDocument("进货", "", "boss", "3", "base", 45000)
	edit["id"], edit["version"] = documentID, 2
	quantumStockCall(t, a, edit)
	if got := quantumBalance(t, a, "boss"); got != 18000000 {
		t.Fatalf("edit did not apply only one bucket difference: %d", got)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 2)
	for _, action := range []struct {
		name    string
		version int
		want    int64
	}{{"void", 3, 0}, {"restore", 4, 18000000}} {
		state := map[string]any{"id": documentID, "version": action.version, "action": action.name, "reason": "合成作废验证"}
		if _, err := stockFixCall(a, stockFixActor(), "/api/stock-state", id(), state); err != nil {
			t.Fatal(err)
		}
		if got := quantumBalance(t, a, "boss"); got != action.want {
			t.Fatalf("%s balance=%d want=%d", action.name, got, action.want)
		}
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 4)
}

func TestInventoryQuantumMigrationRejectsAmbiguousHistoryMySQL(t *testing.T) {
	a := quantumFixture(t)
	// Simulate a pre-441 database row whose saved conversion ratio no longer
	// agrees with the current product definition. Migration must fail before
	// marking 441 complete, rather than silently interpreting the old quantity.
	lineItems := `[{
		"product_id":"material","qty_micro":1000000,"unit":"桶","aux_unit":"小组",
		"aux_ratio":"5.000000","base_factor":"6","aux_factor":"1",
		"input_quantity":"1","input_unit":"base","quantity_quantum":"6000000","total_cents":24000
	}]`
	if _, err := a.db.Exec(`INSERT INTO stock_documents(id,kind,work_date,source_id,destination_id,note,created_by,total_cents,line_items,handler)
		VALUES('ambiguous-history','进货','2026-10-11','','boss','',?,?,?,?)`,
		"migration-test", 24000, lineItems, "合成经手人"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("DELETE FROM schema_migrations WHERE version=441"); err != nil {
		t.Fatal(err)
	}
	if err := migrateInventoryQuantum(a.db); err == nil {
		t.Fatal("ambiguous historical conversion was silently accepted")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=441", 0)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM product_purchase_costs", 0)
}

func TestInventoryQuantumMovementUsesSourceDateMySQL(t *testing.T) {
	a := quantumFixture(t)
	// Different source tables may legally have the same ID. Source types must
	// keep the joins separate so count, date filtering and address stay correct.
	for _, statement := range []string{
		`INSERT INTO companies(id,name) VALUES('movement-company','合成公司')`,
		`INSERT INTO stock_documents(id,kind,work_date,note,created_by) VALUES('shared-source','进货','2026-08-01','','test')`,
		`INSERT INTO orders(id,number,company_id,address,work_date,note,created_by) VALUES('shared-source','SG-SOURCE-DATE','movement-company','合成施工地址','2026-09-02','','test')`,
		`INSERT INTO stock_movements(operation_id,warehouse_id,product_id,delta_micro,delta_quantum,source_type,source_id,actor_id,created_at) VALUES
		('movement-stock','boss','material',1000000,6000000,'进货','shared-source','test','2026-10-11 10:00:00'),
		('movement-order','boss','material',-166667,-1000000,'施工单','shared-source','test','2026-10-11 11:00:00'),
		('movement-orphan','boss','material',-166667,-1000000,'报损','no-source','test','2026-10-11 12:00:00')`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		from, to, date, kind, number, address string
	}{
		{"2026-08-01", "2026-08-01", "2026-08-01", "进货", "", ""},
		{"2026-09-02", "2026-09-02", "2026-09-02", "施工单", "SG-SOURCE-DATE", "合成施工地址"},
		{"2026-10-11", "2026-10-11", "2026-10-11", "报损", "", ""},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/api/stock-movements?product_id=material&from="+item.from+"&to="+item.to, nil)
		if err := a.listStockMovements(response, request, stockFixActor()); err != nil {
			t.Fatal(err)
		}
		var movements queryPageResult[stockMovementRecord]
		if err := json.Unmarshal(response.Body.Bytes(), &movements); err != nil || movements.Total != 1 || len(movements.Rows) != 1 {
			t.Fatalf("wrong rows for source date %s: %+v err=%v", item.date, movements, err)
		}
		row := movements.Rows[0]
		if row.WorkDate != item.date || row.SourceType != item.kind || row.OrderNumber != item.number || row.ProjectAddress != item.address {
			t.Fatalf("source date/address crossed tables: got=%+v want=%+v", row, item)
		}
	}
}
