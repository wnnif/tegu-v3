package main

import (
	"database/sql"
	"time"
)

// Civil DATE columns retain their dates; UTC activity timestamps are assigned
// to their Shanghai month before deciding which completed months to archive.
func (a *App) pendingArchiveMonths(now time.Time) ([]string, error) {
	month := now.In(businessLocation).Format("2006-01")
	utcFrom, _, err := businessMonthRangeUTC(month)
	if err != nil {
		return nil, err
	}
	return scanRows[string](a.db, `SELECT month FROM (
		SELECT DISTINCT DATE_FORMAT(work_date,'%Y-%m') AS month FROM orders WHERE work_date<?
		UNION SELECT DISTINCT DATE_FORMAT(received_at,'%Y-%m') AS month FROM payment_receipts WHERE received_at<?
		UNION SELECT DISTINCT DATE_FORMAT(received_at,'%Y-%m') AS month FROM company_receipts WHERE received_at<?
		UNION SELECT DISTINCT DATE_FORMAT(DATE_ADD(updated_at,INTERVAL 8 HOUR),'%Y-%m') AS month FROM project_tickets WHERE updated_at<?
		UNION SELECT DISTINCT DATE_FORMAT(effective_date,'%Y-%m') AS month FROM payroll_adjustments WHERE effective_date<?
		UNION SELECT DISTINCT DATE_FORMAT(deduction_date,'%Y-%m') AS month FROM payroll_deductions WHERE deduction_date<?
		UNION SELECT DISTINCT DATE_FORMAT(advance_date,'%Y-%m') AS month FROM payroll_advances WHERE advance_date<?
		UNION SELECT DISTINCT DATE_FORMAT(paid_date,'%Y-%m') AS month FROM payroll_payments WHERE paid_date<?
		UNION SELECT DISTINCT DATE_FORMAT(work_date,'%Y-%m') AS month FROM stock_documents WHERE kind='进货' AND work_date<?
		UNION SELECT DISTINCT DATE_FORMAT(expense_date,'%Y-%m') AS month FROM expense_claims WHERE expense_date<?
		UNION SELECT DISTINCT DATE_FORMAT(paid_date,'%Y-%m') AS month FROM expense_payments WHERE paid_date<?
		UNION SELECT DISTINCT DATE_FORMAT(advance_date,'%Y-%m') AS month FROM expense_advances WHERE advance_date<?
		UNION SELECT DISTINCT DATE_FORMAT(applied_date,'%Y-%m') AS month FROM expense_advance_allocations WHERE applied_date<?
		UNION SELECT DISTINCT DATE_FORMAT(returned_date,'%Y-%m') AS month FROM expense_advance_returns WHERE returned_date<?
	) activity WHERE month NOT IN (SELECT month FROM archive_runs WHERE automatic=1) ORDER BY month`, []any{month + "-01", month + "-01", month + "-01", utcFrom, month + "-01", month + "-01", month + "-01", month + "-01", month + "-01", month + "-01", month + "-01", month + "-01", month + "-01", month + "-01"}, func(r *sql.Rows, value *string) error { return r.Scan(value) })
}
