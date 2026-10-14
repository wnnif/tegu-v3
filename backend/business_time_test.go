package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestBusinessMonthRangeUTC(t *testing.T) {
	for _, tc := range []struct{ month, from, to string }{
		{"2026-10", "2026-09-30 16:00:00", "2026-10-31 16:00:00"},
		{"2028-02", "2028-01-31 16:00:00", "2028-02-29 16:00:00"},
		{"2026-12", "2026-11-30 16:00:00", "2026-12-31 16:00:00"},
	} {
		from, to, err := businessMonthRangeUTC(tc.month)
		if err != nil || from != tc.from || to != tc.to {
			t.Fatalf("%s UTC month: %s..%s, %v", tc.month, from, to, err)
		}
		dateFrom, dateTo, err := monthRange(tc.month)
		if err != nil || dateFrom != tc.month+"-01" || len(dateTo) != 10 {
			t.Fatal("civil DATE month range changed", dateFrom, dateTo, err)
		}
	}
	if _, _, err := businessMonthRangeUTC("2026-13"); err == nil {
		t.Fatal("invalid month accepted")
	}
}

func TestShanghaiTicketMonthArchivesMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	maintenanceFaultExec(t, a.db, "INSERT INTO companies(id,name) VALUES('month-company','合成月份公司')")
	maintenanceFaultExec(t, a.db, "INSERT INTO projects(id,number,company_id,name,address,created_by,ticket_mode_snapshot) VALUES('month-project','MONTH-PROJECT','month-company','合成月份工地','合成地址','fixture','single')")
	for _, tc := range []struct{ id, utc string }{
		{"september-last", "2026-09-30 15:59:59"},
		{"october-first", "2026-09-30 16:00:00"},
		{"october-early", "2026-09-30 17:00:00"},
		{"october-last", "2026-10-31 15:59:59"},
		{"november-first", "2026-10-31 16:00:00"},
	} {
		if _, err := a.db.Exec(`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,total_cents,created_at,updated_at) VALUES(?,'month-project','month-company','合成月份公司','合成月份工地','合成地址',?,'append',10000,?,?)`, tc.id, tc.id, tc.utc, tc.utc); err != nil {
			t.Fatal(err)
		}
	}
	// Arrival dates remain civil DATE values even when the linked ticket's UTC
	// creation/activity timestamp belongs to a different displayed month.
	maintenanceFaultExec(t, a.db, "INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,created_by) VALUES('receipt-september','october-first',100,'2026-09-30','fixture'),('receipt-october','september-last',200,'2026-10-01','fixture')")
	finance := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	for _, tc := range []struct {
		month string
		ids   []string
	}{
		{"2026-09", []string{"september-last"}},
		{"2026-10", []string{"october-early", "october-first", "october-last"}},
		{"2026-11", []string{"november-first"}},
	} {
		w := httptest.NewRecorder()
		path := "/api/project-tickets?company_id=month-company&month=" + tc.month
		if err := a.listProjectTickets(w, httptest.NewRequest("GET", path, nil), finance); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Rows  []ticketFinanceRow `json:"rows"`
			Total int                `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, row := range result.Rows {
			got = append(got, row.ID)
		}
		sort.Strings(got)
		if result.Total != len(tc.ids) || !reflect.DeepEqual(got, tc.ids) {
			t.Fatal("Shanghai ticket month list", tc.month, got, result.Total)
		}
		w = httptest.NewRecorder()
		if err := a.listProjectTickets(w, httptest.NewRequest("GET", path+"&export=1", nil), finance); err != nil {
			t.Fatal(err)
		}
		records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
		if err != nil || len(records) != len(tc.ids)+1 {
			t.Fatal("Shanghai ticket CSV", tc.month, len(records), err)
		}
		got = []string{}
		for _, row := range records[1:] {
			got = append(got, row[3])
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.ids) {
			t.Fatal("CSV/list month mismatch", tc.month, got)
		}
	}
	assertPending := func(now string, want []string) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, now)
		if err != nil {
			t.Fatal(err)
		}
		got, err := a.pendingArchiveMonths(at)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("automatic business months", now, got, want, err)
		}
	}
	assertPending("2026-09-30T16:00:00Z", []string{"2026-09"})
	assertPending("2026-10-31T16:00:00Z", []string{"2026-09", "2026-10"})
	for _, tc := range []struct {
		month       string
		ticketIDs   []string
		receiptDate string
	}{
		{"2026-09", []string{"september-last"}, "2026-09-30"},
		{"2026-10", []string{"october-early", "october-first", "october-last"}, "2026-10-01"},
	} {
		files, err := a.archive(tc.month, "month-company", true)
		if err != nil {
			t.Fatal("monthly archive", tc.month, err)
		}
		seenFinance, seenReceipt := false, false
		for _, file := range files {
			if file.Kind != "票据应收" && file.Kind != "收款流水" {
				continue
			}
			content, err := os.ReadFile(filepath.Join(os.Getenv("BACKUP_DIR"), "monthly", filepath.FromSlash(file.File)))
			if err != nil {
				t.Fatal(err)
			}
			records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(content), "\ufeff"))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if file.Kind == "票据应收" {
				seenFinance = true
				got := []string{}
				for _, row := range records[1 : len(records)-1] {
					got = append(got, row[1])
				}
				sort.Strings(got)
				if file.Count != len(tc.ticketIDs) || !reflect.DeepEqual(got, tc.ticketIDs) {
					t.Fatal("monthly finance artifact", tc.month, got, file.Count)
				}
			} else {
				seenReceipt = true
				if file.Count != 1 || len(records) != 3 || records[1][2] != tc.receiptDate {
					t.Fatal("arrival DATE month changed", tc.month, records, file.Count)
				}
			}
		}
		if !seenFinance || !seenReceipt {
			t.Fatal("missing expected finance/arrival archive", tc.month, files)
		}
	}
	assertPending("2026-10-31T16:00:00Z", []string{})
	t.Log("Shanghai boundary list, CSV and actual monthly artifacts match; arrival DATE values and automatic completed-month exclusions are preserved")
}
