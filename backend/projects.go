package main

import (
	"database/sql"
	"net/http"
	"strings"
)

type Project struct {
	ID      string `json:"id"`
	Company string `json:"company_id"`
	PackageID string `json:"package_id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Status  string `json:"status"`
	Action  string `json:"action"`
	Version int64  `json:"version"`
}

func (a *App) associateProject(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("orders") && !u.can("customers") {
		return nil, fail(403, "无旧单项目关联权限")
	}
	var p struct {
		OrderID   string `json:"order_id"`
		ProjectID string `json:"project_id"`
		Version   int64  `json:"version"`
	}
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	if p.OrderID == "" || p.ProjectID == "" {
		return nil, fail(400, "请选择施工单和项目")
	}
	if p.Version < 1 {
		return nil, fail(400, "请提供施工单版本，刷新后重试")
	}
	// Keep the same project-then-order lock order as saveOrder.
	var projectCompany, projectName, projectNumber string
	if e := tx.QueryRow("SELECT company_id,name,number FROM projects WHERE id=? FOR SHARE", p.ProjectID).Scan(&projectCompany, &projectName, &projectNumber); e != nil {
		if e == sql.ErrNoRows {
			return nil, fail(404, "工地项目不存在")
		}
		return nil, e
	}
	var orderCompany, previousProject, previousProjectName, previousProjectNumber string
	var version int64
	if e := tx.QueryRow("SELECT company_id,project_id,project_name,project_number,version FROM orders WHERE id=? FOR UPDATE", p.OrderID).Scan(&orderCompany, &previousProject, &previousProjectName, &previousProjectNumber, &version); e != nil {
		if e == sql.ErrNoRows {
			return nil, fail(404, "施工单不存在")
		}
		return nil, e
	}
	if projectCompany != orderCompany {
		return nil, fail(400, "项目与施工单公司不匹配")
	}
	var grantErr error
	u, grantErr = withCompanyCraftSources(tx, u, orderCompany)
	if grantErr != nil {
		return nil, grantErr
	}
	if u.Role == "employee" {
		if !u.can("orders") || len(u.Crafts) == 0 {
			return nil, fail(403, "无该施工单的完整工艺权限")
		}
		lines, e := tx.Query("SELECT craft_id,temporary FROM order_lines WHERE order_id=?", p.OrderID)
		if e != nil {
			return nil, e
		}
		seen := false
		allowed := true
		for lines.Next() {
			var craftID string
			var temporary bool
			if e = lines.Scan(&craftID, &temporary); e != nil {
				lines.Close()
				return nil, e
			}
			seen = true
			if (!temporary && !u.canFillCompanyCraft(craftID)) || (temporary && !u.can("orders_temporary")) {
				allowed = false
			}
		}
		e = lines.Err()
		lines.Close()
		if e != nil {
			return nil, e
		}
		if !seen || !allowed {
			return nil, fail(403, "无该施工单的完整工艺权限")
		}
	}
	if version != p.Version {
		return nil, fail(409, "施工单版本已变化，请刷新后重试")
	}
	if previousProject != "" {
		return nil, fail(409, "施工单已关联项目，不能再次关联")
	}
	result, e := tx.Exec("UPDATE orders SET project_id=?,project_name=?,project_number=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND project_id='' AND version=?", p.ProjectID, projectName, projectNumber, p.OrderID, p.Version)
	if e != nil {
		return nil, e
	}
	changed, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if changed != 1 {
		return nil, fail(409, "施工单版本已变化，请刷新后重试")
	}
	if e := audit(tx, u, "关联旧施工单到项目", p.OrderID, map[string]any{
		"before": map[string]any{"project_id": previousProject, "project_name": previousProjectName, "project_number": previousProjectNumber, "version": version},
		"after":  map[string]any{"project_id": p.ProjectID, "project_name": projectName, "project_number": projectNumber, "version": version + 1},
	}); e != nil {
		return nil, e
	}
	return map[string]any{"ok": true, "version": version + 1}, nil
}

func (a *App) saveProject(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("orders") && !u.can("customers") {
		return nil, fail(403, "无工地项目维护权限")
	}
	var p Project
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	p.Company = strings.TrimSpace(p.Company)
	p.Name = strings.TrimSpace(p.Name)
	p.Address = strings.TrimSpace(p.Address)
	if p.Company == "" || p.Name == "" || p.Address == "" || len([]rune(p.Name)) > 200 || len([]rune(p.Address)) > 300 {
		return nil, fail(400, "请选择公司并填写有效的项目名称和工地地址")
	}
	if p.ID != "" {
		if p.PackageID != "" {
			return nil, fail(400, "已建工地不能更改套餐；请在首次施工建档时选择")
		}
		if p.Version < 1 {
			return nil, fail(400, "请提供项目版本，刷新后重试")
		}
		if e := lockProjectCompany(tx, p.Company); e != nil {
			return nil, e
		}
		var oldCompany, oldName, oldAddress, oldStatus string
		var oldVersion int64
		if e := tx.QueryRow("SELECT company_id,name,address,status,version FROM projects WHERE id=? FOR UPDATE", p.ID).Scan(&oldCompany, &oldName, &oldAddress, &oldStatus, &oldVersion); e != nil {
			if e == sql.ErrNoRows {
				return nil, fail(404, "工地项目不存在")
			}
			return nil, e
		}
		if oldCompany != p.Company {
			return nil, fail(400, "项目所属公司不可更改")
		}
		if oldVersion != p.Version {
			return nil, fail(409, "项目版本已变化，请刷新后重试")
		}
		if oldAddress != p.Address {
			if e := checkProjectAddress(tx, p.Company, p.Address, p.ID); e != nil {
				return nil, e
			}
		}
		status := oldStatus
		switch p.Action {
		case "":
		case "disable":
			if oldStatus != "进行中" {
				return nil, fail(409, "项目状态已变化，请刷新后重试")
			}
			status = "已停用"
		case "enable":
			if oldStatus == "进行中" {
				return nil, fail(409, "项目已启用，请刷新后重试")
			}
			status = "进行中"
		case "close":
			if oldStatus == "已结项" {
				return nil, fail(409, "项目已结项，请刷新后重试")
			}
			status = "已结项"
		default:
			return nil, fail(400, "项目操作无效")
		}
		if _, e := tx.Exec("UPDATE projects SET name=?,address=?,status=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", p.Name, p.Address, status, p.ID); e != nil {
			return nil, e
		}
		if e := audit(tx, u, "修改工地项目", p.ID, map[string]any{
			"action": p.Action,
			"before": map[string]any{"company_id": oldCompany, "name": oldName, "address": oldAddress, "status": oldStatus, "version": oldVersion},
			"after":  map[string]any{"company_id": p.Company, "name": p.Name, "address": p.Address, "status": status, "version": oldVersion + 1},
		}); e != nil {
			return nil, e
		}
		return map[string]any{"id": p.ID, "status": status, "version": oldVersion + 1}, nil
	}
	if p.Action != "" || (p.Status != "" && p.Status != "进行中") || p.Version != 0 {
		return nil, fail(400, "新项目只能以进行中状态创建")
	}
	created, e := createProject(tx, u, p)
	if e != nil {
		return nil, e
	}
	return map[string]any{"id": created.ID, "number": created.Number, "status": "进行中", "version": int64(1)}, nil
}

func (a *App) listProjects(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	if q.Get("unassigned") == "1" {
		return a.listUnassignedProjectOrders(w, r, u)
	}
	if q.Get("unassigned") != "" {
		return fail(400, "未关联施工单筛选无效")
	}
	if !u.can("orders") && !u.can("customers") && !u.can("finance") {
		return fail(403, "无工地项目查看权限")
	}
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	where, args := "1=1", []any{}
	if c := q.Get("company_id"); c != "" {
		where += " AND p.company_id=?"
		args = append(args, c)
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		if len([]rune(s)) > 300 {
			return fail(400, "搜索内容过长")
		}
		where += " AND (INSTR(p.name,?)>0 OR INSTR(p.address,?)>0 OR INSTR(p.number,?)>0)"
		args = append(args, s, s, s)
	}
	if status := q.Get("status"); status != "" {
		if !contains([]string{"进行中", "已停用", "已结项"}, status) {
			return fail(400, "项目状态筛选无效")
		}
		where += " AND p.status=?"
		args = append(args, status)
	}
	if pid := q.Get("id"); pid != "" {
		where += " AND p.id=?"
		args = append(args, pid)
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int64
	if e = tx.QueryRow("SELECT COUNT(*) FROM projects p WHERE "+where, args...).Scan(&count); e != nil {
		return e
	}
	if q.Get("id") != "" && count == 0 {
		return fail(404, "工地项目不存在")
	}
	// Aggregate each order once. Shared expressions count legacy paid orders in full and cancel neither amounts nor receipts.
	query := "SELECT p.id,p.number,p.company_id,p.name,p.address,p.status,p.version,p.created_at,c.name AS company_name,COUNT(o.id) AS order_count," +
		"CAST(COALESCE(SUM(CASE WHEN o.status<>'已取消' THEN o.total_cents ELSE 0 END),0) AS SIGNED) AS total_cents," +
		"CAST(COALESCE(SUM(" + orderReceivedCentsSQL + "),0) AS SIGNED) AS received_cents," +
		"CAST(COALESCE(SUM(CASE WHEN o.status IN ('进行中','待交单') THEN " + orderOutstandingCentsSQL + " ELSE 0 END),0) AS SIGNED) AS undelivered_cents," +
		"CAST(COALESCE(SUM(CASE WHEN o.status IN ('已交单','部分收款') THEN " + orderOutstandingCentsSQL + " ELSE 0 END),0) AS SIGNED) AS delivered_outstanding_cents," +
		"CAST(COALESCE(SUM(" + orderOutstandingCentsSQL + "),0) AS SIGNED) AS outstanding_cents " +
		"FROM (SELECT * FROM projects p WHERE " + where + " ORDER BY p.created_at DESC,p.id DESC LIMIT 100 OFFSET ?) p " +
		"JOIN companies c ON c.id=p.company_id LEFT JOIN orders o ON o.project_id=p.id LEFT JOIN (SELECT order_id,SUM(amount_cents) received_cents FROM payment_receipts WHERE voided=0 GROUP BY order_id) r ON r.order_id=o.id " +
		"GROUP BY p.id,p.number,p.company_id,p.name,p.address,p.status,p.version,p.created_at,c.name ORDER BY p.created_at DESC,p.id DESC"
	projectOffset := (page - 1) * 100
	if q.Get("id") != "" {
		projectOffset = 0
	}
	items, e := scanProjectRecords(tx, query, append(args, projectOffset), u)
	if e != nil {
		return e
	}
	if e = addProjectTicketBalances(tx, items); e != nil {
		return e
	}
	if q.Get("id") != "" {
		if len(items) == 0 {
			return fail(404, "工地项目所属公司不存在")
		}
		orderQuery := "SELECT o.id,o.number,o.work_date,o.address,o.status,o.total_cents," +
			orderReceivedCentsSQL + " AS received_cents," + orderOutstandingCentsSQL + " AS outstanding_cents," +
			"o.company_id,o.company_name,o.worker_id,o.worker_name," + orderWorkerNamesSQL + " FROM orders o " +
			"LEFT JOIN (SELECT order_id,SUM(amount_cents) received_cents FROM payment_receipts WHERE voided=0 GROUP BY order_id) r ON r.order_id=o.id " +
			"WHERE o.project_id=? ORDER BY o.work_date DESC,o.id DESC LIMIT 100 OFFSET ?"
		orders, err := scanProjectOrders(tx, orderQuery, []any{q.Get("id"), (page - 1) * 100}, u)
		if err != nil {
			return err
		}
		write(w, 200, projectDetailPage{Project: items[0], Orders: orders, Page: page})
		return nil
	}
	write(w, 200, queryPageResult[projectRecord]{Rows: items, Total: count, Page: page})
	return nil
}

func (a *App) listUnassignedProjectOrders(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("orders") && !u.can("customers") {
		return fail(403, "无旧单项目关联权限")
	}
	q := r.URL.Query()
	companyID := strings.TrimSpace(q.Get("company_id"))
	if companyID == "" {
		return fail(400, "请先选择公司")
	}
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	where := "o.project_id='' AND o.company_id=?"
	args := []any{companyID}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		if len([]rune(search)) > 200 {
			return fail(400, "搜索内容过长")
		}
		where += " AND (INSTR(o.number,?)>0 OR INSTR(o.address,?)>0 OR INSTR(CAST(o.work_date AS CHAR),?)>0)"
		args = append(args, search, search, search)
	}
	if u.Role == "employee" {
		if !u.can("orders") || len(u.Crafts) == 0 {
			where += " AND 1=0"
		} else {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(u.Crafts)), ",")
			where += " AND EXISTS (SELECT 1 FROM order_lines visible WHERE visible.order_id=o.id)" +
				" AND NOT EXISTS (SELECT 1 FROM order_lines hidden LEFT JOIN crafts fixed ON fixed.id=hidden.craft_id AND fixed.company_id=o.company_id LEFT JOIN crafts public ON public.id=fixed.source_id AND public.company_id='' WHERE hidden.order_id=o.id AND (hidden.temporary=1 OR public.id IS NULL OR public.id NOT IN (" + placeholders + ")))"
			for _, craft := range u.Crafts {
				args = append(args, craft)
			}
		}
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int64
	if e := tx.QueryRow("SELECT COUNT(*) FROM orders o WHERE "+where, args...).Scan(&count); e != nil {
		return e
	}
	items, e := scanUnassignedProjectOrders(tx, "SELECT o.id,o.number,o.work_date,o.address,o.status,o.total_cents,o.version FROM orders o WHERE "+where+" ORDER BY o.work_date DESC,o.id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100), u)
	if e != nil {
		return e
	}
	write(w, 200, queryPageResult[unassignedProjectOrderRecord]{Rows: items, Total: count, Page: page})
	return nil
}
