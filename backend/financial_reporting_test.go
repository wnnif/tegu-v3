package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMonthlyFinancialReportingMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	financialReportingAcceptance(t, a)
}

// Called by the existing isolated database suite before the large-scale fixture.
func financialReportingAcceptance(t *testing.T, a *App) {
	t.Helper()
	var database string
	if e := a.db.QueryRow("SELECT DATABASE()").Scan(&database); e != nil || !strings.HasPrefix(database, "tegu_go_test_") {
		t.Fatal("reporting test requires isolated test database", e)
	}
	company, project := id(), id()
	if _, e := a.db.Exec("INSERT INTO companies(id,name) VALUES(?,?)", company, "收款报表核对"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES(?,?,?,?,?,'test')", project, "RP-"+project, company, "核对项目", "核对地址"); e != nil {
		t.Fatal(e)
	}
	type expectedOrder struct {
		id, number, state   string
		amount              int64
		received, remaining string
	}
	cases := []expectedOrder{
		{id(), "REPORT-PARTIAL", "部分收款", 30000, "50.00", "250.00"},
		{id(), "REPORT-LEGACY-PAID", "已收款", 10000, "100.00", "0.00"},
		{id(), "REPORT-CANCELLED", "已取消", 20000, "0.00", "0.00"},
	}
	for _, c := range cases {
		if _, e := a.db.Exec("INSERT INTO orders(id,number,company_id,project_id,address,work_date,note,status,total_cents,created_by,ticket_no,delivered_at,recipient) VALUES(?,?,?,?,?,'2024-02-10','',?,?,'test','票据核对','2024-02-11','收票核对')", c.id, c.number, company, project, "报表工地", c.state, c.amount); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.db.Exec("UPDATE orders SET worker_name='历史施工人',wage_cents=1234 WHERE id=?", cases[0].id); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES(?,?,'开工','历史工艺','金额',JSON_ARRAY(),1000000,30000,1234,30000,1234,1)", id(), cases[0].id); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO order_materials(order_id,product_id,warehouse_id,qty_micro,product_name,unit,warehouse_name) VALUES(?,?,?,1250000,'历史材料','桶','历史仓库')", cases[0].id, id(), id()); e != nil {
		t.Fatal(e)
	}
	// One snapshotted order retains historical labels; legacy empty snapshots
	// fall back to the current project until migrated or explicitly associated.
	if _, e := a.db.Exec("UPDATE orders SET project_number='HISTORIC-PROJECT',project_name='历史项目名称' WHERE id=?", cases[0].id); e != nil {
		t.Fatal(e)
	}
	for _, r := range []struct {
		order  string
		amount int64
		voided bool
	}{{cases[0].id, 5000, false}, {cases[0].id, 2000, true}, {cases[2].id, 1000, false}} {
		if _, e := a.db.Exec("INSERT INTO payment_receipts(id,order_id,amount_cents,received_at,method,voucher_no,note,created_by,voided) VALUES(?,?,?,'2024-03-12','银行','凭证核对','跨施工月份到账','test',?)", id(), r.order, r.amount, r.voided); e != nil {
			t.Fatal(e)
		}
	}
	finance := User{ID: "report-test", Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	response := httptest.NewRecorder()
	if e := a.dashboard(response, httptest.NewRequest("GET", "/api/dashboard?month=2024-02", nil), finance); e != nil {
		t.Fatal(e)
	}
	var summary map[string]any
	if e := json.Unmarshal(response.Body.Bytes(), &summary); e != nil || number(summary, "receivable_cents") != 25000 {
		t.Fatal("dashboard remaining balance", summary, e)
	}
	parse := func(raw string) []map[string]string {
		t.Helper()
		records, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).ReadAll()
		if e != nil || len(records) == 0 {
			t.Fatal("invalid CSV", e)
		}
		result := []map[string]string{}
		for _, line := range records[1:] {
			row := map[string]string{}
			for i, name := range records[0] {
				row[name] = line[i]
			}
			result = append(result, row)
		}
		return result
	}
	for _, path := range []string{"/api/export?view=finance&month=2024-02&company_id=" + company, "/api/export?view=project&project_id=" + project} {
		response = httptest.NewRecorder()
		if e := a.exportFiltered(response, httptest.NewRequest("GET", path, nil), finance); e != nil {
			t.Fatal(e)
		}
		exported := parse(response.Body.String())
		if len(exported) != len(cases) {
			t.Fatal("export missing orders", len(exported))
		}
		for _, row := range exported {
			orderNumber := row["单号"]
			if orderNumber == "" {
				orderNumber = row["施工单号"]
			}
			projectNumber := row["工地项目编号"]
			if projectNumber == "" {
				projectNumber = row["项目编号"]
			}
			wantNumber := "RP-" + project
			if orderNumber == cases[0].number {
				wantNumber = "HISTORIC-PROJECT"
			}
			if projectNumber != wantNumber {
				t.Fatal("project number snapshot lost", row)
			}
			if strings.Contains(path, "view=project") {
				wantName := "核对项目"
				if orderNumber == cases[0].number {
					wantName = "历史项目名称"
				}
				if row["项目名称"] != wantName {
					t.Fatal("project name snapshot lost", row)
				}
			}
		}
		for _, expected := range cases {
			found := false
			for _, row := range exported {
				if row["单号"] == expected.number || row["施工单号"] == expected.number {
					found = true
					if row["已收金额"] != expected.received || row["剩余应收"] != expected.remaining || row["票据号"] != "票据核对" {
						t.Fatal("financial export mismatch", path, row)
					}
				}
			}
			if !found {
				t.Fatal("missing order", expected.number)
			}
		}
	}
	for _, month := range []string{"2024-02", "2024-03"} {
		files, e := a.archive(month, company, false, finance)
		if e != nil {
			t.Fatal(e)
		}
		ordersList := httptest.NewRecorder()
		ordersOnly := User{Role: "employee", Permissions: []string{"orders", "customer_prices"}}
		if e := a.listArchives(ordersList, httptest.NewRequest("GET", "/api/archives?month="+month, nil), ordersOnly); e != nil || strings.Contains(ordersList.Body.String(), "施工内部留档表") || strings.Contains(ordersList.Body.String(), "工资明细") {
			t.Fatal("orders-only archive list exposed internal wages", e)
		}
		ordersWithoutPrices := User{Role: "employee", Permissions: []string{"orders"}}
		maintenanceFaultError(t, a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/archives?month="+month, nil), ordersWithoutPrices), 403)
		wagesList := httptest.NewRecorder()
		wagesOnly := User{Role: "employee", Permissions: []string{"wages"}}
		wantInternal := month == "2024-02"
		if e := a.listArchives(wagesList, httptest.NewRequest("GET", "/api/archives?month="+month, nil), wagesOnly); e != nil || strings.Contains(wagesList.Body.String(), "施工内部留档表") || strings.Contains(wagesList.Body.String(), "施工验收表") || strings.Contains(wagesList.Body.String(), "工资明细") != wantInternal {
			t.Fatal("wages-only archive list permission mismatch", e)
		}
		wagesWithPrices := User{Role: "employee", Permissions: []string{"wages", "customer_prices"}}
		internalList := httptest.NewRecorder()
		if e := a.listArchives(internalList, httptest.NewRequest("GET", "/api/archives?month="+month, nil), wagesWithPrices); e != nil || strings.Contains(internalList.Body.String(), "施工内部留档表") != wantInternal || strings.Contains(internalList.Body.String(), "施工验收表") {
			t.Fatal("internal archive list permission mismatch", e)
		}
		found := false
		for _, file := range files {
			if file.Kind == "施工内部留档表" {
				path := "/api/archives?file=" + file.File
				if e := a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil), finance); e == nil {
					t.Fatal("internal workbook leaked without wages permission")
				}
				maintenanceFaultError(t, a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil), wagesOnly), 403)
				download := httptest.NewRecorder()
				if e := a.listArchives(download, httptest.NewRequest("GET", path, nil), wagesWithPrices); e != nil || download.Code != 200 || !strings.Contains(download.Header().Get("Content-Type"), "spreadsheetml.sheet") {
					t.Fatal("internal workbook download", download.Code, e)
				}
				if month == "2024-02" {
					workbook, e := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
					if e != nil {
						t.Fatal(e)
					}
					var sheet string
					for _, part := range workbook.File {
						if part.Name == "xl/worksheets/sheet1.xml" {
							reader, e := part.Open()
							if e != nil {
								t.Fatal(e)
							}
							content, e := io.ReadAll(reader)
							reader.Close()
							if e != nil {
								t.Fatal(e)
							}
							sheet = string(content)
						}
					}
					for _, expected := range []string{"历史施工人", "历史工艺", "历史材料", "历史仓库", `<v>12.34</v>`, "1.250000 桶"} {
						if !strings.Contains(sheet, expected) {
							t.Fatal("internal workbook lost historical record", expected)
						}
					}
				}
				continue
			}
			if file.Kind == "施工验收表" {
				path := "/api/archives?file=" + file.File
				if e := a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil), finance); e == nil {
					t.Fatal("acceptance workbook leaked without orders permission")
				}
				maintenanceFaultError(t, a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil), ordersWithoutPrices), 403)
				ordersOnly := User{Role: "employee", Permissions: []string{"orders", "customer_prices"}}
				download := httptest.NewRecorder()
				if e := a.listArchives(download, httptest.NewRequest("GET", path, nil), ordersOnly); e != nil || download.Code != 200 || !strings.Contains(download.Header().Get("Content-Type"), "spreadsheetml.sheet") {
					t.Fatal("acceptance workbook download", download.Code, e)
				}
				continue
			}
			if file.Kind == "工资明细" {
				download := httptest.NewRecorder()
				if e := a.listArchives(download, httptest.NewRequest("GET", "/api/archives?file="+file.File, nil), wagesOnly); e != nil || download.Code != 200 {
					t.Fatal("wage CSV must remain available without customer prices", download.Code, e)
				}
			}
			raw, e := os.ReadFile(filepath.Join(os.Getenv("BACKUP_DIR"), "monthly", file.File))
			if e != nil {
				t.Fatal(e)
			}
			records := parse(string(raw))
			if month == "2024-02" && file.Kind == "旧单据应收" {
				found = true
				last := records[len(records)-1]
				if last["已收金额"] != "150.00" || last["剩余应收"] != "250.00" {
					t.Fatal("archive financial totals", last)
				}
			}
			if month == "2024-03" && file.Kind == "收款流水" {
				found = true
				if file.Count != 3 || file.Amount != 5000 || len(records) != 4 {
					t.Fatal("arrival-month receipt archive", file, records)
				}
				noFinance := User{Role: "employee", Permissions: []string{"orders", "customer_prices"}}
				if e := a.listArchives(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/archives?file="+file.File, nil), noFinance); e == nil {
					t.Fatal("receipt archive leaked without finance permission")
				}
			}
		}
		if !found {
			t.Fatal("expected archive missing", month, files)
		}
	}
	t.Log("dashboard/export/monthly financial balances: partial payments, legacy fully paid and cancelled orders; arrival-month receipts and download permissions verified")
}

func backupCloudFailureRetentionAcceptance(t *testing.T, a *App) {
	t.Helper()
	var database string
	if e := a.db.QueryRow("SELECT DATABASE()").Scan(&database); e != nil || !strings.HasPrefix(database, "tegu_go_test_") {
		t.Fatal("backup retention test requires isolated test database", e)
	}
	oldDirectory := os.Getenv("BACKUP_DIR")
	directory := t.TempDir()
	t.Setenv("BACKUP_DIR", directory)
	t.Setenv("DATA_DIR", t.TempDir())
	defer os.Setenv("BACKUP_DIR", oldDirectory)
	var previous string
	e := a.db.QueryRow("SELECT value FROM settings WHERE setting_key='backup'").Scan(&previous)
	if e != nil && e != sql.ErrNoRows {
		t.Fatal(e)
	}
	hadSettings := e == nil
	defer func() {
		if hadSettings {
			a.db.Exec("UPDATE settings SET value=? WHERE setting_key='backup'", previous)
		} else {
			a.db.Exec("DELETE FROM settings WHERE setting_key='backup'")
		}
	}()
	if _, e = a.db.Exec("INSERT INTO settings(setting_key,value) VALUES('backup',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", jsonText(BackupSettings{Enabled: true, Secret: "invalid-test-ciphertext", Retain: 24, Interval: 60})); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 40; i++ {
		name := filepath.Join(directory, fmt.Sprintf("tegu-go-20200101T%06d-old.ndjson", i))
		if e = os.WriteFile(name, []byte("test snapshot"), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(name+".sha256", []byte("test checksum"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	name, e := a.backup()
	if e == nil || name == "" {
		t.Fatal("cloud failure was not reported", e)
	}
	if _, e = os.Stat(filepath.Join(directory, name)); e != nil {
		t.Fatal("new local snapshot lost", e)
	}
	files, e := filepath.Glob(filepath.Join(directory, "tegu-go-*.ndjson"))
	if e != nil || len(files) > 26 {
		t.Fatal("cloud failure prevented local pruning", len(files), e)
	}
	var status string
	if e = a.db.QueryRow("SELECT status FROM backup_runs WHERE file=?", name).Scan(&status); e != nil || status != "云上传失败" {
		t.Fatal("cloud failure status lost", status, e)
	}
	t.Log("cloud credential failure remains visible while completed local snapshots follow retention")
}
