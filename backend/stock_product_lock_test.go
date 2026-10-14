package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestStockOperationID(t *testing.T) {
	userID := strings.Repeat("a", 32)
	for _, n := range []int{16, 36, 67} {
		key := strings.Repeat("x", n)
		if got := stockOperationID(userID, key); got != userID+":"+key {
			t.Fatalf("short operation ID lost legacy format: %q", got)
		}
	}
	for _, n := range []int{68, 99, 100} {
		key := strings.Repeat("x", n)
		got := stockOperationID(userID, key)
		if len(got) > 100 || strings.Contains(got, ":") || got != stockOperationID(userID, key) || got == stockOperationID("other-user", key) || got == stockOperationID(userID, key+"y") {
			t.Fatalf("invalid or colliding namespaced operation ID: %q", got)
		}
	}
}

// Pin the explicitly selected synthetic instance before creating or dropping
// this test's database. In particular, these cases must never use preview 3386.
func stockFixDB(t *testing.T) *App {
	t.Helper()
	if os.Getenv("GO_MYSQL_TEST") != "1" {
		t.Skip("requires explicit isolated MySQL")
	}
	if !strings.HasPrefix(os.Getenv("MYSQL_DATABASE"), "tegu_go_test_") || os.Getenv("MYSQL_PORT") == "3386" {
		t.Fatal("stock regression requires an isolated database outside preview")
	}
	cfg := mysql.Config{User: os.Getenv("MYSQL_USER"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"), DBName: os.Getenv("MYSQL_DATABASE"), AllowNativePasswords: true}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	var uuid, directory string
	if err = control.QueryRow("SELECT @@port,@@server_uuid,@@datadir").Scan(&port, &uuid, &directory); err != nil {
		control.Close()
		t.Fatal(err)
	}
	if strconv.Itoa(port) != os.Getenv("MYSQL_PORT") || port == 3386 {
		control.Close()
		t.Fatal("unexpected MySQL port")
	}
	guard := func() error {
		var gotPort int
		var gotUUID, gotDirectory string
		if err := control.QueryRow("SELECT @@port,@@server_uuid,@@datadir").Scan(&gotPort, &gotUUID, &gotDirectory); err != nil {
			return err
		}
		if gotPort != port || gotUUID != uuid || gotDirectory != directory {
			return errors.New("synthetic MySQL identity changed")
		}
		return nil
	}
	name := "tegu_go_test_stock_fix_" + id()[:12]
	if err = guard(); err != nil {
		control.Close()
		t.Fatal(err)
	}
	if _, err = control.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer control.Close()
		if err := guard(); err != nil {
			t.Error(err)
			return
		}
		if _, err := control.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { db.Close() })
	if err = initSchema(db); err != nil {
		t.Fatal(err)
	}
	return &App{db: db}
}

func stockFixExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
	stockFixSyncLegacyBalances(t, db)
}

// Keep older synthetic fixtures that only seed qty_micro usable after the
// phase-2 migration made qty_quantum authoritative. Production writes always
// provide both columns; this helper is deliberately test-only and only fills
// an omitted quantum value.
func stockFixSyncLegacyBalances(t *testing.T, db *sql.DB) {
	t.Helper()
	// Fixtures insert products after initSchema has already run the unit
	// definition migration. Populate definitions for those synthetic products
	// before exercising summary/movement queries that require the definition
	// row. This remains test-only; production product writes call the normal
	// synchronisation path.
	rows, err := db.Query(`SELECT p.id,p.unit,p.aux_unit,CAST(p.aux_ratio AS CHAR)
		FROM products p LEFT JOIN product_unit_definitions d ON d.product_id=p.id
		WHERE d.product_id IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	type missingDefinition struct{ id, unit, aux, ratio string }
	var missing []missingDefinition
	for rows.Next() {
		var item missingDefinition
		if err := rows.Scan(&item.id, &item.unit, &item.aux, &item.ratio); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		missing = append(missing, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for _, item := range missing {
		definition, err := newProductUnitDefinition(item.unit, item.aux, item.ratio)
		if err != nil {
			t.Fatalf("synthetic product unit definition %s: %v", item.id, err)
		}
		if _, err := db.Exec(`INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor,quantity_scale)
			VALUES(?,?,?,?,?,6) ON DUPLICATE KEY UPDATE product_id=product_id`, item.id, definition.BaseUnit, definition.AuxUnit, definition.BaseFactor, definition.AuxFactor); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`UPDATE stock_balances b
		JOIN products p ON p.id=b.product_id
		LEFT JOIN product_unit_definitions d ON d.product_id=b.product_id
		SET b.qty_quantum=CAST(CAST(b.qty_micro AS DECIMAL(65,12))*
			COALESCE(d.base_factor,
				CAST(NULLIF(p.aux_ratio,'') AS DECIMAL(65,12)),1) AS SIGNED)
		WHERE b.qty_quantum=0 AND b.qty_micro<>0`)
	if err != nil {
		t.Fatal(err)
	}
}

func stockFixCall(a *App, actor User, path, key string, input any) (any, error) {
	body := []byte(jsonText(input))
	req := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	req.Header.Set("Idempotency-Key", key)
	return a.mutate(req, actor, body, func(tx *sql.Tx, key string) (any, error) {
		return a.post(tx, path, actor, body, key)
	})
}

func stockFixActor() User {
	return User{ID: strings.Repeat("a", 32), Name: "合成库存测试", Role: "admin"}
}

func stockFixProductInput(product string, ratio int) map[string]any {
	return map[string]any{"id": product, "version": 1, "name": "合成材料", "unit": "桶", "aux_unit": "袋", "aux_ratio": ratio, "active": true}
}

func stockFixInbound(product string) map[string]any {
	return map[string]any{"kind": "进货", "date": "2026-10-08", "to": "wh", "handler": "合成经手人", "lines": []StockLine{{Material: Material{Product: product}, InputUnit: "袋", InputQty: "100", Price: 10000}}}
}

func stockFixWaitLock(t *testing.T, db *sql.DB, table string, done <-chan error) {
	t.Helper()
	var schema string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	// The isolated runner has no global performance_schema/PROCESS grants.
	// Observe only its own active conflicting locking statement, in this
	// fixture's schema, twice on the same connection while the holder remains
	// locked. Prepared statements appear as Execute, not Query. The completion
	// channel and final stock/conversion assertions
	// still prove serialization; a fixed sleep is not used as lock evidence.
	statement := "%FROM products WHERE%FOR %"
	if table == "stock_balances" {
		statement = "INSERT INTO stock_balances(%"
	} else if table != "products" {
		t.Fatalf("unsupported lock observation table %q", table)
	}
	deadline := time.Now().Add(10 * time.Second)
	var previous int64
	for {
		var connection int64
		err := db.QueryRow(`SELECT ID FROM INFORMATION_SCHEMA.PROCESSLIST
		 WHERE DB=? AND USER=SUBSTRING_INDEX(CURRENT_USER(),'@',1)
		 AND ID<>CONNECTION_ID() AND COMMAND IN ('Query','Execute') AND INFO LIKE ?
		 ORDER BY ID LIMIT 1`, schema, statement).Scan(&connection)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("operation completed before %s lock: %v", table, err)
		default:
		}
		if err == nil && connection == previous {
			t.Logf("observed own connection %d blocked on %s locking SQL", connection, table)
			return
		}
		previous = connection
		if time.Now().After(deadline) {
			t.Fatalf("operation did not wait for %s lock", table)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stockFixQuantity(t *testing.T, db *sql.DB, wh, product string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id=? AND product_id=?", wh, product).Scan(&got); err != nil || got != want {
		t.Fatalf("balance %s/%s = %d, want %d: %v", wh, product, got, want, err)
	}
}

func TestStockFirstMovementLocksConversionMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('wh','合成仓库')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('p','合成材料','桶','袋',20)")
	stockFixExec(t, a.db, "INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('wh','p',0)")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	holder, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	var qty int64
	if err = holder.QueryRow("SELECT qty_micro FROM stock_balances WHERE warehouse_id='wh' AND product_id='p' FOR UPDATE").Scan(&qty); err != nil {
		t.Fatal(err)
	}
	stockDone := make(chan error, 1)
	go func() {
		_, err := stockFixCall(a, stockFixActor(), "/api/stock", "first-stock-lock-operation", stockFixInbound("p"))
		stockDone <- err
	}()
	stockFixWaitLock(t, a.db, "stock_balances", stockDone)
	productTx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer productTx.Rollback()
	var oldCount int
	if err = productTx.QueryRow("SELECT COUNT(*) FROM stock_movements").Scan(&oldCount); err != nil || oldCount != 0 {
		t.Fatal("failed to establish pre-movement RR snapshot", oldCount, err)
	}
	productDone := make(chan error, 1)
	go func() {
		_, err := saveProductEntity(productTx, stockFixActor(), []byte(jsonText(stockFixProductInput("p", 10))))
		productDone <- err
	}()
	stockFixWaitLock(t, a.db, "products", productDone)
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-stockDone; err != nil {
		t.Fatal(err)
	}
	maintenanceFaultError(t, <-productDone, 400)
	if err = productTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	stockFixQuantity(t, a.db, "wh", "p", 5000000)
	var ratio string
	if err = a.db.QueryRow("SELECT aux_ratio FROM products WHERE id='p'").Scan(&ratio); err != nil || ratio != "20.000000" {
		t.Fatal("product ratio changed despite first movement", ratio, err)
	}
	var documentID, saved string
	if err = a.db.QueryRow("SELECT id,line_items FROM stock_documents").Scan(&documentID, &saved); err != nil {
		t.Fatal(err)
	}
	edit := stockFixInbound("p")
	edit["id"], edit["version"], edit["note"], edit["lines"] = documentID, 1, "只改备注", json.RawMessage(saved)
	if _, err = stockFixCall(a, stockFixActor(), "/api/stock", "stock-note-lock-operation", edit); err != nil {
		t.Fatal(err)
	}
	stockFixQuantity(t, a.db, "wh", "p", 5000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 1)
	t.Log("first stock holds conversion through commit; pending product edit rejects after current read despite an older RR snapshot; note-only edit leaves stock unchanged")
}

func TestStockProductEditLocksAllConsumersMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixExec(t, a.db, "INSERT INTO companies(id,name) VALUES('c','合成公司')")
	stockFixExec(t, a.db, "INSERT INTO workers(id,name,crafts) VALUES('w','合成工人',JSON_ARRAY('开工'))")
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name,worker_id) VALUES('wh','合成仓库','w'),('to','合成调入仓库',NULL)")
	for _, kind := range []string{"进货", "调拨", "耗材", "售料"} {
		t.Run(kind, func(t *testing.T) {
			product := "p-" + kind
			stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES(?,'合成材料','桶','袋',20)", product)
			initial := int64(20000000)
			if kind == "进货" {
				initial = 0
			}
			// This fixture has an opening balance but no movement history.  Seed the
			// canonical quantum explicitly so changing the pre-first-movement ratio
			// keeps the intended 20-base-unit balance under the new factor (10).
			quantum := int64(200000000)
			if kind == "进货" {
				quantum = 0
			}
			stockFixExec(t, a.db, "INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum) VALUES('wh',?,?,?)", product, initial, quantum)
			tx, err := a.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = saveProductEntity(tx, stockFixActor(), []byte(jsonText(stockFixProductInput(product, 10)))); err != nil {
				t.Fatal(err)
			}
			path, input := "/api/stock", any(stockFixInbound(product))
			switch kind {
			case "调拨":
				input = map[string]any{"kind": "调拨", "date": "2026-10-08", "from": "wh", "to": "to", "handler": "合成经手人", "lines": []StockLine{{Material: Material{Product: product}, InputUnit: "袋", InputQty: "100"}}}
			case "耗材", "售料":
				o := Order{Company: "c", Address: "合成地址-" + kind, Date: "2026-10-08", Worker: "w"}
				if kind == "耗材" {
					o.Lines = []Line{{Temporary: true, Category: "开工", Name: "合成工艺", Mode: "金额", Price: 1000, Wage: 100}}
					o.Materials = []Material{{Product: product, InputUnit: "aux", InputQty: "100"}}
				} else {
					o.Sales = []Sale{{Product: product, InputUnit: "aux", InputQty: "100", Price: 100}}
				}
				path, input = "/api/order", o
			}
			done := make(chan error, 1)
			go func() {
				_, err := stockFixCall(a, stockFixActor(), path, id(), input)
				done <- err
			}()
			stockFixWaitLock(t, a.db, "products", done)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			stockFixQuantity(t, a.db, "wh", product, 10000000)
			if kind == "调拨" {
				stockFixQuantity(t, a.db, "to", product, 10000000)
			}
		})
	}
	t.Log("product edit serializes before inbound, transfer, construction material and material sale; every consumer uses committed new conversion")
}

func TestStockHistoricalConversionSnapshotMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('wh','合成仓库')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('p','合成材料','桶','袋',20)")
	result, err := stockFixCall(a, stockFixActor(), "/api/stock", "historical-stock-operation", stockFixInbound("p"))
	if err != nil {
		t.Fatal(err)
	}
	documentID := result.(map[string]any)["id"].(string)
	// Synthesize the mismatch left by the previously confirmed race. A current
	// API product edit is separately tested and is not allowed to make this state.
	stockFixExec(t, a.db, "UPDATE products SET aux_ratio=10 WHERE id='p'")
	var saved string
	if err = a.db.QueryRow("SELECT line_items FROM stock_documents WHERE id=?", documentID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	edit := stockFixInbound("p")
	edit["id"], edit["version"], edit["note"], edit["lines"] = documentID, 1, "只改历史备注", json.RawMessage(saved)
	if _, err = stockFixCall(a, stockFixActor(), "/api/stock", "historical-stock-note-operation", edit); err != nil {
		t.Fatal(err)
	}
	stockFixQuantity(t, a.db, "wh", "p", 5000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 1)
	var lines []StockLine
	if err = a.db.QueryRow("SELECT line_items FROM stock_documents WHERE id=?", documentID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(saved), &lines); err != nil || len(lines) != 1 || lines[0].AuxRatio != "20.000000" {
		t.Fatal("historical conversion lost", lines, err)
	}
}

func TestStockLongOperationKeyHTTPMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('wh','合成仓库')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('p','合成材料','桶','袋',20)")
	first, second := stockFixActor().ID, strings.Repeat("b", 32)
	for _, userID := range []string{first, second} {
		stockFixExec(t, a.db, "INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES(?,?,?,'unused','admin',JSON_ARRAY(),JSON_ARRAY())", userID, "synthetic-"+userID, "合成仓储管理员")
		stockFixExec(t, a.db, "INSERT INTO sessions(token,user_id,expires_at) VALUES(?,?,DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR))", hash([]byte(userID)), userID)
	}
	call := func(userID, path, key string, input any) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(jsonText(input)))
		req.Header.Set("Idempotency-Key", key)
		req.AddCookie(&http.Cookie{Name: "tegu_session", Value: userID})
		recorder := httptest.NewRecorder()
		a.route(recorder, req)
		return recorder
	}
	key := strings.Repeat("x", 100)
	input := stockFixInbound("p")
	response := call(first, "/api/stock", key, input)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	retry := call(first, "/api/stock", key, input)
	if retry.Code != 200 || retry.Body.String() != response.Body.String() {
		t.Fatal("same-key retry did not replay original result", retry.Code, retry.Body.String())
	}
	input["note"] = "不同内容"
	if conflict := call(first, "/api/stock", key, input); conflict.Code != 409 {
		t.Fatal("same-key different body must conflict", conflict.Code, conflict.Body.String())
	}
	input["note"] = ""
	if other := call(second, "/api/stock", key, input); other.Code != 200 {
		t.Fatal("same key from different user collided", other.Code, other.Body.String())
	}
	stockFixQuantity(t, a.db, "wh", "p", 10000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(DISTINCT operation_id) FROM stock_movements", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements WHERE CHAR_LENGTH(operation_id)>100", 0)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"id": created.ID, "version": 1, "action": "void", "reason": "合成长编号作废"}
	voidKey := strings.Repeat("v", 100)
	for i := 0; i < 2; i++ {
		if result := call(first, "/api/stock-state", voidKey, state); result.Code != 200 {
			t.Fatal("long-key reversal", result.Code, result.Body.String())
		}
	}
	stockFixQuantity(t, a.db, "wh", "p", 5000000)
	state["version"], state["action"] = 2, "restore"
	if result := call(first, "/api/stock-state", strings.Repeat("r", 100), state); result.Code != 200 {
		t.Fatal("long-key restore", result.Code, result.Body.String())
	}
	stockFixQuantity(t, a.db, "wh", "p", 10000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 4)
	for _, n := range []int{16, 67, 68} {
		boundaryKey := strings.Repeat("k", n)
		result := call(first, "/api/stock", boundaryKey, input)
		if result.Code != 200 {
			t.Fatal("valid boundary key failed", n, result.Code, result.Body.String())
		}
		if replay := call(first, "/api/stock", boundaryKey, input); replay.Code != 200 || replay.Body.String() != result.Body.String() {
			t.Fatal("boundary key replay changed", n, replay.Code, replay.Body.String())
		}
		if err := json.Unmarshal(result.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		var persisted string
		if err := a.db.QueryRow("SELECT operation_id FROM stock_movements WHERE source_id=?", created.ID).Scan(&persisted); err != nil || persisted != stockOperationID(first, boundaryKey) {
			t.Fatal("boundary ID did not use compatible representation", n, persisted, err)
		}
	}
	for _, n := range []int{15, 101} {
		if result := call(first, "/api/stock", strings.Repeat("i", n), input); result.Code != 400 {
			t.Fatal("out-of-contract key accepted", n, result.Code, result.Body.String())
		}
	}
	stockFixQuantity(t, a.db, "wh", "p", 25000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 7)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('to','合成调入仓库')")
	transfer := map[string]any{"kind": "调拨", "date": "2026-10-08", "from": "wh", "to": "to", "handler": "合成经手人", "lines": []StockLine{{Material: Material{Product: "p"}, InputUnit: "袋", InputQty: "100"}}}
	for i := 0; i < 2; i++ {
		if result := call(first, "/api/stock", strings.Repeat("t", 100), transfer); result.Code != 200 {
			t.Fatal("long-key two-sided transfer", result.Code, result.Body.String())
		}
	}
	stockFixQuantity(t, a.db, "wh", "p", 20000000)
	stockFixQuantity(t, a.db, "to", "p", 5000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 9)
	stockFixExec(t, a.db, "INSERT INTO companies(id,name) VALUES('c','合成公司')")
	stockFixExec(t, a.db, "INSERT INTO workers(id,name,crafts) VALUES('w','合成工人',JSON_ARRAY('开工'))")
	stockFixExec(t, a.db, "UPDATE warehouses SET worker_id='w' WHERE id='wh'")
	order := Order{Company: "c", Address: "合成长编号地址", Date: "2026-10-08", Worker: "w", Lines: []Line{{Temporary: true, Category: "开工", Name: "合成工艺", Mode: "金额", Price: 1000, Wage: 100}}, Materials: []Material{{Product: "p", InputUnit: "aux", InputQty: "20"}}, Sales: []Sale{{Product: "p", InputUnit: "aux", InputQty: "20", Price: 100}}}
	for i := 0; i < 2; i++ {
		if result := call(first, "/api/order", strings.Repeat("o", 100), order); result.Code != 200 {
			t.Fatal("long-key material and sale order", result.Code, result.Body.String())
		}
	}
	stockFixQuantity(t, a.db, "wh", "p", 18000000)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_movements", 10)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders", 1)
	t.Log("HTTP keys 16/67/68/100 succeed and replay once; 15/101 reject; different body conflicts, other account is independent; void/restore, two-sided transfer and construction materials/sales persist bounded IDs")
}

func stockFixMovementSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query("SELECT id,operation_id,warehouse_id,product_id,delta_micro,source_type,source_id,actor_id,created_at FROM stock_movements ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := [][]any{}
	for rows.Next() {
		var movementID, quantity int64
		var operation, warehouse, product, kind, source, actor, created string
		if err := rows.Scan(&movementID, &operation, &warehouse, &product, &quantity, &kind, &source, &actor, &created); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{movementID, operation, warehouse, product, quantity, kind, source, actor, created})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jsonText(result)
}

func stockFixAssertProductIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	var columns string
	if err := db.QueryRow("SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stock_movements' AND INDEX_NAME='idx_stock_movement_product'").Scan(&columns); err != nil || columns != "product_id,id" {
		t.Fatal("missing or wrong stock product index", columns, err)
	}
	maintenanceFaultCount(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version=15", 1)
}

func TestStockProductIndexMigrationRestoreMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixAssertProductIndex(t, a.db)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('wh','合成仓库')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('p','合成材料','桶','袋',20)")
	if _, err := stockFixCall(a, stockFixActor(), "/api/stock", "legacy-stock-movement-operation", stockFixInbound("p")); err != nil {
		t.Fatal(err)
	}
	before := stockFixMovementSnapshot(t, a.db)
	stockFixExec(t, a.db, "DROP INDEX idx_stock_movement_product ON stock_movements")
	stockFixExec(t, a.db, "DELETE FROM schema_migrations WHERE version=15")
	// This snapshot contains old user:key movement IDs and is generated from
	// the exact pre-index shape; independent recovery uses the current schema.
	t.Setenv("BACKUP_DIR", t.TempDir())
	name, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrateVersions(a.db); err != nil {
			t.Fatal("index migration/replay", err)
		}
		stockFixAssertProductIndex(t, a.db)
		if got := stockFixMovementSnapshot(t, a.db); got != before {
			t.Fatal("index migration changed historical movement rows", got, before)
		}
	}
	restored := stockFixDB(t)
	if err := restored.restore(filepath.Join(os.Getenv("BACKUP_DIR"), name)); err != nil {
		t.Fatal("old snapshot independent restore", err)
	}
	stockFixAssertProductIndex(t, restored.db)
	if got := stockFixMovementSnapshot(t, restored.db); got != before {
		t.Fatal("independent recovery changed historical movement rows", got, before)
	}
	stockFixQuantity(t, restored.db, "wh", "p", 5000000)
	// Old request records still replay after restore without a second movement.
	if _, err := stockFixCall(restored, stockFixActor(), "/api/stock", "legacy-stock-movement-operation", stockFixInbound("p")); err != nil {
		t.Fatal("old-key replay after restore", err)
	}
	if got := stockFixMovementSnapshot(t, restored.db); got != before {
		t.Fatal("old-key replay changed recovered movements")
	}
	t.Log("version 15 adds only product_id,id index; repeat migration and independent old-snapshot recovery preserve full movement rows and legacy key replay")
}

func TestStockProductEditDoesNotWaitForOtherProductMySQL(t *testing.T) {
	a := stockFixDB(t)
	stockFixAssertProductIndex(t, a.db)
	stockFixExec(t, a.db, "INSERT INTO warehouses(id,name) VALUES('wh','合成仓库')")
	stockFixExec(t, a.db, "INSERT INTO products(id,name,unit,aux_unit,aux_ratio) VALUES('a','合成材料','桶','袋',20),('b','合成其他材料','桶','袋',20)")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	holder, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if err = applyStock(holder, stockFixActor(), "other-product-held-operation", "进货", "synthetic-held-stock", map[string]int64{"wh|b": 5000000}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		input := stockFixProductInput("a", 10)
		input["entity"] = "products"
		_, err := stockFixCall(a, stockFixActor(), "/api/entity", "independent-product-edit-operation", input)
		done <- err
	}()
	// Keep B's real first stock movement uncommitted until A has finished.
	// A full movement scan would wait for B and cannot pass this check.
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		holder.Rollback()
		t.Fatal("product A edit waited for unrelated product B stock movement")
	}
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	var ratio string
	if err = a.db.QueryRow("SELECT aux_ratio FROM products WHERE id='a'").Scan(&ratio); err != nil || ratio != "10.000000" {
		t.Fatal("independent product edit was not persisted", ratio, err)
	}
	t.Log("actual product A maintenance finishes while product B's first stock movement remains uncommitted")
}
