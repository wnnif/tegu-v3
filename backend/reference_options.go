package main

import (
	"database/sql"
	"net/http"
	"strings"
)

// referenceOptions serves form selectors without loading entire catalog tables.
func (a *App) referenceOptions(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	table := q.Get("entity")
	if !contains([]string{"companies", "workers", "warehouses", "products", "crafts"}, table) {
		return fail(400, "基础资料类别无效")
	}
	permitted := map[string]bool{
		"companies":  u.can("customers") || u.can("orders") || u.can("finance") || u.can("wages") || u.can("warehouse"),
		"workers":    u.can("workers") || u.can("wages") || u.can("orders") || u.can("warehouse") || u.can("finance"),
		"warehouses": u.can("warehouse") || u.can("orders"),
		"products":   u.can("warehouse") || u.can("orders"),
		"crafts":     u.can("rates") || u.can("customers") || u.can("orders") || u.can("users"),
	}[table]
	if !permitted {
		return fail(403, "无此资料查看权限")
	}
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	where, args := "1=1", []any{}
	if recordID := q.Get("id"); recordID != "" {
		if len(recordID) > 64 {
			return fail(400, "资料编号无效")
		}
		where += " AND id=?"
		args = append(args, recordID)
	}
	if query := strings.TrimSpace(q.Get("q")); query != "" {
		if len([]rune(query)) > 200 {
			return fail(400, "搜索内容过长")
		}
		fields := map[string][]string{
			"companies":  {"name", "customer_type"},
			"workers":    {"name"},
			"warehouses": {"name", "category", "worker_name"},
			"products":   {"name", "brand", "spec", "color"},
			"crafts":     {"name", "spec", "category"},
		}[table]
		if table == "companies" && u.can("customers") {
			fields = append(fields, "contact", "phone")
		}
		if table == "workers" && (u.can("workers") || u.can("wages")) {
			fields = append(fields, "phone")
		}
		parts := make([]string, 0, len(fields))
		for _, field := range fields {
			parts = append(parts, "INSTR("+field+",?)>0")
			args = append(args, query)
		}
		where += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	if active := q.Get("active"); active != "" {
		if !contains([]string{"0", "1"}, active) || table == "companies" || table == "workers" {
			return fail(400, "启用状态筛选无效")
		}
		where += " AND active=?"
		args = append(args, active)
	}
	if category := q.Get("category"); category != "" {
		if table != "crafts" || !contains(categories, category) {
			return fail(400, "工艺类别筛选无效")
		}
		where += " AND category=?"
		args = append(args, category)
	}
	if workerID := q.Get("worker_id"); workerID != "" {
		if table != "warehouses" || len(workerID) > 64 {
			return fail(400, "施工人筛选无效")
		}
		where += " AND worker_id=?"
		args = append(args, workerID)
	}
	if craft := q.Get("craft"); craft != "" {
		if table != "workers" || !contains(categories, craft) {
			return fail(400, "施工工艺筛选无效")
		}
		where += " AND JSON_CONTAINS(crafts,JSON_QUOTE(?))"
		args = append(args, craft)
	}
	if table == "crafts" {
		companyID := q.Get("company_id")
		if len(companyID) > 64 {
			return fail(400, "公司筛选无效")
		}
		if q.Get("craft_scope") == "order" {
			if companyID == "" {
				return fail(400, "请选择公司")
			}
			where += " AND company_id=?"
			args = append(args, companyID)
		} else if companyID == "" {
			where += " AND company_id=''"
		} else {
			where += " AND (company_id='' OR company_id=?)"
			args = append(args, companyID)
		}
	} else if q.Get("company_id") != "" {
		return fail(400, "公司筛选仅适用于工艺")
	}
	source := table
	if table == "crafts" {
		source = craftConfigurationSource
	}
	if table == "warehouses" {
		source = "(SELECT w.*,COALESCE(x.name,'') AS worker_name FROM warehouses w LEFT JOIN workers x ON x.id=w.worker_id) catalog_warehouses"
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*) FROM "+source+" WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	order := "name,id"
	if table == "crafts" {
		order = craftCatalogOrder
	}
	items, err := readCatalog(tx, table, source, " WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, catalogPageSize, (page-1)*catalogPageSize), u)
	if err != nil {
		return err
	}
	if projectID := q.Get("project_id"); projectID != "" && table == "crafts" && q.Get("craft_scope") == "order" {
		if err = applyProjectCraftConfiguration(tx, projectID, q.Get("company_id"), items.([]craftsRecord)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, catalogPage{items, total, page, catalogPageSize})
	return nil
}
