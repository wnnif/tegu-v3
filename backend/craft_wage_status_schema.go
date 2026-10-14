package main

import "database/sql"

const craftWageStatusMigrationVersion = 457

// Missing standard rates are explicit; existing catalog and order snapshots
// keep their historical interpretation as configured.
func migrateCraftWageStatus(db *sql.DB) error {
	for _, column := range []struct{ table, name string }{
		{"crafts", "wage_configured"},
		{"order_lines", "wage_configured"},
		{"order_worker_wages", "wage_configured"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " BOOLEAN NOT NULL DEFAULT 1"); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", craftWageStatusMigrationVersion)
	return err
}
