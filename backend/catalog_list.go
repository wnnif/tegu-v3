package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

type catalogUserRow struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Crafts      []string `json:"crafts"`
	Active      int64    `json:"active"`
	Version     int64    `json:"version"`
}

type catalogUserPage struct {
	Rows     []catalogUserRow `json:"rows"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

const catalogPageSize = 50

func (a *App) listCatalog(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	table := q.Get("entity")
	permission := map[string]string{
		"companies": "customers", "workers": "workers", "products": "warehouse",
		"warehouses": "warehouse", "crafts": "rates",
	}[table]
	if permission == "" || !u.can(permission) {
		return fail(403, "无此资料查看权限")
	}
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	where, args := "1=1", []any{}
	if query := strings.TrimSpace(q.Get("q")); query != "" {
		if len([]rune(query)) > 200 {
			return fail(400, "搜索内容过长")
		}
		fields := map[string][]string{
			"companies":  {"name", "contact", "phone", "customer_type"},
			"workers":    {"name", "phone"},
			"products":   {"name", "brand", "spec", "color", "note"},
			"warehouses": {"name", "category", "note"},
			"crafts":     {"name", "spec", "category"},
		}[table]
		clauses := make([]string, 0, len(fields))
		for _, field := range fields {
			clauses = append(clauses, "INSTR("+field+",?)>0")
			args = append(args, query)
		}
		where += " AND (" + strings.Join(clauses, " OR ") + ")"
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
	if table == "crafts" {
		where += " AND company_id=''"
	}
	source := table
	if table == "crafts" {
		source = craftConfigurationSource
	}
	if table == "warehouses" {
		source = "(SELECT w.*,COALESCE(x.name,'') AS worker_name FROM warehouses w LEFT JOIN workers x ON x.id=w.worker_id) catalog_warehouses"
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var total int64
	if e = tx.QueryRow("SELECT COUNT(*) FROM "+source+" WHERE "+where, args...).Scan(&total); e != nil {
		return e
	}
	order := "name,id"
	if table == "crafts" {
		order = craftCatalogOrder
	}
	items, e := readCatalog(tx, table, source, " WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, catalogPageSize, (page-1)*catalogPageSize), u)
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	write(w, 200, catalogPage{items, total, page, catalogPageSize})
	return nil
}

func (a *App) listUsers(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("users") {
		return fail(403, "无账号管理权限")
	}
	q := r.URL.Query()
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	where, args := "deleted=0", []any{}
	if query := strings.TrimSpace(q.Get("q")); query != "" {
		if len([]rune(query)) > 100 {
			return fail(400, "搜索内容过长")
		}
		where += " AND (INSTR(username,?)>0 OR INSTR(name,?)>0)"
		args = append(args, query, query)
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var total int64
	if e = tx.QueryRow("SELECT COUNT(*) FROM users WHERE "+where, args...).Scan(&total); e != nil {
		return e
	}
	records, e := tx.Query("SELECT id,username,name,role,permissions,crafts,active,version FROM users WHERE "+where+" ORDER BY username,id LIMIT ? OFFSET ?", append(args, catalogPageSize, (page-1)*catalogPageSize)...)
	if e != nil {
		return e
	}
	items := []catalogUserRow{}
	for records.Next() {
		var item catalogUserRow
		var permissions, crafts []byte
		if e = records.Scan(&item.ID, &item.Username, &item.Name, &item.Role, &permissions, &crafts, &item.Active, &item.Version); e != nil {
			break
		}
		if e = json.Unmarshal(permissions, &item.Permissions); e != nil {
			break
		}
		if e = json.Unmarshal(crafts, &item.Crafts); e != nil {
			break
		}
		if item.Permissions == nil {
			item.Permissions = []string{}
		}
		if item.Crafts == nil {
			item.Crafts = []string{}
		}
		items = append(items, item)
	}
	if e == nil {
		e = records.Err()
	}
	records.Close()
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	write(w, 200, catalogUserPage{Rows: items, Total: total, Page: page, PageSize: catalogPageSize})
	return nil
}
