package main

import "database/sql"

// The caller holds the application's migration lock. Each DDL step can resume
// after MySQL implicitly commits DDL; an existing business row is never regrouped.
func migrateOrderWorkflow(db *sql.DB) error {
	columns := []struct{ table, name, definition string }{
		{"orders", "delivery_wage_cents", "BIGINT NOT NULL DEFAULT 0"},
		{"orders", "ticket_managed", "BOOLEAN NOT NULL DEFAULT 0"},
		{"orders", "sales_ticket_slot", "INT NOT NULL DEFAULT 1"},
		{"order_lines", "subitem_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"order_lines", "sort_order", "INT NOT NULL DEFAULT 0"},
		{"order_lines", "use_included_price", "BOOLEAN NOT NULL DEFAULT 0"},
		{"order_lines", "included_by_craft_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"order_lines", "charge_note", "VARCHAR(300) NOT NULL DEFAULT ''"},
		{"projects", "ticket_mode_snapshot", "VARCHAR(20) NOT NULL DEFAULT ''"},
	}
	for i, c := range columns {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", c.table, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.name + " " + c.definition); err != nil {
				return err
			}
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 410+i); err != nil {
			return err
		}
	}
	tables := []string{
		`CREATE TABLE IF NOT EXISTS order_sales(id VARCHAR(64) PRIMARY KEY,order_id VARCHAR(64) NOT NULL,product_id VARCHAR(64) NOT NULL,warehouse_id VARCHAR(64) NOT NULL,qty_micro BIGINT NOT NULL,sale_qty_micro BIGINT NOT NULL,price_cents BIGINT NOT NULL,total_cents BIGINT NOT NULL,product_name VARCHAR(200) NOT NULL,unit VARCHAR(30) NOT NULL,price_unit VARCHAR(30) NOT NULL,input_unit VARCHAR(10) NOT NULL,input_qty VARCHAR(100) NOT NULL,sort_order INT NOT NULL DEFAULT 0,INDEX(order_id,sort_order,id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
		`CREATE TABLE IF NOT EXISTS project_craft_slots(project_id VARCHAR(64) NOT NULL,craft_id VARCHAR(64) NOT NULL,slot INT NOT NULL,included_by_craft_id VARCHAR(64) NOT NULL DEFAULT '',PRIMARY KEY(project_id,craft_id),FOREIGN KEY(project_id) REFERENCES projects(id))`,
		`CREATE TABLE IF NOT EXISTS project_tickets(id VARCHAR(64) PRIMARY KEY,project_id VARCHAR(64) NOT NULL,company_id VARCHAR(64) NOT NULL,number VARCHAR(80) NOT NULL UNIQUE,kind VARCHAR(20) NOT NULL,slot INT NULL,status VARCHAR(20) NOT NULL DEFAULT '草稿',total_cents BIGINT NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 1,ticket_no VARCHAR(100) NOT NULL DEFAULT '',delivered_at DATE NULL,recipient VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(project_id,slot),INDEX(company_id,status,created_at,id),FOREIGN KEY(project_id) REFERENCES projects(id))`,
		`CREATE TABLE IF NOT EXISTS project_ticket_items(item_id VARCHAR(64) PRIMARY KEY,ticket_id VARCHAR(64) NOT NULL,order_id VARCHAR(64) NOT NULL,item_kind VARCHAR(10) NOT NULL,INDEX(ticket_id,order_id,item_kind),INDEX(order_id),FOREIGN KEY(ticket_id) REFERENCES project_tickets(id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
		`CREATE TABLE IF NOT EXISTS project_ticket_versions(ticket_id VARCHAR(64) NOT NULL,version BIGINT NOT NULL,event VARCHAR(40) NOT NULL,snapshot JSON NOT NULL,created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(ticket_id,version),FOREIGN KEY(ticket_id) REFERENCES project_tickets(id))`,
		`CREATE TABLE IF NOT EXISTS project_ticket_receipts(id VARCHAR(64) PRIMARY KEY,ticket_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,received_at DATE NOT NULL,method VARCHAR(40) NOT NULL DEFAULT '',voucher_no VARCHAR(100) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,voided BOOLEAN NOT NULL DEFAULT 0,INDEX(ticket_id,received_at,id),FOREIGN KEY(ticket_id) REFERENCES project_tickets(id))`,
	}
	for i, ddl := range tables {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 419+i); err != nil {
			return err
		}
	}
	for i, c := range []struct{ table, name, definition string }{
		{"project_tickets", "company_name", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"project_tickets", "project_name", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"project_tickets", "address", "VARCHAR(300) NOT NULL DEFAULT ''"},
		{"order_sales", "conversion_ratio", "VARCHAR(100) NOT NULL DEFAULT '1'"},
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", c.table, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.name + " " + c.definition); err != nil {
				return err
			}
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 425+i); err != nil {
			return err
		}
	}
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=429").Scan(&done); err != nil {
		return err
	}
	if done == 0 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Only an interrupted feature rollout can have tickets without snapshots.
		// Earlier project/company names cannot be reconstructed from current rows.
		if _, err = tx.Exec(`UPDATE project_tickets t JOIN projects p ON p.id=t.project_id JOIN companies c ON c.id=t.company_id SET t.company_name=IF(t.company_name='',c.name,t.company_name),t.project_name=IF(t.project_name='',p.name,t.project_name),t.address=IF(t.address='',p.address,t.address) WHERE t.company_name='' OR t.project_name='' OR t.address=''`); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(429)"); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
