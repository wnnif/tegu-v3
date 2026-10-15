package main

import "database/sql"

// Schema 430–434 runs under initSchema's database-scoped migration lock.
// DDL steps can resume; historical amounts/names/paid flags are never recalculated.
func migrateOrderWorkers(db *sql.DB) error {
	for i, c := range []struct{ name, definition string }{
		{"worker_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"worker_name", "VARCHAR(100) NOT NULL DEFAULT ''"},
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_lines' AND COLUMN_NAME=?", c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec("ALTER TABLE order_lines ADD COLUMN " + c.name + " " + c.definition); err != nil {
				return err
			}
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 430+i); err != nil {
			return err
		}
	}
	for i, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS order_worker_wages(id VARCHAR(64) PRIMARY KEY,order_id VARCHAR(64) NOT NULL,worker_id VARCHAR(64) NOT NULL DEFAULT '',worker_name VARCHAR(100) NOT NULL DEFAULT '',wage_cents BIGINT NOT NULL,delivery_wage_cents BIGINT NOT NULL DEFAULT 0,wage_paid BOOLEAN NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 1,legacy BOOLEAN NOT NULL DEFAULT 0,UNIQUE(order_id,worker_id),INDEX(worker_id,wage_paid,order_id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
		`CREATE TABLE IF NOT EXISTS wage_settlements(id BIGINT AUTO_INCREMENT PRIMARY KEY,wage_id VARCHAR(64) NOT NULL,order_id VARCHAR(64) NOT NULL,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,kind VARCHAR(30) NOT NULL,amount_cents BIGINT NOT NULL,actor_id VARCHAR(64) NOT NULL,operation_id VARCHAR(100) NOT NULL,note VARCHAR(1000) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(operation_id,wage_id,kind),INDEX(order_id,worker_id,id),FOREIGN KEY(order_id) REFERENCES orders(id))`,
	} {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 432+i); err != nil {
			return err
		}
	}
	var done int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=434").Scan(&done); err != nil {
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
	if _, err = tx.Exec(`UPDATE order_lines l JOIN orders o ON o.id=l.order_id SET l.worker_id=o.worker_id,l.worker_name=o.worker_name WHERE l.worker_id=''`); err != nil {
		return err
	}
	if err = backfillWorkerWages(tx); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(434)"); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillWorkerWages(tx *sql.Tx) error {
	if err := validateWorkerAccountsForBackfill(tx); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version,legacy)
	 SELECT MD5(CONCAT('legacy-wage:',o.id)),o.id,o.worker_id,o.worker_name,o.wage_cents,o.delivery_wage_cents,o.wage_paid,o.version,1 FROM orders o
	 WHERE NOT EXISTS(SELECT 1 FROM order_worker_wages w WHERE w.order_id=o.id)`)
	return err
}

func validateWorkerAccountsForBackfill(tx *sql.Tx) error {
	var wageColumns int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA=DATABASE() AND (
			(TABLE_NAME='order_lines' AND COLUMN_NAME IN ('wage_override_cents','wage_configured')) OR
			(TABLE_NAME='order_worker_wages' AND COLUMN_NAME='wage_configured'))`).Scan(&wageColumns); err != nil {
		return err
	}
	if wageColumns == 3 {
		return validateWorkerAccounts(tx)
	}

	// Before migrations 443 and 457, those wage-state columns do not exist and
	// no worker-wage rows can be validly present yet. Check only the invariants
	// representable at this migration stage.
	var invalid int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM orders o WHERE
		EXISTS(SELECT 1 FROM order_worker_wages w FORCE INDEX(order_id) WHERE w.order_id=o.id) OR
		EXISTS(SELECT 1 FROM order_lines l FORCE INDEX(order_id) WHERE l.order_id=o.id AND l.worker_id<>'' AND l.worker_id<>o.worker_id)`).Scan(&invalid); err != nil {
		return err
	}
	if invalid > 0 {
		return fail(422, "施工记录的分人工资归属不完整，请先核对恢复数据")
	}
	return nil
}
