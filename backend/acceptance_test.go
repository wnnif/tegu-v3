package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func acceptanceAPI(t *testing.T, a *App, adminPassword string) {
	t.Helper()
	a.attempts = map[string][]time.Time{}
	if e := a.bootstrap(); e != nil {
		t.Fatal(e)
	}
	request := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		b := ""
		if body != nil {
			b = jsonText(body)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(b))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Idempotency-Key", id())
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.route(w, r)
		return w
	}
	if r := request("GET", "/api/users", nil, nil); r.Code != 401 {
		t.Fatal("unauthenticated access", r.Code)
	}
	r := request("POST", "/api/login", map[string]string{"username": "admin", "password": adminPassword}, nil)
	if r.Code != 200 {
		t.Fatal("login", r.Code, r.Body.String())
	}
	adminCookie := r.Result().Cookies()[0]
	r = request("POST", "/api/project", Project{Company: "c", Name: "账号权限测试项目", Address: "工艺隔离"}, adminCookie)
	if r.Code != 200 {
		t.Fatal("create test project", r.Body.String())
	}
	var projectResult map[string]any
	if e := json.Unmarshal(r.Body.Bytes(), &projectResult); e != nil {
		t.Fatal(e)
	}
	projectID := projectResult["id"].(string)
	account := map[string]any{"username": "employee", "name": "员工", "password": "a", "role": "employee", "permissions": []string{"orders"}, "crafts": []string{"public-r"}, "active": true}
	if r = request("POST", "/api/user", account, adminCookie); r.Code != 200 {
		t.Fatal("create employee", r.Body.String())
	}
	r = request("POST", "/api/login", map[string]string{"username": "employee", "password": "a"}, nil)
	if r.Code != 200 {
		t.Fatal("short password login", r.Body.String())
	}
	employeeCookie := r.Result().Cookies()[0]
	for _, p := range []string{"/api/users", "/api/backups", "/api/backup-settings"} {
		if r = request("GET", p, nil, employeeCookie); r.Code != 403 {
			t.Fatal("permission bypass", p, r.Code)
		}
	}
	invalid := Order{Worker: "w", Company: "c", Project: projectID, Date: "2026-09-19", Address: "越权", Lines: []Line{{Category: "回填", Name: "test", Temporary: true, Mode: "金额", Price: 100}}}
	if r = request("POST", "/api/order", invalid, employeeCookie); r.Code != 403 {
		t.Fatal("craft permission bypass", r.Code, r.Body.String())
	}
	initial := Order{Worker: "w", Company: "c", Project: projectID, Date: "2026-09-19", Address: "工艺隔离", Lines: []Line{{CraftID: "r", Segments: []Segment{{Length: 1, Width: 1}}}, {Category: "回填", Name: "回填", Temporary: true, Mode: "金额", Price: 200}}}
	r = request("POST", "/api/order", initial, adminCookie)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var result map[string]any
	json.Unmarshal(r.Body.Bytes(), &result)
	oid := result["id"].(string)
	r = request("GET", "/api/order?id="+oid, nil, employeeCookie)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var loaded Order
	if e := json.Unmarshal(r.Body.Bytes(), &loaded); e != nil {
		t.Fatal(e)
	}
	if len(loaded.Lines) != 2 || loaded.Lines[0].Category != "开工" {
		t.Fatal("full company craft visibility missing")
	}
	// A read model carries combinations, but an ordinary employee edit must
	// omit them; explicitly resubmitting even an empty list requires combo permission.
	if loaded.Combinations == nil {
		t.Fatal("read model must expose the saved combination state")
	}
	if r = request("POST", "/api/order", loaded, employeeCookie); r.Code != 403 {
		t.Fatal("employee without combo permission resubmitted combinations", r.Code, r.Body.String())
	}
	loaded.Combinations = nil
	loaded.Lines = loaded.Lines[:1]
	loaded.Lines[0].Segments[0].Length = 2
	if r = request("POST", "/api/order", loaded, employeeCookie); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	json.Unmarshal(r.Body.Bytes(), &result)
	loaded.Version = int64(result["version"].(float64))
	var total int64
	if e := a.db.QueryRow("SELECT total_cents FROM orders WHERE id=?", oid).Scan(&total); e != nil || total != 1400 {
		t.Fatal("hidden craft lost", total, e)
	}
	loaded.Delete = true
	if r = request("POST", "/api/order", loaded, employeeCookie); r.Code != 403 {
		t.Fatal("employee without delete permission cancelled order", r.Code, r.Body.String())
	}
	if r = request("POST", "/api/order", loaded, adminCookie); r.Code != 200 {
		t.Fatal("admin could not cancel order", r.Code, r.Body.String())
	}
	var uid string
	a.db.QueryRow("SELECT id FROM users WHERE username='employee'").Scan(&uid)
	account["id"] = uid
	account["version"] = 1
	account["role"] = "admin"
	if r = request("POST", "/api/user", account, adminCookie); r.Code != 403 {
		t.Fatal("second admin permitted", r.Body.String())
	}
	account["role"] = "employee"
	account["active"] = false
	if r = request("POST", "/api/user", account, adminCookie); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = request("GET", "/api/me", nil, employeeCookie); r.Code != 401 {
		t.Fatal("disabled session still active")
	}
	account["id"] = ""
	account["username"] = "operator"
	account["version"] = 0
	account["active"] = true
	account["role"] = "operator"
	if r = request("POST", "/api/user", account, adminCookie); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = request("POST", "/api/login", map[string]string{"username": "operator", "password": "a"}, nil)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	operatorCookie := r.Result().Cookies()[0]
	if r = request("GET", "/api/users", nil, operatorCookie); r.Code != 200 {
		t.Fatal("operator cannot manage employees")
	}
	if r = request("POST", "/api/maintenance/backup", map[string]any{}, operatorCookie); r.Code != 403 {
		t.Fatal("operator backup allowed")
	}
	forged := httptest.NewRequest("POST", "/api/logout", nil)
	forged.Header.Set("Origin", "https://other.example")
	forged.AddCookie(adminCookie)
	rw := httptest.NewRecorder()
	a.route(rw, forged)
	if rw.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	t.Log("HTTP login, short passwords, role and craft restrictions, protected craft preservation, account revocation, single admin and CSRF checks passed")

	start := time.Now()
	if _, e := a.db.Exec("INSERT INTO workers(id,name,crafts) VALUES('scale-worker','规模施工人',JSON_ARRAY('开工'))"); e != nil {
		t.Fatal(e)
	}
	for c := 0; c < 10; c++ {
		if _, e := a.db.Exec("INSERT INTO companies(id,name) VALUES(?,?)", fmt.Sprint("scale-company-", c), fmt.Sprint("规模公司", c)); e != nil {
			t.Fatal(e)
		}
	}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	const count = 109500 // 300 documents/day for a year; data stays only in the isolated test DB.
	for batch := 0; batch < count; batch += 500 {
		values := []string{}
		args := []any{}
		for n := batch; n < min(batch+500, count); n++ {
			values = append(values, "(?,?,?,?,?,'scale-worker','规模施工人',?,?,?)")
			key := fmt.Sprint("scale-", n)
			args = append(args, key, key, fmt.Sprint("scale-company-", n%10), "测试工地", base.AddDate(0, 0, n/300).Format("2006-01-02"), "", 10000, "test")
		}
		if _, e := a.db.Exec("INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,total_cents,created_by) VALUES"+strings.Join(values, ","), args...); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.db.Exec("INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary,worker_id,worker_name) SELECT id,id,'开工','规模测试','金额',JSON_ARRAY(),1000000,10000,0,10000,0,1,worker_id,worker_name FROM orders WHERE id LIKE 'scale-%'"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.db.Exec("INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version,legacy) SELECT CONCAT('scale-wage-',id),id,worker_id,worker_name,0,0,0,1,0 FROM orders WHERE id LIKE 'scale-%'"); e != nil {
		t.Fatal(e)
	}
	var accountCount int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM order_worker_wages WHERE order_id LIKE 'scale-%' AND worker_id='scale-worker' AND wage_cents=0 AND legacy=0").Scan(&accountCount); e != nil || accountCount != count {
		t.Fatal("scale wage fixture incomplete", accountCount, e)
	}
	t.Logf("seeded %d orders + %d assigned lines + %d nonlegacy wage accounts in %s", count, count, accountCount, time.Since(start).Round(time.Millisecond))
	var wg sync.WaitGroup
	errors := make(chan string, 160)
	timings := make(chan time.Duration, 160)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				s := time.Now()
				r := request("GET", "/api/orders?month=2025-01&company_id=scale-company-0&view=finance&page=1", nil, adminCookie)
				var result struct {
					Total      int   `json:"total"`
					TotalCents int64 `json:"total_cents"`
					Rows       []any `json:"rows"`
				}
				e := json.Unmarshal(r.Body.Bytes(), &result)
				if e != nil || r.Code != 200 || result.Total != 930 || result.TotalCents != 9300000 || len(result.Rows) != 100 {
					errors <- r.Body.String()
				}
				timings <- time.Since(s)
			}
		}()
	}
	wg.Wait()
	close(errors)
	close(timings)
	for e := range errors {
		t.Fatal("scale pagination mismatch", e)
	}
	var maxTime, sum time.Duration
	for d := range timings {
		sum += d
		if d > maxTime {
			maxTime = d
		}
	}
	t.Logf("8 concurrent readers / 160 company-month requests: mean %s, max %s; count930 and full-month totals verified", (sum / 160).Round(time.Millisecond), maxTime.Round(time.Millisecond))
	files, e := a.archive("2025-01", "scale-company-0", false)
	if e != nil || len(files) != 5 {
		t.Fatal("monthly archives", files, e)
	}
	for _, f := range files {
		if f.Count != 930 {
			t.Fatal("monthly export truncated", f)
		}
		if f.Kind == "施工验收表" || f.Kind == "施工内部留档表" {
			continue
		}
		contents, e := os.Open(filepath.Join(os.Getenv("BACKUP_DIR"), "monthly", filepath.FromSlash(f.File)))
		if e != nil {
			t.Fatal(e)
		}
		records, e := csv.NewReader(contents).ReadAll()
		contents.Close()
		if e != nil {
			t.Fatal(e)
		}
		expected := 932 // Header, 930 detail rows, and the current summary row.
		if len(records) != expected {
			t.Fatalf("CSV actual rows %s: %d", f.Kind, len(records))
		}
	}
	for _, view := range []string{"orders", "finance", "wages"} {
		r := request("GET", "/api/export?view="+view+"&month=2025-01&company_id=scale-company-0", nil, adminCookie)
		if r.Code != 200 {
			t.Fatal("filtered export", r.Code, r.Body.String())
		}
		records, e := csv.NewReader(strings.NewReader(r.Body.String())).ReadAll()
		if e != nil || len(records) != 931 {
			t.Fatal("filtered export truncation", view, len(records), e)
		}
	}
	t.Log("monthly construction, finance and wage CSV exports each contain full 930 matching records")
}
