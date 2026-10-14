package main

import "database/sql"

const bonusWorkerLocksMigrationVersion = 455

func migrateBonusWorkerLocks(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS payroll_bonus_worker_locks(
		worker_id VARCHAR(64) PRIMARY KEY,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", bonusWorkerLocksMigrationVersion).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT IGNORE INTO payroll_bonus_worker_locks(worker_id)
		SELECT worker_id FROM payroll_bonus_entries WHERE worker_id<>''
		UNION SELECT worker_id FROM payroll_bonus_settlements WHERE worker_id<>''`); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(?)", bonusWorkerLocksMigrationVersion); err != nil {
		return err
	}
	return tx.Commit()
}
