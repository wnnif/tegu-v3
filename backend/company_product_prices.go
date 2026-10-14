package main

import (
	"net/http"
	"sort"
	"strings"
)

type companyProductPrice struct {
	ID        string
	ProductID string
	InputUnit string
	PriceCents int64
}
type companyProductPriceOption struct {
	InputUnit string `json:"input_unit"`
	PriceCents int64 `json:"price_cents"`
}

type companyProductPricesResponse struct {
	CompanyID string                      `json:"company_id"`
	ProductID string                      `json:"product_id"`
	Prices    []companyProductPriceOption `json:"prices"`
}

func loadCompanyProductPrices(q rowQuery, companyID string, productIDs []string, lock bool) (map[string]map[string]companyProductPrice, error) {
	ids := make([]string, 0, len(productIDs))
	seen := make(map[string]bool, len(productIDs))
	for _, productID := range productIDs {
		productID = strings.TrimSpace(productID)
		if productID != "" && !seen[productID] {
			seen[productID] = true
			ids = append(ids, productID)
		}
	}
	prices := make(map[string]map[string]companyProductPrice, len(ids))
	if companyID == "" || len(ids) == 0 {
		return prices, nil
	}
	sort.Strings(ids)
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, companyID)
	for index, productID := range ids {
		placeholders[index] = "?"
		args = append(args, productID)
	}
	query := `SELECT id,product_id,input_unit,price_cents FROM company_product_prices WHERE company_id=? AND product_id IN (` + strings.Join(placeholders, ",") + `) ORDER BY product_id,input_unit`
	if lock {
		query += " FOR SHARE"
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var price companyProductPrice
		if err = rows.Scan(&price.ID, &price.ProductID, &price.InputUnit, &price.PriceCents); err != nil {
			return nil, err
		}
		if price.InputUnit != "base" && price.InputUnit != "aux" {
			return nil, fail(409, "公司商品固定价单位配置无效")
		}
		if prices[price.ProductID] == nil {
			prices[price.ProductID] = map[string]companyProductPrice{}
		}
		if _, exists := prices[price.ProductID][price.InputUnit]; exists {
			return nil, fail(409, "公司商品固定价单位重复，请核对初始化资料")
		}
		prices[price.ProductID][price.InputUnit] = price
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return prices, nil
}

func (a *App) companyProductPrices(w http.ResponseWriter, r *http.Request, u User) error {
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	query := r.URL.Query()
	companyID, productID := strings.TrimSpace(query.Get("company_id")), strings.TrimSpace(query.Get("product_id"))
	if companyID == "" || productID == "" || len(companyID) > 64 || len(productID) > 64 {
		return fail(400, "公司和商品必须有效选择")
	}
	prices, err := loadCompanyProductPrices(a.db, companyID, []string{productID}, false)
	if err != nil {
		return err
	}
	response := companyProductPricesResponse{CompanyID: companyID, ProductID: productID, Prices: []companyProductPriceOption{}}
	for _, inputUnit := range []string{"base", "aux"} {
		if price, found := prices[productID][inputUnit]; found {
			response.Prices = append(response.Prices, companyProductPriceOption{InputUnit: inputUnit, PriceCents: price.PriceCents})
		}
	}
	write(w, 200, response)
	return nil
}

func applyCompanyProductSalePrice(sale *Sale, old *Sale, configured map[string]companyProductPrice) error {
	unit := sale.InputUnit
	if unit == "" {
		unit = "base"
	}
	oldUnit := "base"
	if old != nil && old.InputUnit != "" {
		oldUnit = old.InputUnit
	}
	if old != nil && old.CompanyProductPriceID != "" && oldUnit == unit {
		sale.Price = old.Price
		sale.CompanyProductPriceID = old.CompanyProductPriceID
		return nil
	}
	if old != nil && old.CompanyProductPriceID == "" && oldUnit == unit {
		sale.CompanyProductPriceID = ""
		return nil
	}
	if price, found := configured[unit]; found {
		sale.Price = price.PriceCents
		sale.CompanyProductPriceID = price.ID
		return nil
	}
	if len(configured) > 0 {
		return fail(400, "该公司商品未配置所选计价单位的固定价格")
	}
	sale.CompanyProductPriceID = ""
	return nil
}
