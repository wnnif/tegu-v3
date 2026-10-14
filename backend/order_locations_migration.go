package main

import "database/sql"

// Schema 435/436 runs under initSchema's existing database-scoped migration lock.
// Nullable columns keep old rows and snapshots unchanged. DDL can resume after
// an interrupted step; no historical text, quantities or amounts are backfilled.
func migrateOrderLocations(db *sql.DB) error {
	for i, name := range []string{"subitem_name", "actual_location"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_lines' AND COLUMN_NAME=?", name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec("ALTER TABLE order_lines ADD COLUMN " + name + " VARCHAR(200) NULL"); err != nil {
				return err
			}
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 435+i); err != nil {
			return err
		}
	}
	return nil
}
