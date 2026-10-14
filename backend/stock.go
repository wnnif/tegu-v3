package main

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"
)

func applyStock(tx *sql.Tx, u User, key, kind, source string, delta map[string]int64) error {
	return applyStockWithPostings(tx, u, key, source, []stockPosting{{Kind: kind, Delta: delta}})
}

type stockPosting struct {
	Kind      string
	KeySuffix string
	Delta     map[string]int64
}

// Lock and update each physical balance once using the combined change. Keep
// material and sale ledger rows distinct, including when their changes offset.
func applyStockWithPostings(tx *sql.Tx, u User, key, source string, postings []stockPosting) error {
	delta := map[string]int64{}
	for _, posting := range postings {
		for k, v := range posting.Delta {
			if v == 0 {
				continue
			}
			if err := addOrderStockDelta(delta, k, v); err != nil {
				return err
			}
		}
	}
	keys := []string{}
	for k := range delta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	productIDs := make([]string, 0, len(keys))
	for _, k := range keys {
		parts := strings.Split(k, "|")
		if len(parts) != 2 {
			return fail(400, "库存项目无效")
		}
		productIDs = append(productIDs, parts[1])
	}
	products, err := lockStockProducts(tx, productIDs)
	if err != nil {
		return err
	}
	for _, k := range keys {
		parts := strings.Split(k, "|")
		wh, p := parts[0], parts[1]
		var active bool
		if e := tx.QueryRow("SELECT active FROM warehouses WHERE id=? FOR SHARE", wh).Scan(&active); e != nil || !active {
			return fail(400, "仓库不存在或已停用")
		}
		if !products[p].Active {
			return fail(400, "商品不存在或已停用")
		}
		if _, e := tx.Exec("INSERT INTO stock_balances(warehouse_id,product_id,qty_micro,qty_quantum) VALUES(?,?,0,0) ON DUPLICATE KEY UPDATE qty_quantum=qty_quantum", wh, p); e != nil {
			return e
		}
		var current int64
		if e := tx.QueryRow("SELECT qty_quantum FROM stock_balances WHERE warehouse_id=? AND product_id=? FOR UPDATE", wh, p).Scan(&current); e != nil {
			return e
		}
		next := new(big.Int).Add(big.NewInt(current), big.NewInt(delta[k]))
		if next.Sign() < 0 {
			return fail(409, "库存不足或已被其他人领用，请刷新后核对")
		}
		if !next.IsInt64() {
			return fail(400, "库存数量超出上限")
		}
		factor := products[p].Definition.BaseFactor
		legacyBalance := roundedBaseMicro(next.Int64(), factor)
		if _, e := tx.Exec("UPDATE stock_balances SET qty_micro=?,qty_quantum=?,version=version+1 WHERE warehouse_id=? AND product_id=?", legacyBalance, next.Int64(), wh, p); e != nil {
			return e
		}
		for _, posting := range postings {
			amount := posting.Delta[k]
			if amount == 0 {
				continue
			}
			operationKey := key
			if posting.KeySuffix != "" {
				operationKey += ":" + posting.KeySuffix
			}
			if _, e := tx.Exec("INSERT INTO stock_movements(operation_id,warehouse_id,product_id,delta_micro,delta_quantum,source_type,source_id,actor_id) VALUES(?,?,?,?,?,?,?,?)", stockOperationID(u.ID, operationKey), wh, p, roundedBaseMicro(amount, factor), amount, posting.Kind, source, u.ID); e != nil {
				return e
			}
		}
	}
	return nil
}

func roundedBaseMicro(quantum, factor int64) int64 {
	if factor <= 0 {
		return 0
	}
	whole, remainder := quantum/factor, quantum%factor
	threshold := (factor + 1) / 2
	if remainder >= threshold {
		return whole + 1
	}
	if remainder <= -threshold {
		return whole - 1
	}
	return whole
}

type StockLine struct {
	Material
	Name            string `json:"name"`
	Unit            string `json:"unit"`
	Price           int64  `json:"price_cents"`
	Total           int64  `json:"total_cents"`
	Note            string `json:"note"`
	InputUnit       string `json:"input_unit"`
	InputQty        string `json:"input_qty"`
	InputQuantity   string `json:"input_quantity"`
	QuantityQuantum string `json:"quantity_quantum"`
	BaseFactor      string `json:"base_factor"`
	AuxFactor       string `json:"aux_factor"`
	AuxUnit         string `json:"aux_unit"`
	AuxRatio        string `json:"aux_ratio"`
}

func stockLineQuantum(line StockLine, product stockProduct) (int64, error) {
	if line.QuantityQuantum != "" {
		quantity, ok := new(big.Int).SetString(line.QuantityQuantum, 10)
		if !ok || !quantity.IsInt64() || quantity.Sign() < 0 {
			return 0, fail(400, "商品精确数量无效")
		}
		return quantity.Int64(), nil
	}
	if line.Qty < 0 {
		return 0, fail(400, "商品数量无效")
	}
	quantity := new(big.Int).Mul(big.NewInt(line.Qty), big.NewInt(product.Definition.BaseFactor))
	if !quantity.IsInt64() {
		return 0, fail(400, "商品数量超出精确换算范围")
	}
	return quantity.Int64(), nil
}

func stockInputQuantum(line *StockLine, product stockProduct) (int64, error) {
	input := strings.TrimSpace(line.InputQuantity)
	if input == "" {
		input = strings.TrimSpace(line.InputQty)
	}
	if input == "" {
		quantity, err := stockLineQuantum(*line, product)
		if err != nil {
			return 0, err
		}
		line.InputUnit = "base"
		line.InputQuantity = scaledDecimal(line.Qty, 6)
		return quantity, nil
	}
	unit := line.InputUnit
	if unit == product.Unit {
		unit = "base"
	} else if unit == product.AuxUnit && product.AuxUnit != "" {
		unit = "aux"
	}
	quantity, err := product.Definition.quantityQuantum(input, unit)
	if err != nil {
		return 0, err
	}
	if line.QuantityQuantum != "" {
		claimed, ok := new(big.Int).SetString(line.QuantityQuantum, 10)
		if !ok || !claimed.IsInt64() || claimed.Int64() != quantity {
			return 0, fail(400, "商品数量与单位换算不一致，请重新填写")
		}
	}
	line.InputUnit, line.InputQuantity = unit, input
	return quantity, nil
}

func updatePurchaseCosts(tx *sql.Tx, previous map[string]StockLine, current []StockLine, products map[string]stockProduct) error {
	type change struct{ quantity, amount *big.Int }
	changes := map[string]change{}
	apply := func(line StockLine, sign int64) error {
		product, ok := products[line.Product]
		if !ok {
			return fail(400, "进货商品不存在")
		}
		quantity, err := stockLineQuantum(line, product)
		if err != nil {
			return err
		}
		if line.Total < 0 {
			return fail(400, "进货金额无效")
		}
		v := changes[line.Product]
		if v.quantity == nil {
			v.quantity, v.amount = new(big.Int), new(big.Int)
		}
		v.quantity.Add(v.quantity, new(big.Int).Mul(big.NewInt(quantity), big.NewInt(sign)))
		v.amount.Add(v.amount, new(big.Int).Mul(big.NewInt(line.Total), big.NewInt(sign)))
		changes[line.Product] = v
		return nil
	}
	for _, line := range previous {
		if err := apply(line, -1); err != nil {
			return err
		}
	}
	for _, line := range current {
		if err := apply(line, 1); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(changes))
	for productID := range changes {
		keys = append(keys, productID)
	}
	sort.Strings(keys)
	for _, productID := range keys {
		change := changes[productID]
		if change.quantity.Sign() == 0 && change.amount.Sign() == 0 {
			continue
		}
		if _, err := tx.Exec("INSERT INTO product_purchase_costs(product_id) VALUES(?) ON DUPLICATE KEY UPDATE product_id=product_id", productID); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE product_purchase_costs SET purchased_quantum=purchased_quantum+?,purchase_cents=purchase_cents+?
			WHERE product_id=? AND purchased_quantum+?>=0 AND purchase_cents+?>=0`, change.quantity.String(), change.amount.String(), productID, change.quantity.String(), change.amount.String())
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return fail(409, "进货参考金额已变化，请刷新后核对")
		}
	}
	return nil
}

func (a *App) saveStock(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if !u.can("warehouse") {
		return nil, fail(403, "无仓储操作权限")
	}
	var d struct {
		ID      string      `json:"id"`
		Version int64       `json:"version"`
		Kind    string      `json:"kind"`
		Date    string      `json:"date"`
		From    string      `json:"from"`
		To      string      `json:"to"`
		Note    string      `json:"note"`
		Handler string      `json:"handler"`
		Lines   []StockLine `json:"lines"`
	}
	if e := decode(b, &d); e != nil {
		return nil, e
	}
	if strings.TrimSpace(d.Handler) == "" || len([]rune(d.Handler)) > 100 || len([]rune(d.Note)) > 5000 {
		return nil, fail(400, "请填写经手人（最多100字），备注最多5000字")
	}
	var before any
	previousLines := map[string]StockLine{}
	if d.ID != "" {
		old, err := loadStockDocumentForUpdate(tx, d.ID)
		if err == sql.ErrNoRows {
			return nil, fail(404, "库存单不存在")
		}
		if err != nil {
			return nil, err
		}
		if old.Version != d.Version || old.Voided {
			return nil, fail(409, "单据已变更或已作废，请重新打开")
		}
		if old.Kind != d.Kind {
			return nil, fail(400, "不能修改单据类型")
		}
		before = old
		var saved []StockLine
		if old.LineItems == nil {
			return nil, fail(400, "旧单缺少完整明细，不能直接修改")
		}
		if e := json.Unmarshal([]byte(*old.LineItems), &saved); e != nil {
			return nil, fail(400, "旧单缺少完整明细，不能直接修改")
		}
		for _, l := range saved {
			previousLines[l.Product] = l
		}
	}
	if !contains([]string{"进货", "领料", "退料", "转料", "调拨", "报损", "报溢"}, d.Kind) {
		return nil, fail(400, "库存单据类型无效")
	}
	if _, e := time.Parse("2006-01-02", d.Date); e != nil {
		return nil, fail(400, "日期无效")
	}
	if contains([]string{"领料", "退料", "转料", "调拨"}, d.Kind) && (d.From == d.To || d.To == "" || d.From == "") {
		return nil, fail(400, "请选择不同的调出和调入仓库")
	}
	if contains([]string{"进货", "报溢"}, d.Kind) && d.To == "" || d.Kind == "报损" && d.From == "" {
		return nil, fail(400, "请选择材料所在仓库")
	}
	if contains([]string{"领料", "退料", "转料"}, d.Kind) {
		var fromWorker, toWorker sql.NullString
		if err := tx.QueryRow("SELECT worker_id FROM warehouses WHERE id=? AND active=1", d.From).Scan(&fromWorker); err != nil {
			return nil, fail(400, "调出位置不存在或已停用")
		}
		if err := tx.QueryRow("SELECT worker_id FROM warehouses WHERE id=? AND active=1", d.To).Scan(&toWorker); err != nil {
			return nil, fail(400, "调入位置不存在或已停用")
		}
		if d.Kind == "领料" && (fromWorker.Valid || !toWorker.Valid) ||
			d.Kind == "退料" && (!fromWorker.Valid || toWorker.Valid) ||
			d.Kind == "转料" && (!fromWorker.Valid || !toWorker.Valid) {
			return nil, fail(400, "领料、退料或转料的位置类型不符")
		}
	}
	if len(d.Lines) > 500 {
		return nil, fail(400, "商品明细过多")
	}
	productIDs := make([]string, 0, len(d.Lines)+len(previousLines))
	for _, l := range d.Lines {
		productIDs = append(productIDs, l.Product)
	}
	for productID := range previousLines {
		productIDs = append(productIDs, productID)
	}
	products, err := lockStockProducts(tx, productIDs)
	if err != nil {
		return nil, err
	}
	delta := map[string]int64{}
	totalAmount := new(big.Int)
	seen := map[string]bool{}
	activeLines := make([]StockLine, 0, len(d.Lines))
	for i := range d.Lines {
		l := &d.Lines[i]
		if len([]rune(l.Note)) > 1000 {
			return nil, fail(400, "明细备注最多1000字")
		}
		product := products[l.Product]
		l.Name, l.Unit, l.AuxUnit, l.AuxRatio = product.Name, product.Unit, product.AuxUnit, product.AuxRatio
		l.BaseFactor = big.NewInt(product.Definition.BaseFactor).String()
		l.AuxFactor = big.NewInt(product.Definition.AuxFactor).String()
		if old, ok := previousLines[l.Product]; ok && old.Unit != "" && old.AuxRatio != "" {
			// Preserve the conversion saved with the original document, including
			// historical documents made before product locking was introduced.
			l.Unit, l.AuxUnit, l.AuxRatio = old.Unit, old.AuxUnit, old.AuxRatio
		}
		legacyPriceInput := l.InputQuantity == "" && l.Total == 0 && l.Price > 0
		quantum, err := stockInputQuantum(l, product)
		if err != nil {
			return nil, err
		}
		if quantum == 0 {
			continue
		}
		if quantum < 0 || quantum > 1000000000000000 {
			return nil, fail(400, "商品数量无效")
		}
		l.QuantityQuantum = big.NewInt(quantum).String()
		l.Qty = roundedBaseMicro(quantum, product.Definition.BaseFactor)
		if seen[l.Product] {
			return nil, fail(400, "商品不能重复")
		}
		seen[l.Product] = true
		if old, ok := previousLines[l.Product]; ok && old.Name != "" {
			l.Name, l.Unit = old.Name, old.Unit
		}
		if d.Kind == "进货" {
			if legacyPriceInput {
				// Older clients submitted only a basic-unit price.
				amount := new(big.Int).Mul(big.NewInt(l.Qty), big.NewInt(l.Price))
				amount.Add(amount, big.NewInt(500000)).Quo(amount, big.NewInt(1000000))
				if !amount.IsInt64() {
					return nil, fail(400, "进货金额超出范围")
				}
				l.Total = amount.Int64()
			}
			if l.Total < 0 || l.Total > 100000000000000 {
				return nil, fail(400, "进货金额超出范围")
			}
			price := new(big.Int).Mul(big.NewInt(l.Total), big.NewInt(product.Definition.BaseFactor*productQuantityScale))
			price.Add(price, big.NewInt(quantum/2)).Quo(price, big.NewInt(quantum))
			if !price.IsInt64() {
				return nil, fail(400, "参考单价超出范围")
			}
			l.Price = price.Int64()
			totalAmount.Add(totalAmount, big.NewInt(l.Total))
		} else {
			l.Price, l.Total = 0, 0
		}
		switch d.Kind {
		case "进货", "报溢":
			delta[d.To+"|"+l.Product] += quantum
		case "报损":
			delta[d.From+"|"+l.Product] -= quantum
		case "调拨", "领料", "退料", "转料":
			delta[d.From+"|"+l.Product] -= quantum
			delta[d.To+"|"+l.Product] += quantum
		}
		activeLines = append(activeLines, *l)
	}
	d.Lines = activeLines
	if len(delta) == 0 {
		return nil, fail(400, "请填写至少一项数量")
	}
	if totalAmount.Cmp(big.NewInt(100000000000000)) > 0 {
		return nil, fail(400, "进货总额超出上限")
	}
	total := totalAmount.Int64()
	uid := d.ID
	if uid == "" {
		uid = id()
	} else {
		prior, e := stockMovementTotals(tx, uid)
		if e != nil {
			return nil, e
		}
		for _, p := range prior {
			delta[p.WarehouseID+"|"+p.ProductID] -= p.Quantity
		}
	}
	if e := applyStock(tx, u, key, d.Kind, uid, delta); e != nil {
		return nil, e
	}
	if d.Kind == "进货" {
		if e := updatePurchaseCosts(tx, previousLines, d.Lines, products); e != nil {
			return nil, e
		}
	}
	if d.ID == "" {
		if _, e := tx.Exec("INSERT INTO stock_documents(id,kind,work_date,source_id,destination_id,note,created_by,total_cents,line_items,handler) VALUES(?,?,?,?,?,?,?,?,?,?)", uid, d.Kind, d.Date, d.From, d.To, d.Note, u.ID, total, jsonText(d.Lines), d.Handler); e != nil {
			return nil, e
		}
	} else {
		if _, e := tx.Exec("UPDATE stock_documents SET work_date=?,source_id=?,destination_id=?,note=?,total_cents=?,line_items=?,handler=?,version=version+1 WHERE id=?", d.Date, d.From, d.To, d.Note, total, jsonText(d.Lines), d.Handler, uid); e != nil {
			return nil, e
		}
	}
	if e := audit(tx, u, d.Kind+"保存", uid, map[string]any{"before": before, "after": d}); e != nil {
		return nil, e
	}
	return map[string]any{"id": uid, "ok": true}, nil
}

func (a *App) stockState(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if !u.can("warehouse") {
		return nil, fail(403, "无仓储操作权限")
	}
	var p struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	}
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	if !contains([]string{"void", "restore"}, p.Action) {
		return nil, fail(400, "操作无效")
	}
	if p.Action == "void" && (strings.TrimSpace(p.Reason) == "" || len([]rune(p.Reason)) > 1000) {
		return nil, fail(400, "请填写作废原因（最多1000字）")
	}
	d, e := loadStockDocumentForUpdate(tx, p.ID)
	if e == sql.ErrNoRows {
		return nil, fail(404, "单据不存在")
	}
	if e != nil {
		return nil, e
	}
	if d.Version != p.Version || d.Voided != (p.Action == "restore") {
		return nil, fail(409, "单据状态已变化，请重新打开")
	}
	var lines []StockLine
	if d.LineItems == nil || json.Unmarshal([]byte(*d.LineItems), &lines) != nil || len(lines) == 0 {
		return nil, fail(400, "旧单缺少商品明细，无法自动处理")
	}
	productIDs := make([]string, 0, len(lines))
	for _, line := range lines {
		productIDs = append(productIDs, line.Product)
	}
	products, err := lockStockProducts(tx, productIDs)
	if err != nil {
		return nil, err
	}
	delta := map[string]int64{}
	if p.Action == "void" {
		prior, e := stockMovementTotals(tx, p.ID)
		if e != nil {
			return nil, e
		}
		for _, r := range prior {
			delta[r.WarehouseID+"|"+r.ProductID] = -r.Quantity
		}
	} else {
		for _, l := range lines {
			quantity, err := stockLineQuantum(l, products[l.Product])
			if err != nil {
				return nil, err
			}
			switch d.Kind {
			case "进货", "报溢":
				delta[d.DestinationID+"|"+l.Product] += quantity
			case "报损":
				delta[d.SourceID+"|"+l.Product] -= quantity
			case "调拨", "领料", "退料", "转料":
				delta[d.SourceID+"|"+l.Product] -= quantity
				delta[d.DestinationID+"|"+l.Product] += quantity
			}
		}
	}
	if e = applyStock(tx, u, key, "库存"+p.Action, p.ID, delta); e != nil {
		return nil, e
	}
	if d.Kind == "进货" {
		oldLines, newLines := map[string]StockLine{}, []StockLine{}
		if p.Action == "void" {
			for _, line := range lines {
				oldLines[line.Product] = line
			}
		} else {
			newLines = lines
		}
		if e = updatePurchaseCosts(tx, oldLines, newLines, products); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec("UPDATE stock_documents SET voided=?,void_reason=?,version=version+1 WHERE id=?", p.Action == "void", p.Reason, p.ID); e != nil {
		return nil, e
	}
	if e = audit(tx, u, "库存"+p.Action, p.ID, map[string]any{"before": d, "reason": p.Reason}); e != nil {
		return nil, e
	}
	return map[string]any{"ok": true}, nil
}
