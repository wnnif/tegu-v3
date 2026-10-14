package main

import (
	"database/sql"
	"net/http"
	"net/url"
	"time"
)

type stockMovementRecord struct {
	ID             int64  `json:"id"`
	WorkDate       string `json:"work_date"`
	CreatedAt      string `json:"created_at"`
	ProductID      string `json:"product_id"`
	ProductName    string `json:"product_name"`
	WarehouseID    string `json:"warehouse_id"`
	WarehouseName  string `json:"warehouse_name"`
	DeltaQuantum   string `json:"delta_quantum"`
	Quantity       string `json:"quantity"`
	Unit           string `json:"unit"`
	AuxUnit        string `json:"aux_unit"`
	AuxRatio       string `json:"aux_ratio"`
	BaseFactor     string `json:"base_factor"`
	AuxFactor      string `json:"aux_factor"`
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	OrderNumber    string `json:"order_number"`
	ProjectAddress string `json:"project_address"`
}

const stockMovementDateSQL = "COALESCE(sd.work_date,o.work_date,DATE(m.created_at))"

func stockMovementsQuery(q url.Values) (string, []any, error) {
	from := ` FROM stock_movements m JOIN products p ON p.id=m.product_id
		JOIN product_unit_definitions d ON d.product_id=p.id
		JOIN warehouses w ON w.id=m.warehouse_id
		LEFT JOIN stock_documents sd ON sd.id=m.source_id AND m.source_type NOT IN ('施工单','施工耗材','商品销售')
		LEFT JOIN orders o ON o.id=m.source_id AND m.source_type IN ('施工单','施工耗材','商品销售') WHERE 1=1`
	args := []any{}
	for _, filter := range []struct{ key, column string }{
		{"product_id", "m.product_id"}, {"warehouse_id", "m.warehouse_id"},
	} {
		if value := q.Get(filter.key); value != "" {
			if len(value) > 64 {
				return "", nil, fail(400, "库存流水筛选无效")
			}
			from += " AND " + filter.column + "=?"
			args = append(args, value)
		}
	}
	for _, filter := range []struct{ key, operator string }{
		{"from", ">="}, {"to", "<"},
	} {
		if value := q.Get(filter.key); value != "" {
			date, err := time.Parse("2006-01-02", value)
			if err != nil || date.Format("2006-01-02") != value {
				return "", nil, fail(400, "日期无效")
			}
			if filter.key == "to" {
				date = date.AddDate(0, 0, 1)
			}
			from += " AND " + stockMovementDateSQL + filter.operator + "?"
			args = append(args, date.Format("2006-01-02"))
		}
	}
	return from, args, nil
}

func (a *App) listStockMovements(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("warehouse") {
		return fail(403, "无仓储权限")
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	from, args, err := stockMovementsQuery(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
		return err
	}
	query := `SELECT m.id,` + stockMovementDateSQL + `,m.created_at,
		m.product_id,p.name,m.warehouse_id,w.name,m.delta_quantum,
		CAST(CAST(m.delta_quantum AS DECIMAL(65,12))/(1000000*d.base_factor) AS DECIMAL(65,6)),
		p.unit,p.aux_unit,p.aux_ratio,d.base_factor,d.aux_factor,
		m.source_type,m.source_id,COALESCE(o.number,''),COALESCE(o.address,'')`
	rows, err := scanRows[stockMovementRecord](tx, query+from+" ORDER BY m.id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100), func(row *sql.Rows, item *stockMovementRecord) error {
		return row.Scan(&item.ID, &item.WorkDate, &item.CreatedAt, &item.ProductID, &item.ProductName,
			&item.WarehouseID, &item.WarehouseName, &item.DeltaQuantum, &item.Quantity,
			&item.Unit, &item.AuxUnit, &item.AuxRatio, &item.BaseFactor, &item.AuxFactor,
			&item.SourceType, &item.SourceID, &item.OrderNumber, &item.ProjectAddress)
	})
	if err != nil {
		return err
	}
	write(w, http.StatusOK, queryPageResult[stockMovementRecord]{Rows: rows, Total: total, Page: page})
	return nil
}
