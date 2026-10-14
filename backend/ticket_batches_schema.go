package main

import "database/sql"

// Migration 444 adds the company-level paper delivery record. Batch items are
// immutable row snapshots; only the nullable pointers on actual rows describe
// their current active batch claim.
func migrateTicketBatches(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS ticket_batches(id VARCHAR(64) PRIMARY KEY,company_id VARCHAR(64) NOT NULL,company_name VARCHAR(200) NOT NULL,number VARCHAR(80) NOT NULL UNIQUE,ticket_no VARCHAR(100) NOT NULL DEFAULT '',delivered_at DATE NOT NULL,recipient VARCHAR(100) NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'delivered',version BIGINT NOT NULL DEFAULT 1,total_cents BIGINT NOT NULL DEFAULT 0,created_by VARCHAR(64) NOT NULL,created_by_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,void_reason VARCHAR(1000) NOT NULL DEFAULT '',voided_by VARCHAR(64) NOT NULL DEFAULT '',voided_by_name VARCHAR(100) NOT NULL DEFAULT '',voided_at DATETIME NULL,INDEX(company_id,delivered_at,id),INDEX(company_id,status,delivered_at,id))`,
		`CREATE TABLE IF NOT EXISTS ticket_batch_items(batch_id VARCHAR(64) NOT NULL,item_kind VARCHAR(10) NOT NULL,item_id VARCHAR(64) NOT NULL,order_id VARCHAR(64) NOT NULL,project_id VARCHAR(64) NOT NULL,source_project_ticket_id VARCHAR(64) NOT NULL DEFAULT '',craft_id VARCHAR(64) NOT NULL DEFAULT '',subitem_id VARCHAR(64) NOT NULL DEFAULT '',total_cents BIGINT NOT NULL,quantity DECIMAL(18,6) NOT NULL,unit VARCHAR(30) NOT NULL DEFAULT '',item_name VARCHAR(200) NOT NULL DEFAULT '',subitem_name VARCHAR(200) NOT NULL DEFAULT '',location VARCHAR(200) NOT NULL DEFAULT '',worker_name VARCHAR(100) NOT NULL DEFAULT '',snapshot JSON NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(batch_id,item_kind,item_id),INDEX(item_kind,item_id),INDEX(project_id,order_id,item_kind,item_id),FOREIGN KEY(batch_id) REFERENCES ticket_batches(id))`,
		`CREATE TABLE IF NOT EXISTS ticket_batch_versions(batch_id VARCHAR(64) NOT NULL,version BIGINT NOT NULL,event VARCHAR(40) NOT NULL,snapshot JSON NOT NULL,actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(batch_id,version),FOREIGN KEY(batch_id) REFERENCES ticket_batches(id))`,
	} {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
	}
	for _, column := range []struct{ table, name, definition string }{
		{"order_lines", "ticket_batch_id", "VARCHAR(64) NULL"},
		{"order_sales", "ticket_batch_id", "VARCHAR(64) NULL"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	for _, index := range []struct{ table, name string }{
		{"order_lines", "idx_order_lines_ticket_batch"},
		{"order_sales", "idx_order_sales_ticket_batch"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?", index.table, index.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec("CREATE INDEX " + index.name + " ON " + index.table + "(ticket_batch_id)"); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(444)")
	return err
}
