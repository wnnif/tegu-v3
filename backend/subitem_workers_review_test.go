package main

import (
	"database/sql"
	"encoding/csv"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSubitemWorkersReviewTicketFiltering(t *testing.T) {
	amount := func(n int64) *int64 { return &n }
	fixture := func(wages bool) orderDetailView {
		o := orderDetailView{ID: "order", WorkerID: "c", WorkerName: "合成送货人", WorkerNames: "合成主卫师傅、合成厨房师傅、合成送货人", Lines: []orderLineView{
			{ID: "a-line", WorkerID: "a", WorkerName: "合成主卫师傅", Total: 12000},
			{ID: "b-line", WorkerID: "b", WorkerName: "合成厨房师傅", Total: 8000},
			{ID: "b-fiber", WorkerID: "b", WorkerName: "合成厨房师傅", Total: 0},
		}, Sales: []Sale{{ID: "sale", Total: 3000}}}
		if wages {
			o.Lines[0].Labor, o.Lines[1].Labor, o.Lines[2].Labor = amount(1800), amount(1200), amount(1600)
			o.DeliveryWage, o.WageCents = amount(500), amount(5100)
			records := []workerWageRecord{{ID: "wa", WorkerID: "a", WageCents: 1800}, {ID: "wb", WorkerID: "b", WageCents: 2800}, {ID: "wc", WorkerID: "c", WageCents: 500, DeliveryWage: 500}}
			o.WageRecords = &records
		}
		return o
	}
	t.Run("customer only includes workers on the chosen ticket", func(t *testing.T) {
		o := fixture(false)
		filterOrderForTicket(&o, map[string]bool{"a-line": true})
		if o.WorkerNames != "合成主卫师傅" || o.TotalCents != 12000 || o.WageRecords != nil || o.WageCents != nil || o.DeliveryWage != nil {
			t.Fatalf("customer ticket retained other workers or created wage fields: %+v", o)
		}
	})
	t.Run("customer sales ticket only includes its delivery person", func(t *testing.T) {
		o := fixture(false)
		filterOrderForTicket(&o, map[string]bool{"sale": true})
		if o.WorkerNames != "合成送货人" || len(o.Lines) != 0 || o.TotalCents != 3000 {
			t.Fatalf("sales ticket retained unrelated construction workers: %+v", o)
		}
	})
	t.Run("one worker's ticket counts only included lines", func(t *testing.T) {
		o := fixture(true)
		filterOrderForTicket(&o, map[string]bool{"b-line": true})
		if o.WorkerNames != "合成厨房师傅" || *o.WageCents != 1200 || *o.DeliveryWage != 0 || len(*o.WageRecords) != 1 || (*o.WageRecords)[0].WageCents != 1200 {
			t.Fatalf("ticket reused the worker's whole-order wage: %+v", o)
		}
	})
	t.Run("construction and sales keep stable separate wages", func(t *testing.T) {
		for n := 0; n < 20; n++ {
			o := fixture(true)
			filterOrderForTicket(&o, map[string]bool{"a-line": true, "sale": true})
			records := *o.WageRecords
			if len(records) != 2 || !reflect.DeepEqual([]string{records[0].WorkerID, records[1].WorkerID}, []string{"a", "c"}) || records[0].WageCents != 1800 || records[1].WageCents != 500 || records[1].DeliveryWage != 500 || *o.WageCents != 2300 {
				t.Fatalf("ticket wage order or delivery wage changed: %+v", records)
			}
		}
	})
}

func TestSubitemWorkersReviewRestoreIntegrityMySQL(t *testing.T) {
	target, _ := maintenanceFaultDB(t)
	fixture := func(accounts []snapshotRecord, paid bool) string {
		records := []snapshotRecord{
			{Format: "tegu-go-v1"},
			{Table: "companies", Row: map[string]any{"id": "co", "name": "合成完整性公司"}},
			{Table: "orders", Row: map[string]any{"id": "o", "number": "REVIEW", "company_id": "co", "address": "合成恢复地址", "work_date": "2026-10-04", "worker_id": "a", "worker_name": "合成历史甲", "note": "", "created_by": "review", "wage_cents": 3000, "wage_paid": paid}},
			{Table: "order_lines", Row: map[string]any{"id": "la", "order_id": "o", "category": "防水涂料", "name": "合成主卫", "mode": "平方", "segments": "[]", "qty_micro": 6000000, "price_cents": 2000, "wage_cents": 300, "total_cents": 12000, "labor_cents": 1800, "temporary": 1, "worker_id": "a", "worker_name": "合成历史甲"}},
			{Table: "order_lines", Row: map[string]any{"id": "lb", "order_id": "o", "category": "防水涂料", "name": "合成厨房", "mode": "平方", "segments": "[]", "qty_micro": 4000000, "price_cents": 2000, "wage_cents": 300, "total_cents": 8000, "labor_cents": 1200, "temporary": 1, "worker_id": "b", "worker_name": "合成历史乙"}},
		}
		records = append(records, accounts...)
		records = append(records, snapshotRecord{End: true, Counts: map[string]int{"companies": 1, "orders": 1, "order_lines": 2, "order_worker_wages": len(accounts)}})
		return maintenanceFaultSnapshot(t, t.TempDir(), records...)
	}
	account := func(worker string, wage int, paid bool) snapshotRecord {
		return snapshotRecord{Table: "order_worker_wages", Row: map[string]any{"id": "w" + worker, "order_id": "o", "worker_id": worker, "worker_name": "合成人员" + worker, "wage_cents": wage, "wage_paid": paid, "legacy": 0, "version": 2}}
	}
	for _, c := range []struct {
		name     string
		accounts []snapshotRecord
		paid     bool
	}{
		{"missing one worker", []snapshotRecord{account("a", 1800, true)}, true},
		{"wrong split with matching order total", []snapshotRecord{account("a", 2000, false), account("b", 1000, false)}, false},
		{"stale partial-paid cache", []snapshotRecord{account("a", 1800, true), account("b", 1200, false)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			maintenanceFaultError(t, target.restore(fixture(c.accounts, c.paid)), 422)
			for _, table := range backupTables {
				maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM "+table, 0)
			}
			maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM schema_migrations WHERE version=434", 1)
		})
	}
	// A complete new snapshot passes the same guard, preserving per-worker paid
	// state and versions rather than merely having the expected number of rows.
	if err := target.restore(fixture([]snapshotRecord{account("a", 1800, true), account("b", 1200, false)}, true)); err != nil {
		t.Fatal("complete mixed-worker snapshot rejected", err)
	}
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='a' AND wage_cents=1800 AND wage_paid=1 AND version=2", 1)
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM order_worker_wages WHERE worker_id='b' AND wage_cents=1200 AND wage_paid=0 AND version=2", 1)
}

func TestSubitemWorkersReviewLegacyAndScopeMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	file := maintenanceFaultSnapshot(t, t.TempDir(),
		snapshotRecord{Format: "tegu-go-v1"},
		snapshotRecord{Table: "companies", Row: map[string]any{"id": "co", "name": "合成旧公司"}},
		snapshotRecord{Table: "orders", Row: map[string]any{"id": "old", "number": "OLD-REVIEW", "company_id": "co", "address": "合成旧地址", "work_date": "2026-10-04", "worker_id": "a", "worker_name": "旧人名", "note": "", "created_by": "review", "wage_cents": 987, "wage_paid": 1, "version": 7}},
		snapshotRecord{Table: "order_lines", Row: map[string]any{"id": "old-line", "order_id": "old", "category": "开工", "name": "合成旧项", "mode": "金额", "segments": "[]", "qty_micro": 1000000, "price_cents": 1000, "wage_cents": 111, "total_cents": 1000, "labor_cents": 111, "temporary": 1, "worker_id": "a", "worker_name": "旧人名"}},
		snapshotRecord{Table: "order_worker_wages", Row: map[string]any{"id": "old-wage", "order_id": "old", "worker_id": "a", "worker_name": "旧人名", "wage_cents": 987, "wage_paid": 1, "version": 7, "legacy": 1}},
		snapshotRecord{End: true, Counts: map[string]int{"companies": 1, "orders": 1, "order_lines": 1, "order_worker_wages": 1}},
	)
	if err := a.restore(file); err != nil {
		t.Fatal("legacy whole-order amount was incorrectly recalculated", err)
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE wage_cents=987 AND wage_paid=1 AND version=7 AND legacy=1", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE labor_cents=111", 1)
	output := httptest.NewRecorder()
	if err := a.exportFiltered(output, httptest.NewRequest("GET", "/api/export?view=wages&month=2026-10", nil), User{ID: "review", Role: "admin"}); err != nil {
		t.Fatal("legacy wage export failed", err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(output.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][1] != "合成旧公司" || rows[1][8] != "9.87" {
		t.Fatal("legacy export lost company or exact wage", rows, err)
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,created_by,wage_cents) VALUES('bad','BAD-REVIEW','co','合成其它月份','2026-09-04','a','甲','','review',3000)`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary,worker_id) VALUES('bad-line','bad','防水涂料','合成缺失行','金额',JSON_ARRAY(),1000000,0,1200,0,1200,1,'b')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents) VALUES('bad-wage','bad','a','甲',1800)`)
	tx, err := a.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := validateOrderWorkerAccount(tx, "old"); err != nil {
		t.Fatal("detail validation scanned unrelated damaged orders", err)
	}
	if err := validateWorkerAccountScope(tx, "o.work_date>=? AND o.work_date<?", []any{"2026-10-01", "2026-11-01"}); err != nil {
		t.Fatal("month validation scanned unrelated damaged orders", err)
	}
	archived, err := archiveWorkerWages(tx, "o.work_date>=? AND o.work_date<?", []any{"2026-10-01", "2026-11-01", "co", ""})
	if err != nil || len(archived) != 1 || archived[0].Company != "合成旧公司" || archived[0].WageCents != 987 {
		t.Fatal("legacy wage archive lost company or exact wage", archived, err)
	}
	maintenanceFaultError(t, validateOrderWorkerAccount(tx, "bad"), 422)
}
