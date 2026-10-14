package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type payrollHistoryRow struct {
	ID             string          `json:"id"`
	EntityType     string          `json:"entity_type"`
	EntityID       string          `json:"entity_id"`
	Event          string          `json:"event"`
	Reason         string          `json:"reason"`
	BeforeSnapshot json.RawMessage `json:"before_snapshot"`
	AfterSnapshot  json.RawMessage `json:"after_snapshot"`
	ActorName      string          `json:"actor_name"`
	OperationID    string          `json:"operation_id"`
	CreatedAt      string          `json:"created_at"`
}

func validatePayrollHistoryFilters(q url.Values) (string, string, string, string, error) {
	workerID, err := validatePayrollID(q.Get("worker_id"), "施工人")
	if err != nil {
		return "", "", "", "", err
	}
	from, to := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	for _, value := range []string{from, to} {
		if value != "" {
			if len(value) != 10 {
				return "", "", "", "", fail(400, "工资历史日期范围无效")
			}
			if _, err = time.Parse("2006-01-02", value); err != nil {
				return "", "", "", "", fail(400, "工资历史日期范围无效")
			}
		}
	}
	if from != "" && to != "" && from > to {
		return "", "", "", "", fail(400, "开始日期不能晚于结束日期")
	}
	projectID := strings.TrimSpace(q.Get("project_id"))
	if len(projectID) > 64 {
		return "", "", "", "", fail(400, "工地编号无效")
	}
	return workerID, from, to, projectID, nil
}

func payrollSettlementProjectFilter(alias string) string {
	return ` AND EXISTS (
	 SELECT 1 FROM payroll_settlement_sources src
	 WHERE src.settlement_id=` + alias + `.id AND src.worker_id=` + alias + `.worker_id AND src.project_id=?
	)`
}

func payrollHistoryQuery(workerID, from, to, projectID string) (string, []any) {
	where := []string{}
	args := []any{}
	if from != "" {
		where = append(where, "h.created_at>=?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "h.created_at<DATE_ADD(?,INTERVAL 1 DAY)")
		args = append(args, to)
	}

	projectPredicate := ""
	if projectID != "" {
		projectPredicate = " AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(h.before_snapshot,'$.project_id')),JSON_UNQUOTE(JSON_EXTRACT(h.before_snapshot,'$.ProjectID')),'')=? OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.project_id')),JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.ProjectID')),'')=?)"
	}
	entities := []struct {
		kind  string
		table string
	}{
		{"adjustment", "payroll_adjustments"},
		{"deduction", "payroll_deductions"},
		{"advance", "payroll_advances"},
	}
	matches := make([]string, 0, 8)
	for _, entity := range entities {
		matches = append(matches, "(h.entity_type=? AND EXISTS(SELECT 1 FROM "+entity.table+" e WHERE e.id=h.entity_id AND e.worker_id=?"+projectPredicate+"))")
		args = append(args, entity.kind, workerID)
		if projectID != "" {
			args = append(args, projectID, projectID)
		}
	}
	bonusProject := ""
	bonusSettlementProject := ""
	bonusPaymentProject := ""
	if projectID != "" {
		bonusProject = " AND e.project_id=?"
		bonusSettlementProject = " AND EXISTS(SELECT 1 FROM payroll_bonus_entries e WHERE e.worker_id=s.worker_id AND e.bonus_year=s.bonus_year AND e.project_id=?)"
		bonusPaymentProject = " AND EXISTS(SELECT 1 FROM payroll_bonus_entries e WHERE e.worker_id=s.worker_id AND e.bonus_year=s.bonus_year AND e.project_id=?)"
	}
	matches = append(matches, "(h.entity_type='bonus' AND EXISTS(SELECT 1 FROM payroll_bonus_entries e WHERE e.id=h.entity_id AND e.worker_id=?"+bonusProject+"))")
	args = append(args, workerID)
	if projectID != "" {
		args = append(args, projectID)
	}
	settlementProject := ""
	paymentProject := ""
	if projectID != "" {
		settlementProject = payrollSettlementProjectFilter("s")
		paymentProject = payrollSettlementProjectFilter("s")
	}
	matches = append(matches,
		"(h.entity_type='settlement' AND EXISTS(SELECT 1 FROM payroll_settlements s WHERE s.id=h.entity_id AND s.worker_id=?"+settlementProject+"))",
		"(h.entity_type='payment' AND EXISTS(SELECT 1 FROM payroll_payments p JOIN payroll_settlements s ON s.id=p.settlement_id WHERE p.id=h.entity_id AND s.worker_id=?"+paymentProject+"))",
		"(h.entity_type='bonus_settlement' AND EXISTS(SELECT 1 FROM payroll_bonus_settlements s WHERE s.id=h.entity_id AND s.worker_id=?"+bonusSettlementProject+"))",
		"(h.entity_type='bonus_payment' AND EXISTS(SELECT 1 FROM payroll_bonus_payments p JOIN payroll_bonus_settlements s ON s.id=p.settlement_id WHERE p.id=h.entity_id AND s.worker_id=?"+bonusPaymentProject+"))",
	)
	args = append(args, workerID)
	if projectID != "" {
		args = append(args, projectID)
	}
	args = append(args, workerID)
	if projectID != "" {
		args = append(args, projectID)
	}
	args = append(args, workerID)
	if projectID != "" {
		args = append(args, projectID)
	}
	args = append(args, workerID)
	if projectID != "" {
		args = append(args, projectID)
	}
	where = append(where, "("+strings.Join(matches, " OR ")+")")
	return strings.Join(where, " AND "), args
}

func scanPayrollHistory(r *sql.Rows, row *payrollHistoryRow) error {
	var before, after []byte
	if err := r.Scan(&row.ID, &row.EntityType, &row.EntityID, &row.Event, &row.Reason, &before, &after, &row.ActorName, &row.OperationID, &row.CreatedAt); err != nil {
		return err
	}
	beforeSnapshot, afterSnapshot, err := payrollHistorySnapshots(before, after)
	if err != nil {
		return err
	}
	row.BeforeSnapshot, row.AfterSnapshot = beforeSnapshot, afterSnapshot
	return nil
}

func payrollHistorySnapshots(before, after []byte) (json.RawMessage, json.RawMessage, error) {
	if len(before) == 0 {
		before = []byte("null")
	}
	if !json.Valid(before) || !json.Valid(after) {
		return nil, nil, fmt.Errorf("invalid payroll history snapshot JSON")
	}
	return append(json.RawMessage(nil), before...), append(json.RawMessage(nil), after...), nil
}

func (a *App) listPayrollHistory(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	workerID, from, to, projectID, err := validatePayrollHistoryFilters(r.URL.Query())
	if err != nil {
		return err
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	where, args := payrollHistoryQuery(workerID, from, to, projectID)
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM payroll_history h WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows[payrollHistoryRow](tx, `SELECT h.id,h.entity_type,h.entity_id,h.event,h.reason,h.before_snapshot,h.after_snapshot,h.actor_name,h.operation_id,h.created_at
	 FROM payroll_history h WHERE `+where+` ORDER BY h.created_at DESC,h.id DESC LIMIT 100 OFFSET ?`, append(append([]any{}, args...), (page-1)*100), scanPayrollHistory)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "page_size": 100})
	return nil
}
