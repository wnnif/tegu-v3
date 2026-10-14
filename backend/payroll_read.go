package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type payrollSummary struct {
	WorkerID             string `json:"worker_id,omitempty"`
	WorkerName           string `json:"worker_name,omitempty"`
	OrderCount           int64  `json:"order_count,omitempty"`
	WagePendingCount     int64  `json:"wage_pending_count"`
	GrossCents           int64  `json:"gross_cents"`
	AdjustmentCents      int64  `json:"adjustment_cents"`
	DeductionCents       int64  `json:"deduction_cents"`
	AdvanceIssued        int64  `json:"advance_issued_cents"`
	AdvanceApplied       int64  `json:"advance_applied_cents"`
	AdvanceBalance       int64  `json:"advance_balance_cents"`
	PaidCents            int64  `json:"paid_cents"`
	UnpaidCents          int64  `json:"unpaid_cents"`
	BonusAccruedCents    int64  `json:"bonus_accrued_cents"`
	BonusAdjustmentCents int64  `json:"bonus_adjustment_cents"`
	BonusPaidCents       int64  `json:"bonus_paid_cents"`
	BonusBalanceCents    int64  `json:"bonus_balance_cents"`
}

type payrollWorkerPage struct {
	Rows     []payrollSummary `json:"rows"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

type payrollProjectRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

type payrollPage struct {
	Rows     any            `json:"rows"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Summary  payrollSummary `json:"summary"`
}

type payrollWageRow struct {
	ID             string `json:"id"`
	WorkerID       string `json:"worker_id"`
	WorkerName     string `json:"worker_name"`
	OrderID        string `json:"order_id"`
	Number         string `json:"number"`
	WorkDate       string `json:"work_date"`
	ProjectID      string `json:"project_id"`
	ProjectName    string `json:"project_name"`
	Address        string `json:"address"`
	WageCents      *int64 `json:"wage_cents,omitempty"`
	DeliveryWage   int64  `json:"delivery_wage_cents"`
	WageConfigured bool   `json:"wage_configured"`
	Settled        bool   `json:"settled"`
	Version        int64  `json:"version"`
}

type payrollAdjustmentRow struct {
	ID          string `json:"id"`
	WorkerID    string `json:"worker_id"`
	WorkerName  string `json:"worker_name"`
	Amount      int64  `json:"amount_cents"`
	Effective   string `json:"effective_date"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Address     string `json:"address"`
	Reason      string `json:"reason"`
	Actor       string `json:"actor"`
	Version     int64  `json:"version"`
	ReversalID  string `json:"reversal_id,omitempty"`
	ReversalOf  string `json:"reversal_of,omitempty"`
	Settled     bool   `json:"settled"`
}

type payrollDeductionRow struct {
	ID          string `json:"id"`
	WorkerID    string `json:"worker_id"`
	WorkerName  string `json:"worker_name"`
	Amount      int64  `json:"amount_cents"`
	DeductionAt string `json:"deduction_date"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Address     string `json:"address"`
	Reason      string `json:"reason"`
	LegalBasis  string `json:"legal_basis"`
	Actor       string `json:"actor"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
	Settled     bool   `json:"settled"`
}

type payrollAdvanceRow struct {
	ID          string `json:"id"`
	WorkerID    string `json:"worker_id"`
	WorkerName  string `json:"worker_name"`
	Amount      int64  `json:"amount_cents"`
	AdvanceAt   string `json:"advance_date"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Address     string `json:"address"`
	Note        string `json:"note"`
	Actor       string `json:"actor"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
	Allocated   int64  `json:"allocated_cents"`
	Remaining   int64  `json:"remaining_cents"`
}

type payrollPaymentRow struct {
	ID             string `json:"id"`
	Amount         int64  `json:"amount_cents"`
	PaidDate       string `json:"paid_date"`
	Method         string `json:"method"`
	Note           string `json:"note"`
	Version        int64  `json:"version"`
	Reversed       bool   `json:"reversed"`
	ReversalReason string `json:"reversal_reason,omitempty"`
}

type payrollSettlementRow struct {
	ID           string              `json:"id"`
	WorkerID     string              `json:"worker_id"`
	WorkerName   string              `json:"worker_name"`
	SettlementAt string              `json:"settlement_date"`
	Gross        int64               `json:"gross_cents"`
	Adjustment   int64               `json:"adjustment_cents"`
	Deduction    int64               `json:"deduction_cents"`
	Advance      int64               `json:"advance_cents"`
	Payable      int64               `json:"payable_cents"`
	Paid         int64               `json:"paid_cents"`
	Remaining    int64               `json:"remaining_cents"`
	Status       string              `json:"status"`
	LegacyPaid   bool                `json:"legacy_paid,omitempty"`
	Note         string              `json:"note"`
	Actor        string              `json:"actor"`
	Version      int64               `json:"version"`
	Payments     []payrollPaymentRow `json:"payments"`
}

func (a *App) listPayrollWorkers(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 100 {
		return fail(400, "搜索内容不能超过100字")
	}
	if r.URL.Query().Get("export") == "1" {
		return a.exportPayrollWorkers(w, r, u, q)
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	base := payrollWorkerSummarySQL
	args := []any{}
	if q != "" {
		base = "SELECT * FROM (" + base + ") s WHERE INSTR(s.worker_name,?)>0"
		args = append(args, q)
	}
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*) FROM ("+base+") filtered", args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows[payrollSummary](tx, "SELECT worker_id,worker_name,order_count,wage_pending_count,gross_cents,adjustment_cents,deduction_cents,advance_issued_cents,advance_applied_cents,advance_balance_cents,paid_cents,unpaid_cents,bonus_accrued_cents,bonus_adjustment_cents,bonus_paid_cents,bonus_balance_cents FROM ("+base+") s ORDER BY worker_name,worker_id LIMIT 100 OFFSET ?", append(append([]any{}, args...), (page-1)*100), scanPayrollSummary)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, payrollWorkerPage{Rows: rows, Total: total, Page: page, PageSize: 100})
	return nil
}

const payrollWorkerSummarySQL = `
SELECT ids.worker_id,COALESCE(NULLIF(w.name,''),NULLIF(names.worker_name,''),'') worker_name,
	COALESCE(ws.order_count,0) order_count,COALESCE(ws.wage_pending_count,0) wage_pending_count,COALESCE(ws.gross_cents,0) gross_cents,
 COALESCE(adj.adjustment_cents,0) adjustment_cents,COALESCE(ded.deduction_cents,0) deduction_cents,
 COALESCE(adv.advance_issued_cents,0) advance_issued_cents,COALESCE(applied.advance_applied_cents,0) advance_applied_cents,
 COALESCE(adv.advance_issued_cents,0)-COALESCE(applied.advance_applied_cents,0) advance_balance_cents,
 COALESCE(pay.paid_cents,0) paid_cents,
	COALESCE(ws.gross_cents,0)+COALESCE(adj.adjustment_cents,0)-COALESCE(used.deduction_applied_cents,0)-COALESCE(applied.advance_applied_cents,0)-COALESCE(pay.paid_cents,0) unpaid_cents,
	COALESCE(bonus.accrued_cents,0) bonus_accrued_cents,COALESCE(bonus.adjustment_cents,0) bonus_adjustment_cents,
	COALESCE(bonus.paid_cents,0) bonus_paid_cents,COALESCE(bonus.accrued_cents,0)+COALESCE(bonus.adjustment_cents,0)-COALESCE(bonus.paid_cents,0) bonus_balance_cents
FROM (
 SELECT id worker_id FROM workers
 UNION SELECT worker_id FROM order_worker_wages WHERE worker_id<>''
 UNION SELECT worker_id FROM payroll_adjustments WHERE worker_id<>''
 UNION SELECT worker_id FROM payroll_deductions WHERE worker_id<>''
 UNION SELECT worker_id FROM payroll_advances WHERE worker_id<>''
 UNION SELECT worker_id FROM payroll_settlements WHERE worker_id<>''
	UNION SELECT worker_id FROM payroll_bonus_entries WHERE worker_id<>''
	UNION SELECT worker_id FROM payroll_bonus_settlements WHERE worker_id<>''
) ids
LEFT JOIN workers w ON w.id=ids.worker_id
LEFT JOIN (SELECT worker_id,MAX(worker_name) worker_name FROM (
 SELECT worker_id,worker_name FROM order_worker_wages
 UNION ALL SELECT worker_id,worker_name FROM payroll_adjustments
 UNION ALL SELECT worker_id,worker_name FROM payroll_deductions
 UNION ALL SELECT worker_id,worker_name FROM payroll_advances
	UNION ALL SELECT worker_id,worker_name FROM payroll_settlements
	UNION ALL SELECT worker_id,worker_name FROM payroll_bonus_entries
	UNION ALL SELECT worker_id,worker_name FROM payroll_bonus_settlements
) all_names GROUP BY worker_id) names ON names.worker_id=ids.worker_id
LEFT JOIN (SELECT ww.worker_id,COUNT(*) order_count,SUM(IF(ww.wage_configured=0,1,0)) wage_pending_count,SUM(IF(ww.wage_configured=1,ww.wage_cents,0)) gross_cents FROM ` + workerWageSource + ` ww JOIN orders o ON o.id=ww.order_id WHERE o.status<>'已取消' GROUP BY ww.worker_id) ws ON ws.worker_id=ids.worker_id
LEFT JOIN (SELECT worker_id,SUM(amount_cents) adjustment_cents FROM payroll_adjustments GROUP BY worker_id) adj ON adj.worker_id=ids.worker_id
LEFT JOIN (SELECT worker_id,SUM(amount_cents) deduction_cents FROM payroll_deductions WHERE status='confirmed' GROUP BY worker_id) ded ON ded.worker_id=ids.worker_id
LEFT JOIN (SELECT worker_id,SUM(amount_cents) advance_issued_cents FROM payroll_advances WHERE status='active' GROUP BY worker_id) adv ON adv.worker_id=ids.worker_id
LEFT JOIN (SELECT s.worker_id,SUM(src.amount_cents) advance_applied_cents FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND s.status='confirmed' GROUP BY s.worker_id) applied ON applied.worker_id=ids.worker_id
LEFT JOIN (SELECT s.worker_id,SUM(IF(s.legacy_paid=1,s.payable_cents,0)+COALESCE(p.paid_cents,0)) paid_cents FROM payroll_settlements s LEFT JOIN (SELECT settlement_id,SUM(amount_cents) paid_cents FROM payroll_payments WHERE status='active' GROUP BY settlement_id) p ON p.settlement_id=s.id WHERE s.status='confirmed' GROUP BY s.worker_id) pay ON pay.worker_id=ids.worker_id
LEFT JOIN (SELECT s.worker_id,SUM(src.amount_cents) deduction_applied_cents FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='deduction' AND s.status='confirmed' GROUP BY s.worker_id) used ON used.worker_id=ids.worker_id
LEFT JOIN (SELECT e.worker_id,SUM(IF(e.entry_kind='automatic',e.amount_cents,0)) accrued_cents,
	SUM(IF(e.entry_kind IN ('manual','reversal'),e.amount_cents,0)) adjustment_cents,COALESCE(MAX(p.paid_cents),0) paid_cents
	FROM payroll_bonus_entries e LEFT JOIN (SELECT s.worker_id,SUM(p.amount_cents) paid_cents FROM payroll_bonus_payments p JOIN payroll_bonus_settlements s ON s.id=p.settlement_id WHERE p.status='active' AND s.status='confirmed' GROUP BY s.worker_id) p ON p.worker_id=e.worker_id GROUP BY e.worker_id) bonus ON bonus.worker_id=ids.worker_id`

func scanPayrollSummary(r *sql.Rows, v *payrollSummary) error {
	return r.Scan(&v.WorkerID, &v.WorkerName, &v.OrderCount, &v.WagePendingCount, &v.GrossCents, &v.AdjustmentCents, &v.DeductionCents, &v.AdvanceIssued, &v.AdvanceApplied, &v.AdvanceBalance, &v.PaidCents, &v.UnpaidCents, &v.BonusAccruedCents, &v.BonusAdjustmentCents, &v.BonusPaidCents, &v.BonusBalanceCents)
}

func (a *App) listPayrollProjects(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 100 {
		return fail(400, "搜索内容不能超过100字")
	}
	where, args := "1=1", []any{}
	if q != "" {
		where += " AND (INSTR(p.name,?)>0 OR INSTR(p.address,?)>0 OR INSTR(p.number,?)>0)"
		args = append(args, q, q, q)
	}
	var total int64
	if err = a.db.QueryRow("SELECT COUNT(*) FROM projects p WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows[payrollProjectRow](a.db, "SELECT p.id,p.name,p.address,p.status FROM projects p WHERE "+where+" ORDER BY p.name,p.address,p.id LIMIT 100 OFFSET ?", append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollProjectRow) error { return r.Scan(&v.ID, &v.Name, &v.Address, &v.Status) })
	if err != nil {
		return err
	}
	write(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "page_size": 100})
	return nil
}

func validatePayrollFilters(q url.Values) (string, []any, string, error) {
	tab := q.Get("tab")
	if !contains([]string{"wages", "adjustments", "deductions", "advances", "settlements"}, tab) {
		return "", nil, "", fail(400, "工资明细类型无效")
	}
	workerID := strings.TrimSpace(q.Get("worker_id"))
	if workerID == "" || len(workerID) > 64 {
		return "", nil, "", fail(400, "请选择有效施工人")
	}
	from, to := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	for _, value := range []string{from, to} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return "", nil, "", fail(400, "工资日期范围无效")
			}
		}
	}
	if from != "" && to != "" && from > to {
		return "", nil, "", fail(400, "开始日期不能晚于结束日期")
	}
	projectID := strings.TrimSpace(q.Get("project_id"))
	if len(projectID) > 64 {
		return "", nil, "", fail(400, "工地编号无效")
	}
	return tab, []any{workerID, from, to, projectID}, workerID, nil
}

func payrollWhere(alias, dateCol, projectCol string, filters []any) (string, []any) {
	workerID, from, to, projectID := filters[0].(string), filters[1].(string), filters[2].(string), filters[3].(string)
	where, args := alias+".worker_id=?", []any{workerID}
	if from != "" {
		where += " AND " + dateCol + ">=?"
		args = append(args, from)
	}
	if to != "" {
		where += " AND " + dateCol + "<=?"
		args = append(args, to)
	}
	if projectID != "" {
		where += " AND " + projectCol + "=?"
		args = append(args, projectID)
	}
	return where, args
}

func (a *App) listPayroll(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	tab, filters, workerID, err := validatePayrollFilters(r.URL.Query())
	if err != nil {
		return err
	}
	if r.URL.Query().Get("export") == "1" {
		return a.exportPayrollRows(w, r, u, tab, filters)
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	summary, err := readPayrollSummary(tx, workerID)
	if err != nil {
		return err
	}
	var rows any
	var total int64
	switch tab {
	case "wages":
		rows, total, err = readPayrollWages(tx, filters, page)
	case "adjustments":
		rows, total, err = readPayrollAdjustments(tx, filters, page)
	case "deductions":
		rows, total, err = readPayrollDeductions(tx, filters, page)
	case "advances":
		rows, total, err = readPayrollAdvances(tx, filters, page)
	case "settlements":
		rows, total, err = readPayrollSettlements(tx, r, filters, page)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, payrollPage{Rows: rows, Total: total, Page: page, PageSize: 100, Summary: summary})
	return nil
}

func readPayrollSummary(tx *sql.Tx, workerID string) (payrollSummary, error) {
	var summary payrollSummary
	err := tx.QueryRow("SELECT * FROM ("+payrollWorkerSummarySQL+") summary WHERE worker_id=?", workerID).Scan(
		&summary.WorkerID, &summary.WorkerName, &summary.OrderCount, &summary.WagePendingCount, &summary.GrossCents, &summary.AdjustmentCents,
		&summary.DeductionCents, &summary.AdvanceIssued, &summary.AdvanceApplied, &summary.AdvanceBalance, &summary.PaidCents, &summary.UnpaidCents,
		&summary.BonusAccruedCents, &summary.BonusAdjustmentCents, &summary.BonusPaidCents, &summary.BonusBalanceCents)
	if err == sql.ErrNoRows {
		return summary, fail(404, "施工人不存在")
	}
	return summary, err
}

func readPayrollWages(tx *sql.Tx, filters []any, page int) (any, int64, error) {
	where, args := payrollWhere("ww", "o.work_date", "o.project_id", filters)
	where += " AND o.status<>'已取消'"
	base := " FROM " + workerWageSource + " ww JOIN orders o ON o.id=ww.order_id LEFT JOIN projects p ON p.id=o.project_id WHERE " + where
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[payrollWageRow](tx, `SELECT ww.id,ww.worker_id,ww.worker_name,ww.order_id,o.number,DATE_FORMAT(o.work_date,'%Y-%m-%d'),o.project_id,COALESCE(NULLIF(o.project_name,''),p.name,''),o.address,ww.wage_cents-ww.delivery_wage_cents,ww.delivery_wage_cents,ww.wage_configured,
	 EXISTS(SELECT 1 FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='wage' AND src.source_id=ww.id AND s.status='confirmed'),ww.version`+base+` ORDER BY o.work_date DESC,ww.order_id,ww.id LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollWageRow) error {
		var wage int64
		if err := r.Scan(&v.ID, &v.WorkerID, &v.WorkerName, &v.OrderID, &v.Number, &v.WorkDate, &v.ProjectID, &v.ProjectName, &v.Address, &wage, &v.DeliveryWage, &v.WageConfigured, &v.Settled, &v.Version); err != nil {
			return err
		}
		if v.WageConfigured {
			v.WageCents = &wage
		}
		return nil
	})
	return rows, total, err
}

func readPayrollAdjustments(tx *sql.Tx, filters []any, page int) (any, int64, error) {
	where, args := payrollWhere("a", "a.effective_date", "a.project_id", filters)
	base := " FROM payroll_adjustments a WHERE " + where
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[payrollAdjustmentRow](tx, `SELECT a.id,a.worker_id,a.worker_name,a.amount_cents,DATE_FORMAT(a.effective_date,'%Y-%m-%d'),a.project_id,a.project_name,a.address,a.reason,a.actor_name,a.version,
	 COALESCE((SELECT r.id FROM payroll_adjustments r WHERE r.reversal_of=a.id LIMIT 1),''),COALESCE(a.reversal_of,''),
	 EXISTS(SELECT 1 FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='adjustment' AND src.source_id=a.id AND s.status='confirmed')`+base+` ORDER BY a.effective_date DESC,a.created_at DESC,a.id LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollAdjustmentRow) error {
		return r.Scan(&v.ID, &v.WorkerID, &v.WorkerName, &v.Amount, &v.Effective, &v.ProjectID, &v.ProjectName, &v.Address, &v.Reason, &v.Actor, &v.Version, &v.ReversalID, &v.ReversalOf, &v.Settled)
	})
	return rows, total, err
}

func readPayrollDeductions(tx *sql.Tx, filters []any, page int) (any, int64, error) {
	where, args := payrollWhere("d", "d.deduction_date", "d.project_id", filters)
	base := " FROM payroll_deductions d WHERE " + where
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[payrollDeductionRow](tx, `SELECT d.id,d.worker_id,d.worker_name,d.amount_cents,DATE_FORMAT(d.deduction_date,'%Y-%m-%d'),d.project_id,d.project_name,d.address,d.reason,d.legal_basis,d.actor_name,d.status,d.version,
	 EXISTS(SELECT 1 FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='deduction' AND src.source_id=d.id AND s.status='confirmed')`+base+` ORDER BY d.deduction_date DESC,d.created_at DESC,d.id LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollDeductionRow) error {
		return r.Scan(&v.ID, &v.WorkerID, &v.WorkerName, &v.Amount, &v.DeductionAt, &v.ProjectID, &v.ProjectName, &v.Address, &v.Reason, &v.LegalBasis, &v.Actor, &v.Status, &v.Version, &v.Settled)
	})
	return rows, total, err
}

func readPayrollAdvances(tx *sql.Tx, filters []any, page int) (any, int64, error) {
	where, args := payrollWhere("a", "a.advance_date", "a.project_id", filters)
	base := " FROM payroll_advances a WHERE " + where
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[payrollAdvanceRow](tx, `SELECT a.id,a.worker_id,a.worker_name,a.amount_cents,DATE_FORMAT(a.advance_date,'%Y-%m-%d'),a.project_id,a.project_name,a.address,a.note,a.actor_name,a.status,a.version,
	 COALESCE((SELECT SUM(src.amount_cents) FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND src.source_id=a.id AND s.status='confirmed'),0),
	 a.amount_cents-COALESCE((SELECT SUM(src.amount_cents) FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND src.source_id=a.id AND s.status='confirmed'),0)`+base+` ORDER BY a.advance_date DESC,a.created_at DESC,a.id LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollAdvanceRow) error {
		return r.Scan(&v.ID, &v.WorkerID, &v.WorkerName, &v.Amount, &v.AdvanceAt, &v.ProjectID, &v.ProjectName, &v.Address, &v.Note, &v.Actor, &v.Status, &v.Version, &v.Allocated, &v.Remaining)
	})
	return rows, total, err
}

func readPayrollSettlements(tx *sql.Tx, r *http.Request, filters []any, page int) (any, int64, error) {
	workerID, from, to, projectID := filters[0].(string), filters[1].(string), filters[2].(string), filters[3].(string)
	where, args := "s.worker_id=?", []any{workerID}
	if from != "" {
		where += " AND s.settlement_date>=?"
		args = append(args, from)
	}
	if to != "" {
		where += " AND s.settlement_date<=?"
		args = append(args, to)
	}
	if projectID != "" {
		where += payrollSettlementProjectFilter("s")
		args = append(args, projectID)
	}
	base := " FROM payroll_settlements s WHERE " + where
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[payrollSettlementRow](tx, `SELECT s.id,s.worker_id,s.worker_name,COALESCE(DATE_FORMAT(s.settlement_date,'%Y-%m-%d'),''),s.gross_cents,s.adjustment_cents,s.deduction_cents,s.advance_cents,s.payable_cents,
	 IF(s.legacy_paid=1,s.payable_cents,0)+COALESCE((SELECT SUM(p.amount_cents) FROM payroll_payments p WHERE p.settlement_id=s.id AND p.status='active'),0),
	 s.payable_cents-IF(s.legacy_paid=1,s.payable_cents,0)-COALESCE((SELECT SUM(p.amount_cents) FROM payroll_payments p WHERE p.settlement_id=s.id AND p.status='active'),0),s.status,s.legacy_paid,s.note,s.actor_name,s.version`+base+` ORDER BY s.settlement_date DESC,s.created_at DESC,s.id LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *payrollSettlementRow) error {
		return r.Scan(&v.ID, &v.WorkerID, &v.WorkerName, &v.SettlementAt, &v.Gross, &v.Adjustment, &v.Deduction, &v.Advance, &v.Payable, &v.Paid, &v.Remaining, &v.Status, &v.LegacyPaid, &v.Note, &v.Actor, &v.Version)
	})
	if err != nil {
		return nil, 0, err
	}
	if err = readPayrollPayments(tx, r, rows); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func readPayrollPayments(tx *sql.Tx, r *http.Request, settlements []payrollSettlementRow) error {
	if len(settlements) == 0 {
		return nil
	}
	args := make([]any, len(settlements))
	marks := make([]string, len(settlements))
	byID := make(map[string]int, len(settlements))
	for i := range settlements {
		args[i], marks[i] = settlements[i].ID, "?"
		settlements[i].Payments = []payrollPaymentRow{}
		byID[settlements[i].ID] = i
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT id,settlement_id,amount_cents,COALESCE(DATE_FORMAT(paid_date,'%Y-%m-%d'),''),method,note,version,status,reversal_reason FROM payroll_payments WHERE settlement_id IN (`+strings.Join(marks, ",")+") ORDER BY created_at,id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payment payrollPaymentRow
		var settlementID, status string
		if err = rows.Scan(&payment.ID, &settlementID, &payment.Amount, &payment.PaidDate, &payment.Method, &payment.Note, &payment.Version, &status, &payment.ReversalReason); err != nil {
			return err
		}
		payment.Reversed = status == "reversed"
		settlements[byID[settlementID]].Payments = append(settlements[byID[settlementID]].Payments, payment)
	}
	return rows.Err()
}

func (a *App) exportPayrollWorkers(w http.ResponseWriter, r *http.Request, u User, q string) error {
	return a.servePayrollCSV(w, r, u, "payroll-workers.csv", "导出工资汇总", []string{"工人编号", "工人", "施工记录数", "工价待配置记录数", "施工工资（元）", "工资调整（元）", "已确认扣款（元）", "累计预支（元）", "已抵扣预支（元）", "未抵扣预支余额（元）", "实际已发（元）", "剩余未发工资（元）", "自动累计奖金（元）", "手工奖金调整（元）", "已发奖金（元）", "剩余未发奖金（元）"}, func(tx *sql.Tx, writer *csv.Writer) error {
		base, args := payrollWorkerSummarySQL, []any{}
		if q != "" {
			base = "SELECT * FROM (" + base + ") s WHERE INSTR(s.worker_name,?)>0"
			args = append(args, q)
		}
		rows, err := tx.QueryContext(r.Context(), "SELECT worker_id,worker_name,order_count,wage_pending_count,gross_cents,adjustment_cents,deduction_cents,advance_issued_cents,advance_applied_cents,advance_balance_cents,paid_cents,unpaid_cents,bonus_accrued_cents,bonus_adjustment_cents,bonus_paid_cents,bonus_balance_cents FROM ("+base+") s ORDER BY worker_name,worker_id", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v payrollSummary
			if err = scanPayrollSummary(rows, &v); err != nil {
				return err
			}
			if err = writer.Write([]string{payrollCSVText(v.WorkerID), payrollCSVText(v.WorkerName), strconv.FormatInt(v.OrderCount, 10), strconv.FormatInt(v.WagePendingCount, 10), payrollMoneyCSV(v.GrossCents), payrollMoneyCSV(v.AdjustmentCents), payrollMoneyCSV(v.DeductionCents), payrollMoneyCSV(v.AdvanceIssued), payrollMoneyCSV(v.AdvanceApplied), payrollMoneyCSV(v.AdvanceBalance), payrollMoneyCSV(v.PaidCents), payrollMoneyCSV(v.UnpaidCents), payrollMoneyCSV(v.BonusAccruedCents), payrollMoneyCSV(v.BonusAdjustmentCents), payrollMoneyCSV(v.BonusPaidCents), payrollMoneyCSV(v.BonusBalanceCents)}); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

func (a *App) exportPayrollRows(w http.ResponseWriter, r *http.Request, u User, tab string, filters []any) error {
	name := "payroll-" + tab + ".csv"
	return a.servePayrollCSV(w, r, u, name, "导出工资明细", payrollCSVHeaders(tab), func(tx *sql.Tx, writer *csv.Writer) error {
		for page := 1; ; page++ {
			rows, total, err := readPayrollTabPage(tx, r, tab, filters, page)
			if err != nil {
				return err
			}
			count, err := writePayrollTabRows(writer, tab, rows)
			if err != nil {
				return err
			}
			if int64(page)*100 >= total || count == 0 {
				return nil
			}
		}
	})
}

func readPayrollTabPage(tx *sql.Tx, r *http.Request, tab string, filters []any, page int) (any, int64, error) {
	switch tab {
	case "wages":
		return readPayrollWages(tx, filters, page)
	case "adjustments":
		return readPayrollAdjustments(tx, filters, page)
	case "deductions":
		return readPayrollDeductions(tx, filters, page)
	case "advances":
		return readPayrollAdvances(tx, filters, page)
	case "settlements":
		return readPayrollSettlements(tx, r, filters, page)
	default:
		return nil, 0, fail(400, "工资明细类型无效")
	}
}

func payrollCSVHeaders(tab string) []string {
	switch tab {
	case "wages":
		return []string{"来源编号", "工人", "施工单号", "日期", "工地", "地址", "工艺工资（元）", "送货工资（元）", "已纳入结算"}
	case "adjustments":
		return []string{"调整编号", "工人", "日期", "工地", "地址", "调整金额（元）", "原因", "操作人", "状态", "已纳入结算"}
	case "deductions":
		return []string{"扣款编号", "工人", "日期", "工地", "地址", "扣款金额（元）", "原因", "合法或约定依据", "状态", "操作人", "已纳入结算"}
	case "advances":
		return []string{"预支编号", "工人", "日期", "工地", "地址", "预支金额（元）", "已抵扣（元）", "未抵扣余额（元）", "备注", "操作人", "状态"}
	case "settlements":
		return []string{"结算编号", "工人", "结算日期", "施工工资（元）", "工资调整（元）", "扣款（元）", "预支抵扣（元）", "应结（元）", "实际已发（元）", "剩余未发（元）", "状态", "备注", "操作人", "历史已发", "付款记录"}
	default:
		return nil
	}
}

func writePayrollTabRows(writer *csv.Writer, tab string, value any) (int, error) {
	count := 0
	write := func(fields ...string) error {
		count++
		return writer.Write(fields)
	}
	switch rows := value.(type) {
	case []payrollWageRow:
		for _, v := range rows {
			if err := write(payrollCSVText(v.ID), payrollCSVText(v.WorkerName), payrollCSVText(v.Number), v.WorkDate, payrollCSVText(v.ProjectName), payrollCSVText(v.Address), payrollOptionalMoneyCSV(v.WageCents), payrollMoneyCSV(v.DeliveryWage), payrollCSVYesNo(v.Settled)); err != nil {
				return count, err
			}
		}
	case []payrollAdjustmentRow:
		for _, v := range rows {
			status := "有效"
			if v.ReversalOf != "" {
				status = "更正记录"
			} else if v.ReversalID != "" {
				status = "已更正"
			}
			if err := write(payrollCSVText(v.ID), payrollCSVText(v.WorkerName), v.Effective, payrollCSVText(v.ProjectName), payrollCSVText(v.Address), payrollMoneyCSV(v.Amount), payrollCSVText(v.Reason), payrollCSVText(v.Actor), status, payrollCSVYesNo(v.Settled)); err != nil {
				return count, err
			}
		}
	case []payrollDeductionRow:
		for _, v := range rows {
			status := payrollDeductionStatusLabel(v.Status)
			if err := write(payrollCSVText(v.ID), payrollCSVText(v.WorkerName), v.DeductionAt, payrollCSVText(v.ProjectName), payrollCSVText(v.Address), payrollMoneyCSV(v.Amount), payrollCSVText(v.Reason), payrollCSVText(v.LegalBasis), status, payrollCSVText(v.Actor), payrollCSVYesNo(v.Settled)); err != nil {
				return count, err
			}
		}
	case []payrollAdvanceRow:
		for _, v := range rows {
			status := "有效"
			if v.Status == "voided" {
				status = "已撤销"
			}
			if err := write(payrollCSVText(v.ID), payrollCSVText(v.WorkerName), v.AdvanceAt, payrollCSVText(v.ProjectName), payrollCSVText(v.Address), payrollMoneyCSV(v.Amount), payrollMoneyCSV(v.Allocated), payrollMoneyCSV(v.Remaining), payrollCSVText(v.Note), payrollCSVText(v.Actor), status); err != nil {
				return count, err
			}
		}
	case []payrollSettlementRow:
		for _, v := range rows {
			payments := make([]string, 0, len(v.Payments))
			for _, payment := range v.Payments {
				state := "有效"
				if payment.Reversed {
					state = "已撤销"
				}
				payments = append(payments, strings.Join([]string{payment.PaidDate, payrollMoneyCSV(payment.Amount), payment.Method, payment.Note, state}, " "))
			}
			status := "已确认"
			if v.Status == "voided" {
				status = "已撤销"
			}
			if err := write(payrollCSVText(v.ID), payrollCSVText(v.WorkerName), v.SettlementAt, payrollMoneyCSV(v.Gross), payrollMoneyCSV(v.Adjustment), payrollMoneyCSV(v.Deduction), payrollMoneyCSV(v.Advance), payrollMoneyCSV(v.Payable), payrollMoneyCSV(v.Paid), payrollMoneyCSV(v.Remaining), status, payrollCSVText(v.Note), payrollCSVText(v.Actor), payrollCSVYesNo(v.LegacyPaid), payrollCSVText(strings.Join(payments, "; "))); err != nil {
				return count, err
			}
		}
	default:
		return 0, fail(500, "工资导出数据类型无效")
	}
	return count, nil
}

func payrollDeductionStatusLabel(status string) string {
	switch status {
	case "confirmed":
		return "已确认"
	case "voided":
		return "已撤销"
	default:
		return "待确认"
	}
}

func payrollCSVYesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func payrollCSVText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && strings.ContainsRune("=+-@", []rune(trimmed)[0]) {
		return "'" + value
	}
	return value
}

func payrollMoneyCSV(cents int64) string {
	negative := cents < 0
	var magnitude uint64
	if negative {
		magnitude = uint64(-(cents + 1)) + 1
	} else {
		magnitude = uint64(cents)
	}
	value := strconv.FormatUint(magnitude/100, 10) + "."
	if magnitude%100 < 10 {
		value += "0"
	}
	value += strconv.FormatUint(magnitude%100, 10)
	if negative {
		return "-" + value
	}
	return value
}

func payrollOptionalMoneyCSV(cents *int64) string {
	if cents == nil {
		return ""
	}
	return payrollMoneyCSV(*cents)
}

func (a *App) servePayrollCSV(w http.ResponseWriter, r *http.Request, u User, filename, auditAction string, headers []string, export func(*sql.Tx, *csv.Writer) error) error {
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	file, err := os.CreateTemp("", "tegu-payroll-export-*.csv")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	writer.UseCRLF = true
	if err = writer.Write(headers); err != nil {
		return err
	}
	if err = export(tx, writer); err != nil {
		return err
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	logTx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer logTx.Rollback()
	if err = audit(logTx, u, auditAction, "", map[string]string{"filename": filename}); err != nil {
		return err
	}
	if err = logTx.Commit(); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, stat.ModTime(), file)
	return nil
}
