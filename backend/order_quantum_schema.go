package main

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// orderSaleQuantumSnapshot is the persisted sale-unit snapshot from the
// pre-quantum order table.  It is deliberately validated against the current
// product definition before version 442 writes anything.  A sale row contains
// both the quantity consumed from stock (qty_micro) and the quantity shown to
// the customer (sale_qty_micro); either value being rounded differently would
// make the old row impossible to restore exactly.
type orderSaleQuantumSnapshot struct {
	ID              string
	QtyMicro        int64
	SaleQtyMicro    int64
	InputQty        string
	InputUnit       string
	ConversionRatio string
}

type orderMaterialQuantumSnapshot struct {
	ID              string
	QtyMicro        int64
	InputQty        string
	InputUnit       string
	ConversionRatio string
}

func exactOrderMaterialSnapshotQuantum(row orderMaterialQuantumSnapshot, definition productUnitDefinition) (int64, error) {
	if row.QtyMicro <= 0 {
		return 0, fmt.Errorf("material %q has non-positive quantity", row.ID)
	}
	baseQuantum := new(big.Int).Mul(big.NewInt(row.QtyMicro), big.NewInt(definition.BaseFactor))
	if !baseQuantum.IsInt64() {
		return 0, fmt.Errorf("material %q quantity overflows exact range", row.ID)
	}
	input := strings.TrimSpace(row.InputQty)
	if input == "" {
		return baseQuantum.Int64(), nil
	}
	unit := strings.TrimSpace(row.InputUnit)
	if unit != "base" && unit != "aux" {
		return 0, fmt.Errorf("material %q has no canonical input unit snapshot", row.ID)
	}
	parsedInput, ok := new(big.Rat).SetString(input)
	if !ok || parsedInput.Sign() <= 0 {
		return 0, fmt.Errorf("material %q has invalid input quantity snapshot", row.ID)
	}
	converted, err := definition.quantityQuantum(input, unit)
	if err != nil || converted != baseQuantum.Int64() {
		return 0, fmt.Errorf("material %q input quantity disagrees with saved stock quantity", row.ID)
	}
	savedRatio, ok := new(big.Rat).SetString(strings.TrimSpace(row.ConversionRatio))
	wantRatio := new(big.Rat).SetFrac(big.NewInt(definition.BaseFactor), big.NewInt(definition.AuxFactor))
	if !ok || savedRatio.Cmp(wantRatio) != 0 {
		return 0, fmt.Errorf("material %q conversion snapshot differs from current product units", row.ID)
	}
	return baseQuantum.Int64(), nil
}

func exactOrderSaleSnapshotQuantum(row orderSaleQuantumSnapshot, definition productUnitDefinition) (int64, error) {
	if row.QtyMicro <= 0 || row.SaleQtyMicro <= 0 {
		return 0, fmt.Errorf("sale %q has non-positive quantity", row.ID)
	}
	baseQuantum := new(big.Int).Mul(big.NewInt(row.QtyMicro), big.NewInt(definition.BaseFactor))
	if !baseQuantum.IsInt64() {
		return 0, fmt.Errorf("sale %q quantity overflows exact range", row.ID)
	}
	unit := strings.TrimSpace(row.InputUnit)
	if unit != "base" && unit != "aux" {
		return 0, fmt.Errorf("sale %q has no canonical input unit snapshot", row.ID)
	}
	input := strings.TrimSpace(row.InputQty)
	if input == "" {
		return 0, fmt.Errorf("sale %q has no input quantity snapshot", row.ID)
	}
	parsedInput, ok := new(big.Rat).SetString(input)
	if !ok || parsedInput.Sign() <= 0 {
		return 0, fmt.Errorf("sale %q has invalid input quantity snapshot", row.ID)
	}
	// The old sale amount was stored in millionths of the selected display
	// unit.  If it cannot be represented exactly, the old rounded value cannot
	// be reconstructed and migration must stop.
	saleQuantum := new(big.Rat).Mul(parsedInput, big.NewRat(productQuantityScale, 1))
	if !saleQuantum.IsInt() || !saleQuantum.Num().IsInt64() || saleQuantum.Num().Int64() != row.SaleQtyMicro {
		return 0, fmt.Errorf("sale %q input quantity disagrees with saved sale quantity", row.ID)
	}
	converted, err := definition.quantityQuantum(input, unit)
	if err != nil || converted != baseQuantum.Int64() {
		return 0, fmt.Errorf("sale %q input quantity disagrees with saved stock quantity", row.ID)
	}
	savedRatio, ok := new(big.Rat).SetString(strings.TrimSpace(row.ConversionRatio))
	wantRatio := new(big.Rat).SetFrac(big.NewInt(definition.BaseFactor), big.NewInt(definition.AuxFactor))
	if !ok || savedRatio.Cmp(wantRatio) != 0 {
		return 0, fmt.Errorf("sale %q conversion snapshot differs from current product units", row.ID)
	}
	return baseQuantum.Int64(), nil
}

// Version 442 converts saved order material/sale quantities after the stock
// ledger migration. Legacy micro columns remain for existing reports.
func migrateOrderQuantum(db *sql.DB) error {
	for _, column := range []struct{ table, name, definition string }{
		{"order_materials", "qty_quantum", "BIGINT NOT NULL DEFAULT 0"},
		{"order_materials", "input_qty", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"order_materials", "input_unit", "VARCHAR(10) NOT NULL DEFAULT 'base'"},
		{"order_materials", "conversion_ratio", "VARCHAR(100) NOT NULL DEFAULT '1'"},
		{"order_sales", "qty_quantum", "BIGINT NOT NULL DEFAULT 0"},
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
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=442").Scan(&done); err != nil || done != 0 {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"order_materials", "order_sales"} {
		var invalid int
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %s s LEFT JOIN product_unit_definitions d ON d.product_id=s.product_id
			WHERE d.product_id IS NULL OR s.qty_micro<=0 OR CAST(s.qty_micro AS DECIMAL(65,0))*d.base_factor>?`, table)
		if err = tx.QueryRow(query, math.MaxInt64).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("order quantum migration: %d %s rows have missing units or invalid quantity", invalid, table)
		}
	}
	// Sales already carry a customer-facing unit snapshot.  Validate every
	// populated row before updating qty_quantum so a changed product ratio or a
	// rounded legacy value cannot be silently reinterpreted during migration.
	rows, err := tx.Query(`SELECT s.id,s.qty_micro,s.sale_qty_micro,s.input_qty,s.input_unit,s.conversion_ratio,
		d.base_unit,d.aux_unit,d.base_factor,d.aux_factor
		FROM order_sales s JOIN product_unit_definitions d ON d.product_id=s.product_id
		ORDER BY s.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row orderSaleQuantumSnapshot
		var definition productUnitDefinition
		if err = rows.Scan(&row.ID, &row.QtyMicro, &row.SaleQtyMicro, &row.InputQty, &row.InputUnit, &row.ConversionRatio,
			&definition.BaseUnit, &definition.AuxUnit, &definition.BaseFactor, &definition.AuxFactor); err != nil {
			rows.Close()
			return err
		}
		if _, err = exactOrderSaleSnapshotQuantum(row, definition); err != nil {
			rows.Close()
			return fmt.Errorf("order quantum migration: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(`SELECT CONCAT(s.order_id,':',s.product_id,':',s.warehouse_id),s.qty_micro,s.input_qty,s.input_unit,s.conversion_ratio,
		d.base_unit,d.aux_unit,d.base_factor,d.aux_factor
		FROM order_materials s JOIN product_unit_definitions d ON d.product_id=s.product_id
		ORDER BY s.order_id,s.product_id,s.warehouse_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row orderMaterialQuantumSnapshot
		var definition productUnitDefinition
		if err = rows.Scan(&row.ID, &row.QtyMicro, &row.InputQty, &row.InputUnit, &row.ConversionRatio,
			&definition.BaseUnit, &definition.AuxUnit, &definition.BaseFactor, &definition.AuxFactor); err != nil {
			rows.Close()
			return err
		}
		if _, err = exactOrderMaterialSnapshotQuantum(row, definition); err != nil {
			rows.Close()
			return fmt.Errorf("order quantum migration: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err = tx.Exec(`UPDATE order_materials m JOIN product_unit_definitions d ON d.product_id=m.product_id
		JOIN products p ON p.id=m.product_id SET m.qty_quantum=m.qty_micro*d.base_factor,
		m.input_qty=CAST(CAST(m.qty_micro AS DECIMAL(24,6))/1000000 AS DECIMAL(24,6)),
		m.input_unit='base',m.conversion_ratio=CAST(p.aux_ratio AS CHAR)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE order_sales s JOIN product_unit_definitions d ON d.product_id=s.product_id
		SET s.qty_quantum=s.qty_micro*d.base_factor`); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(442)"); err != nil {
		return err
	}
	return tx.Commit()
}
