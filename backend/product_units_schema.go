package main

import "database/sql"

func migrateProductUnitDefinitions(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS product_unit_definitions(
		product_id VARCHAR(64) PRIMARY KEY,base_unit VARCHAR(30) NOT NULL,aux_unit VARCHAR(30) NOT NULL,
		base_factor BIGINT NOT NULL,aux_factor BIGINT NOT NULL,quantity_scale INT NOT NULL DEFAULT 6,
		version BIGINT NOT NULL DEFAULT 1,
		CONSTRAINT chk_product_unit_factors CHECK(base_factor>0 AND aux_factor>0 AND quantity_scale=6),
		FOREIGN KEY(product_id) REFERENCES products(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=440").Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id,unit,aux_unit,CAST(aux_ratio AS CHAR) FROM products ORDER BY id")
	if err != nil {
		return err
	}
	type definitionInput struct{ id, base, aux, ratio string }
	var inputs []definitionInput
	for rows.Next() {
		var input definitionInput
		if err = rows.Scan(&input.id, &input.base, &input.aux, &input.ratio); err != nil {
			rows.Close()
			return err
		}
		inputs = append(inputs, input)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if err = syncProductUnitDefinition(tx, input.id, input.base, input.aux, input.ratio); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(440)"); err != nil {
		return err
	}
	return tx.Commit()
}
