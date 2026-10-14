package main

import (
	"context"
	"database/sql"
	"fmt"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS users(id VARCHAR(64) PRIMARY KEY, username VARCHAR(100) NOT NULL UNIQUE, name VARCHAR(100) NOT NULL, password_hash VARCHAR(255) NOT NULL, role VARCHAR(20) NOT NULL, permissions JSON NOT NULL, crafts JSON NOT NULL, active BOOLEAN NOT NULL DEFAULT 1, version BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS sessions(token CHAR(64) PRIMARY KEY,user_id VARCHAR(64) NOT NULL,expires_at DATETIME NOT NULL,INDEX(expires_at))`,
	`CREATE TABLE IF NOT EXISTS companies(id VARCHAR(64) PRIMARY KEY,name VARCHAR(200) NOT NULL,contact VARCHAR(100) NOT NULL DEFAULT '',phone VARCHAR(60) NOT NULL DEFAULT '',version BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS projects(id VARCHAR(64) PRIMARY KEY,number VARCHAR(80) NOT NULL UNIQUE,company_id VARCHAR(64) NOT NULL,name VARCHAR(200) NOT NULL,address VARCHAR(300) NOT NULL,status VARCHAR(20) NOT NULL DEFAULT '进行中',created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(company_id,status,id))`,
	`CREATE TABLE IF NOT EXISTS workers(id VARCHAR(64) PRIMARY KEY,name VARCHAR(100) NOT NULL,phone VARCHAR(60) NOT NULL DEFAULT '',version BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS warehouses(id VARCHAR(64) PRIMARY KEY,name VARCHAR(100) NOT NULL,worker_id VARCHAR(64) NULL UNIQUE,active BOOLEAN NOT NULL DEFAULT 1,version BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS products(id VARCHAR(64) PRIMARY KEY,name VARCHAR(200) NOT NULL,unit VARCHAR(30) NOT NULL,aux_unit VARCHAR(30) NOT NULL DEFAULT '',aux_ratio DECIMAL(18,6) NOT NULL DEFAULT 1,active BOOLEAN NOT NULL DEFAULT 1,version BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS crafts(id VARCHAR(64) PRIMARY KEY,company_id VARCHAR(64) NOT NULL DEFAULT '',category VARCHAR(30) NOT NULL,name VARCHAR(200) NOT NULL,mode VARCHAR(20) NOT NULL,price_cents BIGINT NOT NULL,wage_cents BIGINT NOT NULL,active BOOLEAN NOT NULL DEFAULT 1,version BIGINT NOT NULL DEFAULT 1,INDEX(company_id,category))`,
	`CREATE TABLE IF NOT EXISTS orders(id VARCHAR(64) PRIMARY KEY,number VARCHAR(80) NOT NULL UNIQUE,company_id VARCHAR(64) NOT NULL,address VARCHAR(300) NOT NULL,work_date DATE NOT NULL,worker_id VARCHAR(64) NOT NULL DEFAULT '',warehouse_id VARCHAR(64) NOT NULL DEFAULT '',contact VARCHAR(100) NOT NULL DEFAULT '',note TEXT NOT NULL,status VARCHAR(20) NOT NULL DEFAULT '进行中',wage_paid BOOLEAN NOT NULL DEFAULT 0,total_cents BIGINT NOT NULL DEFAULT 0,wage_cents BIGINT NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 1,created_by VARCHAR(64) NOT NULL,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(company_id,work_date,id),INDEX(work_date,id),INDEX(worker_id,wage_paid,work_date))`,
	`CREATE TABLE IF NOT EXISTS order_lines(id VARCHAR(64) PRIMARY KEY,order_id VARCHAR(64) NOT NULL,craft_id VARCHAR(64) NOT NULL DEFAULT '',category VARCHAR(30) NOT NULL,name VARCHAR(200) NOT NULL,mode VARCHAR(20) NOT NULL,segments JSON NOT NULL,qty_micro BIGINT NOT NULL,price_cents BIGINT NOT NULL,wage_cents BIGINT NOT NULL,total_cents BIGINT NOT NULL,labor_cents BIGINT NOT NULL,temporary BOOLEAN NOT NULL,INDEX(order_id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
	`CREATE TABLE IF NOT EXISTS order_materials(order_id VARCHAR(64) NOT NULL,product_id VARCHAR(64) NOT NULL,warehouse_id VARCHAR(64) NOT NULL,qty_micro BIGINT NOT NULL,PRIMARY KEY(order_id,product_id,warehouse_id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
	`CREATE TABLE IF NOT EXISTS stock_balances(warehouse_id VARCHAR(64) NOT NULL,product_id VARCHAR(64) NOT NULL,qty_micro BIGINT NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(warehouse_id,product_id))`,
	`CREATE TABLE IF NOT EXISTS stock_documents(id VARCHAR(64) PRIMARY KEY,kind VARCHAR(20) NOT NULL,work_date DATE NOT NULL,source_id VARCHAR(64) NOT NULL DEFAULT '',destination_id VARCHAR(64) NOT NULL DEFAULT '',note TEXT NOT NULL,created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS stock_movements(id BIGINT AUTO_INCREMENT PRIMARY KEY,operation_id VARCHAR(100) NOT NULL,warehouse_id VARCHAR(64) NOT NULL,product_id VARCHAR(64) NOT NULL,delta_micro BIGINT NOT NULL,source_type VARCHAR(30) NOT NULL,source_id VARCHAR(64) NOT NULL,actor_id VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(operation_id,warehouse_id,product_id),INDEX(warehouse_id,product_id,id),INDEX(source_id))`,
	`CREATE TABLE IF NOT EXISTS settlements(id BIGINT AUTO_INCREMENT PRIMARY KEY,order_id VARCHAR(64) NOT NULL,kind VARCHAR(30) NOT NULL,amount_cents BIGINT NOT NULL,actor_id VARCHAR(64) NOT NULL,operation_id VARCHAR(100) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(operation_id,order_id,kind))`,
	`CREATE TABLE IF NOT EXISTS payment_receipts(id VARCHAR(64) PRIMARY KEY,order_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,received_at DATE NOT NULL,method VARCHAR(40) NOT NULL DEFAULT '',voucher_no VARCHAR(100) NOT NULL DEFAULT '',attachment VARCHAR(500) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,voided BOOLEAN NOT NULL DEFAULT 0,INDEX(order_id,received_at,id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
	`CREATE TABLE IF NOT EXISTS attachment_files(id CHAR(32) PRIMARY KEY,order_id VARCHAR(64) NOT NULL,kind VARCHAR(20) NOT NULL,file_name VARCHAR(200) NOT NULL,content_type VARCHAR(40) NOT NULL,size_bytes INT NOT NULL,content_hash CHAR(64) NOT NULL,content_data MEDIUMTEXT NOT NULL,created_by VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(order_id,created_at,id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
	`CREATE TABLE IF NOT EXISTS requests(user_id VARCHAR(64) NOT NULL,request_key VARCHAR(100) NOT NULL,body_hash CHAR(64) NOT NULL,response JSON NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(user_id,request_key))`,
	`CREATE TABLE IF NOT EXISTS audit(id BIGINT AUTO_INCREMENT PRIMARY KEY,actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL,action VARCHAR(100) NOT NULL,target_id VARCHAR(100) NOT NULL,detail JSON NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(actor_id,id))`,
	`CREATE TABLE IF NOT EXISTS archive_runs(id VARCHAR(64) PRIMARY KEY,month CHAR(7) NOT NULL,automatic BOOLEAN NOT NULL,manifest JSON NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(month))`,
	`CREATE TABLE IF NOT EXISTS backup_runs(id VARCHAR(64) PRIMARY KEY,file VARCHAR(255) NOT NULL,status VARCHAR(20) NOT NULL,message TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS maintenance_jobs(id VARCHAR(64) PRIMARY KEY,kind VARCHAR(20) NOT NULL,actor_id VARCHAR(64) NOT NULL,request_hash CHAR(64) NOT NULL,active_key VARCHAR(20) NULL UNIQUE,status VARCHAR(20) NOT NULL,stage VARCHAR(100) NOT NULL,progress INT NOT NULL DEFAULT 0,total INT NOT NULL DEFAULT 0,message VARCHAR(1000) NOT NULL DEFAULT '',result JSON NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(actor_id,kind,created_at,id),INDEX(status))`,
	`CREATE TABLE IF NOT EXISTS settings(setting_key VARCHAR(100) PRIMARY KEY,value JSON NOT NULL)`,
}

var backupTables = []string{"users", "companies", "catalog_seed_imports", "catalog_seed_provenance", "catalog_seed_packages", "catalog_seed_package_coverage", "projects", "workers", "payroll_bonus_worker_locks", "warehouses", "products", "product_unit_definitions", "product_purchase_costs", "company_product_prices", "crafts", "company_wage_overrides", "orders", "order_lines", "order_line_wage_adjustments", "order_worker_wages", "wage_settlements", "order_materials", "order_sales", "project_craft_slots", "project_combinations", "project_tickets", "project_packages", "project_ticket_items", "project_ticket_versions", "project_ticket_receipts", "company_receipts", "company_receipt_versions", "customer_discounts", "customer_discount_versions", "ticket_batches", "ticket_batch_items", "ticket_batch_versions", "stock_balances", "stock_documents", "stock_movements", "settlements", "payment_receipts", "attachment_files", "requests", "audit", "archive_runs", "backup_runs", "maintenance_jobs", "settings", "payroll_adjustments", "payroll_deductions", "payroll_advances", "payroll_settlements", "payroll_settlement_sources", "payroll_payments", "payroll_history", "payroll_bonus_entries", "payroll_bonus_settlements", "payroll_bonus_payments", "expense_claims", "expense_advances", "expense_advance_allocations", "expense_payments", "expense_advance_returns", "expense_history"}

func initSchema(db *sql.DB) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(context.Background(), "SELECT GET_LOCK(CONCAT(DATABASE(),':migrations'),30)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("database migration lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':migrations'))")
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations(version INT PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB"); err != nil {
		return err
	}
	for _, s := range schema {
		if _, e := db.Exec(s + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); e != nil {
			return e
		}
	}
	if _, err = db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(100)"); err != nil {
		return err
	}
	// IDs are 200+index. Append new columns; never reorder existing entries.
	for index, column := range []struct{ table, name, definition string }{
		{"users", "deleted", "BOOLEAN NOT NULL DEFAULT 0"},
		{"companies", "customer_type", "VARCHAR(100) NOT NULL DEFAULT '装修公司'"},
		{"companies", "note", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"crafts", "spec", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"orders", "phone", "VARCHAR(60) NOT NULL DEFAULT ''"},
		{"orders", "company_name", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"orders", "worker_name", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"order_lines", "spec", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"order_lines", "region", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"order_lines", "description", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"stock_documents", "handler", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"products", "color", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"products", "weight_kg", "DECIMAL(18,6) NOT NULL DEFAULT 0"},
		{"products", "brand", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"products", "spec", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"products", "note", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"warehouses", "category", "VARCHAR(100) NOT NULL DEFAULT '默认分类'"},
		{"warehouses", "warehouse_type", "VARCHAR(30) NOT NULL DEFAULT '普通仓库'"},
		{"warehouses", "note", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"stock_documents", "total_cents", "BIGINT NULL"},
		{"stock_documents", "line_items", "JSON NULL"},
		{"stock_documents", "version", "BIGINT NOT NULL DEFAULT 1"},
		{"stock_documents", "voided", "BOOLEAN NOT NULL DEFAULT 0"},
		{"stock_documents", "void_reason", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"order_materials", "product_name", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"order_materials", "unit", "VARCHAR(30) NOT NULL DEFAULT ''"},
		{"order_materials", "warehouse_name", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"workers", "user_id", "VARCHAR(64) NULL UNIQUE"},
		{"workers", "crafts", "JSON NOT NULL DEFAULT (JSON_ARRAY())"},
		{"orders", "project_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"orders", "ticket_no", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"orders", "delivered_at", "DATE NULL"},
		{"orders", "recipient", "VARCHAR(100) NOT NULL DEFAULT ''"},
		{"orders", "ticket_attachment", "VARCHAR(500) NOT NULL DEFAULT ''"},
		{"orders", "return_note", "VARCHAR(1000) NOT NULL DEFAULT ''"},
		{"projects", "status", "VARCHAR(20) NOT NULL DEFAULT '进行中'"},
		{"projects", "updated_at", "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP"},
		{"projects", "version", "BIGINT NOT NULL DEFAULT 1"},
		{"orders", "project_name", "VARCHAR(200) NOT NULL DEFAULT ''"},
		{"orders", "project_number", "VARCHAR(80) NOT NULL DEFAULT ''"},
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
		if _, err = db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 200+index); err != nil {
			return err
		}
	}
	if err := migrateVersions(db); err != nil {
		return err
	}
	if err := migrateProjectSnapshots(db); err != nil {
		return err
	}
	if _, err = db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(301)"); err != nil {
		return err
	}
	if _, err = db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(302)"); err != nil {
		return err
	}
	if err = migrateCraftConfiguration(db); err != nil {
		return err
	}
	if err = migrateOrderWorkflow(db); err != nil {
		return err
	}
	if err = migrateOrderWorkers(db); err != nil {
		return err
	}
	if err = migrateOrderLocations(db); err != nil {
		return err
	}
	if err = migrateProjectCombinations(db); err != nil {
		return err
	}
	if err = migrateCompanyCraftCategoryOrder(db); err != nil {
		return err
	}
	if err = migratePublicCraftFoundation(db); err != nil {
		return err
	}
	if err = migrateProductUnitDefinitions(db); err != nil {
		return err
	}
	if err = migrateInventoryQuantum(db); err != nil {
		return err
	}
	if err = migrateOrderQuantum(db); err != nil {
		return err
	}
	if err = migrateOrderPhase3(db); err != nil {
		return err
	}
	if err = migrateTicketBatches(db); err != nil {
		return err
	}
	if err = migrateCompanyReceipts(db); err != nil {
		return err
	}
	if err = migratePayroll(db); err != nil {
		return err
	}
	if err := migrateExpenses(db); err != nil {
		return err
	}
	if err := migrateBonusPayrollMarker(db); err != nil {
		return err
	}
	if err := migrateCatalogSeed(db); err != nil {
		return err
	}
	if err := migrateCompanyWageOverrides(db); err != nil {
		return err
	}
	if err = migrateProjectPackages(db); err != nil {
		return err
	}
	if err := migrateBonusWorkerLocks(db); err != nil {
		return err
	}
	if err := migrateCompanyProductPrices(db); err != nil {
		return err
	}
	return migrateCraftWageStatus(db)
}

// Version 300 fills the first project snapshot for orders saved before snapshot
// columns existed. Older project names cannot be reconstructed after a rename.
func migrateProjectSnapshots(db *sql.DB) error {
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=300").Scan(&done); err != nil {
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
	if _, err = tx.Exec("UPDATE orders o JOIN projects p ON p.id=o.project_id SET o.project_name=IF(o.project_name='',p.name,o.project_name),o.project_number=IF(o.project_number='',p.number,o.project_number) WHERE o.project_id<>'' AND (o.project_name='' OR o.project_number='')"); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(300)"); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateVersions(db *sql.DB) error {
	if _, e := db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations(version INT PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB"); e != nil {
		return e
	}
	for _, m := range []struct {
		version              int
		table, name, columns string
	}{
		{1, "stock_documents", "idx_stock_date", "work_date,id"},
		{2, "stock_documents", "idx_stock_source", "source_id,work_date,id"},
		{3, "stock_documents", "idx_stock_destination", "destination_id,work_date,id"},
		{4, "audit", "idx_audit_created", "created_at,id"},
		{5, "orders", "idx_order_status", "status,work_date,id"},
		{6, "backup_runs", "idx_backup_created", "created_at,id"},
		{7, "archive_runs", "idx_archive_created", "created_at,id"},
		{8, "orders", "idx_order_project", "project_id,work_date,id"},
		{9, "orders", "idx_order_unassigned_company", "project_id,company_id,work_date,id"},
		{10, "companies", "idx_company_name", "name,id"},
		{11, "workers", "idx_worker_name", "name,id"},
		{12, "products", "idx_product_active_name", "active,name,id"},
		{13, "warehouses", "idx_warehouse_active_name", "active,name,id"},
		{14, "crafts", "idx_craft_company_name", "company_id,name,id"},
		{15, "stock_movements", "idx_stock_movement_product", "product_id,id"},
	} {
		var done int
		if e := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", m.version).Scan(&done); e != nil {
			return e
		}
		if done > 0 {
			continue
		}
		if e := db.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?", m.table, m.name).Scan(&done); e != nil {
			return e
		}
		if done == 0 {
			if _, e := db.Exec("CREATE INDEX " + m.name + " ON " + m.table + "(" + m.columns + ")"); e != nil {
				return e
			}
		}
		if _, e := db.Exec("INSERT INTO schema_migrations(version) VALUES(?)", m.version); e != nil {
			return e
		}
	}
	return nil
}
