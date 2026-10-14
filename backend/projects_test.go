package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func projectAcceptance(t *testing.T, a *App) {
	t.Helper()
	u := User{ID: "project-test", Name: "项目测试", Role: "admin"}
	call := func(path string, input any) (any, error) {
		b := []byte(jsonText(input))
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		r.Header.Set("Idempotency-Key", id())
		return a.mutate(r, u, b, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, path, u, b, key) })
	}
	must := func(path string, input any) any {
		t.Helper()
		v, e := call(path, input)
		if e != nil {
			t.Fatal(path, e)
		}
		if path == "/api/order" {
			if o, ok := input.(Order); ok && o.ID == "" {
				legacyOrderFixture(t, a, v.(map[string]any)["id"].(string))
			}
		}
		return v
	}
	expectCode := func(path string, input any, code int) {
		t.Helper()
		_, e := call(path, input)
		f, ok := e.(failure)
		if !ok || f.Code != code {
			t.Fatalf("%s: expected %d, got %v", path, code, e)
		}
	}
	created := must("/api/project", Project{Company: "c", Name: "分期工地", Address: "测试工地"}).(map[string]any)
	projectID := created["id"].(string)
	if created["status"] != "进行中" || created["version"] != int64(1) {
		t.Fatalf("new project status/version: %+v", created)
	}
	if _, e := a.db.Exec("INSERT INTO companies(id,name) VALUES('project-other-company','另一家公司')"); e != nil {
		t.Fatal(e)
	}
	makeOrder := func(company string) Order {
		return Order{Company: company, Project: projectID, Address: "测试工地", Date: "2026-09-25", Worker: "w", Lines: []Line{{CraftID: "r", Category: "开工", Mode: "平方", Segments: []Segment{{Length: 2, Width: 3}}}}}
	}
	if _, e := call("/api/order", makeOrder("project-other-company")); e == nil {
		t.Fatal("cross-company project accepted")
	}
	first := must("/api/order", makeOrder("c")).(map[string]any)["id"].(string)
	for i, action := range []string{"submit", "deliver", "receive"} {
		payload := map[string]any{"action": action, "orders": []map[string]any{{"id": first, "version": i + 1}}}
		if action == "deliver" {
			payload["ticket_no"], payload["delivered_at"], payload["recipient"] = "PJ-001", "2026-09-25", "测试收票人"
		}
		must("/api/settle", payload)
	}
	second := must("/api/order", makeOrder("c")).(map[string]any)["id"].(string)
	third := must("/api/order", makeOrder("c")).(map[string]any)["id"].(string)
	thirdOrder := makeOrder("c")
	thirdOrder.ID, thirdOrder.Version, thirdOrder.Delete = third, 1, true
	must("/api/order", thirdOrder)
	// Historical inconsistency must never turn a cancelled order into a receipt.
	if _, e := a.db.Exec("INSERT INTO payment_receipts(id,order_id,amount_cents,received_at,created_by) VALUES(?,?,?,?,?)", id(), third, 500, "2026-09-25", u.ID); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRecorder()
	if e := a.listProjects(r, httptest.NewRequest("GET", "/api/projects?id="+projectID, nil), u); e != nil {
		t.Fatal(e)
	}
	var got struct {
		Project struct {
			OrderCount                int64  `json:"order_count"`
			TotalCents                int64  `json:"total_cents"`
			ReceivedCents             int64  `json:"received_cents"`
			UndeliveredCents          int64  `json:"undelivered_cents"`
			DeliveredOutstandingCents int64  `json:"delivered_outstanding_cents"`
			OutstandingCents          int64  `json:"outstanding_cents"`
			Status                    string `json:"status"`
			Version                   int64  `json:"version"`
		} `json:"project"`
		Orders []map[string]any `json:"orders"`
	}
	if e := json.Unmarshal(r.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if got.Project.OrderCount != 3 || got.Project.TotalCents != 7200 || got.Project.ReceivedCents != 3600 || got.Project.UndeliveredCents != 3600 || got.Project.DeliveredOutstandingCents != 0 || got.Project.OutstandingCents != 3600 || got.Project.Status != "进行中" || got.Project.Version != 1 || len(got.Orders) != 3 {
		t.Fatalf("project summary %+v", got.Project)
	}
	var status string
	if e := a.db.QueryRow("SELECT status FROM orders WHERE id=?", first).Scan(&status); e != nil || status != "已收款" {
		t.Fatal("first period changed after second order", status, e)
	}
	export := httptest.NewRecorder()
	financeUser := User{ID: "finance-test", Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	if e := a.exportFiltered(export, httptest.NewRequest("GET", "/api/export?view=project&project_id="+projectID, nil), financeUser); e != nil {
		t.Fatal("project export", e)
	}
	csvRows, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(export.Body.String(), "\ufeff"))).ReadAll()
	if e != nil || len(csvRows) != 4 || csvRows[0][0] != "项目编号" || csvRows[1][8] == "" {
		t.Fatalf("project export rows %d: %v", len(csvRows), e)
	}
	refresh := func() {
		t.Helper()
		w := httptest.NewRecorder()
		if e := a.listProjects(w, httptest.NewRequest("GET", "/api/projects?id="+projectID, nil), u); e != nil {
			t.Fatal(e)
		}
		if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
	}
	partial := must("/api/order", makeOrder("c")).(map[string]any)["id"].(string)
	for i, action := range []string{"submit", "deliver", "receive_partial"} {
		payload := map[string]any{"action": action, "orders": []map[string]any{{"id": partial, "version": i + 1}}}
		if action == "deliver" {
			payload["ticket_no"], payload["delivered_at"], payload["recipient"] = "PJ-002", "2026-09-25", "测试收票人"
		}
		if action == "receive_partial" {
			payload["amount_cents"], payload["received_at"], payload["method"] = 1000, "2026-09-26", "转账"
		}
		must("/api/settle", payload)
	}
	refresh()
	if got.Project.OrderCount != 4 || got.Project.TotalCents != 10800 || got.Project.ReceivedCents != 4600 || got.Project.UndeliveredCents != 3600 || got.Project.DeliveredOutstandingCents != 2600 || got.Project.OutstandingCents != 6200 {
		t.Fatalf("partial receipt project totals: %+v", got.Project)
	}
	// A paid legacy order with no receipt rows is still fully received.
	if _, e := a.db.Exec("UPDATE payment_receipts SET voided=1 WHERE order_id=?", first); e != nil {
		t.Fatal(e)
	}
	refresh()
	if got.Project.ReceivedCents != 4600 || got.Project.OutstandingCents != 6200 {
		t.Fatalf("legacy paid order totals: %+v", got.Project)
	}
	for _, row := range got.Orders {
		if row["id"] == first && (number(row, "received_cents") != 3600 || number(row, "outstanding_cents") != 0) {
			t.Fatalf("legacy paid order detail: %+v", row)
		}
		if row["id"] == third && (number(row, "received_cents") != 0 || number(row, "outstanding_cents") != 0) {
			t.Fatalf("cancelled order detail: %+v", row)
		}
	}

	// Simulate two old unassigned orders; one contains a category hidden from the employee.
	legacy := must("/api/order", makeOrder("c")).(map[string]any)["id"].(string)
	mixedOrder := makeOrder("c")
	mixedOrder.Address = "含其他工艺的旧单"
	mixedOrder.Lines = append(mixedOrder.Lines, Line{Category: "回填", Name: "回填", Temporary: true, Mode: "金额", Price: 100})
	mixed := must("/api/order", mixedOrder).(map[string]any)["id"].(string)
	if _, e := a.db.Exec("UPDATE orders SET project_id='' WHERE id IN (?,?)", legacy, mixed); e != nil {
		t.Fatal(e)
	}
	getUnassigned := func(actor User) (int64, []map[string]any, string, error) {
		w := httptest.NewRecorder()
		e := a.listProjects(w, httptest.NewRequest("GET", "/api/projects?unassigned=1&company_id=c&page=1", nil), actor)
		if e != nil {
			return 0, nil, "", e
		}
		var result struct {
			Rows  []map[string]any `json:"rows"`
			Total int64            `json:"total"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			return 0, nil, "", e
		}
		return result.Total, result.Rows, w.Body.String(), nil
	}
	if count, _, body, e := getUnassigned(u); e != nil || count != 2 || strings.Contains(body, "wage_cents") {
		t.Fatalf("admin unassigned list count/privacy: %d %v", count, e)
	}
	employee := User{ID: "project-employee", Role: "employee", Permissions: []string{"orders"}, Crafts: []string{"public-r"}}
	if count, rows, _, e := getUnassigned(employee); e != nil || count != 1 || len(rows) != 1 || rows[0]["id"] != legacy {
		t.Fatalf("employee craft visibility: %d %+v %v", count, rows, e)
	}
	if _, _, _, e := getUnassigned(financeUser); e == nil {
		t.Fatal("finance-only user listed unassigned orders")
	}
	if count, _, _, e := getUnassigned(User{ID: "project-no-crafts", Role: "employee", Permissions: []string{"customers"}}); e != nil || count != 0 {
		t.Fatalf("employee without crafts saw old orders: %d %v", count, e)
	}

	edit := Project{ID: projectID, Company: "c", Name: "分期工地更正", Address: "更正后的项目主地址", Version: 1}
	updated := must("/api/project", edit).(map[string]any)
	if updated["version"] != int64(2) || updated["status"] != "进行中" {
		t.Fatalf("project version after edit: %+v", updated)
	}
	expectCode("/api/project", edit, 409)
	wrongCompany := edit
	wrongCompany.Company, wrongCompany.Version = "project-other-company", 2
	expectCode("/api/project", wrongCompany, 400)
	invalidAction := edit
	invalidAction.Action, invalidAction.Version = "remove", 2
	expectCode("/api/project", invalidAction, 400)
	var originalAddress string
	if e := a.db.QueryRow("SELECT address FROM orders WHERE id=?", first).Scan(&originalAddress); e != nil || originalAddress != "测试工地" {
		t.Fatal("project correction altered historical order address", originalAddress, e)
	}
	var savedProjectName, savedProjectNumber string
	if e := a.db.QueryRow("SELECT project_name,project_number FROM orders WHERE id=?", first).Scan(&savedProjectName, &savedProjectNumber); e != nil || savedProjectName != "分期工地" || savedProjectNumber != created["number"] {
		t.Fatal("project correction altered historical project snapshot", savedProjectName, savedProjectNumber, e)
	}
	orderDetail := httptest.NewRecorder()
	if e := a.get(orderDetail, httptest.NewRequest("GET", "/api/order?id="+first, nil), u); e != nil || !strings.Contains(orderDetail.Body.String(), `"project_name":"分期工地"`) {
		t.Fatal("saved order detail lost historical project name", e)
	}
	edit.Version, edit.Action = 2, "disable"
	if result := must("/api/project", edit).(map[string]any); result["version"] != int64(3) || result["status"] != "已停用" {
		t.Fatalf("disable project: %+v", result)
	}
	expectCode("/api/order", makeOrder("c"), 400)
	secondEdit := makeOrder("c")
	secondEdit.ID, secondEdit.Version, secondEdit.Address = second, 1, "停用后更正原施工单"
	must("/api/order", secondEdit)
	filter := httptest.NewRecorder()
	if e := a.listProjects(filter, httptest.NewRequest("GET", "/api/projects?status="+url.QueryEscape("已停用"), nil), u); e != nil {
		t.Fatal(e)
	}
	var filtered struct {
		Total int64            `json:"total"`
		Rows  []map[string]any `json:"rows"`
	}
	if e := json.Unmarshal(filter.Body.Bytes(), &filtered); e != nil || filtered.Total != 1 || len(filtered.Rows) != 1 || filtered.Rows[0]["id"] != projectID {
		t.Fatalf("project status filter: %+v %v", filtered, e)
	}
	edit.Version, edit.Action = 3, "close"
	if result := must("/api/project", edit).(map[string]any); result["version"] != int64(4) || result["status"] != "已结项" {
		t.Fatalf("close project: %+v", result)
	}
	expectCode("/api/order", makeOrder("c"), 400)
	expectCode("/api/project-associate", map[string]any{"order_id": legacy, "project_id": projectID}, 400)
	expectCode("/api/project-associate", map[string]any{"order_id": legacy, "project_id": projectID, "version": 99}, 409)
	otherProject := must("/api/project", Project{Company: "project-other-company", Name: "其他公司项目", Address: "其他工地"}).(map[string]any)["id"].(string)
	expectCode("/api/project-associate", map[string]any{"order_id": legacy, "project_id": otherProject, "version": 1}, 400)
	limitedBody := []byte(jsonText(map[string]any{"order_id": mixed, "project_id": projectID, "version": 1}))
	limitedRequest := httptest.NewRequest("POST", "/api/project-associate", strings.NewReader(string(limitedBody)))
	limitedRequest.Header.Set("Idempotency-Key", id())
	if _, e := a.mutate(limitedRequest, employee, limitedBody, func(tx *sql.Tx, key string) (any, error) {
		return a.post(tx, "/api/project-associate", employee, limitedBody, key)
	}); e == nil {
		t.Fatal("employee associated order with hidden craft")
	} else if f, ok := e.(failure); !ok || f.Code != 403 {
		t.Fatalf("hidden craft association should be denied: %v", e)
	}
	assocBody := []byte(jsonText(map[string]any{"order_id": legacy, "project_id": projectID, "version": 1}))
	assocKey := "project-associate-" + legacy
	associate := func() (any, error) {
		req := httptest.NewRequest("POST", "/api/project-associate", strings.NewReader(string(assocBody)))
		req.Header.Set("Idempotency-Key", assocKey)
		return a.mutate(req, u, assocBody, func(tx *sql.Tx, key string) (any, error) {
			return a.post(tx, "/api/project-associate", u, assocBody, key)
		})
	}
	if _, e := associate(); e != nil {
		t.Fatal("associate legacy order with closed project", e)
	}
	if _, e := associate(); e != nil {
		t.Fatal("idempotent associate retry", e)
	}
	var associatedProject string
	var associatedVersion int64
	if e := a.db.QueryRow("SELECT project_id,version FROM orders WHERE id=?", legacy).Scan(&associatedProject, &associatedVersion); e != nil || associatedProject != projectID || associatedVersion != 2 {
		t.Fatalf("legacy association: %s %d %v", associatedProject, associatedVersion, e)
	}
	if e := a.db.QueryRow("SELECT project_name,project_number FROM orders WHERE id=?", legacy).Scan(&savedProjectName, &savedProjectNumber); e != nil || savedProjectName != "分期工地更正" || savedProjectNumber != created["number"] {
		t.Fatal("old-order association project snapshot", savedProjectName, savedProjectNumber, e)
	}
	var associationAudits int
	if e := a.db.QueryRow("SELECT COUNT(*) FROM audit WHERE action='关联旧施工单到项目' AND target_id=?", legacy).Scan(&associationAudits); e != nil || associationAudits != 1 {
		t.Fatalf("association audit count: %d %v", associationAudits, e)
	}
	expectCode("/api/project-associate", map[string]any{"order_id": legacy, "project_id": projectID, "version": 2}, 409)
	if count, _, _, e := getUnassigned(u); e != nil || count != 1 {
		t.Fatalf("associated order remained unassigned: %d %v", count, e)
	}
	edit.Version, edit.Action = 4, "enable"
	if result := must("/api/project", edit).(map[string]any); result["version"] != int64(5) || result["status"] != "进行中" {
		t.Fatalf("reopen closed project: %+v", result)
	}
	must("/api/order", makeOrder("c"))
	var auditDetail string
	if e := a.db.QueryRow("SELECT detail FROM audit WHERE action='修改工地项目' AND target_id=? ORDER BY id DESC LIMIT 1", projectID).Scan(&auditDetail); e != nil || !strings.Contains(auditDetail, `"before"`) || !strings.Contains(auditDetail, `"after"`) {
		t.Fatal("project before/after audit missing", e)
	}
	if _, e := call("/api/project", Project{Company: "c", Name: "", Address: ""}); e == nil {
		t.Fatal("empty project accepted")
	}
	if e := a.listProjects(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/projects", nil), User{Role: "employee"}); e == nil {
		t.Fatal("unauthorized project list accepted")
	}
	// First work establishes its project from the address. Explicit old
	// unassigned rows still remain editable without creating another project.
	standalone := makeOrder("c")
	standalone.Project = ""
	standalone.Address = "普通施工地址"
	standaloneID := must("/api/order", standalone).(map[string]any)["id"].(string)
	var automaticProject string
	if e := a.db.QueryRow("SELECT project_id FROM orders WHERE id=?", standaloneID).Scan(&automaticProject); e != nil || automaticProject == "" || automaticProject == projectID {
		t.Fatal("first address did not establish a separate project", automaticProject, e)
	}
	if _, e := a.db.Exec("UPDATE orders SET project_id='',project_name='',project_number='' WHERE id=?", standaloneID); e != nil {
		t.Fatal(e)
	}
	standalone.Project = ""
	var standaloneProject, standaloneName, standaloneNumber string
	if e := a.db.QueryRow("SELECT project_id,project_name,project_number FROM orders WHERE id=?", standaloneID).Scan(&standaloneProject, &standaloneName, &standaloneNumber); e != nil || standaloneProject != "" || standaloneName != "" || standaloneNumber != "" {
		t.Fatalf("standalone order project fields: %q %q %q %v", standaloneProject, standaloneName, standaloneNumber, e)
	}
	standalone.ID, standalone.Version, standalone.Address = standaloneID, 1, "普通施工地址更正"
	must("/api/order", standalone)
	standaloneDetail := httptest.NewRecorder()
	if e := a.get(standaloneDetail, httptest.NewRequest("GET", "/api/order?id="+standaloneID, nil), u); e != nil || !strings.Contains(standaloneDetail.Body.String(), `"project_id":""`) {
		t.Fatal("standalone order detail lost empty project", e)
	}
	must("/api/project-associate", map[string]any{"order_id": standaloneID, "project_id": projectID, "version": 2})
	_ = second
}
