package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func archiveCompanyReadTest(t *testing.T, a *App, u User, query string) (archiveCompanyResponse, string) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := a.get(w, httptest.NewRequest("GET", "/api/archive-companies?"+query, nil), u); err != nil {
		t.Fatal(err)
	}
	var result archiveCompanyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result, w.Body.String()
}

func TestArchiveCompanyPaginationAndLegacyMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	admin := User{Role: "admin"}
	base := id()
	files := []ArchiveFile{}
	for i := 0; i < 204; i++ {
		files = append(files, ArchiveFile{File: base + "/" + id() + ".xlsx", CompanyID: fmt.Sprintf("co-%03d", i), Company: "相同公司名", Kind: "施工验收表", Count: 1, Amount: int64(i)})
	}
	if _, err := a.db.Exec("INSERT INTO archive_runs(id,month,automatic,manifest,created_at) VALUES(?,'2026-10',0,?,'2026-10-01')", base, jsonText(files)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		run := id()
		manifest := []ArchiveFile{{File: run + "/" + id() + ".xlsx", CompanyID: "co-000", Company: "公司后改名", Kind: "施工验收表", Count: i + 2, Amount: 10000}}
		if _, err := a.db.Exec("INSERT INTO archive_runs(id,month,automatic,manifest,created_at) VALUES(?,'2026-10',0,?,?)", run, jsonText(manifest), fmt.Sprintf("2026-10-02 00:%02d:%02d", i/60, i%60)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		run := id()
		f := ArchiveFile{File: run + "/" + id() + ".csv", Company: "相同公司名", Kind: "财务应收", Count: 1, Amount: 1234}
		if _, err := a.db.Exec("INSERT INTO archive_runs(id,month,automatic,manifest) VALUES(?,'2026-10',0,?)", run, jsonText([]ArchiveFile{f})); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		result, _ := archiveCompanyReadTest(t, a, admin, fmt.Sprintf("month=2026-10&page=%d", page))
		if result.Total != 204 || result.LegacyTotal != 2 || result.PageSize != 100 {
			t.Fatal("unbounded company grouping", result.Total, result.LegacyTotal)
		}
		want := 100
		if page == 3 {
			want = 4
		}
		if len(result.Rows) != want {
			t.Fatal("page company count", page, len(result.Rows))
		}
		for _, row := range result.Rows {
			if seen[row.CompanyID] || row.Legacy {
				t.Fatal("duplicate or legacy latest group", row)
			}
			seen[row.CompanyID] = true
			if row.CompanyID == "co-000" && (row.Company != "公司后改名" || row.OrderCount == nil || *row.OrderCount != 102) {
				t.Fatal("stable ID rename latest", row)
			}
		}
	}
	if len(seen) != 204 {
		t.Fatal("same names merged", len(seen))
	}
	history, _ := archiveCompanyReadTest(t, a, admin, "history=1&month=2026-10&page=4")
	if history.Total != 307 || len(history.Rows) != 7 {
		t.Fatal("history >100 runs missing", history.Total, len(history.Rows))
	}
	filtered, _ := archiveCompanyReadTest(t, a, admin, "history=1&company_id=co-000&page=2")
	if filtered.Total != 102 || len(filtered.Rows) != 2 || filtered.LegacyTotal != 0 {
		t.Fatal("stable company filter", filtered.Total, len(filtered.Rows), filtered.LegacyTotal)
	}
	legacySeen := 0
	for page := 1; page <= 4; page++ {
		r, _ := archiveCompanyReadTest(t, a, admin, fmt.Sprintf("history=1&page=%d", page))
		for _, row := range r.Rows {
			if row.Legacy {
				legacySeen++
				if !strings.HasPrefix(row.Key, "l:") || len(row.Key) != 66 || row.Received != nil || row.Outstanding != nil {
					t.Fatal("legacy unknown balance guessed", row)
				}
			}
		}
	}
	if legacySeen != 2 {
		t.Fatal("legacy same name batches merged", legacySeen)
	}
	for _, q := range []string{"history=2", "month=2026-13", "page=1000001", "company_id=" + strings.Repeat("a", 65)} {
		err := a.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/archive-companies?"+q, nil), admin)
		maintenanceFaultError(t, err, 400)
	}
	maintenanceFaultError(t, a.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/archive-companies", nil), User{Role: "employee"}), 403)
}

func archiveZipContents(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	for _, file := range reader.File {
		if filepath.Base(file.Name) != file.Name || strings.ContainsAny(file.Name, "/\\:") || contents[file.Name] != nil {
			t.Fatal("unsafe or duplicate ZIP name", file.Name)
		}
		opened, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(opened)
		opened.Close()
		if e != nil {
			t.Fatal("CRC/read", e)
		}
		contents[file.Name] = data
	}
	return contents
}

func TestArchiveCompanyGenerationAndDownloadsHTTPMySQL(t *testing.T) {
	a, adminCookie, wagesCookie, ordersCookie := wageDetailHTTPFixture(t)
	// Full construction/internal workbooks need the independent customer-price
	// grant. Keep the shared wage-only fixture unchanged for its own tests.
	maintenanceFaultExec(t, a.db, `UPDATE users SET permissions=JSON_ARRAY('wages','customer_prices') WHERE username='wage-viewer'`)
	maintenanceFaultExec(t, a.db, `UPDATE users SET permissions=JSON_ARRAY('orders','customer_prices') WHERE username='construction-viewer'`)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('group-a','合成同名公司'),('group-b','合成同名公司')`,
		`INSERT INTO workers(id,name) VALUES('group-worker','合成人员')`,
		`INSERT INTO projects(id,number,company_id,name,address,created_by) VALUES('group-project','GROUP','group-a','合成工地','合成地址','test')`,
		`INSERT INTO orders(id,number,company_id,company_name,address,work_date,note,worker_id,worker_name,status,total_cents,wage_cents,created_by,ticket_managed) VALUES('group-order','GROUP-ORDER','group-a','合成同名公司','合成地址','2026-10-08','','group-worker','合成人员','进行中',11500,3600,'test',1),('group-zero','GROUP-ZERO','group-b','合成同名公司','零元实际施工','2026-10-08','','group-worker','合成人员','进行中',0,0,'test',1)`,
		`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES('group-line','group-order','开工','实际施工','金额',JSON_ARRAY(),1000000,10000,3600,10000,3600,1),('group-zero-line','group-zero','维修','零元项目','金额',JSON_ARRAY(),1000000,0,0,0,0,1)`,
		`INSERT INTO order_sales(id,order_id,product_id,warehouse_id,qty_micro,sale_qty_micro,price_cents,total_cents,product_name,unit,price_unit,input_unit,input_qty,sort_order) VALUES('group-sale','group-order','group-product','group-warehouse',1000000,1000000,1500,1500,'售料核对','桶','桶','base','1',1)`,
		`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,slot,status,total_cents,created_at,updated_at) VALUES('group-ticket','group-project','group-a','合成同名公司','合成工地','合成地址','GROUP-TICKET','normal',1,'部分收款',11500,'2026-10-08','2026-10-08')`,
		`INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,created_by,voided) VALUES('group-receipt','group-ticket',2000,'2026-10-08','test',0),('group-void','group-ticket',1000,'2026-10-08','test',1)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	generate := func() archiveCompanyResponse {
		t.Helper()
		started := accountIdentityRequest(a, "POST", "/api/maintenance/archive", map[string]string{"month": "2026-10", "company": ""}, adminCookie)
		job := maintenanceFaultWaitJob(t, a, started)
		if job.Status != "完成" {
			t.Fatal("HTTP generation", job)
		}
		w := accountIdentityRequest(a, "GET", "/api/archive-companies?month=2026-10", nil, adminCookie)
		if w.Code != 200 {
			t.Fatal("summary route", w.Code, w.Body.String())
		}
		var result archiveCompanyResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := generate()
	if result.Total != 2 || len(result.Rows) != 2 {
		t.Fatal("same names merged", result)
	}
	var row archiveCompanyRecord
	for _, r := range result.Rows {
		if r.CompanyID == "group-a" {
			row = r
		} else {
			if len(r.Files) != 4 || r.OrderCount == nil || *r.OrderCount != 1 || r.Construction == nil || *r.Construction != 0 {
				t.Fatal("zero real record dropped", r)
			}
		}
	}
	if len(row.Files) != 7 || row.OrderCount == nil || *row.OrderCount != 1 || *row.Construction != 11500 || *row.Receivable != 11500 || *row.Received != 2000 || *row.Outstanding != 9500 || *row.Wage != 3600 {
		t.Fatal("snapshot totals duplicated or stale", row)
	}
	before := maintenanceFaultFiles(t, dir)
	for _, file := range row.Files {
		if file.Count == 0 || file.CompanyID != "group-a" || file.Kind == "财务应收" || file.Kind == "旧单据应收" {
			t.Fatal("empty report or ID omitted", file)
		}
	}
	zipPath := "/api/archive-company-download?run_id=" + row.RunID + "&key=" + url.QueryEscape(row.Key)
	// Wage-only users still download their CSV without customer-price access,
	// but never the internal workbook containing customer money.
	wageOnly := httptest.NewRecorder()
	if err := a.get(wageOnly, httptest.NewRequest("GET", zipPath, nil), User{Role: "employee", Permissions: []string{"wages"}}); err != nil {
		t.Fatal("wage-only archive", err)
	}
	wageFiles := archiveZipContents(t, wageOnly.Body.Bytes())
	if len(wageFiles) != 1 || wageFiles["工资明细.csv"] == nil {
		t.Fatal("wage-only archive leaked customer-price reports", wageFiles)
	}
	maintenanceFaultError(t, a.get(httptest.NewRecorder(), httptest.NewRequest("GET", zipPath, nil), User{Role: "employee", Permissions: []string{"orders"}}), 403)
	for _, tc := range []struct {
		cookie    *http.Cookie
		count     int
		forbidden []string
	}{{adminCookie, 7, nil}, {ordersCookie, 3, []string{"工资明细", "内部", "票据应收", "收款流水"}}, {wagesCookie, 2, []string{"施工验收表", "施工明细", "售料", "票据应收", "收款流水"}}} {
		w := accountIdentityRequest(a, "GET", zipPath, nil, tc.cookie)
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
			t.Fatal("ZIP route", w.Code, w.Body.String())
		}
		content := archiveZipContents(t, w.Body.Bytes())
		if len(content) != tc.count {
			t.Fatal("permission ZIP set", len(content), tc.count)
		}
		for name, data := range content {
			for _, forbidden := range tc.forbidden {
				if strings.Contains(name, forbidden) {
					t.Fatal("unauthorized ZIP member", name)
				}
			}
			if strings.Contains(string(data), "GROUP-ZERO") {
				t.Fatal("cross company ZIP leak", name)
			}
			if name == "施工验收表.xlsx" {
				xlsx, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range xlsx.File {
					reader, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					part, err := io.ReadAll(reader)
					reader.Close()
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(part), "工资") || strings.Contains(string(part), "36.00") {
						t.Fatal("client workbook includes wage")
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		cookie                 *http.Cookie
		forbidden              []string
		orders, finance, wages bool
	}{{ordersCookie, []string{"wage_cents", "receivable_cents", "received_cents", "outstanding_cents"}, true, false, false}, {wagesCookie, []string{"construction_cents", "order_count", "receivable_cents"}, false, false, true}} {
		w := accountIdentityRequest(a, "GET", "/api/archive-companies?month=2026-10", nil, tc.cookie)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response archiveCompanyResponse
		json.Unmarshal(w.Body.Bytes(), &response)
		if response.CanOrders != tc.orders || response.CanFinance != tc.finance || response.CanWages != tc.wages {
			t.Fatal("capabilities", response)
		}
		for _, field := range tc.forbidden {
			if strings.Contains(w.Body.String(), `"`+field+`"`) {
				t.Fatal("summary permission field leak", field)
			}
		}
	}
	maintenanceFaultExec(t, a.db, `UPDATE project_ticket_receipts SET amount_cents=3000 WHERE id='group-receipt'`)
	maintenanceFaultExec(t, a.db, `UPDATE companies SET name='后来更名' WHERE id='group-a'`)
	old, _ := archiveCompanyReadTest(t, a, User{Role: "admin"}, "company_id=group-a")
	if *old.Rows[0].Received != 2000 || old.Rows[0].Company != "合成同名公司" {
		t.Fatal("archive read live mutable state", old)
	}
	second := generate()
	if second.Total != 2 {
		t.Fatal("latest batches accumulated", second.Total)
	}
	for file, hash := range before {
		if after := maintenanceFaultFiles(t, dir)[file]; after != hash {
			t.Fatal("old file changed", file)
		}
	}
	history, _ := archiveCompanyReadTest(t, a, User{Role: "admin"}, "history=1&month=2026-10")
	if history.Total != 4 {
		t.Fatal("old batch lost", history.Total)
	}
	firstMissing := filepath.Join(dir, "monthly", row.Files[0].File)
	if err := os.Remove(firstMissing); err != nil {
		t.Fatal(err)
	}
	w := accountIdentityRequest(a, "GET", zipPath, nil, adminCookie)
	if w.Code != 404 || w.Header().Get("Content-Type") == "application/zip" {
		t.Fatal("partial successful ZIP", w.Code, w.Body.String())
	}
	list, _ := archiveCompanyReadTest(t, a, User{Role: "admin"}, "history=1&company_id=group-a")
	unavailable := false
	for _, r := range list.Rows {
		if r.RunID == row.RunID {
			for _, f := range r.Files {
				unavailable = unavailable || !f.Available
			}
		}
	}
	if !unavailable {
		t.Fatal("missing file hidden")
	}
	if after := maintenanceFaultFiles(t, dir); len(after) <= len(before) {
		t.Fatal("expected new version files retained")
	}
}

func TestArchiveFileSafetyAndLegacyLabels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	run := id()
	runDir := filepath.Join(dir, "monthly", run)
	if err := os.MkdirAll(runDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{run + "/../secret.csv", run + "/..", run + "/a\\b.csv", run + "/C:secret.csv", id() + "/other.csv"} {
		if f, err := openArchiveFile(run, bad); err == nil {
			f.Close()
			t.Fatal("unsafe path accepted", bad)
		}
	}
	name := id() + ".csv"
	data := []byte("\ufeff公司,施工/送货记录号,实际日期,工地,销售材料,销售数量,单位,销售单价元,货款元\r\n")
	if err := os.WriteFile(filepath.Join(runDir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	f := ArchiveFile{File: run + "/" + name, Company: "旧公司", Kind: "施工明细"}
	shown := readableArchiveFile(run, f)
	if shown.Kind != "售料明细" || !shown.Available {
		t.Fatal("legacy CSV header classification", shown)
	}
	after, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil || !reflect.DeepEqual(data, after) {
		t.Fatal("legacy bytes changed", err)
	}
	if canReadArchive(User{Role: "admin"}, "未知表格") {
		t.Fatal("unknown kind allowed")
	}
	if err := os.Mkdir(filepath.Join(runDir, "directory.csv"), 0700); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultError(t, func() error { _, e := openArchiveFile(run, run+"/directory.csv"); return e }(), 422)
	outside := filepath.Join(t.TempDir(), "outside.csv")
	os.WriteFile(outside, []byte("outside"), 0600)
	if err := os.Symlink(outside, filepath.Join(runDir, "link.csv")); err != nil {
		if runtime.GOOS == "windows" {
			// Windows permits a directory junction without the symlink privilege.
			// Keep both link and target inside this test's owned temporary root.
			target := filepath.Join(dir, "link-target")
			if e := os.Mkdir(target, 0700); e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(target, "file.csv"), []byte("junction"), 0600)
			linkRun := id()
			linkPath := filepath.Join(dir, "monthly", linkRun)
			if output, e := exec.Command("cmd.exe", "/c", "mklink", "/J", linkPath, target).CombinedOutput(); e != nil {
				t.Fatalf("junction creation failed: %v %s", e, output)
			}
			defer os.Remove(linkPath)
			maintenanceFaultError(t, func() error { _, e := openArchiveFile(linkRun, linkRun+"/file.csv"); return e }(), 422)
			t.Log("Windows directory junction rejected; leaf symlink creation unavailable to this account")
		} else {
			t.Fatalf("symlink fixture unavailable: %v", err)
		}
	} else {
		maintenanceFaultError(t, func() error { _, e := openArchiveFile(run, run+"/link.csv"); return e }(), 422)
	}
}

func TestArchiveCompanySameSecondAndPermissionMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	received, outstanding := int64(20), int64(80)
	for _, tc := range []struct {
		run, at string
		amount  int64
	}{{strings.Repeat("f", 32), "2026-10-08T10:00:00.100000000Z", 1}, {strings.Repeat("1", 32), "2026-10-08T10:00:00.900000000Z", 100}} {
		runDir := filepath.Join(dir, "monthly", tc.run)
		if err := os.MkdirAll(runDir, 0700); err != nil {
			t.Fatal(err)
		}
		files := []ArchiveFile{}
		for _, kind := range []string{"旧单据应收", "票据应收", "收款流水", "收款流水", "未知表格"} {
			name := id() + ".csv"
			data := []byte("公司,金额\r\n原始公司,1.00\r\n")
			os.WriteFile(filepath.Join(runDir, name), data, 0600)
			f := ArchiveFile{File: tc.run + "/" + name, CompanyID: "stable", Company: "原始公司", Kind: kind, Count: 1, Amount: tc.amount, GeneratedAt: tc.at}
			if kind == "旧单据应收" || kind == "票据应收" {
				f.Received, f.Outstanding = &received, &outstanding
			}
			files = append(files, f)
		}
		if _, err := a.db.Exec("INSERT INTO archive_runs(id,month,automatic,manifest,created_at) VALUES(?,'2026-10',0,?,'2026-10-08 10:00:00')", tc.run, jsonText(files)); err != nil {
			t.Fatal(err)
		}
	}
	finance := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	result, raw := archiveCompanyReadTest(t, a, finance, "")
	if result.Total != 1 || result.Rows[0].RunID != strings.Repeat("1", 32) || !result.CanFinance || result.CanOrders || result.CanWages {
		t.Fatal("same second generation or capabilities", result)
	}
	row := result.Rows[0]
	if *row.Receivable != 200 || *row.Received != 40 || *row.Outstanding != 160 || len(row.Files) != 4 {
		t.Fatal("legacy/new finance or unknown kind", row)
	}
	for _, field := range []string{"wage_cents", "order_count", "construction_cents", "未知表格"} {
		if strings.Contains(raw, field) {
			t.Fatal("finance permission leak", field)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/archive-company-download?run_id="+row.RunID+"&key="+url.QueryEscape(row.Key), nil)
	if err := a.get(w, req, finance); err != nil {
		t.Fatal(err)
	}
	contents := archiveZipContents(t, w.Body.Bytes())
	if len(contents) != 4 || contents["收款流水.csv"] == nil || contents["收款流水-2.csv"] == nil {
		t.Fatal("ZIP repeated kind overwrites", contents)
	}
	for _, query := range []string{"run_id=bad&key=c:stable", "run_id=" + row.RunID + "&key=c:other"} {
		err := a.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/archive-company-download?"+query, nil), finance)
		if err == nil {
			t.Fatal("other company or invalid run accepted")
		}
	}
	maintenanceFaultError(t, a.get(httptest.NewRecorder(), httptest.NewRequest("GET", req.URL.String(), nil), User{Role: "employee", Permissions: []string{"orders", "customer_prices"}}), 404)
	// Corrupting an authorized leaf in the manifest must fail before ZIP headers.
	var manifest []ArchiveFile
	var saved string
	a.db.QueryRow("SELECT manifest FROM archive_runs WHERE id=?", row.RunID).Scan(&saved)
	json.Unmarshal([]byte(saved), &manifest)
	manifest[0].File = row.RunID + "/../outside.csv"
	if _, err := a.db.Exec("UPDATE archive_runs SET manifest=? WHERE id=?", jsonText(manifest), row.RunID); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	maintenanceFaultError(t, a.get(w, req, finance), 400)
	if w.Header().Get("Content-Type") == "application/zip" || w.Body.Len() != 0 {
		t.Fatal("path failure sent partial archive")
	}
}

func TestArchiveCompanyLatestDoesNotFallBackToOlderAuthorizedFilesMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	for _, entry := range []struct{ kind, at string }{{"工资明细", "2026-10-08 09:00:00"}, {"票据应收", "2026-10-09 09:00:00"}} {
		run := id()
		file := ArchiveFile{File: run + "/" + id() + ".csv", CompanyID: "same-company", Company: "同一公司", Kind: entry.kind, Count: 1, Amount: 100}
		if _, err := a.db.Exec("INSERT INTO archive_runs(id,month,automatic,manifest,created_at) VALUES(?,'2026-10',0,?,?)", run, jsonText([]ArchiveFile{file}), entry.at); err != nil {
			t.Fatal(err)
		}
	}
	wages := User{Role: "employee", Permissions: []string{"wages"}}
	latest, _ := archiveCompanyReadTest(t, a, wages, "")
	if latest.Total != 0 || len(latest.Rows) != 0 {
		t.Fatal("latest view silently selected an older wage snapshot", latest)
	}
	history, _ := archiveCompanyReadTest(t, a, wages, "history=1")
	if history.Total != 1 || len(history.Rows) != 1 || history.Rows[0].Files[0].Kind != "工资明细" {
		t.Fatal("authorized wage history lost", history)
	}
	finance, _ := archiveCompanyReadTest(t, a, User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}, "")
	if finance.Total != 1 || finance.Rows[0].Files[0].Kind != "票据应收" {
		t.Fatal("latest authorized finance snapshot lost", finance)
	}
}
