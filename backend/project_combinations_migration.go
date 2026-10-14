package main

import "database/sql"

// Version 437 is additive. It never translates included_by_craft_id, recalculates
// historical prices/wages, or grants permissions. Safe to resume after DDL failure.
func migrateProjectCombinations(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='projects' AND COLUMN_NAME='combination_version'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE projects ADD COLUMN combination_version BIGINT NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS project_combinations(
        id VARCHAR(64) PRIMARY KEY,
        project_id VARCHAR(64) NOT NULL,
        waterproof_line_id VARCHAR(64) NOT NULL,
        polypropylene_line_id VARCHAR(64) NOT NULL,
        waive_direction VARCHAR(20) NOT NULL,
        waived_qty_micro BIGINT NOT NULL,
        version BIGINT NOT NULL DEFAULT 1,
        created_by VARCHAR(64) NOT NULL,
        updated_by VARCHAR(64) NOT NULL,
        created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        UNIQUE KEY combination_pair(project_id,waterproof_line_id,polypropylene_line_id),
        INDEX(waterproof_line_id), INDEX(polypropylene_line_id),
        CONSTRAINT combo_project_fk FOREIGN KEY(project_id) REFERENCES projects(id),
        CONSTRAINT combo_waterproof_fk FOREIGN KEY(waterproof_line_id) REFERENCES order_lines(id),
        CONSTRAINT combo_polypropylene_fk FOREIGN KEY(polypropylene_line_id) REFERENCES order_lines(id),
        CONSTRAINT combo_direction_check CHECK(waive_direction IN ('waterproof','polypropylene')),
        CONSTRAINT combo_area_check CHECK(waived_qty_micro>0 AND waived_qty_micro<=1000000000000)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(437)")
	return err
}
