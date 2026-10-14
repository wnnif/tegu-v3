package main

import "database/sql"

// Version 443 adds actual-line material attribution and explicit charge/wage
// snapshots. Existing totals, quantities, workers and material rows are untouched.
func migrateOrderPhase3(db *sql.DB) error {
	for _, column := range []struct{ table, name, definition string }{
		{"order_lines", "charge_mode", "VARCHAR(10) NOT NULL DEFAULT 'normal'"},
		{"order_lines", "wage_override_cents", "BIGINT NULL"},
		{"order_lines", "wage_adjustment_reason", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"order_materials", "line_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
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
	var primary string
	if err := db.QueryRow("SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_materials' AND INDEX_NAME='PRIMARY'").Scan(&primary); err != nil {
		return err
	}
	if primary != "order_id,line_id,product_id,warehouse_id" {
		if _, err := db.Exec("ALTER TABLE order_materials DROP PRIMARY KEY, ADD PRIMARY KEY(order_id,line_id,product_id,warehouse_id)"); err != nil {
			return err
		}
	}
	// No line foreign key: public consumables use the empty ID and adjustment
	// history must survive deletion of an editable, unpaid line.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS order_line_wage_adjustments(
		id VARCHAR(64) PRIMARY KEY,
		order_id VARCHAR(64) NOT NULL,
		line_id VARCHAR(64) NOT NULL,
		before_cents BIGINT NOT NULL,
		after_cents BIGINT NOT NULL,
		standard_cents BIGINT NOT NULL,
		worker_id VARCHAR(64) NOT NULL,
		worker_name VARCHAR(100) NOT NULL,
		reason VARCHAR(1000) NOT NULL,
		actor_id VARCHAR(64) NOT NULL,
		actor_name VARCHAR(100) NOT NULL,
		operation_id VARCHAR(100) NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY wage_adjustment_operation(operation_id,line_id),
		INDEX(order_id,created_at,id), INDEX(line_id,created_at,id),
		FOREIGN KEY(order_id) REFERENCES orders(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(443)")
	return err
}
