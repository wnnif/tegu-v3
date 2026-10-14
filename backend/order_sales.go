package main

import (
	"database/sql"
	"math/big"
	"strconv"
	"strings"
)

type Sale struct {
	ID              string `json:"id"`
	Product         string `json:"product_id"`
	Warehouse       string `json:"warehouse_id"`
	Qty             int64  `json:"qty_micro"`
	QtyQuantum      string `json:"qty_quantum"`
	SaleQty         int64  `json:"sale_qty_micro"`
	InputQty        string `json:"input_qty"`
	InputUnit       string `json:"input_unit"`
	Price           int64  `json:"price_cents"`
	Total           int64  `json:"total_cents"`
	CompanyProductPriceID string `json:"-"`
	CompanyPriceFixed bool `json:"company_price_fixed,omitempty"`
	ProductName     string `json:"product_name"`
	Unit            string `json:"unit"`
	PriceUnit       string `json:"price_unit"`
	SortOrder       int    `json:"sort_order"`
	ConversionRatio string `json:"conversion_ratio"`
	Editable        *bool  `json:"editable,omitempty"`
	ticketBatchID   string `json:"-"`
}

// All quantities are positive and rounded once to six decimal places. The sale
// amount uses the selected selling unit; inventory always uses the basic unit.
func orderInputQuantity(input string, fallback int64, inputUnit, auxUnit, ratioText string) (int64, int64, error) {
	if input == "" {
		input = scaledDecimal(fallback, 6)
	}
	if len(input) > 100 {
		return 0, 0, fail(400, "材料数量无效")
	}
	q, ok := new(big.Rat).SetString(input)
	if !ok || q.Sign() <= 0 || q.Cmp(big.NewRat(1000000, 1)) > 0 {
		return 0, 0, fail(400, "材料数量须大于零且不超过1000000")
	}
	saleQty := roundPositive(new(big.Rat).Mul(q, big.NewRat(1000000, 1)))
	if inputUnit == "aux" {
		ratio, valid := new(big.Rat).SetString(ratioText)
		if !valid || auxUnit == "" || ratio.Sign() <= 0 {
			return 0, 0, fail(400, "商品未设置有效辅助单位换算")
		}
		q.Quo(q, ratio)
	} else if inputUnit != "base" && inputUnit != "" {
		return 0, 0, fail(400, "材料单位无效")
	}
	if q.Cmp(big.NewRat(1000000, 1)) > 0 {
		return 0, 0, fail(400, "换算后材料数量超出范围")
	}
	baseQty := roundPositive(new(big.Rat).Mul(q, big.NewRat(1000000, 1)))
	if baseQty <= 0 || saleQty <= 0 {
		return 0, 0, fail(400, "材料数量小于最小精度0.000001")
	}
	return baseQty, saleQty, nil
}

func storedOrderQuantum(value string) (int64, error) {
	qty, err := strconv.ParseInt(value, 10, 64)
	if err != nil || qty <= 0 {
		return 0, fail(400, "历史材料精确数量无效，请核对迁移")
	}
	return qty, nil
}

func checkOrderInputQuantum(claimed string, quantity int64) error {
	if claimed == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(claimed, 10, 64)
	if err != nil || parsed <= 0 || parsed != quantity {
		return fail(400, "材料数量与单位换算不一致，请重新填写")
	}
	return nil
}

func addOrderStockDelta(delta map[string]int64, key string, quantity int64) error {
	next := new(big.Int).Add(big.NewInt(delta[key]), big.NewInt(quantity))
	if !next.IsInt64() {
		return fail(400, "材料合计数量超出精确换算范围")
	}
	delta[key] = next.Int64()
	return nil
}

func materialSnapshotKey(m Material) string {
	return m.LineID + "|" + m.Product + "|" + m.Warehouse
}

func resolveMaterialLineReferences(o *Order) error {
	refs := make(map[string]string, len(o.Lines)*2)
	for _, line := range o.Lines {
		refs[line.ID] = line.ID
	}
	for _, line := range o.Lines {
		if line.ClientRef == "" {
			continue
		}
		if existing, ok := refs[line.ClientRef]; ok && existing != line.ID {
			return fail(400, "施工行引用与已有明细冲突")
		}
		refs[line.ClientRef] = line.ID
	}
	for i := range o.Materials {
		m := &o.Materials[i]
		if m.LineID == "" {
			continue
		}
		lineID, found := refs[m.LineID]
		if !found {
			return fail(400, "工艺耗材必须关联本次实际施工明细；删除明细时请一并移除或重新归属耗材")
		}
		m.LineID = lineID
	}
	return nil
}

func orderStockDeltas(oldMaterials, materials []Material, oldSales, sales []Sale) (map[string]int64, map[string]int64, error) {
	materialDelta, saleDelta := map[string]int64{}, map[string]int64{}
	for _, group := range []struct {
		rows  []Material
		sign  int64
		delta map[string]int64
	}{{oldMaterials, 1, materialDelta}, {materials, -1, materialDelta}} {
		for _, row := range group.rows {
			quantity, err := storedOrderQuantum(row.QtyQuantum)
			if err != nil {
				return nil, nil, err
			}
			if err = addOrderStockDelta(group.delta, row.Warehouse+"|"+row.Product, group.sign*quantity); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, group := range []struct {
		rows []Sale
		sign int64
	}{{oldSales, 1}, {sales, -1}} {
		for _, row := range group.rows {
			quantity, err := storedOrderQuantum(row.QtyQuantum)
			if err != nil {
				return nil, nil, err
			}
			if err = addOrderStockDelta(saleDelta, row.Warehouse+"|"+row.Product, group.sign*quantity); err != nil {
				return nil, nil, err
			}
		}
	}
	return materialDelta, saleDelta, nil
}

func exactOrderInput(input string, fallback int64, unit string, product stockProduct) (int64, int64, string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		input = scaledDecimal(fallback, 6)
		unit = "base"
	}
	if unit == "" || unit == product.Unit {
		unit = "base"
	} else if unit == product.AuxUnit && product.AuxUnit != "" {
		unit = "aux"
	}
	value, ok := new(big.Rat).SetString(input)
	if !ok || value.Sign() <= 0 || value.Cmp(big.NewRat(1000000, 1)) > 0 {
		return 0, 0, "", "", fail(400, "材料数量须大于零且不超过1000000")
	}
	qty, err := product.Definition.quantityQuantum(input, unit)
	if err != nil || qty <= 0 {
		return 0, 0, "", "", fail(400, "材料数量无法按所选单位精确换算")
	}
	sale := new(big.Rat).Mul(value, big.NewRat(productQuantityScale, 1))
	if !sale.IsInt() || !sale.Num().IsInt64() {
		return 0, 0, "", "", fail(400, "材料输入数量超过精度或范围")
	}
	return qty, sale.Num().Int64(), input, unit, nil
}

func readOrderSales(tx *sql.Tx, orderID string) ([]Sale, error) {
	rows, err := tx.Query(`SELECT id,product_id,warehouse_id,qty_micro,qty_quantum,sale_qty_micro,input_qty,input_unit,price_cents,total_cents,product_name,unit,price_unit,sort_order,conversion_ratio,COALESCE(company_product_price_id,''),COALESCE(ticket_batch_id,'') FROM order_sales WHERE order_id=? ORDER BY sort_order,id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sales := []Sale{}
	for rows.Next() {
		var s Sale
		if err = rows.Scan(&s.ID, &s.Product, &s.Warehouse, &s.Qty, &s.QtyQuantum, &s.SaleQty, &s.InputQty, &s.InputUnit, &s.Price, &s.Total, &s.ProductName, &s.Unit, &s.PriceUnit, &s.SortOrder, &s.ConversionRatio, &s.CompanyProductPriceID, &s.ticketBatchID); err != nil {
			return nil, err
		}
		s.CompanyPriceFixed = s.CompanyProductPriceID != ""
		sales = append(sales, s)
	}
	return sales, rows.Err()
}

func prepareOrderStock(tx *sql.Tx, u User, o *Order, oldMaterials []Material, oldSales []Sale) (map[string]int64, int64, error) {
	if !o.Delete {
		if err := resolveMaterialLineReferences(o); err != nil {
			return nil, 0, err
		}
	}
	productIDs := make([]string, 0, len(oldMaterials)+len(oldSales)+len(o.Materials)+len(o.Sales))
	for _, m := range oldMaterials {
		productIDs = append(productIDs, m.Product)
	}
	for _, s := range oldSales {
		productIDs = append(productIDs, s.Product)
	}
	if !o.Delete {
		for _, m := range o.Materials {
			productIDs = append(productIDs, m.Product)
		}
		for _, s := range o.Sales {
			productIDs = append(productIDs, s.Product)
		}
	}
	products, err := lockStockProducts(tx, productIDs)
	if err != nil {
		return nil, 0, err
	}
	companyPrices := map[string]map[string]companyProductPrice{}
	if !o.Delete && len(o.Sales) > 0 {
		saleProductIDs := make([]string, 0, len(o.Sales))
		for _, sale := range o.Sales {
			saleProductIDs = append(saleProductIDs, sale.Product)
		}
		companyPrices, err = loadCompanyProductPrices(tx, o.Company, saleProductIDs, true)
		if err != nil {
			return nil, 0, err
		}
	}
	delta := map[string]int64{}
	for _, m := range oldMaterials {
		qty, err := storedOrderQuantum(m.QtyQuantum)
		if err != nil {
			return nil, 0, err
		}
		if err = addOrderStockDelta(delta, m.Warehouse+"|"+m.Product, qty); err != nil {
			return nil, 0, err
		}
	}
	for _, s := range oldSales {
		qty, err := storedOrderQuantum(s.QtyQuantum)
		if err != nil {
			return nil, 0, err
		}
		if err = addOrderStockDelta(delta, s.Warehouse+"|"+s.Product, qty); err != nil {
			return nil, 0, err
		}
	}
	if o.Delete {
		o.Materials = nil
		o.Sales = nil
		return delta, 0, nil
	}
	previous := map[string]Sale{}
	for _, s := range oldSales {
		previous[s.ID] = s
	}
	seen := map[string]bool{}
	for i := range o.Materials {
		m := &o.Materials[i]
		if m.Warehouse == "" {
			m.Warehouse = o.Warehouse
		}
		if m.Warehouse == "" || (m.Warehouse != o.Warehouse && !u.can("warehouse")) {
			return nil, 0, fail(403, "选择其他材料来源需要仓库权限")
		}
		product := products[m.Product]
		m.ProductName, m.Unit = product.Name, product.Unit
		if !product.Active {
			return nil, 0, fail(400, "施工耗材商品已停用")
		}
		if err := tx.QueryRow("SELECT name FROM warehouses WHERE id=?", m.Warehouse).Scan(&m.WarehouseName); err != nil {
			return nil, 0, fail(400, "材料仓库不存在")
		}
		qty, _, input, unit, err := exactOrderInput(m.InputQty, m.Qty, m.InputUnit, product)
		if err != nil {
			return nil, 0, err
		}
		if err = checkOrderInputQuantum(m.QtyQuantum, qty); err != nil {
			return nil, 0, err
		}
		m.InputQty, m.InputUnit, m.QtyQuantum = input, unit, strconv.FormatInt(qty, 10)
		m.Qty = roundedBaseMicro(qty, product.Definition.BaseFactor)
		m.ConversionRatio = product.AuxRatio
		materialKey := materialSnapshotKey(*m)
		if seen[materialKey] {
			return nil, 0, fail(400, "同一施工明细、商品及材料来源不可重复")
		}
		seen[materialKey] = true
		for _, old := range oldMaterials {
			if old.Product == m.Product && old.ProductName != "" {
				m.ProductName, m.Unit = old.ProductName, old.Unit
			}
			if old.Warehouse == m.Warehouse && old.WarehouseName != "" {
				m.WarehouseName = old.WarehouseName
			}
		}
		if err = addOrderStockDelta(delta, m.Warehouse+"|"+m.Product, -qty); err != nil {
			return nil, 0, err
		}
	}
	var total int64
	seen = map[string]bool{}
	for i := range o.Sales {
		s := &o.Sales[i]
		if s.Warehouse == "" {
			s.Warehouse = o.Warehouse
		}
		if s.Warehouse == "" || (s.Warehouse != o.Warehouse && !u.can("warehouse")) {
			return nil, 0, fail(403, "选择其他发货位置需要仓库权限")
		}
		s.SortOrder = orderSaleSortOrder(*s, i, previous)
		if s.ID != "" {
			if _, ok := previous[s.ID]; !ok {
				return nil, 0, fail(400, "售料明细不属于本次记录")
			}
			if seen[s.ID] {
				return nil, 0, fail(400, "售料明细重复")
			}
			seen[s.ID] = true
		} else {
			s.ID = id()
		}
		product := products[s.Product]
		s.ProductName, s.Unit = product.Name, product.Unit
		aux, ratio := product.AuxUnit, product.AuxRatio
		if !product.Active {
			return nil, 0, fail(400, "售料商品已停用")
		}
		if s.Price < 0 || s.Price > 100000000 {
			return nil, 0, fail(400, "材料销售单价超出范围")
		}
		if old, ok := previous[s.ID]; ok {
			if old.Product != s.Product {
				return nil, 0, fail(400, "已保存的售料明细不能更换商品，请删除后另增")
			}
			// Unit changes are blocked once stock/order history exists, so a
			// stored sale must still use the same exact product ratio.  Do not
			// combine a saved pricing ratio with a different inventory factor.
			oldRatio, valid := new(big.Rat).SetString(old.ConversionRatio)
			currentRatio := new(big.Rat).SetFrac(big.NewInt(product.Definition.BaseFactor), big.NewInt(product.Definition.AuxFactor))
			if !valid || oldRatio.Cmp(currentRatio) != 0 {
				return nil, 0, fail(409, "历史售料换算比例与商品定义不一致，请先核对历史资料")
			}
		}
		s.ConversionRatio = ratio
		baseQty, saleQty, input, unit, err := exactOrderInput(s.InputQty, s.Qty, s.InputUnit, product)
		if err != nil {
			return nil, 0, err
		}
		if err = checkOrderInputQuantum(s.QtyQuantum, baseQty); err != nil {
			return nil, 0, err
		}
		s.Qty, s.SaleQty = roundedBaseMicro(baseQty, product.Definition.BaseFactor), saleQty
		s.QtyQuantum, s.InputQty, s.InputUnit = strconv.FormatInt(baseQty, 10), input, unit
		s.PriceUnit = s.Unit
		if s.InputUnit == "aux" {
			s.PriceUnit = aux
		}
		var oldSale *Sale
		if old, ok := previous[s.ID]; ok {
			oldSale = &old
		}
		if err = applyCompanyProductSalePrice(s, oldSale, companyPrices[s.Product]); err != nil {
			return nil, 0, err
		}
		if old, ok := previous[s.ID]; ok {
			s.ProductName, s.Unit = old.ProductName, old.Unit
			if old.InputUnit == s.InputUnit {
				s.PriceUnit = old.PriceUnit
			}
		}
		s.Total = scaledAmount(s.SaleQty, s.Price)
		total += s.Total
		if total > 100000000000000 {
			return nil, 0, fail(400, "售料总额超出上限")
		}
		if err = addOrderStockDelta(delta, s.Warehouse+"|"+s.Product, -baseQty); err != nil {
			return nil, 0, err
		}
	}
	return delta, total, nil
}

func orderSaleSortOrder(s Sale, index int, previous map[string]Sale) int {
	if stored, ok := previous[s.ID]; ok {
		return stored.SortOrder
	}
	return index
}

func persistOrderSales(tx *sql.Tx, o Order) error {
	if _, err := tx.Exec("DELETE FROM order_sales WHERE order_id=? AND ticket_batch_id IS NULL", o.ID); err != nil {
		return err
	}
	for _, s := range o.Sales {
		if s.ticketBatchID != "" {
			continue
		}
		var priceID any
		if s.CompanyProductPriceID != "" {
			priceID = s.CompanyProductPriceID
		}
		if _, err := tx.Exec(`INSERT INTO order_sales(id,order_id,product_id,warehouse_id,qty_micro,qty_quantum,sale_qty_micro,price_cents,total_cents,product_name,unit,price_unit,input_unit,input_qty,sort_order,conversion_ratio,company_product_price_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, s.ID, o.ID, s.Product, s.Warehouse, s.Qty, s.QtyQuantum, s.SaleQty, s.Price, s.Total, s.ProductName, s.Unit, s.PriceUnit, s.InputUnit, s.InputQty, s.SortOrder, s.ConversionRatio, priceID); err != nil {
			return err
		}
	}
	return nil
}

func validSortOrder(n int) bool { return n >= 0 && n <= 10000 }
