package main

import (
	"database/sql"
	"math"
	"strings"
	"time"
)

type bonusActionRequest struct {
	Action            string `json:"action"`
	ID                string `json:"id"`
	SettlementID      string `json:"settlement_id"`
	WorkerID          string `json:"worker_id"`
	BonusYear         int    `json:"bonus_year"`
	AmountCents       int64  `json:"amount_cents"`
	EffectiveDate     string `json:"effective_date"`
	SettlementDate    string `json:"settlement_date"`
	PaidDate          string `json:"paid_date"`
	ProjectID         string `json:"project_id"`
	Reason            string `json:"reason"`
	Note              string `json:"note"`
	Method            string `json:"method"`
	Version           int64  `json:"version"`
	SettlementVersion int64  `json:"settlement_version"`
}

type bonusSettlementSource struct {
	ID           string
	WorkerID     string
	WorkerName   string
	BonusYear    int
	SettlementAt string
	Amount       int64
	Status       string
	Version      int64
	Note         string
	ActorName    string
}

type bonusPaymentSource struct {
	ID             string
	SettlementID   string
	Amount         int64
	PaidDate       string
	Method         string
	Note           string
	Status         string
	Version        int64
	ReversalReason string
}

func bonusYear(value int) error {
	if value < 1000 || value > 9999 {
		return fail(400, "奖金年度无效")
	}
	return nil
}

func bonusDate(value, label string, expectedYear int) error {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil || len(value) != 10 {
		return fail(400, label+"无效")
	}
	if expectedYear != 0 && parsed.Year() != expectedYear {
		return fail(400, label+"必须属于所选奖金年度")
	}
	return nil
}

func bonusSignedAmount(value int64) error {
	if value == 0 || value < -maxPayrollCents || value > maxPayrollCents {
		return fail(400, "奖金调整金额不能为0且绝对值不能超过1万亿元")
	}
	return nil
}

func lockBonusWorker(tx *sql.Tx, workerID string) (payrollWorkerSnapshot, error) {
	workerID, err := validatePayrollID(workerID, "施工人")
	if err != nil {
		return payrollWorkerSnapshot{}, err
	}
	if _, err = tx.Exec("INSERT IGNORE INTO payroll_bonus_worker_locks(worker_id) VALUES(?)", workerID); err != nil {
		return payrollWorkerSnapshot{}, err
	}
	var lockedID string
	if err = tx.QueryRow("SELECT worker_id FROM payroll_bonus_worker_locks WHERE worker_id=? FOR UPDATE", workerID).Scan(&lockedID); err != nil {
		return payrollWorkerSnapshot{}, err
	}
	var worker payrollWorkerSnapshot
	err = tx.QueryRow("SELECT id,name FROM workers WHERE id=? FOR UPDATE", workerID).Scan(&worker.ID, &worker.Name)
	if err == sql.ErrNoRows {
		worker, err = getPayrollWorker(tx, workerID)
		if err == nil {
			return worker, nil
		}
		missingWorker, ok := err.(failure)
		if !ok || missingWorker.Code != 404 {
			return payrollWorkerSnapshot{}, err
		}
		for _, query := range []string{
			"SELECT worker_name FROM payroll_bonus_entries WHERE worker_id=? ORDER BY created_at DESC,id DESC LIMIT 1",
			"SELECT worker_name FROM payroll_bonus_settlements WHERE worker_id=? ORDER BY created_at DESC,id DESC LIMIT 1",
		} {
			if nameErr := tx.QueryRow(query, workerID).Scan(&worker.Name); nameErr == nil {
				worker.ID = workerID
				return worker, nil
			} else if nameErr != sql.ErrNoRows {
				return payrollWorkerSnapshot{}, nameErr
			}
		}
		return payrollWorkerSnapshot{}, err
	}
	return worker, err
}

func bonusHistory(tx *sql.Tx, u User, operationID, entityType, entityID, event, reason string, before, after any) error {
	return payrollHistory(tx, u, operationID, entityType, entityID, event, reason, before, after)
}

func bonusPayrollAction(tx *sql.Tx, u User, body []byte, key string) (any, error) {
	var request bonusActionRequest
	if err := decode(body, &request); err != nil {
		return nil, err
	}
	request.Action = strings.TrimSpace(request.Action)
	switch request.Action {
	case "bonus-create-adjustment":
		return createBonusAdjustment(tx, u, request, key)
	case "bonus-reverse-adjustment":
		return reverseBonusAdjustment(tx, u, request, key)
	case "bonus-create-settlement":
		return createBonusSettlement(tx, u, request, key)
	case "bonus-reverse-settlement":
		return reverseBonusSettlement(tx, u, request, key)
	case "bonus-create-payment":
		return createBonusPayment(tx, u, request, key)
	case "bonus-reverse-payment":
		return reverseBonusPayment(tx, u, request, key)
	default:
		return nil, fail(400, "奖金操作类型无效")
	}
}

func createBonusAdjustment(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	if err := bonusYear(p.BonusYear); err != nil {
		return nil, err
	}
	if err := bonusDate(p.EffectiveDate, "生效日期", p.BonusYear); err != nil {
		return nil, err
	}
	if err := bonusSignedAmount(p.AmountCents); err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	worker, err := lockBonusWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	summary, err := readBonusSummary(tx, worker.ID, p.BonusYear)
	if err != nil {
		return nil, err
	}
	if err = validateBonusChangeAgainstSettlements(summary, 0, p.AmountCents); err != nil {
		return nil, err
	}
	project, err := getPayrollProject(tx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	row := bonusEntry{ID: id(), SourceKey: "manual:" + id(), EntryKind: "manual", WorkerID: worker.ID, WorkerName: worker.Name,
		ProjectID: project.ID, ProjectName: project.Name, Address: project.Address, WorkDate: p.EffectiveDate, BonusYear: p.BonusYear,
		AmountCents: p.AmountCents, Reason: reason, ActorName: u.Name, Version: 1}
	if _, err = insertBonusEntry(tx, u, key, row); err != nil {
		return nil, err
	}
	if err = bonusHistory(tx, u, key, "bonus", row.ID, "create-bonus-adjustment", reason, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "version": int64(1)}, nil
}

func insertBonusEntry(tx *sql.Tx, u User, operationID string, row bonusEntry) (bonusEntry, error) {
	if row.ID == "" {
		row.ID = id()
	}
	if row.SourceKey == "" {
		row.SourceKey = "manual:" + row.ID
	}
	if row.EntryKind == "" {
		row.EntryKind = "manual"
	}
	_, err := tx.Exec(`INSERT INTO payroll_bonus_entries(id,source_key,entry_kind,rule_key,worker_id,worker_name,order_id,line_id,order_number,
		project_id,project_name,address,work_date,bonus_year,category,line_name,subitem_name,unit,qty_micro,rate_cents,amount_cents,reason,actor_id,actor_name,operation_id,reversal_of)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, row.ID, row.SourceKey, row.EntryKind, row.RuleKey, row.WorkerID, row.WorkerName, row.OrderID, row.LineID, row.OrderNumber,
		row.ProjectID, row.ProjectName, row.Address, row.WorkDate, row.BonusYear, row.Category, row.LineName, row.SubitemName, row.Unit, row.QtyMicro, row.RateCents, row.AmountCents, row.Reason, u.ID, u.Name, operationID, bonusNullableString(row.ReversalOf))
	return row, err
}

func lockBonusEntry(tx *sql.Tx, idValue string, version int64) (bonusEntry, error) {
	idValue, err := validatePayrollID(idValue, "奖金记录")
	if err != nil {
		return bonusEntry{}, err
	}
	var row bonusEntry
	var reversal sql.NullString
	err = tx.QueryRow(`SELECT id,source_key,entry_kind,rule_key,worker_id,worker_name,order_id,line_id,order_number,project_id,project_name,address,
		DATE_FORMAT(work_date,'%Y-%m-%d'),bonus_year,category,line_name,subitem_name,unit,qty_micro,rate_cents,amount_cents,reason,actor_name,reversal_of,version
		FROM payroll_bonus_entries WHERE id=? FOR UPDATE`, idValue).Scan(&row.ID, &row.SourceKey, &row.EntryKind, &row.RuleKey, &row.WorkerID, &row.WorkerName, &row.OrderID, &row.LineID, &row.OrderNumber,
		&row.ProjectID, &row.ProjectName, &row.Address, &row.WorkDate, &row.BonusYear, &row.Category, &row.LineName, &row.SubitemName, &row.Unit, &row.QtyMicro, &row.RateCents, &row.AmountCents, &row.Reason, &row.ActorName, &reversal, &row.Version)
	if err == sql.ErrNoRows {
		return row, fail(404, "奖金记录不存在")
	}
	if err != nil {
		return row, err
	}
	if reversal.Valid {
		row.ReversalOf = reversal.String
	}
	if row.Version != version {
		return row, fail(409, "奖金记录已变化，请刷新后重试")
	}
	return row, nil
}

func reverseBonusAdjustment(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	original, err := lockBonusEntry(tx, p.ID, p.Version)
	if err != nil {
		return nil, err
	}
	if original.EntryKind != "manual" || original.ReversalOf != "" {
		return nil, fail(409, "只有未更正的手动奖金调整可以撤销")
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	if original.AmountCents == math.MinInt64 {
		return nil, fail(400, "奖金金额超出可处理范围")
	}
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM payroll_bonus_entries WHERE reversal_of=?", original.ID).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fail(409, "该奖金调整已撤销")
	}
	if _, err = lockBonusWorker(tx, original.WorkerID); err != nil {
		return nil, err
	}
	summary, err := readBonusSummary(tx, original.WorkerID, original.BonusYear)
	if err != nil {
		return nil, err
	}
	if err = validateBonusChangeAgainstSettlements(summary, 0, -original.AmountCents); err != nil {
		return nil, err
	}
	row := original
	row.ID, row.SourceKey, row.EntryKind = id(), "reverse:"+id(), "reversal"
	row.AmountCents, row.Reason, row.ActorName, row.ReversalOf, row.Version = -original.AmountCents, reason, u.Name, original.ID, 1
	if _, err = insertBonusEntry(tx, u, key, row); err != nil {
		return nil, err
	}
	if err = bonusHistory(tx, u, key, "bonus", original.ID, "reverse-bonus-adjustment", reason, original, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "reversal_of": original.ID, "version": int64(1)}, nil
}

func createBonusSettlement(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	if err := bonusYear(p.BonusYear); err != nil {
		return nil, err
	}
	if err := bonusDate(p.SettlementDate, "结算日期", 0); err != nil {
		return nil, err
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	worker, err := lockBonusWorker(tx, p.WorkerID)
	if err != nil {
		return nil, err
	}
	summary, err := readBonusSummary(tx, worker.ID, p.BonusYear)
	if err != nil {
		return nil, err
	}
	if err = bonusSettlementAmount(summary.BonusAvailableCents, p.AmountCents); err != nil {
		return nil, err
	}
	rowID := id()
	row := bonusSettlement{ID: rowID, WorkerID: worker.ID, WorkerName: worker.Name, BonusYear: p.BonusYear, SettlementAt: p.SettlementDate,
		AmountCents: p.AmountCents, Remaining: p.AmountCents, Status: "confirmed", Version: 1, Note: note, ActorName: u.Name}
	if _, err = tx.Exec(`INSERT INTO payroll_bonus_settlements(id,worker_id,worker_name,bonus_year,settlement_date,amount_cents,status,version,note,actor_id,actor_name,operation_id)
		VALUES(?,?,?,?,?,?,'confirmed',1,?,?,?,?)`, row.ID, row.WorkerID, row.WorkerName, row.BonusYear, row.SettlementAt, row.AmountCents, row.Note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	if err = bonusHistory(tx, u, key, "bonus_settlement", row.ID, "create-bonus-settlement", note, nil, row); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "version": row.Version}, nil
}

func lockBonusSettlement(tx *sql.Tx, settlementID string, version int64) (bonusSettlementSource, error) {
	settlementID, err := validatePayrollID(settlementID, "奖金结算")
	if err != nil {
		return bonusSettlementSource{}, err
	}
	var row bonusSettlementSource
	err = tx.QueryRow(`SELECT id,worker_id,worker_name,bonus_year,DATE_FORMAT(settlement_date,'%Y-%m-%d'),amount_cents,status,version,note,actor_name
		FROM payroll_bonus_settlements WHERE id=? FOR UPDATE`, settlementID).Scan(&row.ID, &row.WorkerID, &row.WorkerName, &row.BonusYear, &row.SettlementAt, &row.Amount, &row.Status, &row.Version, &row.Note, &row.ActorName)
	if err == sql.ErrNoRows {
		return row, fail(404, "奖金结算记录不存在")
	}
	if err != nil {
		return row, err
	}
	if version != 0 && version != row.Version {
		return row, fail(409, "奖金结算记录已变化，请刷新后重试")
	}
	return row, nil
}

func reverseBonusSettlement(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	row, err := lockBonusSettlement(tx, p.ID, p.Version)
	if err != nil {
		return nil, err
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	if row.Status != "confirmed" {
		return nil, fail(409, "奖金结算已撤销")
	}
	var paymentCount int
	if err = tx.QueryRow("SELECT COUNT(*) FROM payroll_bonus_payments WHERE settlement_id=? AND status='active'", row.ID).Scan(&paymentCount); err != nil {
		return nil, err
	}
	if paymentCount > 0 {
		return nil, fail(409, "奖金结算已发生付款，不能撤销；请先撤销付款")
	}
	result, err := tx.Exec(`UPDATE payroll_bonus_settlements SET status='voided',version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='confirmed'`, row.ID, row.Version)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fail(409, "奖金结算记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": row.ID, "status": "voided", "reason": reason, "version": row.Version + 1}
	if err = bonusHistory(tx, u, key, "bonus_settlement", row.ID, "reverse-bonus-settlement", reason, row, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "version": row.Version + 1}, nil
}

func createBonusPayment(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	settlement, err := lockBonusSettlement(tx, p.SettlementID, p.SettlementVersion)
	if err != nil {
		return nil, err
	}
	if settlement.Status != "confirmed" {
		return nil, fail(409, "奖金结算已撤销，不能付款")
	}
	if err = bonusDate(p.PaidDate, "付款日期", 0); err != nil {
		return nil, err
	}
	method := strings.TrimSpace(p.Method)
	if len([]rune(method)) > 40 {
		return nil, fail(400, "付款方式不能超过40字")
	}
	note, err := payrollNote(p.Note)
	if err != nil {
		return nil, err
	}
	var paid int64
	if err = tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM payroll_bonus_payments WHERE settlement_id=? AND status='active'", settlement.ID).Scan(&paid); err != nil {
		return nil, err
	}
	if _, err = validateBonusPaymentAmount(settlement.Amount, paid, p.AmountCents); err != nil {
		return nil, err
	}
	row := bonusPayment{ID: id(), AmountCents: p.AmountCents, PaidDate: p.PaidDate, Method: method, Note: note, Version: 1}
	if _, err = tx.Exec(`INSERT INTO payroll_bonus_payments(id,settlement_id,amount_cents,paid_date,method,note,status,version,actor_id,actor_name,operation_id)
		VALUES(?,?,?,?,?,?,'active',1,?,?,?)`, row.ID, settlement.ID, row.AmountCents, row.PaidDate, row.Method, row.Note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	result, err := tx.Exec("UPDATE payroll_bonus_settlements SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='confirmed'", settlement.ID, settlement.Version)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fail(409, "奖金结算记录已变化，请刷新后重试")
	}
	after := map[string]any{"id": row.ID, "settlement_id": settlement.ID, "amount_cents": row.AmountCents, "paid_date": row.PaidDate, "method": row.Method, "note": row.Note, "version": row.Version}
	if err = bonusHistory(tx, u, key, "bonus_payment", row.ID, "create-bonus-payment", note, nil, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "settlement_id": settlement.ID, "settlement_version": settlement.Version + 1, "version": row.Version}, nil
}

func lockBonusPayment(tx *sql.Tx, paymentID string, version int64) (bonusPaymentSource, error) {
	paymentID, err := validatePayrollID(paymentID, "奖金付款")
	if err != nil {
		return bonusPaymentSource{}, err
	}
	var row bonusPaymentSource
	err = tx.QueryRow(`SELECT id,settlement_id,amount_cents,DATE_FORMAT(paid_date,'%Y-%m-%d'),method,note,status,version,reversal_reason
		FROM payroll_bonus_payments WHERE id=? FOR UPDATE`, paymentID).Scan(&row.ID, &row.SettlementID, &row.Amount, &row.PaidDate, &row.Method, &row.Note, &row.Status, &row.Version, &row.ReversalReason)
	if err == sql.ErrNoRows {
		return row, fail(404, "奖金付款记录不存在")
	}
	if err != nil {
		return row, err
	}
	if version != row.Version {
		return row, fail(409, "奖金付款记录已变化，请刷新后重试")
	}
	return row, nil
}

func reverseBonusPayment(tx *sql.Tx, u User, p bonusActionRequest, key string) (any, error) {
	paymentID, err := validatePayrollID(p.ID, "奖金付款")
	if err != nil {
		return nil, err
	}
	var settlementID string
	if err = tx.QueryRow("SELECT settlement_id FROM payroll_bonus_payments WHERE id=?", paymentID).Scan(&settlementID); err == sql.ErrNoRows {
		return nil, fail(404, "奖金付款记录不存在")
	} else if err != nil {
		return nil, err
	}
	settlement, err := lockBonusSettlement(tx, settlementID, 0)
	if err != nil {
		return nil, err
	}
	payment, err := lockBonusPayment(tx, paymentID, p.Version)
	if err != nil {
		return nil, err
	}
	if payment.Status != "active" {
		return nil, fail(409, "奖金付款已经撤销")
	}
	reason, err := payrollReason(p.Reason)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(`UPDATE payroll_bonus_payments SET status='reversed',reversal_reason=?,version=version+1,updated_at=UTC_TIMESTAMP()
		WHERE id=? AND version=? AND status='active'`, reason, payment.ID, payment.Version)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fail(409, "奖金付款记录已变化，请刷新后重试")
	}
	if _, err = tx.Exec("UPDATE payroll_bonus_settlements SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", settlement.ID, settlement.Version); err != nil {
		return nil, err
	}
	after := map[string]any{"id": payment.ID, "settlement_id": settlement.ID, "status": "reversed", "reason": reason, "version": payment.Version + 1}
	if err = bonusHistory(tx, u, key, "bonus_payment", payment.ID, "reverse-bonus-payment", reason, payment, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": payment.ID, "settlement_id": settlement.ID, "version": payment.Version + 1, "settlement_version": settlement.Version + 1}, nil
}

func bonusNullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
