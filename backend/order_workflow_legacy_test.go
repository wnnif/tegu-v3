package main

import "testing"

// Existing suites exercise compatibility with pre-project-ticket records. Their
// synthetic fixtures explicitly opt into that legacy shape; new workflow tests
// never use this helper and always exercise managed tickets.
func legacyOrderFixture(t *testing.T, a *App, orderID string) {
	t.Helper()
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = removeOrderTicketItems(tx, User{ID: "legacy-fixture", Role: "admin"}, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE orders SET ticket_managed=0 WHERE id=?", orderID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
