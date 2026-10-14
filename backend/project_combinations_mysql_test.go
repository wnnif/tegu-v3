package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type combinationMySQLFixture struct{ priceMySQLFixture }

func newCombinationMySQLFixture(t *testing.T) combinationMySQLFixture {
	t.Helper()
	f := combinationMySQLFixture{newPriceMySQLFixture(t)}
	for _, q := range []string{
		`UPDATE workers SET crafts=JSON_ARRAY('开工','防水涂料','室外防水','丙纶') WHERE id IN ('loc-a','loc-b')`,
		`INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,subitems) VALUES('combo-w-source','','防水涂料','合成防水','平方',0,2000,JSON_ARRAY()),('combo-p-source','','丙纶','合成丙纶','平方',0,1500,JSON_ARRAY())`,
		// Retain the legacy configuration deliberately: it must not force new
		// customer waivers or cause either actual worker's wage to become zero.
		`INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents,included_by_craft_id) VALUES('combo-w','loc-co','combo-w-source','防水涂料','合成防水','平方',5000,0,''),('combo-p','loc-co','combo-p-source','丙纶','合成丙纶','平方',3000,0,'combo-w')`,
	} {
		f.exec(t, q)
	}
	return f
}

func (f combinationMySQLFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.a.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func combinationActualLines() []Line {
	return []Line{
		{ClientRef: "new-w", CraftID: "combo-w", Worker: "loc-a", ActualLocation: "主卫", Description: "防水真实备注", Segments: []Segment{{Length: 6, Width: 10}}, Price: 1, Wage: 1, Labor: 0},
		{ClientRef: "new-p", CraftID: "combo-p", Worker: "loc-b", ActualLocation: "厨房", Description: "丙纶真实备注", Segments: []Segment{{Length: 10, Width: 10}}, Price: 1, Wage: 1, Labor: 0},
	}
}
func (f combinationMySQLFixture) read(t *testing.T, orderID string) orderDetailView {
	t.Helper()
	response := f.request(t, f.admin, "GET", "/api/order?id="+orderID, "", nil, 200)
	var d orderDetailView
	if err := json.Unmarshal(response.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func (f combinationMySQLFixture) save(t *testing.T, order Order) orderDetailView {
	t.Helper()
	result := priceTestJSON(t, f.request(t, f.admin, "POST", "/api/order", "", order, 200))
	return f.read(t, result["id"].(string))
}
func combinationPair(d orderDetailView, direction string, waived int64) ProjectCombination {
	c := ProjectCombination{Direction: direction, WaivedQty: waived, WaterproofVersion: d.Version, PolypropyleneVersion: d.Version}
	for _, l := range d.Lines {
		if l.Category == "丙纶" {
			c.PolypropyleneLine = l.ID
		} else if l.Category == "防水涂料" {
			c.WaterproofLine = l.ID
		}
	}
	return c
}
func combinationPayload(d orderDetailView, desired []ProjectCombination) map[string]any {
	return map[string]any{"project_id": d.ProjectID, "order_id": d.ID, "order_version": d.Version, "combination_version": d.CombinationVersion, "combinations": desired}
}
func (f combinationMySQLFixture) set(t *testing.T, d orderDetailView, desired []ProjectCombination, status int) orderDetailView {
	t.Helper()
	f.request(t, f.admin, "POST", "/api/project-combination", "", combinationPayload(d, desired), status)
	return f.read(t, d.ID)
}
func (f combinationMySQLFixture) assertWages(t *testing.T, d orderDetailView, water, poly int64) {
	t.Helper()
	if d.WageCents == nil || *d.WageCents != water+poly {
		t.Fatal("whole construction wages changed", d.WageCents)
	}
	for _, l := range d.Lines {
		expected := water
		worker := "loc-a"
		location := "主卫"
		if l.Category == "丙纶" {
			expected = poly
			worker = "loc-b"
			location = "厨房"
		}
		if l.Labor == nil || *l.Labor != expected || l.WorkerID != worker || l.ActualLocation != location {
			t.Fatal("actual wages/person/location changed", l)
		}
	}
	var sum int64
	var count int
	if err := f.a.db.QueryRow("SELECT COUNT(*),SUM(wage_cents) FROM order_worker_wages WHERE order_id=?", d.ID).Scan(&count, &sum); err != nil || count != len(d.Lines) || sum != water+poly {
		t.Fatal("worker wage accounts duplicated or changed", count, sum, err)
	}
}
func (f combinationMySQLFixture) assertDraftTicket(t *testing.T, project string, expected int64) {
	t.Helper()
	var total int64
	if err := f.a.db.QueryRow("SELECT COALESCE(SUM(total_cents),0) FROM project_tickets WHERE project_id=? AND status='草稿'", project).Scan(&total); err != nil || total != expected {
		t.Fatal("ticket customer amount differs", total, expected, err)
	}
}

func TestProjectCombinationModesAndFullWagesMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	for _, tc := range []struct {
		name, direction string
		waived, total   int64
	}{
		{"none", "", 0, 600000}, {"poly-part", "polypropylene", 40000000, 480000}, {"water-part", "waterproof", 20000000, 500000}, {"poly-full", "polypropylene", 100000000, 300000}, {"water-full", "waterproof", 60000000, 300000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Order{Company: "loc-co", Address: "合成模式-" + tc.name, Date: "2026-10-10", Lines: combinationActualLines()}
			if tc.direction != "" {
				links := []ProjectCombination{{WaterproofLine: "new-w", PolypropyleneLine: "new-p", Direction: tc.direction, WaivedQty: tc.waived}}
				o.Combinations = &links
			}
			d := f.save(t, o)
			if d.TotalCents != tc.total {
				t.Fatal("wrong customer fee", d.TotalCents, tc.total)
			}
			f.assertWages(t, d, 120000, 150000)
			f.assertDraftTicket(t, d.ProjectID, tc.total)
			for _, l := range d.Lines {
				if l.UseIncludedPrice || l.IncludedByCraftID != "" || l.Price <= 1 {
					t.Fatal("old configuration/forged request controlled new pricing", l)
				}
			}
			if tc.direction == "" && len(d.Combinations) != 0 {
				t.Fatal("automatically combined without human selection")
			}
			if tc.direction != "" {
				if len(d.Combinations) != 1 || d.Combinations[0].Direction != tc.direction || d.Combinations[0].WaivedQty != tc.waived {
					t.Fatal("saved combination failed to reopen", d.Combinations)
				}
				d = f.set(t, d, []ProjectCombination{}, 200)
				if d.TotalCents != 600000 || len(d.Combinations) != 0 {
					t.Fatal("closing combination did not restore normal charge", d)
				}
				f.assertWages(t, d, 120000, 150000)
				f.assertDraftTicket(t, d.ProjectID, 600000)
			}
		})
	}
}

func TestProjectCombinationInvalidAreasAndAtomicRollbackMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成面积边界", Date: "2026-10-10", Lines: combinationActualLines()})
	for _, tc := range []struct {
		name, direction string
		qty             int64
		duplicate       bool
		status          int
	}{
		{"negative", "polypropylene", -1, false, 400}, {"zero", "polypropylene", 0, false, 400}, {"direction", "auto", 1, false, 400},
		{"over-target", "waterproof", 60000001, false, 409}, {"over-global-limit", "polypropylene", 1000000000001, false, 400}, {"duplicate-pair", "polypropylene", 40000000, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := orderLocationsState(t, f.a.db)
			desired := []ProjectCombination{combinationPair(d, tc.direction, tc.qty)}
			if tc.duplicate {
				desired = append(desired, desired[0])
			}
			f.set(t, d, desired, tc.status)
			if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
				t.Fatal("rejected waiver left partial rows")
			}
		})
	}
	d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}, 200)
	edit := f.edit(t, f.admin, d.ID)
	edit.Combinations = nil
	for i := range edit.Lines {
		if edit.Lines[i].Category == "丙纶" {
			edit.Lines[i].Segments = []Segment{{Length: 3, Width: 10}}
		}
	}
	before := orderLocationsState(t, f.a.db)
	f.request(t, f.admin, "POST", "/api/order", "", edit, 409)
	if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
		t.Fatal("quantity below reserved area partially saved")
	}
	// A late audit failure after relation and customer projections are written
	// must roll back relations, orders, ticket versions, accounts and requests.
	f.exec(t, `CREATE TRIGGER combo_late_fault BEFORE INSERT ON audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic late combo failure'`)
	before = orderLocationsState(t, f.a.db)
	f.set(t, d, []ProjectCombination{combinationPair(d, "waterproof", 20000000)}, 500)
	if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
		t.Fatal("late failure left partial customer/payroll state")
	}
}

func TestProjectCombinationCrossDateReservationsMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	p := f.save(t, Order{Company: "loc-co", Address: "合成跨日期", Date: "2026-10-01", Lines: combinationActualLines()[1:]})
	w := f.save(t, Order{Company: "loc-co", Project: p.ProjectID, Address: p.Address, Date: "2026-10-09", Lines: combinationActualLines()[:1]})
	unrelated := f.save(t, Order{Company: "loc-co", Project: p.ProjectID, Address: p.Address, Date: "2026-10-08", Lines: []Line{{CraftID: "combo-p", Worker: "loc-b", ActualLocation: "次卫", Segments: []Segment{{Length: 2, Width: 10}}}}})
	if p.TotalCents != 300000 || w.TotalCents != 300000 || len(w.Combinations) != 0 {
		t.Fatal("poly first or same site automatically waived")
	}
	c := ProjectCombination{WaterproofLine: w.Lines[0].ID, PolypropyleneLine: p.Lines[0].ID, WaterproofVersion: w.Version, PolypropyleneVersion: p.Version, Direction: "polypropylene", WaivedQty: 40000000}
	w = f.set(t, w, []ProjectCombination{c}, 200)
	p = f.read(t, p.ID)
	if p.TotalCents != 180000 || w.TotalCents != 300000 || p.WorkDate != "2026-10-01" || w.WorkDate != "2026-10-09" || *p.WageCents != 150000 || *w.WageCents != 120000 || f.read(t, unrelated.ID).TotalCents != 60000 {
		t.Fatal("cross-date real work changed", p, w)
	}
	f.assertDraftTicket(t, p.ProjectID, 540000)
	second := f.save(t, Order{Company: "loc-co", Project: p.ProjectID, Address: p.Address, Date: "2026-10-10", Lines: combinationActualLines()[:1]})
	other := ProjectCombination{WaterproofLine: second.Lines[0].ID, PolypropyleneLine: p.Lines[0].ID, WaterproofVersion: second.Version, PolypropyleneVersion: p.Version, Direction: "polypropylene", WaivedQty: 70000000}
	f.set(t, second, []ProjectCombination{other}, 409)
	other.WaivedQty = 60000000
	second = f.set(t, second, []ProjectCombination{other}, 200)
	if f.read(t, p.ID).TotalCents != 0 {
		t.Fatal("independent valid reservations not fully waived")
	}
	// Removing only the first relation releases its own 40, not the later 60.
	w = f.read(t, w.ID)
	f.set(t, w, []ProjectCombination{}, 200)
	p = f.read(t, p.ID)
	if p.TotalCents != 120000 || p.Lines[0].WaivedQty != 60000000 || len(p.Combinations) != 1 || p.Combinations[0].WaterproofLine != second.Lines[0].ID {
		t.Fatal("removal changed an unrelated reservation", p)
	}
	f.assertDraftTicket(t, p.ProjectID, 780000)
	f.request(t, f.admin, "GET", "/api/project-combinations?project_id="+p.ProjectID+"&line_ids="+p.Lines[0].ID, "", nil, 200)
	edit := f.edit(t, f.admin, p.ID)
	edit.Combinations = nil
	edit.Lines = nil
	f.request(t, f.admin, "POST", "/api/order", "", edit, 409)
}

func TestProjectCombinationIdempotencyAndConcurrencyMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	links := []ProjectCombination{{WaterproofLine: "new-w", PolypropyleneLine: "new-p", Direction: "polypropylene", WaivedQty: 40000000}}
	o := Order{Company: "loc-co", Address: "合成重复请求", Date: "2026-10-10", Lines: combinationActualLines(), Combinations: &links}
	key := id()
	first := f.request(t, f.admin, "POST", "/api/order", key, o, 200).Body.String()
	again := f.request(t, f.admin, "POST", "/api/order", key, o, 200).Body.String()
	if first != again {
		t.Fatal("idempotent response changed")
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM project_combinations", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM orders", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM order_worker_wages", 2)
	links[0].WaivedQty = 50000000
	f.request(t, f.admin, "POST", "/api/order", key, o, 409)
	d := f.save(t, Order{Company: "loc-co", Address: "合成并发组合", Date: "2026-10-10", Lines: combinationActualLines()})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, c := range []ProjectCombination{combinationPair(d, "polypropylene", 40000000), combinationPair(d, "waterproof", 20000000)} {
		wg.Add(1)
		go func(c ProjectCombination) {
			defer wg.Done()
			_, err := f.call(f.u, "/api/project-combination", id(), combinationPayload(d, []ProjectCombination{c}))
			errs <- err
		}(c)
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if e, ok := err.(failure); ok && e.Code == 409 {
			conflicts++
		} else {
			t.Fatal("unexpected concurrent failure", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("parallel combination duplicated or lost changes", successes, conflicts)
	}
	current := f.read(t, d.ID)
	if len(current.Combinations) != 1 || (current.TotalCents != 480000 && current.TotalCents != 500000) {
		t.Fatal("concurrent charge invalid", current)
	}
	f.assertWages(t, current, 120000, 150000)
}

func TestProjectCombinationPermissionPricePayrollIsolationMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成组合权限", Date: "2026-10-10", Lines: combinationActualLines()})
	employee := f.employee(t, []string{"orders", customerPricePermission, "wages"}, "combo-w-source", "combo-p-source")
	payload := combinationPayload(d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)})
	f.request(t, employee, "POST", "/api/project-combination", "", payload, 403)
	forged := f.edit(t, employee, d.ID)
	links := []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}
	forged.Combinations = &links
	f.request(t, employee, "POST", "/api/order", "", forged, 403)
	employee = f.permissions(t, employee, []string{"orders", combinationPermission})
	key := id()
	result := priceTestJSON(t, f.request(t, employee, "POST", "/api/project-combination", key, payload, 200))
	assertNoCustomerMoney(t, result)
	for _, path := range []string{"/api/order?id=" + d.ID, "/api/orders?month=2026-10", "/api/project-work?id=" + d.ProjectID, "/api/project-combinations?project_id=" + d.ProjectID, "/api/projects?company_id=loc-co"} {
		response := priceTestJSON(t, f.request(t, employee, "GET", path, "", nil, 200))
		assertNoCustomerMoney(t, response)
		if strings.Contains(jsonText(response), "labor_cents") || strings.Contains(jsonText(response), "wage_cents") {
			t.Fatal("combo grant bypassed wage access", path, response)
		}
	}
	f.request(t, employee, "GET", "/api/finance-companies", "", nil, 403)
	f.request(t, employee, "GET", "/api/orders?view=wages", "", nil, 403)
	// A price-less employee can still edit actual construction. Missing/forged
	// monetary fields must not zero saved fixed prices, waivers or wages.
	edit := f.edit(t, employee, d.ID)
	edit.Combinations = nil
	edit.Note = "无价员工普通内容修改"
	for i := range edit.Lines {
		edit.Lines[i].Price = 1
		edit.Lines[i].Wage = 1
		edit.Lines[i].Labor = 0
		edit.Lines[i].Total = 0
	}
	result = priceTestJSON(t, f.request(t, employee, "POST", "/api/order", "", edit, 200))
	assertNoCustomerMoney(t, result)
	current := f.read(t, d.ID)
	if current.TotalCents != 480000 || len(current.Combinations) != 1 || current.Note != edit.Note {
		t.Fatal("hidden ordinary save lost customer charge", current)
	}
	f.assertWages(t, current, 120000, 150000)
	employee = f.permissions(t, employee, []string{"orders"})
	// A previously valid idempotent key cannot replay a revoked operation.
	f.request(t, employee, "POST", "/api/project-combination", key, payload, 403)
	assertNoCustomerMoney(t, priceTestJSON(t, f.request(t, employee, "GET", "/api/project-combinations?project_id="+d.ProjectID, "", nil, 200)))
	edit = f.edit(t, employee, d.ID)
	edit.Combinations = nil
	edit.Note = "撤销组合授权后仍可普通保存"
	f.request(t, employee, "POST", "/api/order", "", edit, 200)
	if f.read(t, d.ID).TotalCents != 480000 {
		t.Fatal("revocation ordinary save removed charges")
	}
}

func TestProjectCombinationCraftAndAdministratorBoundariesMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成工艺权限", Date: "2026-10-10", Lines: combinationActualLines()})
	onlyWater := f.employee(t, []string{"orders", combinationPermission, customerPricePermission}, "combo-w-source")
	f.request(t, onlyWater, "POST", "/api/project-combination", "", combinationPayload(d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}), 403)
	d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}, 200)
	f.request(t, onlyWater, "POST", "/api/project-combination", "", combinationPayload(d, []ProjectCombination{}), 403)
	name := "combo-operator-" + id()[:8]
	f.request(t, f.admin, "POST", "/api/user", "", map[string]any{"username": name, "name": "合成普通管理员", "role": "operator", "active": true, "password": "synthetic", "permissions": []string{}}, 200)
	var userID string
	if err := f.a.db.QueryRow("SELECT id FROM users WHERE username=?", name).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	operator := f.session(t, userID)
	f.request(t, operator, "POST", "/api/project-combination", "", combinationPayload(d, []ProjectCombination{}), 403)
	var user User
	if err := f.a.db.QueryRow("SELECT id,username,name,role,active,version FROM users WHERE id=?", userID).Scan(&user.ID, &user.Username, &user.Name, &user.Role, &user.Active, &user.Version); err != nil {
		t.Fatal(err)
	}
	user.Permissions = []string{combinationPermission}
	f.request(t, operator, "POST", "/api/user", "", user, 403)
	f.request(t, operator, "POST", "/api/user", "", map[string]any{"username": "forged-" + id()[:8], "name": "合成越权", "role": "employee", "active": true, "password": "synthetic", "permissions": []string{"orders", combinationPermission}}, 403)
	operator = f.permissions(t, operator, []string{combinationPermission})
	f.request(t, operator, "POST", "/api/project-combination", "", combinationPayload(d, []ProjectCombination{}), 200)
}

func TestProjectCombinationDeliveredReceivedSettledProtectionMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	for _, kind := range []string{"delivered", "receipt", "settled"} {
		t.Run(kind, func(t *testing.T) {
			d := f.save(t, Order{Company: "loc-co", Address: "合成账务保护-" + kind, Date: "2026-10-10", Lines: combinationActualLines()})
			d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}, 200)
			if kind == "settled" {
				var account string
				var version int64
				if err := f.a.db.QueryRow("SELECT id,version FROM order_worker_wages WHERE order_id=? AND worker_id='loc-a'", d.ID).Scan(&account, &version); err != nil {
					t.Fatal(err)
				}
				f.request(t, f.admin, "POST", "/api/wage-settlement", "", SettlementRequest{Action: "pay", Orders: []SettlementOrder{{ID: account, Version: version}}}, 200)
			} else {
				var ticket string
				var version int64
				if err := f.a.db.QueryRow("SELECT id,version FROM project_tickets WHERE project_id=?", d.ProjectID).Scan(&ticket, &version); err != nil {
					t.Fatal(err)
				}
				beforeDelivery := orderLocationsState(t, f.a.db)
				f.request(t, f.admin, "POST", "/api/project-ticket-action", "", ProjectTicketActionRequest{ID: ticket, Version: version, Action: "deliver", DeliveredAt: "2026-10-10", Recipient: "合成收票人"}, 409)
				if !reflect.DeepEqual(beforeDelivery, orderLocationsState(t, f.a.db)) {
					t.Fatal("retired delivery mutated the draft or accounting state")
				}
				fixtureLegacyDeliveredProjectTicket(t, f.a.db, f.u, ticket, version, "2026-10-10")
				if kind == "receipt" {
					if err := f.a.db.QueryRow("SELECT version FROM project_tickets WHERE id=?", ticket).Scan(&version); err != nil {
						t.Fatal(err)
					}
					f.request(t, f.admin, "POST", "/api/project-ticket-action", "", ProjectTicketActionRequest{ID: ticket, Version: version, Action: "receive", Amount: 10000, ReceivedAt: "2026-10-10"}, 200)
				}
			}
			d = f.read(t, d.ID)
			before := orderLocationsState(t, f.a.db)
			f.set(t, d, []ProjectCombination{}, 409)
			if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
				t.Fatal("protected account mutated")
			}
			edit := f.edit(t, f.admin, d.ID)
			edit.Combinations = nil
			edit.Note = "不能绕过已结账保护"
			f.request(t, f.admin, "POST", "/api/order", "", edit, 400)
			if f.read(t, d.ID).TotalCents != 480000 {
				t.Fatal("protected ordinary update changed charge")
			}
		})
	}
}

func TestProjectCombinationLegacySnapshotsAndMigrationMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成旧含价快照", Date: "2026-10-01", Lines: combinationActualLines()})
	f.exec(t, `UPDATE order_lines SET total_cents=123456,labor_cents=0 WHERE order_id=? AND category='防水涂料'`, d.ID)
	f.exec(t, `UPDATE order_lines SET price_cents=0,total_cents=0,use_included_price=1,included_by_craft_id='combo-w',charge_note='原含价收费快照' WHERE order_id=? AND category='丙纶'`, d.ID)
	f.exec(t, `UPDATE orders SET total_cents=123456,wage_cents=150000 WHERE id=?`, d.ID)
	f.exec(t, `UPDATE order_worker_wages SET wage_cents=0 WHERE order_id=? AND worker_id='loc-a'`, d.ID)
	before := orderLocationsState(t, f.a.db)
	if err := initSchema(f.a.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateProjectCombinations(f.a.db); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, orderLocationsState(t, f.a.db)) {
		t.Fatal("migration normalized historical price/wages/permissions")
	}
	d = f.read(t, d.ID)
	edit := f.edit(t, f.admin, d.ID)
	edit.Combinations = nil
	edit.Note = "仅修改旧说明"
	f.request(t, f.admin, "POST", "/api/order", "", edit, 200)
	current := f.read(t, d.ID)
	if current.TotalCents != 123456 || *current.WageCents != 150000 || *current.Lines[0].Labor != 0 || !current.Lines[1].UseIncludedPrice {
		t.Fatal("old charge/wage snapshot automatically repaired", current)
	}
	edit = f.edit(t, f.admin, d.ID)
	edit.Combinations = nil
	edit.Lines[0].Segments = []Segment{{Length: 7, Width: 10}}
	f.request(t, f.admin, "POST", "/api/order", "", edit, 409)
	f.set(t, current, []ProjectCombination{combinationPair(current, "polypropylene", 1000000)}, 400)
	newOrder := Order{Company: "loc-co", Address: "合成拒绝旧含价请求", Date: "2026-10-10", Lines: combinationActualLines()}
	newOrder.Lines[1].UseIncludedPrice = true
	f.request(t, f.admin, "POST", "/api/order", "", newOrder, 400)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=437", 1)
}

func TestProjectCombinationMeasurementAndSiteBoundariesMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成工地甲", Date: "2026-10-10", Lines: combinationActualLines()})
	other := f.save(t, Order{Company: "loc-co", Address: "合成工地乙", Date: "2026-10-09", Lines: combinationActualLines()})
	c := combinationPair(d, "polypropylene", 1000000)
	c.PolypropyleneLine = other.Lines[1].ID
	c.PolypropyleneVersion = other.Version
	f.set(t, d, []ProjectCombination{c}, 400)
	f.exec(t, `UPDATE order_lines SET mode='米' WHERE id=?`, d.Lines[1].ID)
	f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 1000000)}, 400)
	f.exec(t, `UPDATE order_lines SET mode='平方',temporary=1 WHERE id=?`, d.Lines[1].ID)
	f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 1000000)}, 400)
	f.exec(t, `UPDATE order_lines SET temporary=0 WHERE id=?`, d.Lines[1].ID)
	f.exec(t, `UPDATE projects SET status='已结项' WHERE id=?`, d.ProjectID)
	f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 1000000)}, 409)
	response := priceTestJSON(t, f.request(t, f.admin, "GET", "/api/project-combinations?project_id="+d.ProjectID+"&line_ids="+other.Lines[1].ID, "", nil, 200))
	if len(response["rows"].([]any)) != 0 {
		t.Fatal("candidate query leaked another site's line", response)
	}
	// Route-level pre-idempotency guard: explicit empty lists are operations too.
	employee := f.employee(t, []string{"orders"}, "combo-w-source", "combo-p-source")
	f.request(t, employee, "POST", "/api/order", "", map[string]any{"combinations": []any{}}, 403)
}
