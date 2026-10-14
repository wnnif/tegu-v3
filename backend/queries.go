package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Older fully paid orders predate payment_receipts. Cancelled orders have no
// effective receivable even though their original construction amount remains.
const orderReceiptTotalsJoinSQL = " LEFT JOIN (SELECT order_id,SUM(amount_cents) AS received_cents FROM payment_receipts WHERE voided=0 GROUP BY order_id) r ON r.order_id=o.id "
const orderReceivedCentsSQL = "CASE WHEN o.ticket_managed=1 OR o.status='已取消' THEN 0 WHEN o.status='已收款' THEN o.total_cents ELSE COALESCE(r.received_cents,0) END"
const orderOutstandingCentsSQL = "CASE WHEN o.ticket_managed=1 OR o.status IN ('已取消','已收款') THEN 0 ELSE o.total_cents-COALESCE(r.received_cents,0) END"

func queryPage(q url.Values) (int, error) {
	p, _ := strconv.Atoi(q.Get("page"))
	if p < 1 {
		p = 1
	}
	if p > 1000000 {
		return 0, fail(400, "页码超出范围")
	}
	return p, nil
}
func inventoryQuery(q url.Values) (string, []any) {
	where := "1=1"
	args := []any{}
	if q.Get("warehouse_id") != "" {
		where += " AND w.id=?"
		args = append(args, q.Get("warehouse_id"))
	}
	if q.Get("product_id") != "" {
		where += " AND p.id=?"
		args = append(args, q.Get("product_id"))
	}
	if q.Get("show_zero") != "1" {
		where += " AND COALESCE(b.qty_quantum,0)<>0"
	}
	productScope := q.Get("group_by") == "warehouse" || q.Get("product_id") != ""
	if q.Get("state") == "active" {
		where += " AND p.active=1"
		if !productScope {
			where += " AND w.active=1"
		}
	} else if q.Get("state") == "inactive" {
		if productScope {
			where += " AND p.active=0"
		} else {
			where += " AND (p.active=0 OR w.active=0)"
		}
	}
	if s := q.Get("q"); s != "" {
		if productScope {
			where += " AND (INSTR(p.name,?)>0 OR INSTR(p.brand,?)>0 OR INSTR(p.spec,?)>0)"
			args = append(args, s, s, s)
		} else {
			where += " AND (INSTR(p.name,?)>0 OR INSTR(p.brand,?)>0 OR INSTR(p.spec,?)>0 OR INSTR(w.name,?)>0)"
			args = append(args, s, s, s, s)
		}
	}
	if q.Get("show_zero") != "1" {
		return " FROM stock_balances b JOIN warehouses w ON w.id=b.warehouse_id JOIN products p ON p.id=b.product_id JOIN product_unit_definitions d ON d.product_id=p.id LEFT JOIN product_purchase_costs cost ON cost.product_id=p.id WHERE " + where, args
	}
	return " FROM warehouses w CROSS JOIN products p JOIN product_unit_definitions d ON d.product_id=p.id LEFT JOIN product_purchase_costs cost ON cost.product_id=p.id LEFT JOIN stock_balances b ON b.warehouse_id=w.id AND b.product_id=p.id WHERE " + where, args
}

const inventoryQuantitySQL = "CAST(CAST(COALESCE(b.qty_quantum,0) AS DECIMAL(65,12))/(1000000*d.base_factor) AS DECIMAL(65,6))"
const inventoryAuxiliarySQL = "CAST(CAST(COALESCE(b.qty_quantum,0) AS DECIMAL(65,12))/(1000000*d.aux_factor) AS DECIMAL(65,6))"
const inventoryValueSQL = "CAST(CASE WHEN cost.purchased_quantum>0 THEN ROUND(CAST(COALESCE(b.qty_quantum,0) AS DECIMAL(65,0))*cost.purchase_cents/cost.purchased_quantum,0) ELSE 0 END AS DECIMAL(65,0))"
const inventorySelect = "SELECT w.id AS warehouse_id,p.id AS product_id,COALESCE(b.qty_micro,0) AS qty_micro,COALESCE(b.qty_quantum,0) AS qty_quantum,COALESCE(b.version,0) AS version,p.name,p.brand,p.spec,p.unit,p.aux_unit,p.aux_ratio,p.color,p.weight_kg,p.active,w.name AS warehouse_name," + inventoryQuantitySQL + "," + inventoryAuxiliarySQL + "," + inventoryValueSQL

type inventoryRecord struct {
	WarehouseID       string `json:"warehouse_id"`
	ProductID         string `json:"product_id"`
	Qty               int64  `json:"qty_micro"`
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
}

func (a *App) inventoryList(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("warehouse") && !u.can("orders") {
		return fail(403, "无库存权限")
	}
	q := r.URL.Query()
	if e := validateInventoryGrouping(q); e != nil {
		return e
	}
	if q.Get("group_by") == "product" {
		return a.inventorySummaryList(w, r)
	}
	p, e := queryPage(q)
	if e != nil {
		return e
	}
	from, args := inventoryQuery(q)
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int64
	if e = tx.QueryRow("SELECT COUNT(*)"+from, args...).Scan(&count); e != nil {
		return e
	}
	v, e := scanRows[inventoryRecord](tx, inventorySelect+from+" ORDER BY w.name,w.id,p.name,p.id LIMIT 100 OFFSET ?", append(args, (p-1)*100), func(r *sql.Rows, item *inventoryRecord) error {
		return r.Scan(&item.WarehouseID, &item.ProductID, &item.Qty, &item.QuantityQuantum, &item.Version, &item.Name, &item.Brand,
			&item.Spec, &item.Unit, &item.AuxUnit, &item.AuxRatio, &item.Color, &item.WeightKG,
			&item.Active, &item.WarehouseName, &item.Quantity, &item.AuxiliaryQuantity, &item.ReferenceValue)
	})
	if e != nil {
		return e
	}
	write(w, 200, queryPageResult[inventoryRecord]{Rows: v, Total: count, Page: p})
	return nil
}
func materialQuery(q url.Values) (string, []any, error) {
	from, to, e := monthRange(q.Get("month"))
	if e != nil {
		return "", nil, e
	}
	s := " FROM order_materials m JOIN orders o ON o.id=m.order_id JOIN products p ON p.id=m.product_id JOIN warehouses w ON w.id=m.warehouse_id JOIN companies c ON c.id=o.company_id WHERE o.status<>'已取消' AND o.work_date>=? AND o.work_date<?"
	args := []any{from, to}
	for _, f := range []struct{ k, col string }{{"company_id", "o.company_id"}, {"warehouse_id", "m.warehouse_id"}} {
		if q.Get(f.k) != "" {
			s += " AND " + f.col + "=?"
			args = append(args, q.Get(f.k))
		}
	}
	if t := q.Get("q"); t != "" {
		s += " AND (INSTR(p.name,?)>0 OR INSTR(o.number,?)>0 OR INSTR(o.address,?)>0)"
		args = append(args, t, t, t)
	}
	return s, args, nil
}

const materialSelect = "SELECT m.product_id,o.id AS order_id,o.number,o.work_date,o.address,COALESCE(NULLIF(o.company_name,''),c.name) AS company,COALESCE(NULLIF(m.warehouse_name,''),w.name) AS warehouse,COALESCE(NULLIF(m.product_name,''),p.name) AS name,COALESCE(NULLIF(m.unit,''),p.unit) AS unit,m.qty_micro,CAST(m.qty_micro/1000000 AS DECIMAL(24,6)) AS quantity,m.qty_quantum,m.input_qty,m.input_unit,m.conversion_ratio,p.aux_unit"

type materialHistoryRecord struct {
	ProductID       string `json:"product_id"`
	OrderID         string `json:"order_id"`
	Number          string `json:"number"`
	WorkDate        string `json:"work_date"`
	Address         string `json:"address"`
	Company         string `json:"company"`
	Warehouse       string `json:"warehouse"`
	Name            string `json:"name"`
	Unit            string `json:"unit"`
	Qty             int64  `json:"qty_micro"`
	Quantity        string `json:"quantity"`
	QuantityQuantum string `json:"quantity_quantum"`
	InputQty        string `json:"input_qty"`
	InputUnit       string `json:"input_unit"`
	ConversionRatio string `json:"conversion_ratio"`
	AuxUnit         string `json:"aux_unit"`
	BaseFactor      string `json:"base_factor"`
	AuxFactor       string `json:"aux_factor"`
}

func (a *App) materialHistory(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("warehouse") {
		return fail(403, "无仓储权限")
	}
	q := r.URL.Query()
	p, e := queryPage(q)
	if e != nil {
		return e
	}
	from, args, e := materialQuery(q)
	if e != nil {
		return e
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int64
	if e = tx.QueryRow("SELECT COUNT(*)"+from, args...).Scan(&count); e != nil {
		return e
	}
	v, e := scanRows[materialHistoryRecord](tx, materialSelect+from+" ORDER BY o.work_date DESC,o.id,m.product_id LIMIT 100 OFFSET ?", append(args, (p-1)*100), func(r *sql.Rows, item *materialHistoryRecord) error {
		if err := r.Scan(&item.ProductID, &item.OrderID, &item.Number, &item.WorkDate, &item.Address,
			&item.Company, &item.Warehouse, &item.Name, &item.Unit, &item.Qty, &item.Quantity,
			&item.QuantityQuantum, &item.InputQty, &item.InputUnit, &item.ConversionRatio, &item.AuxUnit); err != nil {
			return err
		}
		definition, err := newProductUnitDefinition(item.Unit, item.AuxUnit, item.ConversionRatio)
		if err != nil {
			return err
		}
		item.BaseFactor, item.AuxFactor = strconv.FormatInt(definition.BaseFactor, 10), strconv.FormatInt(definition.AuxFactor, 10)
		return nil
	})
	if e != nil {
		return e
	}
	write(w, 200, queryPageResult[materialHistoryRecord]{Rows: v, Total: count, Page: p})
	return nil
}

func orderFilter(q url.Values) (string, []any, error) {
	where := "1=1"
	args := []any{}
	fromDate, toDate := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if fromDate != "" || toDate != "" {
		if q.Get("view") != "orders" || q.Get("month") != "" || fromDate == "" || toDate == "" {
			return "", nil, fail(400, "精确日期范围仅适用于施工单列表，且须同时提供开始和结束日期")
		}
		for _, date := range []string{fromDate, toDate} {
			if err := expenseDate(date, "日期范围"); err != nil {
				return "", nil, err
			}
		}
		if fromDate > toDate {
			return "", nil, fail(400, "开始日期不能晚于结束日期")
		}
		where = "o.work_date>=? AND o.work_date<=?"
		args = append(args, fromDate, toDate)
	}
	// Only the wage drill-down may explicitly request all construction months.
	// Other callers (including monthly archives) still require a valid month.
	if fromDate == "" && (q.Get("view") != "wages" || q.Get("all_months") != "1" || q.Get("month") != "") {
		from, to, e := monthRange(q.Get("month"))
		if e != nil {
			return "", nil, e
		}
		where += " AND o.work_date>=? AND o.work_date<?"
		args = append(args, from, to)
	}
	if q.Get("view") == "wages" {
		where += " AND o.status<>'已取消'"
	}
	if q.Get("view") == "finance" {
		where += " AND o.ticket_managed=0"
	}
	for _, f := range []string{"company_id", "project_id", "worker_id", "status"} {
		if q.Get(f) != "" {
			if f == "worker_id" {
				where += " AND EXISTS(SELECT 1 FROM " + workerWageSource + " ww WHERE ww.order_id=o.id AND ww.worker_id=?)"
			} else {
				where += " AND o." + f + "=?"
			}
			args = append(args, q.Get(f))
		}
	}
	if q.Get("wage_paid") != "" {
		where += " AND o.wage_paid=?"
		args = append(args, q.Get("wage_paid") == "1")
	}
	if s := q.Get("q"); s != "" {
		where += " AND (INSTR(o.address,?)>0 OR INSTR(o.number,?)>0)"
		args = append(args, s, s)
	}
	if q.Get("view") == "finance" && q.Get("company_id") == "" {
		where += " AND 1=0"
	}
	return where, args, nil
}

// Spool a consistent, complete export before sending headers so SQL/disk errors
// return a normal error instead of a truncated file that appears successful.
func (a *App) exportFiltered(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	kind := q.Get("view")
	var query string
	var args []any
	var headers []string
	var e error
	switch kind {
	case "project":
		if !u.can("orders") && !u.can("finance") {
			return fail(403, "无工地项目导出权限")
		}
		if err := requireCustomerPrices(u); err != nil {
			return err
		}
		pid := q.Get("project_id")
		if pid == "" {
			return fail(400, "请选择工地项目")
		}
		var count int
		if e = a.db.QueryRow("SELECT COUNT(*) FROM projects WHERE id=?", pid).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			return fail(404, "工地项目不存在")
		}
		args = []any{pid}
		base := "SELECT COALESCE(NULLIF(o.project_number,''),p.number),COALESCE(NULLIF(o.project_name,''),p.name),COALESCE(NULLIF(o.company_name,''),c.name),o.number,o.work_date,o.address," + orderWorkerNamesSQL + ",CAST(o.total_cents/100 AS DECIMAL(24,2))"
		from := " FROM orders o JOIN projects p ON p.id=o.project_id JOIN companies c ON c.id=o.company_id LEFT JOIN workers w ON w.id=o.worker_id"
		tail := " WHERE o.project_id=? ORDER BY o.work_date,o.id"
		if u.can("finance") {
			query = base + ",CAST((" + orderReceivedCentsSQL + ")/100 AS DECIMAL(24,2)),CAST((" + orderOutstandingCentsSQL + ")/100 AS DECIMAL(24,2)),o.status,o.ticket_no,o.delivered_at,o.recipient,o.ticket_attachment,o.return_note,o.note" + from + orderReceiptTotalsJoinSQL + tail
			headers = []string{"项目编号", "项目名称", "公司", "施工单号", "日期", "施工地址", "施工人", "施工金额", "已收金额", "剩余应收", "状态", "票据号", "交票日期", "收票人", "交票附件", "退回说明", "备注"}
		} else {
			query = base + ",o.status,o.note" + from + tail
			headers = []string{"项目编号", "项目名称", "公司", "施工单号", "日期", "施工地址", "施工人", "施工金额", "状态", "备注"}
		}
	case "inventory":
		if !u.can("warehouse") {
			return fail(403, "无库存导出权限")
		}
		if e = validateInventoryGrouping(q); e != nil {
			return e
		}
		if q.Get("group_by") == "product" {
			query, args, headers = inventorySummaryExport(q)
		} else {
			from, a := inventoryQuery(q)
			args = a
			query = "SELECT w.name,p.name,p.spec,p.unit," + inventoryQuantitySQL + ",p.aux_unit," + inventoryAuxiliarySQL + "," + inventoryValueSQL + ",IF(p.active,'启用','停用')" + from + " ORDER BY w.id,p.id"
			headers = []string{"仓库", "商品", "规格", "基本单位", "基本数量", "辅助单位", "辅助数量", "参考货值（分）", "商品状态"}
		}
	case "materials":
		if !u.can("warehouse") {
			return fail(403, "无耗材导出权限")
		}
		from, a, err := materialQuery(q)
		if err != nil {
			return err
		}
		args = a
		query = "SELECT o.number,o.work_date,COALESCE(NULLIF(o.company_name,''),c.name),o.address,COALESCE(NULLIF(m.warehouse_name,''),w.name),COALESCE(NULLIF(m.product_name,''),p.name),CASE WHEN m.input_unit='aux' AND p.aux_unit<>'' THEN p.aux_unit ELSE COALESCE(NULLIF(m.unit,''),p.unit) END,COALESCE(NULLIF(m.input_qty,''),CAST(m.qty_micro/1000000 AS DECIMAL(24,6)))" + from + " ORDER BY o.id,m.product_id"
		headers = []string{"单号", "施工日期", "公司", "工地", "仓库", "商品", "单位", "消耗数量"}
	case "orders", "finance", "wages":
		if !u.can(kind) {
			return fail(403, "无导出权限")
		}
		if kind != "wages" {
			if err := requireCustomerPrices(u); err != nil {
				return err
			}
		}
		if q.Get("wage_paid") != "" && !u.can("wages") {
			return fail(403, "无工资筛选权限")
		}
		where, a, err := orderFilter(q)
		if kind == "wages" {
			where, a, err = workerWageFilter(q)
		}
		if err != nil {
			return err
		}
		args = a
		amount, state := "o.total_cents", "o.status"
		if kind == "wages" {
			amount = "o.wage_cents"
			state = "IF(o.wage_paid,'已发放','未发放')"
		}
		query = "SELECT o.number,COALESCE(NULLIF(o.company_name,''),c.name),COALESCE(NULLIF(o.project_number,''),p.number,''),o.address,o.work_date," + orderWorkerNamesSQL + ",o.contact,o.phone,CAST(" + amount + "/100 AS DECIMAL(24,2))," + state + ",o.note FROM orders o JOIN companies c ON c.id=o.company_id LEFT JOIN workers w ON w.id=o.worker_id LEFT JOIN projects p ON p.id=o.project_id WHERE " + where + " ORDER BY o.work_date,o.id"
		if kind == "wages" {
			query = "SELECT o.number,COALESCE(NULLIF(o.company_name,''),c.name),COALESCE(NULLIF(o.project_number,''),p.number,''),o.address,o.work_date,ww.worker_name,o.contact,o.phone,CAST(ww.wage_cents/100 AS DECIMAL(24,2)),IF(ww.wage_paid,'已发放','未发放'),o.note FROM orders o JOIN " + workerWageSource + " ww ON ww.order_id=o.id JOIN companies c ON c.id=o.company_id LEFT JOIN projects p ON p.id=o.project_id WHERE " + where + " ORDER BY o.work_date,o.id,ww.id"
		}
		headers = []string{"单号", "公司", "工地项目编号", "工地", "日期", "施工人", "项目经理", "电话", "金额", "状态", "备注"}
		if kind == "finance" {
			query = "SELECT o.number,COALESCE(NULLIF(o.company_name,''),c.name),COALESCE(NULLIF(o.project_number,''),p.number,''),o.address,o.work_date," + orderWorkerNamesSQL + ",o.contact,o.phone,CAST(o.total_cents/100 AS DECIMAL(24,2)),CAST((" + orderReceivedCentsSQL + ")/100 AS DECIMAL(24,2)),CAST((" + orderOutstandingCentsSQL + ")/100 AS DECIMAL(24,2)),o.status,o.ticket_no,o.delivered_at,o.recipient,o.ticket_attachment,o.return_note,o.note FROM orders o JOIN companies c ON c.id=o.company_id LEFT JOIN workers w ON w.id=o.worker_id LEFT JOIN projects p ON p.id=o.project_id" + orderReceiptTotalsJoinSQL + "WHERE " + where + " ORDER BY o.work_date,o.id"
			headers = []string{"单号", "公司", "工地项目编号", "工地", "日期", "施工人", "项目经理", "电话", "施工金额", "已收金额", "剩余应收", "状态", "票据号", "交票日期", "收票人", "交票附件", "退回说明", "备注"}
		}
	default:
		return fail(400, "导出类型无效")
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if kind == "wages" {
		scope, scopeArgs, err := workerWageOrderFilter(q)
		if err != nil {
			return err
		}
		if e = validateWorkerAccountScope(tx, scope, scopeArgs); e != nil {
			return e
		}
	}
	result, e := tx.QueryContext(r.Context(), query, args...)
	if e != nil {
		return e
	}
	defer result.Close()
	file, e := os.CreateTemp("", "tegu-export-*.csv")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, e = file.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
		return e
	}
	writer := csv.NewWriter(file)
	writer.UseCRLF = true
	if e = writer.Write(headers); e != nil {
		return e
	}
	values := make([]sql.NullString, len(headers))
	dest := make([]any, len(headers))
	for i := range values {
		dest[i] = &values[i]
	}
	for result.Next() {
		if e = result.Scan(dest...); e != nil {
			return e
		}
		line := make([]string, len(values))
		for i, v := range values {
			value := v.String
			if headers[i] == "施工人" && kind != "wages" && kind != "materials" {
				value, e = decodeWorkerNames([]byte(value))
				if e != nil {
					return e
				}
			}
			line[i] = value
			s := strings.TrimSpace(value)
			if s != "" && strings.ContainsAny(s[:1], "=+-@") {
				line[i] = "'" + value
			}
		}
		if e = writer.Write(line); e != nil {
			return e
		}
	}
	if e = result.Err(); e != nil {
		return e
	}
	result.Close()
	writer.Flush()
	if e = writer.Error(); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if e = file.Sync(); e != nil {
		return e
	}
	stat, e := file.Stat()
	if e != nil {
		return e
	}
	if _, e = file.Seek(0, 0); e != nil {
		return e
	}
	logTx, e := a.db.BeginTx(context.Background(), nil)
	if e != nil {
		return e
	}
	defer logTx.Rollback()
	if e = audit(logTx, u, "导出"+kind, "", map[string]string{"filters": q.Encode()}); e != nil {
		return e
	}
	if e = logTx.Commit(); e != nil {
		return e
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.csv", kind))
	http.ServeContent(w, r, kind+".csv", stat.ModTime(), file)
	return nil
}
