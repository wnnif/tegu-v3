package main

import (
	"net/http"
	"strings"
)

type catalogReferences struct {
	Companies  []companiesRecord  `json:"companies"`
	Workers    []workersRecord    `json:"workers"`
	Warehouses []warehousesRecord `json:"warehouses"`
	Products   []productsRecord   `json:"products"`
	Crafts     []craftsRecord     `json:"crafts"`
}

func (a *App) reference(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	out := catalogReferences{Companies: []companiesRecord{}, Workers: []workersRecord{}, Warehouses: []warehousesRecord{}, Products: []productsRecord{}, Crafts: []craftsRecord{}}
	requested := map[string]bool{}
	if names := q.Get("tables"); names != "" {
		for _, name := range strings.Split(names, ",") {
			if !contains([]string{"companies", "workers", "warehouses", "products", "crafts"}, name) {
				return fail(400, "基础资料类别无效")
			}
			requested[name] = true
		}
	}
	for _, table := range []string{"companies", "workers", "warehouses", "products", "crafts"} {
		if len(requested) > 0 && !requested[table] {
			continue
		}
		permitted := map[string]bool{
			"companies":  u.can("customers") || u.can("orders") || u.can("finance") || u.can("wages") || u.can("warehouse"),
			"workers":    u.can("workers") || u.can("wages") || u.can("orders") || u.can("warehouse") || u.can("finance"),
			"warehouses": u.can("warehouse") || u.can("orders"),
			"products":   u.can("warehouse") || u.can("orders"),
			"crafts":     u.can("rates") || u.can("customers") || u.can("orders") || u.can("users"),
		}[table]
		if !permitted {
			continue
		}
		query := "SELECT * FROM " + table
		args := []any{}
		if table == "crafts" && q.Get("craft_scope") == "company" {
			query += " WHERE (company_id='' OR company_id=?)"
			args = append(args, q.Get("company_id"))
		} else if table == "crafts" && q.Get("craft_scope") == "order" {
			query += " WHERE company_id=?"
			args = append(args, q.Get("company_id"))
		}
		source := table
		if table == "warehouses" {
			source = "(SELECT w.*,COALESCE(x.name,'') AS worker_name FROM warehouses w LEFT JOIN workers x ON x.id=w.worker_id) catalog_warehouses"
		}
		suffix := strings.TrimPrefix(query, "SELECT * FROM "+table) + " ORDER BY id"
		if table == "crafts" {
			suffix = strings.TrimPrefix(query, "SELECT * FROM "+table) + " ORDER BY " + craftCatalogOrder
		}
		res, e := readCatalog(a.db, table, source, suffix, args, u)
		if e != nil {
			return e
		}
		switch table {
		case "companies":
			out.Companies = res.([]companiesRecord)
		case "workers":
			out.Workers = res.([]workersRecord)
		case "warehouses":
			out.Warehouses = res.([]warehousesRecord)
		case "products":
			out.Products = res.([]productsRecord)
		case "crafts":
			out.Crafts = res.([]craftsRecord)
			if projectID := q.Get("project_id"); projectID != "" && q.Get("craft_scope") == "order" {
				if e = applyProjectCraftConfiguration(a.db, projectID, q.Get("company_id"), out.Crafts); e != nil {
					return e
				}
			}
		}
	}
	write(w, 200, out)
	return nil

}
