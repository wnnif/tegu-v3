package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// A legacy auxiliary input can already have lost precision when the old
// application rounded it to base micro units. Refuse that ambiguous state
// instead of quietly treating the rounded quantity as an exact V3 quantity.
func inventoryMigrationLineQuantum(line StockLine, definition productUnitDefinition) (*big.Int, error) {
	if line.Qty < 0 || line.Total < 0 {
		return nil, fmt.Errorf("stock line has negative quantity or amount")
	}
	if line.Unit != "" && line.Unit != definition.BaseUnit || line.AuxUnit != "" && line.AuxUnit != definition.AuxUnit {
		return nil, fmt.Errorf("stock line unit snapshot differs from current product")
	}
	if line.AuxRatio != "" {
		ratio, ok := new(big.Rat).SetString(line.AuxRatio)
		if !ok || ratio.Cmp(big.NewRat(definition.BaseFactor, definition.AuxFactor)) != 0 {
			return nil, fmt.Errorf("stock line conversion snapshot differs from current product")
		}
	}
	for _, factor := range []struct {
		value string
		want  int64
	}{{line.BaseFactor, definition.BaseFactor}, {line.AuxFactor, definition.AuxFactor}} {
		if factor.value != "" {
			value, err := strconv.ParseInt(factor.value, 10, 64)
			if err != nil || value != factor.want {
				return nil, fmt.Errorf("stock line factor snapshot differs from current product")
			}
		}
	}
	quantity := new(big.Int).Mul(big.NewInt(line.Qty), big.NewInt(definition.BaseFactor))
	if !quantity.IsInt64() {
		return nil, fmt.Errorf("stock line exact quantity overflows")
	}
	if line.QuantityQuantum != "" {
		parsed, ok := new(big.Int).SetString(line.QuantityQuantum, 10)
		if !ok || !parsed.IsInt64() || parsed.Sign() < 0 || roundedBaseMicro(parsed.Int64(), definition.BaseFactor) != line.Qty {
			return nil, fmt.Errorf("stock line exact quantity is inconsistent with its saved base quantity")
		}
		quantity = parsed
	}
	input := strings.TrimSpace(line.InputQuantity)
	if input == "" {
		input = strings.TrimSpace(line.InputQty)
	}
	if input != "" {
		unit := line.InputUnit
		if unit == definition.BaseUnit {
			unit = "base"
		} else if unit == definition.AuxUnit && definition.AuxUnit != "" {
			unit = "aux"
		}
		exact, err := definition.quantityQuantum(input, unit)
		if err != nil || quantity.Cmp(big.NewInt(exact)) != 0 {
			return nil, fmt.Errorf("stock line input cannot be exactly reconciled with saved inventory")
		}
	}
	return quantity, nil
}

// Version 441 preserves legacy base-micro columns and makes quantum columns
// authoritative. Its data conversion and version marker commit together.
func migrateInventoryQuantum(db *sql.DB) error {
	for _, column := range []struct{ table, name, definition string }{
		{"stock_balances", "qty_quantum", "BIGINT NOT NULL DEFAULT 0"},
		{"stock_movements", "delta_quantum", "BIGINT NOT NULL DEFAULT 0"},
	} {
		var found int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", column.table, column.name).Scan(&found); err != nil {
			return err
		}
		if found == 0 {
			if _, err := db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS product_purchase_costs(
		product_id VARCHAR(64) PRIMARY KEY,
		purchased_quantum DECIMAL(65,0) NOT NULL DEFAULT 0,
		purchase_cents DECIMAL(65,0) NOT NULL DEFAULT 0,
		FOREIGN KEY(product_id) REFERENCES products(id),
		CHECK(purchased_quantum>=0 AND purchase_cents>=0)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=441").Scan(&done); err != nil || done != 0 {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, check := range []struct{ table, quantity string }{
		{"stock_balances", "qty_micro"}, {"stock_movements", "delta_micro"},
	} {
		var unsafe int
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %s s LEFT JOIN product_unit_definitions d ON d.product_id=s.product_id
			WHERE d.product_id IS NULL OR ABS(CAST(s.%s AS DECIMAL(65,0))*d.base_factor)>?`, check.table, check.quantity)
		if err = tx.QueryRow(query, math.MaxInt64).Scan(&unsafe); err != nil {
			return err
		}
		if unsafe != 0 {
			return fmt.Errorf("inventory quantum migration: %d %s rows have missing units or overflow", unsafe, check.table)
		}
	}
	if _, err = tx.Exec(`UPDATE stock_balances s JOIN product_unit_definitions d ON d.product_id=s.product_id
		SET s.qty_quantum=CAST(s.qty_micro AS DECIMAL(65,0))*d.base_factor`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE stock_movements s JOIN product_unit_definitions d ON d.product_id=s.product_id
		SET s.delta_quantum=CAST(s.delta_micro AS DECIMAL(65,0))*d.base_factor`); err != nil {
		return err
	}
	definitions := map[string]productUnitDefinition{}
	factorRows, err := tx.Query("SELECT product_id,base_unit,aux_unit,base_factor,aux_factor FROM product_unit_definitions")
	if err != nil {
		return err
	}
	for factorRows.Next() {
		var productID string
		var definition productUnitDefinition
		if err = factorRows.Scan(&productID, &definition.BaseUnit, &definition.AuxUnit, &definition.BaseFactor, &definition.AuxFactor); err != nil {
			break
		}
		definitions[productID] = definition
	}
	factorErr := factorRows.Err()
	factorRows.Close()
	if err != nil {
		return err
	}
	if factorErr != nil {
		return factorErr
	}
	rows, err := tx.Query(`SELECT id,kind,voided,line_items FROM stock_documents ORDER BY id`)
	if err != nil {
		return err
	}
	costs := map[string]struct{ quantity, cents *big.Int }{}
	for rows.Next() {
		var documentID, kind string
		var voided bool
		var raw sql.NullString
		if err = rows.Scan(&documentID, &kind, &voided, &raw); err != nil {
			break
		}
		if !raw.Valid {
			if kind == "进货" && !voided {
				err = fmt.Errorf("purchase document %q without lines cannot be valued", documentID)
				break
			}
			continue
		}
		var lines []StockLine
		if err = json.Unmarshal([]byte(raw.String), &lines); err != nil {
			break
		}
		for _, line := range lines {
			definition, found := definitions[line.Product]
			if !found || definition.BaseFactor <= 0 || definition.AuxFactor <= 0 {
				err = fmt.Errorf("stock document %q references product %q without valid units", documentID, line.Product)
				break
			}
			quantity, quantityErr := inventoryMigrationLineQuantum(line, definition)
			if quantityErr != nil {
				err = fmt.Errorf("inventory quantum migration: document %q product %q: %w", documentID, line.Product, quantityErr)
				break
			}
			if kind != "进货" || voided {
				continue
			}
			total := costs[line.Product]
			if total.quantity == nil {
				total.quantity, total.cents = new(big.Int), new(big.Int)
			}
			total.quantity.Add(total.quantity, quantity)
			total.cents.Add(total.cents, big.NewInt(line.Total))
			costs[line.Product] = total
		}
		if err != nil {
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	for productID, total := range costs {
		if _, err = tx.Exec("INSERT INTO product_purchase_costs(product_id,purchased_quantum,purchase_cents) VALUES(?,?,?)", productID, total.quantity.String(), total.cents.String()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(441)"); err != nil {
		return err
	}
	return tx.Commit()
}
