package main

import "testing"

func TestBonusWorkerLockMigrationAndBackupRegistration(t *testing.T) {
	if bonusWorkerLocksMigrationVersion != 455 {
		t.Fatalf("bonus worker lock migration version = %d, want 455", bonusWorkerLocksMigrationVersion)
	}
	if !contains(backupTables, "payroll_bonus_worker_locks") {
		t.Fatal("bonus worker lock rows must be included in backup and restore")
	}
}
