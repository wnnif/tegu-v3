package main

import "database/sql"

// Migration 439 keeps the current public/company configuration boundary and
// snapshots. The functional key is NULL for public rows and is invisible to
// SELECT * snapshots, unlike an explicit generated column.
func migratePublicCraftFoundation(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='crafts' AND COLUMN_NAME='display_order'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE crafts ADD COLUMN display_order INT NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	for _, index := range []struct{ name, definition string }{
		{"idx_company_public_craft", "CREATE UNIQUE INDEX idx_company_public_craft ON crafts ((NULLIF(company_id,'')),source_id)"},
		{"idx_craft_display_order", "CREATE INDEX idx_craft_display_order ON crafts (company_id,display_order,id)"},
	} {
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='crafts' AND INDEX_NAME=?", index.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec(index.definition); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(439)")
	return err
}
