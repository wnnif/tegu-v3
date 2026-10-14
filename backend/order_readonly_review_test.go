package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadonlyRecordPreservesSalesTicketSlotMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	admin := User{ID: "review-admin", Name: "合成审核", Role: "admin"}
	for _, query := range []string{
		`INSERT INTO companies(id,name,ticket_mode) VALUES('review-company','合成公司','split')`,
		`INSERT INTO workers(id,name,crafts) VALUES('review-worker','合成师傅',JSON_ARRAY('开工','防水涂料','丙纶'))`,
		`INSERT INTO warehouses(id,name,worker_id) VALUES('review-warehouse','合成仓库','review-worker')`,
		`INSERT INTO products(id,name,unit) VALUES('review-product','合成材料','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('review-warehouse','review-product',10000000)`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('review-public-a','','开工','合成开工','金额',0,100),('review-public-b','','防水涂料','合成防水','金额',0,200),('review-public-c','','丙纶','合成丙纶','金额',0,300)`,
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,ticket_slot,included_by_craft_id) VALUES('review-fixed-a','review-company','review-public-a','开工','合成开工','金额',1000,0,1,''),('review-fixed-b','review-company','review-public-b','防水涂料','合成防水','金额',2000,0,2,''),('review-fixed-c','review-company','review-public-c','丙纶','合成丙纶','金额',3000,0,2,'review-fixed-b')`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	call := func(u User, path string, payload any) (any, error) {
		raw := []byte(jsonText(payload))
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		req.Header.Set("Idempotency-Key", id())
		return a.mutate(req, u, raw, func(tx *sql.Tx, key string) (any, error) {
			return a.post(tx, path, u, raw, key)
		})
	}
	project, err := call(admin, "/api/project", Project{Company: "review-company", Name: "合成工地", Address: "合成地址"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := call(admin, "/api/order", Order{
		Company: "review-company", Project: project.(map[string]any)["id"].(string), Address: "合成地址", Date: "2026-10-01", Worker: "review-worker",
		Lines: []Line{{CraftID: "review-fixed-a"}, {CraftID: "review-fixed-b"}},
		Sales: []Sale{{Product: "review-product", InputQty: "1", Price: 1000}}, SalesTicketSlot: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	orderID := result.(map[string]any)["id"].(string)
	employee := User{ID: "review-employee", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"review-public-a"}}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	detail, err := readOrderDetail(tx, orderID, employee)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	var update Order
	if err = json.Unmarshal([]byte(jsonText(detail)), &update); err != nil {
		t.Fatal(err)
	}
	update.Combinations = nil // The UI omits unchanged combination relations.
	update.Lines = update.Lines[:1]
	update.SalesTicketSlot = 2
	if _, err = call(employee, "/api/order", update); err == nil {
		t.Fatal("readonly record allowed sale reassignment to a different ticket")
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM orders WHERE id='"+orderID+"' AND sales_ticket_slot=1 AND version=1", 1)
	update.SalesTicketSlot = 1
	update.Lines[0].Description = "允许工艺的说明"
	if _, err = call(employee, "/api/order", update); err != nil {
		t.Fatal("allowed line update with unchanged sale", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE order_id='"+orderID+"'", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM stock_balances WHERE warehouse_id='review-warehouse' AND product_id='review-product' AND qty_micro=9000000", 1)
	projectID := update.Project
	maintenanceFaultExec(t, a.db, `UPDATE crafts SET ticket_slot=1 WHERE id IN ('review-fixed-b','review-fixed-c')`)
	checkDirectory := func(path string, parent string, slot int) {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		if strings.Contains(path, "reference-options") {
			err = a.referenceOptions(response, request, employee)
		} else {
			err = a.reference(response, request, employee)
		}
		if err != nil {
			t.Fatal(err)
		}
		var directory struct {
			Crafts []craftsRecord `json:"crafts"`
			Rows   []craftsRecord `json:"rows"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &directory); err != nil {
			t.Fatal(err)
		}
		for _, craft := range append(directory.Crafts, directory.Rows...) {
			if craft.ID == "review-fixed-c" {
				if craft.IncludedByCraftID != parent || craft.TicketSlot != slot || craft.Editable {
					t.Fatal("project catalog did not preserve its combination and readonly state", craft)
				}
				return
			}
		}
		t.Fatal("project directory omitted configured combination")
	}
	checkDirectory("/api/reference?tables=crafts&craft_scope=order&company_id=review-company&project_id="+projectID, "review-fixed-b", 2)
	checkDirectory("/api/reference-options?entity=crafts&craft_scope=order&company_id=review-company&project_id="+projectID, "review-fixed-b", 2)
	checkDirectory("/api/reference?tables=crafts&craft_scope=order&company_id=review-company", "review-fixed-b", 1)
	combined := Order{Company: "review-company", Project: projectID, Address: "合成地址", Date: "2026-12-01", Worker: "review-worker", Lines: []Line{{CraftID: "review-fixed-b"}, {CraftID: "review-fixed-c"}}}
	combinedResult, err := call(admin, "/api/order", combined)
	if err != nil {
		t.Fatal("new order in an old project with no manual waiver rejected", err)
	}
	combinedID := combinedResult.(map[string]any)["id"].(string)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE order_id='"+combinedID+"' AND craft_id='review-fixed-c' AND included_by_craft_id='' AND use_included_price=0 AND total_cents=3000 AND labor_cents=300", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(DISTINCT ticket_id) FROM project_ticket_items WHERE order_id='"+combinedID+"'", 1)
	newProject, err := call(admin, "/api/project", Project{Company: "review-company", Name: "合成新配置工地", Address: "合成新地址"})
	if err != nil {
		t.Fatal(err)
	}
	combined.Project = newProject.(map[string]any)["id"].(string)
	combined.Lines[0].CraftID = "review-fixed-b"
	if _, err = call(admin, "/api/order", combined); err != nil {
		t.Fatal("new project did not use current company combination", err)
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents) VALUES('review-public-new','','丙纶','合成后增公共工艺','金额',0,300)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,ticket_slot,included_by_craft_id) VALUES('review-new-craft','review-company','review-public-new','丙纶','合成后增工艺','金额',3000,0,1,'review-fixed-b')`)
	combined.Project = projectID
	combined.Lines[1].CraftID = "review-new-craft"
	if _, err = call(admin, "/api/order", combined); err != nil {
		t.Fatal("craft first use after project creation", err)
	}
	maintenanceFaultExec(t, a.db, `UPDATE crafts SET included_by_craft_id='review-fixed-a',ticket_slot=1 WHERE id='review-new-craft'`)
	if _, err = call(admin, "/api/order", combined); err != nil {
		t.Fatal("later company change replaced first-use project configuration", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_craft_slots WHERE project_id='"+projectID+"' AND craft_id='review-new-craft' AND slot=1 AND included_by_craft_id='review-fixed-b'", 1)
	combined.TicketAction = "append"
	appendResult, err := call(admin, "/api/order", combined)
	if err != nil {
		t.Fatal(err)
	}
	appendOrderID := appendResult.(map[string]any)["id"].(string)
	appendTicketID := appendResult.(map[string]any)["ticket_ids"].([]string)[0]
	tx, err = a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	appendDetail, err := readOrderDetail(tx, appendOrderID, admin)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	var appendUpdate Order
	if err = json.Unmarshal([]byte(jsonText(appendDetail)), &appendUpdate); err != nil {
		t.Fatal(err)
	}
	appendUpdate.Lines[0].Description = "追加票草稿修改"
	if _, err = call(admin, "/api/order", appendUpdate); err != nil {
		t.Fatal("draft append ticket edit", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_ticket_items WHERE order_id='"+appendOrderID+"' AND ticket_id='"+appendTicketID+"'", 2)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM project_tickets WHERE id='"+appendTicketID+"' AND total_cents=5000", 1)
}

func TestReadonlyStockProtectsSaleOrder(t *testing.T) {
	sales := []Sale{{ID: "first", Product: "product", SortOrder: 0}, {ID: "second", Product: "product", SortOrder: 1}}
	reordered := []Sale{sales[1], sales[0]}
	reordered[0].SortOrder, reordered[1].SortOrder = 0, 1
	if sameOrderStock(nil, nil, sales, reordered) {
		t.Fatal("readonly record allowed sale reordering")
	}
}
