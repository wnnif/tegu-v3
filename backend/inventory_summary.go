package main

import (
	"database/sql"
	"net/http"
	"net/url"
)

func validateInventoryGrouping(q url.Values) error {
	if !contains([]string{"", "product", "warehouse"}, q.Get("group_by")) {
		return fail(400, "库存显示方式无效")
	}
	return nil
}

// Aggregate balances before joining product metadata. This avoids the full
// warehouse/product cross join when zero-stock products need to be displayed.
// SUM uses DECIMAL and its wire value remains a string: several valid BIGINT
// warehouse balances can add up to more than one signed 64-bit integer.
func inventorySummaryQuery(q url.Values) (string, []any) {
	balances := "SELECT b.product_id,SUM(CAST(b.qty_micro AS DECIMAL(65,0))) AS qty_micro,SUM(CAST(b.qty_quantum AS DECIMAL(65,0))) AS qty_quantum,COUNT(CASE WHEN b.qty_quantum<>0 THEN 1 END) AS warehouse_count FROM stock_balances b JOIN warehouses w ON w.id=b.warehouse_id"
	args := []any{}
	if warehouseID := q.Get("warehouse_id"); warehouseID != "" {
		balances += " WHERE w.id=?"
		args = append(args, warehouseID)
	}
	balances += " GROUP BY b.product_id"
	from := " FROM products p JOIN product_unit_definitions d ON d.product_id=p.id LEFT JOIN product_purchase_costs cost ON cost.product_id=p.id LEFT JOIN (" + balances + ") s ON s.product_id=p.id WHERE 1=1"
	if warehouseID := q.Get("warehouse_id"); warehouseID != "" {
		from += " AND EXISTS(SELECT 1 FROM warehouses selected_warehouse WHERE selected_warehouse.id=?)"
		args = append(args, warehouseID)
	}
	if productID := q.Get("product_id"); productID != "" {
		from += " AND p.id=?"
		args = append(args, productID)
	}
	if q.Get("show_zero") != "1" {
		from += " AND COALESCE(s.qty_quantum,0)<>0"
	}
	if q.Get("state") == "active" {
		from += " AND p.active=1"
	} else if q.Get("state") == "inactive" {
		from += " AND p.active=0"
	}
	if query := q.Get("q"); query != "" {
		from += " AND (INSTR(p.name,?)>0 OR INSTR(p.brand,?)>0 OR INSTR(p.spec,?)>0)"
		args = append(args, query, query, query)
	}
	return from, args
}

const inventorySummaryQuantitySQL = "CAST(CAST(COALESCE(s.qty_quantum,0) AS DECIMAL(65,12))/(1000000*d.base_factor) AS DECIMAL(65,6))"
const inventorySummaryAuxiliarySQL = "CAST(CAST(COALESCE(s.qty_quantum,0) AS DECIMAL(65,12))/(1000000*d.aux_factor) AS DECIMAL(65,6))"
const inventorySummaryValueSQL = "CAST(CASE WHEN cost.purchased_quantum>0 THEN ROUND(CAST(COALESCE(s.qty_quantum,0) AS DECIMAL(65,0))*cost.purchase_cents/cost.purchased_quantum,0) ELSE 0 END AS DECIMAL(65,0))"
const inventorySummarySelect = "SELECT '',p.id,COALESCE(s.qty_micro,0),COALESCE(s.qty_quantum,0),0,p.name,p.brand,p.spec,p.unit,p.aux_unit,p.aux_ratio,p.color,p.weight_kg,p.active,''," + inventorySummaryQuantitySQL + "," + inventorySummaryAuxiliarySQL + "," + inventorySummaryValueSQL

func inventorySummaryWarehouseCountSQL(q url.Values) string {
	if q.Get("show_zero") != "1" {
		return "COALESCE(s.warehouse_count,0)"
	}
	if q.Get("warehouse_id") != "" {
		// The summary WHERE clause already excludes nonexistent warehouses.
		return "1"
	}
	return "(SELECT COUNT(*) FROM warehouses)"
}

type inventorySummaryRecord struct {
	WarehouseID       string `json:"warehouse_id"`
	ProductID         string `json:"product_id"`
	Qty               string `json:"qty_micro"`
	QuantityQuantum   string `json:"quantity_quantum"`
	Version           int64  `json:"version"`
	Name              string `json:"name"`
	Brand             string `json:"brand"`
	Spec              string `json:"spec"`
	Unit              string `json:"unit"`
	AuxUnit           string `json:"aux_unit"`
	AuxRatio          string `json:"aux_ratio"`
	Color             string `json:"color"`
	WeightKG          string `json:"weight_kg"`
	Active            int64  `json:"active"`
	WarehouseName     string `json:"warehouse_name"`
	Quantity          string `json:"quantity"`
	AuxiliaryQuantity string `json:"auxiliary_quantity"`
	ReferenceValue    string `json:"reference_value_cents"`
	WarehouseCount    int64  `json:"warehouse_count"`
}

func (a *App) inventorySummaryList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	from, args := inventorySummaryQuery(q)
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int64
	if err = tx.QueryRow("SELECT COUNT(*)"+from, args...).Scan(&count); err != nil {
		return err
	}
	rows, err := scanRows[inventorySummaryRecord](tx, inventorySummarySelect+","+inventorySummaryWarehouseCountSQL(q)+from+" ORDER BY p.name,p.id LIMIT 100 OFFSET ?", append(args, (page-1)*100), func(r *sql.Rows, item *inventorySummaryRecord) error {
		return r.Scan(&item.WarehouseID, &item.ProductID, &item.Qty, &item.QuantityQuantum, &item.Version, &item.Name, &item.Brand,
			&item.Spec, &item.Unit, &item.AuxUnit, &item.AuxRatio, &item.Color, &item.WeightKG,
			&item.Active, &item.WarehouseName, &item.Quantity, &item.AuxiliaryQuantity, &item.ReferenceValue, &item.WarehouseCount)
	})
	if err != nil {
		return err
	}
	write(w, http.StatusOK, queryPageResult[inventorySummaryRecord]{Rows: rows, Total: count, Page: page})
	return nil
}

func inventorySummaryExport(q url.Values) (string, []any, []string) {
	from, args := inventorySummaryQuery(q)
	query := "SELECT p.name,p.spec,p.unit," + inventorySummaryQuantitySQL + ",p.aux_unit," + inventorySummaryAuxiliarySQL + "," + inventorySummaryValueSQL + "," + inventorySummaryWarehouseCountSQL(q) + ",IF(p.active,'启用','停用')" + from + " ORDER BY p.name,p.id"
	return query, args, []string{"商品", "规格", "基本单位", "基本数量", "辅助单位", "辅助数量", "参考货值（分）", "仓库数", "商品状态"}
}
