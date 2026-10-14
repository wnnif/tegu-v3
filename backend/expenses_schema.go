package main

import "database/sql"

// Migration 447 adds the independent employee expense and expense-advance ledger.
func migrateExpenses(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS expense_claims(
		 id VARCHAR(64) PRIMARY KEY,person_name VARCHAR(100) NOT NULL,expense_date DATE NOT NULL,
		 category VARCHAR(30) NOT NULL,amount_cents BIGINT NOT NULL,note VARCHAR(1000) NOT NULL DEFAULT '',
		 purchase_document_id VARCHAR(64) NULL,purchase_number VARCHAR(64) NOT NULL DEFAULT '',purchase_date DATE NULL,purchase_total_cents BIGINT NULL,
		 status VARCHAR(20) NOT NULL DEFAULT 'pending',version BIGINT NOT NULL DEFAULT 1,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_expense_claim_operation(actor_id,operation_id),INDEX(status,expense_date,id),INDEX(person_name,expense_date,id),
		 INDEX(purchase_document_id,status,id),CHECK(amount_cents>0 AND amount_cents<=100000000000000),
		 CHECK(category IN ('材料采购','油费','停车费','餐费','其他')),CHECK(status IN ('pending','confirmed','voided')))`,
		`CREATE TABLE IF NOT EXISTS expense_advances(
		 id VARCHAR(64) PRIMARY KEY,person_name VARCHAR(100) NOT NULL,advance_date DATE NOT NULL,
		 amount_cents BIGINT NOT NULL,purpose VARCHAR(200) NOT NULL,note VARCHAR(1000) NOT NULL DEFAULT '',
		 status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_expense_advance_operation(actor_id,operation_id),INDEX(status,advance_date,id),INDEX(person_name,advance_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','voided')))`,
		`CREATE TABLE IF NOT EXISTS expense_advance_allocations(
		 id VARCHAR(64) PRIMARY KEY,claim_id VARCHAR(64) NOT NULL,advance_id VARCHAR(64) NOT NULL,
		 amount_cents BIGINT NOT NULL,applied_date DATE NOT NULL,note VARCHAR(1000) NOT NULL DEFAULT '',
		 status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,reversal_reason VARCHAR(1000) NOT NULL DEFAULT '',
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_expense_allocation_operation(actor_id,operation_id),INDEX(claim_id,status,applied_date,id),INDEX(advance_id,status,applied_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','reversed')))`,
		`CREATE TABLE IF NOT EXISTS expense_payments(
		 id VARCHAR(64) PRIMARY KEY,claim_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,paid_date DATE NOT NULL,
		 note VARCHAR(1000) NOT NULL DEFAULT '',status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,
		 reversal_reason VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_expense_payment_operation(actor_id,operation_id),INDEX(claim_id,status,paid_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','reversed')))`,
		`CREATE TABLE IF NOT EXISTS expense_advance_returns(
		 id VARCHAR(64) PRIMARY KEY,advance_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,returned_date DATE NOT NULL,
		 note VARCHAR(1000) NOT NULL DEFAULT '',status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,
		 reversal_reason VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_expense_return_operation(actor_id,operation_id),INDEX(advance_id,status,returned_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','reversed')))`,
		`CREATE TABLE IF NOT EXISTS expense_history(
		 id VARCHAR(64) PRIMARY KEY,entity_type VARCHAR(20) NOT NULL,entity_id VARCHAR(64) NOT NULL,
		 event VARCHAR(100) NOT NULL,reason VARCHAR(1000) NOT NULL DEFAULT '',before_snapshot JSON NULL,after_snapshot JSON NOT NULL,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(entity_type,entity_id,id),INDEX(actor_id,id),INDEX(operation_id),INDEX(created_at,id))`,
	} {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(447)")
	return err
}
