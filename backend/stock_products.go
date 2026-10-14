package main

import (
	"database/sql"
	"sort"
)

type stockProduct struct {
	Name, Unit, AuxUnit, AuxRatio string
	Active                        bool
	Definition                    productUnitDefinition
}

// Lock every affected product in one order before reading conversion rates or
// changing balances. Product maintenance takes the exclusive form of this lock,
// so a first movement and a unit change cannot both use stale metadata.
func lockStockProducts(tx *sql.Tx, productIDs []string) (map[string]stockProduct, error) {
	unique := map[string]bool{}
	for _, productID := range productIDs {
		unique[productID] = true
	}
	keys := make([]string, 0, len(unique))
	for productID := range unique {
		keys = append(keys, productID)
	}
	sort.Strings(keys)
	products := make(map[string]stockProduct, len(keys))
	for _, productID := range keys {
		var product stockProduct
		if err := tx.QueryRow("SELECT name,unit,aux_unit,aux_ratio,active FROM products WHERE id=? FOR SHARE", productID).Scan(&product.Name, &product.Unit, &product.AuxUnit, &product.AuxRatio, &product.Active); err != nil {
			if err == sql.ErrNoRows {
				return nil, fail(400, "商品不存在")
			}
			return nil, err
		}
		definition, err := newProductUnitDefinition(product.Unit, product.AuxUnit, product.AuxRatio)
		if err != nil {
			return nil, err
		}
		product.Definition = definition
		products[productID] = product
	}
	return products, nil
}

// Existing short IDs remain readable and unchanged. Longer valid API keys keep
// their user namespace without exceeding stock_movements.operation_id.
func stockOperationID(userID, key string) string {
	legacy := userID + ":" + key
	if len(legacy) <= 100 {
		return legacy
	}
	// The digest format contains no colon, so it cannot collide with any
	// historical userID:key representation, even for imported user IDs.
	return "v2-" + hash([]byte(jsonText([]string{userID, key})))
}
