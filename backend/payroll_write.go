package main

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const maxPayrollCents int64 = 100000000000000

type payrollSourceVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type payrollAdvanceAllocation struct {
	ID          string `json:"id"`
	Version     int64  `json:"version"`
	AmountCents int64  `json:"amount_cents"`
}

type payrollActionRequest struct {
	Action             string                    `json:"action"`
	ID                 string                    `json:"id"`
	SettlementID       string                    `json:"settlement_id"`
	WorkerID           string                    `json:"worker_id"`
	AmountCents        int64                     `json:"amount_cents"`
	EffectiveDate      string                    `json:"effective_date"`
	DeductionDate      string                    `json:"deduction_date"`
	AdvanceDate        string                    `json:"advance_date"`
	SettlementDate     string                    `json:"settlement_date"`
	PaidDate           string                    `json:"paid_date"`
	ProjectID          string                    `json:"project_id"`
	Reason             string                    `json:"reason"`
	LegalBasis         string                    `json:"legal_basis"`
	Note               string                    `json:"note"`
	Method             string                    `json:"method"`
	Version            int64                     `json:"version"`
	SettlementVersion  int64                     `json:"settlement_version"`
	WageIDs            []string                  `json:"wage_ids"`
	WageVersions       []payrollSourceVersion    `json:"wage_versions"`
	AdjustmentIDs      []string                  `json:"adjustment_ids"`
	DeductionIDs       []string                  `json:"deduction_ids"`
	DeductionVersions  []payrollSourceVersion    `json:"deduction_versions"`
	AdvanceAllocations []payrollAdvanceAllocation `json:"advance_allocations"`
}

type payrollWorkerSnapshot struct {
	ID   string
	Name string
}

type payrollProjectSnapshot struct {
	ID      string
	Name    string
	Address string
}

type payrollWageSource struct {
	ID          string
	OrderID     string
	ProjectID   string
	WorkerID    string
	WorkerName  string
	Amount      int64
	Delivery    int64
	Version     int64
	WagePaid    bool
}

type payrollAdjustmentSource struct {
	ID          string
	WorkerID    string
	WorkerName  string
	Amount      int64
	EffectiveAt string
	ProjectID   string
	ProjectName string
	Address     string
	Reason      string
	ActorID     string
	ActorName   string
	ReversalOf  sql.NullString
	Version     int64
}

type payrollDeductionSource struct {
	ID          string
	WorkerID    string
	WorkerName  string
	Amount      int64
	DeductionAt string
	ProjectID   string
	ProjectName string
	Address     string
	Reason      string
	LegalBasis  string
	Status      string
	ActorID     string
	ActorName   string
	Version     int64
}

type payrollAdvanceSource struct {
	ID          string
	WorkerID    string
	WorkerName  string
	Amount      int64
	AdvanceAt   string
	ProjectID   string
	ProjectName string
	Address     string
	Note        string
	Status      string
	ActorID     string
	ActorName   string
	Version     int64
}

type payrollSettlementSource struct {
	ID         string
	WorkerID   string
	WorkerName string
	Payable    int64
	Status     string
	LegacyPaid bool
	Version    int64
}

type payrollPaymentSource struct {
	ID            string
	SettlementID  string
	Amount        int64
	Status        string
	Version       int64
	ReversalReason string
}

func (a *App) payrollAction(tx *sql.Tx, u User, body []byte, key string) (any, error) {
	if !u.can("wages") {
		return nil, fail(403, "无工资权限")
	}
	var p payrollActionRequest
	if err := decode(body, &p); err != nil {
		return nil, err
	}
	p.Action = strings.TrimSpace(p.Action)
	if strings.HasPrefix(p.Action, "bonus-") {
		return bonusPayrollAction(tx, u, body, key)
	}
	switch p.Action {
	case "create-adjustment":
		return createPayrollAdjustment(tx, u, p, key)
	case "reverse-adjustment":
		return reversePayrollAdjustment(tx, u, p, key)
	case "create-deduction":
		return createPayrollDeduction(tx, u, p, key)
	case "edit-deduction":
		return editPayrollDeduction(tx, u, p, key)
	case "confirm-deduction":
		return confirmPayrollDeduction(tx, u, p, key)
	case "reverse-deduction":
		return reversePayrollDeduction(tx, u, p, key)
	case "create-advance":
		return createPayrollAdvance(tx, u, p, key)
	case "edit-advance":
		return editPayrollAdvance(tx, u, p, key)
	case "reverse-advance":
		return reversePayrollAdvance(tx, u, p, key)
	case "create-settlement":
		return createPayrollSettlement(tx, u, p, key)
	case "reverse-settlement":
		return reversePayrollSettlement(tx, u, p, key)
	case "create-payment":
		return createPayrollPayment(tx, u, p, key)
	case "reverse-payment":
		return reversePayrollPayment(tx, u, p, key)
	default:
		return nil, fail(400, "工资操作类型无效")
	}
}

func payrollDate(value, label string) error {
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(value)); err != nil || len(value) != 10 {
		return fail(400, label+"无效")
	}
	return nil
}

func payrollReason(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 1000 {
		return "", fail(400, "请填写1至1000字的原因")
	}
	return value, nil
}

func payrollNote(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 1000 {
		return "", fail(400, "备注不能超过1000字")
	}
	return value, nil
}

func payrollPositiveAmount(value int64) error {
	if value < 1 || value > maxPayrollCents {
		return fail(400, "金额必须大于0且不超过1万亿元")
	}
	return nil
}

func payrollSupplementAmount(value int64) error {
	if value < 1 || value > maxPayrollCents {
		return fail(400, "工资调整只能登记正数补发；减少工资请登记并确认扣款依据")
	}
	return nil
}

func addPayrollCents(total, value int64) (int64, error) {
	if (value > 0 && total > math.MaxInt64-value) || (value < 0 && total < math.MinInt64-value) {
		return 0, fail(400, "工资合计超出可处理范围")
	}
	return total + value, nil
}

func calculatePayrollPayable(gross, adjustment, deduction, advance int64, hasPayrollSource bool) (int64, error) {
	if !hasPayrollSource {
		return 0, fail(400, "请选择本次结算的工资来源")
	}
	base, err := addPayrollCents(gross, adjustment)
	if err != nil {
		return 0, err
	}
	base, err = addPayrollCents(base, -deduction)
	if err != nil {
		return 0, err
	}
	if base < 0 || advance < 0 || advance > base {
		return 0, fail(400, "扣款和预支抵扣不能超过本次工资来源金额")
	}
	payable := base - advance
	if payable > maxPayrollCents {
		return 0, fail(400, "结算金额超出上限")
	}
	return payable, nil
}

func validatePayrollSettlementProjectScope(projectIDs []string) error {
	scope := ""
	first := true
	for _, projectID := range projectIDs {
		if first {
			scope = projectID
			first = false
			continue
		}
		if projectID != scope {
			return fail(400, "一次结算不能混合未关联工地和不同工地的工资来源，请按工地或未关联范围拆分结算")
		}
	}
	return nil
}

func validatePayrollID(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "", fail(400, "请选择有效的"+label)
	}
	return value, nil
}

func getPayrollWorker(tx *sql.Tx, workerID string) (payrollWorkerSnapshot, error) {
	workerID, err := validatePayrollID(workerID, "施工人")
	if err != nil {
		return payrollWorkerSnapshot{}, err
	}
	var w payrollWorkerSnapshot
	err = tx.QueryRow("SELECT id,name FROM workers WHERE id=?", workerID).Scan(&w.ID, &w.Name)
	if err == nil {
		return w, nil
	}
	if err != sql.ErrNoRows {
		return w, err
	}
	queries := []string{
		"SELECT worker_name FROM " + workerWageSource + " ww WHERE worker_id=? ORDER BY worker_name LIMIT 1",
		"SELECT worker_name FROM payroll_adjustments WHERE worker_id=? ORDER BY created_at DESC,id LIMIT 1",
		"SELECT worker_name FROM payroll_deductions WHERE worker_id=? ORDER BY created_at DESC,id LIMIT 1",
		"SELECT worker_name FROM payroll_advances WHERE worker_id=? ORDER BY created_at DESC,id LIMIT 1",
	}
	for _, query := range queries {
		if err = tx.QueryRow(query, workerID).Scan(&w.Name); err == nil {
			w.ID = workerID
			return w, nil
		}
		if err != sql.ErrNoRows {
			return w, err
		}
	}
	return payrollWorkerSnapshot{}, fail(404, "施工人不存在")
}

func getPayrollProject(tx *sql.Tx, projectID string) (payrollProjectSnapshot, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return payrollProjectSnapshot{}, nil
	}
	if len(projectID) > 64 {
		return payrollProjectSnapshot{}, fail(400, "工地编号无效")
	}
	var p payrollProjectSnapshot
	err := tx.QueryRow("SELECT id,name,address FROM projects WHERE id=?", projectID).Scan(&p.ID, &p.Name, &p.Address)
	if err == sql.ErrNoRows {
		return p, fail(404, "关联工地不存在")
	}
	return p, err
}

func payrollHistory(tx *sql.Tx, u User, key, entityType, entityID, event, reason string, before, after any) error {
	var beforeJSON any
	if before != nil {
		beforeJSON = jsonText(before)
	}
	_, err := tx.Exec(`INSERT INTO payroll_history(id,entity_type,entity_id,event,reason,before_snapshot,after_snapshot,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,?,?,?,?)`, id(), entityType, entityID, event, reason, beforeJSON, jsonText(after), u.ID, u.Name, key)
	if err != nil {
		return err
	}
	return audit(tx, u, "payroll."+event, entityID, map[string]any{"entity_type": entityType, "reason": reason, "before": before, "after": after, "operation_id": key})
}

func createPayrollAdjustment(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	worker, err := getPayrollWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	if err = payrollSupplementAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.EffectiveDate, "生效日期"); err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	rowID := id()
	row := map[string]any{"id": rowID, "worker_id": worker.ID, "worker_name": worker.Name, "amount_cents": p.AmountCents, "effective_date": p.EffectiveDate, "project_id": project.ID, "project_name": project.Name, "address": project.Address, "reason": reason, "actor": u.Name, "version": 1}
	if _, err = tx.Exec(`INSERT INTO payroll_adjustments(id,worker_id,worker_name,amount_cents,effective_date,project_id,project_name,address,reason,actor_id,actor_name)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, rowID, worker.ID, worker.Name, p.AmountCents, p.EffectiveDate, project.ID, project.Name, project.Address, reason, u.ID, u.Name); err != nil {
		return nil, err
	}
	if err = payrollHistory(tx, u, key, "adjustment", rowID, "create-adjustment", reason, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": rowID, "version": int64(1)}, nil
}

func lockPayrollAdjustment(tx *sql.Tx, rowID string, version int64) (payrollAdjustmentSource, error) {
	var a payrollAdjustmentSource
	err := tx.QueryRow(`SELECT id,worker_id,worker_name,amount_cents,DATE_FORMAT(effective_date,'%Y-%m-%d'),project_id,project_name,address,reason,actor_id,actor_name,reversal_of,version
	 FROM payroll_adjustments WHERE id=? FOR UPDATE`, rowID).Scan(&a.ID, &a.WorkerID, &a.WorkerName, &a.Amount, &a.EffectiveAt, &a.ProjectID, &a.ProjectName, &a.Address, &a.Reason, &a.ActorID, &a.ActorName, &a.ReversalOf, &a.Version)
	if err == sql.ErrNoRows {
		return a, fail(404, "工资调整记录不存在")
	}
	if err != nil {
		return a, err
	}
	if version != a.Version {
		return a, fail(409, "工资调整记录已变化，请刷新后重试")
	}
	return a, nil
}

func payrollSourceAllocated(tx *sql.Tx, sourceType, sourceID string) (bool, error) {
	var settlementID string
	err := tx.QueryRow(`SELECT s.id FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id
	 WHERE src.source_type=? AND src.source_id=? AND s.status='confirmed' ORDER BY s.id LIMIT 1 FOR UPDATE`, sourceType, sourceID).Scan(&settlementID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func reversePayrollAdjustment(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "工资调整记录")
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	original, err := lockPayrollAdjustment(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	if original.ReversalOf.Valid {
		return nil, fail(409, "更正记录不能再次撤销")
	}
	var reversed int
	if err = tx.QueryRow("SELECT COUNT(*) FROM payroll_adjustments WHERE reversal_of=?", original.ID).Scan(&reversed); err != nil {
		return nil, err
	}
	if reversed != 0 {
		return nil, fail(409, "工资调整已经更正")
	}
	allocated, err := payrollSourceAllocated(tx, "adjustment", original.ID)
	if err != nil {
		return nil, err
	}
	if allocated {
		return nil, fail(409, "已结算工资调整不能撤销，请新增工资补差")
	}
	if original.Amount == math.MinInt64 {
		return nil, fail(400, "工资调整金额超出可处理范围")
	}
	newID := id()
	amount := -original.Amount
	if _, err = tx.Exec(`INSERT INTO payroll_adjustments(id,worker_id,worker_name,amount_cents,effective_date,project_id,project_name,address,reason,actor_id,actor_name,reversal_of)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, newID, original.WorkerID, original.WorkerName, amount, original.EffectiveAt, original.ProjectID, original.ProjectName, original.Address, reason, u.ID, u.Name, original.ID); err != nil {
		return nil, err
	}
	after := map[string]any{"id": newID, "reversal_of": original.ID, "worker_id": original.WorkerID, "amount_cents": amount, "reason": reason}
	if err = payrollHistory(tx, u, key, "adjustment", original.ID, "reverse-adjustment", reason, original, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": newID, "reversal_of": original.ID, "version": int64(1)}, nil
}

func createPayrollDeduction(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	worker, err := getPayrollWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	if err = payrollPositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.DeductionDate, "扣款日期"); err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	rowID := id()
	row := map[string]any{"id": rowID, "worker_id": worker.ID, "worker_name": worker.Name, "amount_cents": p.AmountCents, "deduction_date": p.DeductionDate, "project_id": project.ID, "project_name": project.Name, "address": project.Address, "reason": reason, "status": "pending", "version": 1}
	if _, err = tx.Exec(`INSERT INTO payroll_deductions(id,worker_id,worker_name,amount_cents,deduction_date,project_id,project_name,address,reason,status,actor_id,actor_name)
	 VALUES(?,?,?,?,?,?,?,?,?,'pending',?,?)`, rowID, worker.ID, worker.Name, p.AmountCents, p.DeductionDate, project.ID, project.Name, project.Address, reason, u.ID, u.Name); err != nil {
		return nil, err
	}
	if err = payrollHistory(tx, u, key, "deduction", rowID, "create-deduction", reason, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": rowID, "status": "pending", "version": int64(1)}, nil
}

func lockPayrollDeduction(tx *sql.Tx, rowID string, version int64) (payrollDeductionSource, error) {
	var d payrollDeductionSource
	err := tx.QueryRow(`SELECT id,worker_id,worker_name,amount_cents,DATE_FORMAT(deduction_date,'%Y-%m-%d'),project_id,project_name,address,reason,legal_basis,status,actor_id,actor_name,version
	 FROM payroll_deductions WHERE id=? FOR UPDATE`, rowID).Scan(&d.ID, &d.WorkerID, &d.WorkerName, &d.Amount, &d.DeductionAt, &d.ProjectID, &d.ProjectName, &d.Address, &d.Reason, &d.LegalBasis, &d.Status, &d.ActorID, &d.ActorName, &d.Version)
	if err == sql.ErrNoRows {
		return d, fail(404, "扣款记录不存在")
	}
	if err != nil {
		return d, err
	}
	if version != d.Version {
		return d, fail(409, "扣款记录已变化，请刷新后重试")
	}
	return d, nil
}

func editPayrollDeduction(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "扣款记录")
	if err != nil {
		return nil, err
	}
	d, err := lockPayrollDeduction(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	if d.Status == "voided" {
		return nil, fail(409, "已撤销扣款不能修改")
	}
	allocated, err := payrollSourceAllocated(tx, "deduction", d.ID)
	if err != nil {
		return nil, err
	}
	if allocated {
		return nil, fail(409, "已纳入結算的扣款不能覆盖修改")
	}
	if err = payrollPositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.DeductionDate, "扣款日期"); err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	legalBasis := ""
	if d.Status == "confirmed" {
		legalBasis, err = payrollReason(p.LegalBasis)
		if err != nil {
			return nil, fail(400, "请填写扣款的合法或约定依据")
		}
	} else if d.Status != "pending" {
		return nil, fail(409, "只有待确认或已确认扣款可以修改")
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	before := d
	result, err := tx.Exec(`UPDATE payroll_deductions SET amount_cents=?,deduction_date=?,project_id=?,project_name=?,address=?,reason=?,legal_basis=?,version=version+1,updated_at=UTC_TIMESTAMP()
	 WHERE id=? AND version=? AND status=?`, p.AmountCents, p.DeductionDate, project.ID, project.Name, project.Address, reason, legalBasis, d.ID, d.Version, d.Status)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, fail(409, "扣款记录已变化，请刷新后重试")
	}
	row := map[string]any{"id": d.ID, "worker_id": d.WorkerID, "worker_name": d.WorkerName, "amount_cents": p.AmountCents, "deduction_date": p.DeductionDate, "project_id": project.ID, "project_name": project.Name, "address": project.Address, "reason": reason, "legal_basis": legalBasis, "status": d.Status, "version": d.Version + 1}
	if err = payrollHistory(tx, u, key, "deduction", d.ID, "edit-deduction", reason, before, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": d.ID, "version": d.Version + 1}, nil
}

func confirmPayrollDeduction(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "扣款记录")
	if err != nil {
		return nil, err
	}
	d, err := lockPayrollDeduction(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	basis, err := payrollReason(p.LegalBasis)
	if err != nil {
		return nil, fail(400, "请填写扣款的合法或约定依据")
	}
	if d.Status != "pending" {
		return nil, fail(409, "只有待确认扣款可以确认")
	}
	result, err := tx.Exec(`UPDATE payroll_deductions SET legal_basis=?,status='confirmed',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='pending'`, basis, d.ID, d.Version)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, fail(409, "扣款记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": d.ID, "worker_id": d.WorkerID, "worker_name": d.WorkerName, "amount_cents": d.Amount, "status": "confirmed", "legal_basis": basis, "version": d.Version + 1}
	if err = payrollHistory(tx, u, key, "deduction", d.ID, "confirm-deduction", basis, d, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": d.ID, "status": "confirmed", "version": d.Version + 1}, nil
}

func reversePayrollDeduction(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "扣款记录")
	if err != nil {
		return nil, err
	}
	d, err := lockPayrollDeduction(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	if d.Status == "voided" {
		return nil, fail(409, "扣款已经撤销")
	}
	allocated, err := payrollSourceAllocated(tx, "deduction", d.ID)
	if err != nil {
		return nil, err
	}
	if allocated {
		return nil, fail(409, "已纳入结算的扣款不能撤销；已付款差异请新增工资调整")
	}
	result, err := tx.Exec(`UPDATE payroll_deductions SET status='voided',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?`, d.ID, d.Version)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, fail(409, "扣款记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": d.ID, "status": "voided", "reason": reason, "version": d.Version + 1}
	if err = payrollHistory(tx, u, key, "deduction", d.ID, "reverse-deduction", reason, d, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": d.ID, "version": d.Version + 1}, nil
}

func createPayrollAdvance(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	worker, err := getPayrollWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	if err = payrollPositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.AdvanceDate, "预支日期"); err != nil {
		return nil, err
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	rowID := id()
	row := map[string]any{"id": rowID, "worker_id": worker.ID, "worker_name": worker.Name, "amount_cents": p.AmountCents, "advance_date": p.AdvanceDate, "project_id": project.ID, "project_name": project.Name, "address": project.Address, "note": note, "status": "active", "version": 1}
	if _, err = tx.Exec(`INSERT INTO payroll_advances(id,worker_id,worker_name,amount_cents,advance_date,project_id,project_name,address,note,actor_id,actor_name)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, rowID, worker.ID, worker.Name, p.AmountCents, p.AdvanceDate, project.ID, project.Name, project.Address, note, u.ID, u.Name); err != nil {
		return nil, err
	}
	if err = payrollHistory(tx, u, key, "advance", rowID, "create-advance", note, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": rowID, "status": "active", "version": int64(1)}, nil
}

func lockPayrollAdvance(tx *sql.Tx, rowID string, version int64) (payrollAdvanceSource, error) {
	var a payrollAdvanceSource
	err := tx.QueryRow(`SELECT id,worker_id,worker_name,amount_cents,DATE_FORMAT(advance_date,'%Y-%m-%d'),project_id,project_name,address,note,status,actor_id,actor_name,version
	 FROM payroll_advances WHERE id=? FOR UPDATE`, rowID).Scan(&a.ID, &a.WorkerID, &a.WorkerName, &a.Amount, &a.AdvanceAt, &a.ProjectID, &a.ProjectName, &a.Address, &a.Note, &a.Status, &a.ActorID, &a.ActorName, &a.Version)
	if err == sql.ErrNoRows {
		return a, fail(404, "工资预支记录不存在")
	}
	if err != nil {
		return a, err
	}
	if version != a.Version {
		return a, fail(409, "预支记录已变化，请刷新后重试")
	}
	return a, nil
}

func payrollAdvanceAllocated(tx *sql.Tx, advanceID string) (int64, error) {
	var amount int64
	err := tx.QueryRow(`SELECT COALESCE(SUM(src.amount_cents),0) FROM payroll_settlement_sources src
	 JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND src.source_id=? AND s.status='confirmed'`, advanceID).Scan(&amount)
	return amount, err
}

func editPayrollAdvance(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "工资预支记录")
	if err != nil {
		return nil, err
	}
	a, err := lockPayrollAdvance(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	if a.Status != "active" {
		return nil, fail(409, "已撤销预支不能修改")
	}
	if err = payrollPositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.AdvanceDate, "预支日期"); err != nil {
		return nil, err
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	allocated, err := payrollAdvanceAllocated(tx, a.ID)
	if err != nil {
		return nil, err
	}
	if p.AmountCents < allocated {
		return nil, fail(409, "预支总额不能小于已经抵扣金额")
	}
	result, err := tx.Exec(`UPDATE payroll_advances SET amount_cents=?,advance_date=?,project_id=?,project_name=?,address=?,note=?,version=version+1,updated_at=UTC_TIMESTAMP()
	 WHERE id=? AND version=?`, p.AmountCents, p.AdvanceDate, project.ID, project.Name, project.Address, note, a.ID, a.Version)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, fail(409, "预支记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": a.ID, "worker_id": a.WorkerID, "worker_name": a.WorkerName, "amount_cents": p.AmountCents, "advance_date": p.AdvanceDate, "project_id": project.ID, "project_name": project.Name, "address": project.Address, "note": note, "version": a.Version + 1}
	if err = payrollHistory(tx, u, key, "advance", a.ID, "edit-advance", note, a, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": a.ID, "version": a.Version + 1}, nil
}

func reversePayrollAdvance(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "工资预支记录")
	if err != nil {
		return nil, err
	}
	a, err := lockPayrollAdvance(tx, rowID, p.Version)
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	if a.Status != "active" {
		return nil, fail(409, "预支记录已经撤销")
	}
	allocated, err := payrollAdvanceAllocated(tx, a.ID)
	if err != nil {
		return nil, err
	}
	if allocated != 0 {
		return nil, fail(409, "已有预支抵扣的记录不能撤销，请保留余额并另行更正")
	}
	result, err := tx.Exec(`UPDATE payroll_advances SET status='voided',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?`, a.ID, a.Version)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, fail(409, "预支记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": a.ID, "status": "voided", "reason": reason, "version": a.Version + 1}
	if err = payrollHistory(tx, u, key, "advance", a.ID, "reverse-advance", reason, a, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": a.ID, "version": a.Version + 1}, nil
}

func uniquePayrollIDs(values []string, label string) ([]string, error) {
	if len(values) > 100 {
		return nil, fail(400, label+"数量超出范围")
	}
	seen := make(map[string]bool, len(values))
	result := append([]string{}, values...)
	for i := range result {
		value, err := validatePayrollID(result[i], label)
		if err != nil {
			return nil, err
		}
		if seen[value] {
			return nil, fail(400, label+"不能重复")
		}
		seen[value] = true
		result[i] = value
	}
	sort.Strings(result)
	return result, nil
}

func versionMap(values []payrollSourceVersion, selected []string, label string) (map[string]int64, error) {
	result := make(map[string]int64, len(values))
	for _, v := range values {
		if v.ID == "" || v.Version < 1 || result[v.ID] != 0 {
			return nil, fail(400, label+"版本参数无效")
		}
		result[v.ID] = v.Version
	}
	if len(result) != len(selected) {
		return nil, fail(400, label+"版本信息不完整，请刷新后重试")
	}
	for _, id := range selected {
		if result[id] < 1 {
			return nil, fail(400, label+"版本信息不完整，请刷新后重试")
		}
	}
	return result, nil
}

func lockPayrollWages(tx *sql.Tx, workerID string, ids []string, versions map[string]int64) ([]payrollWageSource, error) {
	if len(ids) == 0 {
		return []payrollWageSource{}, nil
	}
	orderByWage := make(map[string]string, len(ids))
	orderSet := map[string]bool{}
	for _, wageID := range ids {
		var orderID string
		err := tx.QueryRow("SELECT order_id FROM "+workerWageSource+" ww WHERE ww.id=? AND ww.worker_id=?", wageID, workerID).Scan(&orderID)
		if err == sql.ErrNoRows {
			return nil, fail(404, "所选施工工资不存在或已变更")
		}
		if err != nil {
			return nil, err
		}
		orderByWage[wageID], orderSet[orderID] = orderID, true
	}
	orderIDs := make([]string, 0, len(orderSet))
	for orderID := range orderSet {
		orderIDs = append(orderIDs, orderID)
	}
	sort.Strings(orderIDs)
	orderProjects := make(map[string]string, len(orderIDs))
	for _, orderID := range orderIDs {
		var status, projectID string
		if err := tx.QueryRow("SELECT status,project_id FROM orders WHERE id=? FOR UPDATE", orderID).Scan(&status, &projectID); err != nil {
			if err == sql.ErrNoRows {
				return nil, fail(404, "施工记录不存在")
			}
			return nil, err
		}
		orderProjects[orderID] = projectID
		if status == "已取消" {
			return nil, fail(409, "已取消施工记录不能新纳入工资结算")
		}
		if err := validateOrderWorkerAccount(tx, orderID); err != nil {
			return nil, err
		}
		if err := materializeLegacyWage(tx, orderID); err != nil {
			return nil, err
		}
	}
	rows := make([]payrollWageSource, 0, len(ids))
	for _, wageID := range ids {
		var v payrollWageSource
		err := tx.QueryRow(`SELECT id,order_id,worker_id,worker_name,wage_cents,delivery_wage_cents,wage_paid,version
		 FROM order_worker_wages WHERE id=? FOR UPDATE`, wageID).Scan(&v.ID, &v.OrderID, &v.WorkerID, &v.WorkerName, &v.Amount, &v.Delivery, &v.WagePaid, &v.Version)
		if err != nil {
			return nil, fail(409, "施工工资来源已变化，请刷新后重试")
		}
		if orderByWage[wageID] != v.OrderID || v.WorkerID != workerID || v.Version != versions[wageID] || v.WagePaid {
			return nil, fail(409, "施工工资来源已变化或已经发放，请刷新后重试")
		}
		v.ProjectID = orderProjects[v.OrderID]
		allocated, err := payrollSourceAllocated(tx, "wage", wageID)
		if err != nil {
			return nil, err
		}
		if allocated {
			return nil, fail(409, "施工工资已经纳入其他结算")
		}
		rows = append(rows, v)
	}
	return rows, nil
}

func lockPayrollAdjustments(tx *sql.Tx, workerID string, ids []string) ([]payrollAdjustmentSource, error) {
	rows := make([]payrollAdjustmentSource, 0, len(ids))
	for _, rowID := range ids {
		a, err := lockPayrollAdjustment(tx, rowID, 1)
		if err != nil {
			return nil, err
		}
		if a.WorkerID != workerID || a.ReversalOf.Valid {
			return nil, fail(409, "工资调整不属于所选施工人或已被更正")
		}
		var reversed int
		if err = tx.QueryRow("SELECT COUNT(*) FROM payroll_adjustments WHERE reversal_of=?", a.ID).Scan(&reversed); err != nil {
			return nil, err
		}
		if reversed != 0 {
			return nil, fail(409, "工资调整已被更正")
		}
		allocated, err := payrollSourceAllocated(tx, "adjustment", a.ID)
		if err != nil {
			return nil, err
		}
		if allocated {
			return nil, fail(409, "工资调整已纳入其他结算")
		}
		rows = append(rows, a)
	}
	return rows, nil
}

func lockPayrollDeductions(tx *sql.Tx, workerID string, ids []string, versions map[string]int64) ([]payrollDeductionSource, error) {
	rows := make([]payrollDeductionSource, 0, len(ids))
	for _, rowID := range ids {
		d, err := lockPayrollDeduction(tx, rowID, versions[rowID])
		if err != nil {
			return nil, err
		}
		if d.WorkerID != workerID || d.Status != "confirmed" || strings.TrimSpace(d.LegalBasis) == "" {
			return nil, fail(409, "扣款必须属于所选施工人且已确认依据")
		}
		allocated, err := payrollSourceAllocated(tx, "deduction", d.ID)
		if err != nil {
			return nil, err
		}
		if allocated {
			return nil, fail(409, "扣款已纳入其他结算")
		}
		rows = append(rows, d)
	}
	return rows, nil
}

func createPayrollSettlement(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	worker, err := getPayrollWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	if err = payrollDate(p.SettlementDate, "结算日期"); err != nil {
		return nil, err
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	wageIDs, err := uniquePayrollIDs(p.WageIDs, "施工工资")
	if err != nil {
		return nil, err
	}
	adjustmentIDs, err := uniquePayrollIDs(p.AdjustmentIDs, "工资调整")
	if err != nil {
		return nil, err
	}
	deductionIDs, err := uniquePayrollIDs(p.DeductionIDs, "扣款")
	if err != nil {
		return nil, err
	}
	wageVersions, err := versionMap(p.WageVersions, wageIDs, "施工工资")
	if err != nil {
		return nil, err
	}
	deductionVersions, err := versionMap(p.DeductionVersions, deductionIDs, "扣款")
	if err != nil {
		return nil, err
	}
	if len(p.AdvanceAllocations) > 100 || len(wageIDs)+len(adjustmentIDs)+len(deductionIDs) == 0 {
		return nil, fail(400, "请选择本次结算的工资来源")
	}
	advances := append([]payrollAdvanceAllocation{}, p.AdvanceAllocations...)
	sort.Slice(advances, func(i, j int) bool { return advances[i].ID < advances[j].ID })
	seenAdvance := map[string]bool{}
	for _, a := range advances {
		if a.ID == "" || a.Version < 1 || seenAdvance[a.ID] || payrollPositiveAmount(a.AmountCents) != nil {
			return nil, fail(400, "预支抵扣项目无效")
		}
		seenAdvance[a.ID] = true
	}
	wages, err := lockPayrollWages(tx, worker.ID, wageIDs, wageVersions)
	if err != nil {
		return nil, err
	}
	adjustments, err := lockPayrollAdjustments(tx, worker.ID, adjustmentIDs)
	if err != nil {
		return nil, err
	}
	deductions, err := lockPayrollDeductions(tx, worker.ID, deductionIDs, deductionVersions)
	if err != nil {
		return nil, err
	}
	var gross, adjustmentTotal, deductionTotal, advanceTotal int64
	for _, v := range wages {
		gross, err = addPayrollCents(gross, v.Amount)
		if err != nil {
			return nil, err
		}
	}
	for _, v := range adjustments {
		adjustmentTotal, err = addPayrollCents(adjustmentTotal, v.Amount)
		if err != nil {
			return nil, err
		}
	}
	for _, v := range deductions {
		deductionTotal, err = addPayrollCents(deductionTotal, v.Amount)
		if err != nil {
			return nil, err
		}
	}
	advanceRows := make([]payrollAdvanceSource, 0, len(advances))
	for _, selected := range advances {
		a, lockErr := lockPayrollAdvance(tx, selected.ID, selected.Version)
		if lockErr != nil {
			return nil, lockErr
		}
		if a.WorkerID != worker.ID || a.Status != "active" {
			return nil, fail(409, "预支不属于所选施工人或已经撤销")
		}
		allocated, allocErr := payrollAdvanceAllocated(tx, a.ID)
		if allocErr != nil {
			return nil, allocErr
		}
		remaining := a.Amount - allocated
		if selected.AmountCents > remaining {
			return nil, fail(409, "预支可抵扣余额已变化，请刷新后重试")
		}
		advanceTotal, err = addPayrollCents(advanceTotal, selected.AmountCents)
		if err != nil {
			return nil, err
		}
		advanceRows = append(advanceRows, a)
	}
	projectScopes := make([]string, 0, len(wages)+len(adjustments)+len(deductions)+len(advanceRows))
	for _, v := range wages {
		projectScopes = append(projectScopes, v.ProjectID)
	}
	for _, v := range adjustments {
		projectScopes = append(projectScopes, v.ProjectID)
	}
	for _, v := range deductions {
		projectScopes = append(projectScopes, v.ProjectID)
	}
	for _, v := range advanceRows {
		projectScopes = append(projectScopes, v.ProjectID)
	}
	if err = validatePayrollSettlementProjectScope(projectScopes); err != nil {
		return nil, err
	}
	payable, err := calculatePayrollPayable(gross, adjustmentTotal, deductionTotal, advanceTotal, len(wageIDs)+len(adjustmentIDs)+len(deductionIDs) > 0)
	if err != nil {
		return nil, err
	}
	settlementID := id()
	if _, err = tx.Exec(`INSERT INTO payroll_settlements(id,worker_id,worker_name,settlement_date,gross_cents,adjustment_cents,deduction_cents,advance_cents,payable_cents,status,version,note,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,?,?,?,'confirmed',1,?,?,?,?)`, settlementID, worker.ID, worker.Name, p.SettlementDate, gross, adjustmentTotal, deductionTotal, advanceTotal, payable, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	insertSource := func(kind, sourceID, projectID string, amount int64) error {
		_, e := tx.Exec("INSERT INTO payroll_settlement_sources(settlement_id,source_type,source_id,worker_id,project_id,amount_cents) VALUES(?,?,?,?,?,?)", settlementID, kind, sourceID, worker.ID, projectID, amount)
		return e
	}
	for _, v := range wages {
		if err = insertSource("wage", v.ID, v.ProjectID, v.Amount); err != nil {
			return nil, err
		}
	}
	for _, v := range adjustments {
		if err = insertSource("adjustment", v.ID, v.ProjectID, v.Amount); err != nil {
			return nil, err
		}
	}
	for _, v := range deductions {
		if err = insertSource("deduction", v.ID, v.ProjectID, v.Amount); err != nil {
			return nil, err
		}
	}
	for i, selected := range advances {
		if err = insertSource("advance", selected.ID, advanceRows[i].ProjectID, selected.AmountCents); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE payroll_advances SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", advanceRows[i].ID, advanceRows[i].Version); err != nil {
			return nil, err
		}
	}
	row := map[string]any{"id": settlementID, "worker_id": worker.ID, "worker_name": worker.Name, "settlement_date": p.SettlementDate, "gross_cents": gross, "adjustment_cents": adjustmentTotal, "deduction_cents": deductionTotal, "advance_cents": advanceTotal, "payable_cents": payable, "paid_cents": 0, "remaining_cents": payable, "status": "confirmed", "version": 1, "note": note}
	if err = payrollHistory(tx, u, key, "settlement", settlementID, "create-settlement", note, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": settlementID, "version": int64(1), "payable_cents": payable}, nil
}

func lockPayrollSettlement(tx *sql.Tx, rowID string) (payrollSettlementSource, error) {
	var s payrollSettlementSource
	err := tx.QueryRow("SELECT id,worker_id,worker_name,payable_cents,status,legacy_paid,version FROM payroll_settlements WHERE id=? FOR UPDATE", rowID).Scan(&s.ID, &s.WorkerID, &s.WorkerName, &s.Payable, &s.Status, &s.LegacyPaid, &s.Version)
	if err == sql.ErrNoRows {
		return s, fail(404, "工资结算记录不存在")
	}
	return s, err
}

func reversePayrollSettlement(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	rowID, err := validatePayrollID(p.ID, "工资结算记录")
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	s, err := lockPayrollSettlement(tx, rowID)
	if err != nil {
		return nil, err
	}
	if p.Version != s.Version || s.Status != "confirmed" {
		return nil, fail(409, "工资结算已变化或已经撤销")
	}
	var payments int
	if err = tx.QueryRow("SELECT COUNT(*) FROM payroll_payments WHERE settlement_id=?", s.ID).Scan(&payments); err != nil {
		return nil, err
	}
	if payments != 0 || s.LegacyPaid {
		return nil, fail(409, "已有实际发放的结算不能撤销，请用工资调整处理差额")
	}
	before := map[string]any{"id": s.ID, "worker_id": s.WorkerID, "worker_name": s.WorkerName, "payable_cents": s.Payable, "status": s.Status, "version": s.Version}
	if _, err = tx.Exec("UPDATE payroll_settlements SET status='voided',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", s.ID, s.Version); err != nil {
		return nil, err
	}
	after := map[string]any{"id": s.ID, "status": "voided", "reason": reason, "version": s.Version + 1}
	if err = payrollHistory(tx, u, key, "settlement", s.ID, "reverse-settlement", reason, before, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": s.ID, "version": s.Version + 1}, nil
}

func createPayrollPayment(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	settlementID, err := validatePayrollID(p.SettlementID, "工资结算记录")
	if err != nil {
		return nil, err
	}
	if err = payrollPositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = payrollDate(p.PaidDate, "实际发放日期"); err != nil {
		return nil, err
	}
	method := strings.TrimSpace(p.Method)
	if len([]rune(method)) > 40 {
		return nil, fail(400, "发放方式不能超过40字")
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	if err = lockPayrollSettlementWageOrders(tx, settlementID); err != nil {
		return nil, err
	}
	s, err := lockPayrollSettlement(tx, settlementID)
	if err != nil {
		return nil, err
	}
	if s.Version != p.SettlementVersion || s.Status != "confirmed" || s.LegacyPaid {
		return nil, fail(409, "工资结算已变化或不能继续发放")
	}
	var paid int64
	if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=? AND status='active'", s.ID).Scan(&paid); err != nil {
		return nil, err
	}
	remaining := s.Payable - paid
	if remaining < 0 || p.AmountCents > remaining {
		return nil, fail(409, "本次发放超过結算剩余金额，请刷新核对")
	}
	if remaining == 0 {
		return nil, fail(409, "该结算已经全部发放")
	}
	paymentID := id()
	if _, err = tx.Exec(`INSERT INTO payroll_payments(id,settlement_id,amount_cents,paid_date,method,note,status,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,'active',?,?,?)`, paymentID, s.ID, p.AmountCents, p.PaidDate, method, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE payroll_settlements SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", s.ID, s.Version); err != nil {
		return nil, err
	}
	if err = markPayrollWageSourcesPaid(tx, s.ID); err != nil {
		return nil, err
	}
	newPaid := paid + p.AmountCents
	before := map[string]any{"settlement_id": s.ID, "version": s.Version, "paid_cents": paid, "remaining_cents": remaining}
	after := map[string]any{"id": paymentID, "settlement_id": s.ID, "amount_cents": p.AmountCents, "paid_date": p.PaidDate, "method": method, "note": note, "paid_cents": newPaid, "remaining_cents": s.Payable - newPaid, "version": s.Version + 1}
	if err = payrollHistory(tx, u, key, "payment", paymentID, "create-payment", note, before, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": paymentID, "settlement_id": s.ID, "version": s.Version + 1, "paid_cents": newPaid, "remaining_cents": s.Payable - newPaid}, nil
}

func markPayrollWageSourcesPaid(tx *sql.Tx, settlementID string) error {
	orderIDs, err := scanRows[string](tx, `SELECT DISTINCT o.id FROM payroll_settlement_sources src JOIN order_worker_wages ww ON src.source_type='wage' AND src.source_id=ww.id JOIN orders o ON o.id=ww.order_id
	 WHERE src.settlement_id=? ORDER BY o.id`, []any{settlementID}, func(r *sql.Rows, v *string) error { return r.Scan(v) })
	if err != nil {
		return err
	}
	if len(orderIDs) == 0 {
		return nil
	}
	if _, err = tx.Exec(`UPDATE order_worker_wages ww JOIN payroll_settlement_sources src ON src.source_type='wage' AND src.source_id=ww.id
	 SET ww.wage_paid=1,ww.version=ww.version+1 WHERE src.settlement_id=? AND ww.wage_paid=0`, settlementID); err != nil {
		return err
	}
	for _, orderID := range orderIDs {
		if _, err = tx.Exec(`UPDATE orders SET wage_paid=EXISTS(SELECT 1 FROM order_worker_wages w WHERE w.order_id=? AND w.wage_paid=1),version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?`, orderID, orderID); err != nil {
			return err
		}
	}
	return nil
}

func lockPayrollSettlementWageOrders(tx *sql.Tx, settlementID string) error {
	orderIDs, err := scanRows[string](tx, `SELECT DISTINCT ww.order_id FROM payroll_settlement_sources src
	 JOIN order_worker_wages ww ON src.source_type='wage' AND src.source_id=ww.id
	 WHERE src.settlement_id=? ORDER BY ww.order_id`, []any{settlementID}, func(r *sql.Rows, v *string) error { return r.Scan(v) })
	if err != nil {
		return err
	}
	for _, orderID := range orderIDs {
		var lockedID string
		if err = tx.QueryRow("SELECT id FROM orders WHERE id=? FOR UPDATE", orderID).Scan(&lockedID); err != nil {
			return err
		}
	}
	return nil
}

func reversePayrollPayment(tx *sql.Tx, u User, p payrollActionRequest, key string) (any, error) {
	settlementID, err := validatePayrollID(p.SettlementID, "工资结算记录")
	if err != nil {
		return nil, err
	}
	paymentID, err := validatePayrollID(p.ID, "工资付款记录")
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	s, err := lockPayrollSettlement(tx, settlementID)
	if err != nil {
		return nil, err
	}
	if s.Status != "confirmed" || s.LegacyPaid {
		return nil, fail(409, "历史迁移的已发标记不能作为付款撤销")
	}
	var pmt payrollPaymentSource
	err = tx.QueryRow("SELECT id,settlement_id,amount_cents,status,version,reversal_reason FROM payroll_payments WHERE id=? FOR UPDATE", paymentID).Scan(&pmt.ID, &pmt.SettlementID, &pmt.Amount, &pmt.Status, &pmt.Version, &pmt.ReversalReason)
	if err == sql.ErrNoRows || pmt.SettlementID != s.ID {
		return nil, fail(404, "工资付款记录不存在")
	}
	if err != nil {
		return nil, err
	}
	if pmt.Version != p.Version || pmt.Status != "active" {
		return nil, fail(409, "工资付款记录已变化或已经撤销")
	}
	before := map[string]any{"id": pmt.ID, "settlement_id": s.ID, "amount_cents": pmt.Amount, "status": pmt.Status, "version": pmt.Version}
	if _, err = tx.Exec(`UPDATE payroll_payments SET status='reversed',reversal_reason=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='active'`, reason, pmt.ID, pmt.Version); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE payroll_settlements SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", s.ID, s.Version); err != nil {
		return nil, err
	}
	var paid int64
	if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payroll_payments WHERE settlement_id=? AND status='active'", s.ID).Scan(&paid); err != nil {
		return nil, err
	}
	after := map[string]any{"id": pmt.ID, "settlement_id": s.ID, "amount_cents": pmt.Amount, "status": "reversed", "reversal_reason": reason, "paid_cents": paid, "remaining_cents": s.Payable - paid, "version": pmt.Version + 1}
	if err = payrollHistory(tx, u, key, "payment", pmt.ID, "reverse-payment", reason, before, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": pmt.ID, "settlement_id": s.ID, "version": pmt.Version + 1, "paid_cents": paid, "remaining_cents": s.Payable - paid}, nil
}

func payrollActionAvailable(tx *sql.Tx, sourceType, sourceID string) (bool, error) {
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type=? AND src.source_id=? AND s.status='confirmed'", sourceType, sourceID).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func payrollErrorContext(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("payroll %s: %w", action, err)
}
