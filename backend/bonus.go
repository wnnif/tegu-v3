package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type bonusRule struct {
	Key       string
	RateCents int64
	Reason    string
}

type bonusEntry struct {
	ID          string `json:"id"`
	EntryKind   string `json:"entry_kind"`
	RuleKey     string `json:"rule_key"`
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	LineID      string `json:"line_id"`
	Category    string `json:"category"`
	LineName    string `json:"line_name"`
	SubitemName string `json:"subitem_name"`
	WorkerID    string `json:"worker_id"`
	WorkerName  string `json:"worker_name"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Address     string `json:"address"`
	WorkDate    string `json:"work_date"`
	BonusYear   int    `json:"bonus_year"`
	QtyMicro    int64  `json:"qty_micro"`
	Unit        string `json:"unit"`
	RateCents   int64  `json:"rate_cents"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
	ActorName   string `json:"actor_name"`
	Version     int64  `json:"version"`
	ReversalOf  string `json:"reversal_of,omitempty"`
	ReversalID  string `json:"reversal_id,omitempty"`
	SourceKey   string `json:"-"`
}

type bonusSummary struct {
	WorkerID             string `json:"worker_id"`
	WorkerName           string `json:"worker_name"`
	BonusYear            int    `json:"bonus_year"`
	BonusAccruedCents    int64  `json:"bonus_accrued_cents"`
	BonusAdjustmentCents int64  `json:"bonus_adjustment_cents"`
	BonusPaidCents       int64  `json:"bonus_paid_cents"`
	BonusBalanceCents    int64  `json:"bonus_balance_cents"`
	BonusSettledCents    int64  `json:"bonus_settled_cents"`
	BonusAvailableCents  int64  `json:"bonus_available_cents"`
}

type bonusSettlement struct {
	ID           string         `json:"id"`
	WorkerID     string         `json:"worker_id"`
	WorkerName   string         `json:"worker_name"`
	BonusYear    int            `json:"bonus_year"`
	SettlementAt string         `json:"settlement_date"`
	AmountCents  int64          `json:"amount_cents"`
	PaidCents    int64          `json:"paid_cents"`
	Remaining    int64          `json:"remaining_cents"`
	Status       string         `json:"status"`
	Version      int64          `json:"version"`
	Note         string         `json:"note"`
	ActorName    string         `json:"actor_name"`
	Payments     []bonusPayment `json:"payments"`
}

type bonusPayment struct {
	ID             string `json:"id"`
	AmountCents    int64  `json:"amount_cents"`
	PaidDate       string `json:"paid_date"`
	Method         string `json:"method"`
	Note           string `json:"note"`
	Version        int64  `json:"version"`
	Reversed       bool   `json:"reversed"`
	ReversalReason string `json:"reversal_reason,omitempty"`
}

type bonusPage struct {
	Rows     any          `json:"rows"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Summary  bonusSummary `json:"summary"`
}

// Automatic awards are intentionally a closed list. Polypropylene bonuses are
// manually recorded; other craft names and units never inherit a guessed rate.
func automaticBonusRule(category, craftID, name, subitemID, subitemName, mode string, temporary bool) (bonusRule, bool) {
	if temporary || mode != "平方" {
		return bonusRule{}, false
	}
	category = strings.TrimSpace(category)
	name, subitemName = normalizeBonusCraftName(name), normalizeBonusCraftName(subitemName)
	if category == "防水涂料" && (craftID == "craft-v3-waterproof-split-two-coats" || name == normalizeBonusCraftName("分开刷连二遍防水涂料") || name == normalizeBonusCraftName("分开两遍防水工艺")) {
		return bonusRule{Key: "waterproof_split_two_coats", RateCents: 50, Reason: "正常分开两遍防水奖金，按实际施工面积计提"}, true
	}
	if category == "开工" && ((craftID == "craft-v3-fine-wall-ground" && subitemID == "fine-wall") ||
		(name == normalizeBonusCraftName("精开工·墙地固") && subitemName == normalizeBonusCraftName("墙"))) {
		return bonusRule{Key: "fine_start_wall_ground_wall", RateCents: 15, Reason: "精开工墙地固·墙奖金，按实际施工面积计提"}, true
	}
	return bonusRule{}, false
}

func normalizeBonusCraftName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("・", "·", "．", "·", "‧", "·", "　", " ").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func bonusAmountCents(qtyMicro, rateCents int64) (int64, error) {
	if qtyMicro <= 0 || rateCents <= 0 || qtyMicro > 1_000_000_000_000 {
		return 0, fail(400, "奖金计提数量或单价无效")
	}
	amount := scaledAmount(qtyMicro, rateCents)
	if amount <= 0 || amount > maxPayrollCents {
		return 0, fail(400, "奖金金额超出范围")
	}
	return amount, nil
}

func bonusWorkYear(workDate string) (int, error) {
	parsed, err := time.Parse("2006-01-02", workDate)
	if err != nil {
		return 0, err
	}
	return parsed.Year(), nil
}

func automaticBonusDelta(desiredCents, existingCents int64) (int64, error) {
	if existingCents == math.MinInt64 {
		return 0, fail(400, "奖金差额超出可处理范围")
	}
	return addPayrollCents(desiredCents, -existingCents)
}

func bonusBalanceCents(accruedCents, adjustmentCents, paidCents int64) (int64, error) {
	total, err := addPayrollCents(accruedCents, adjustmentCents)
	if err != nil {
		return 0, err
	}
	return addPayrollCents(total, -paidCents)
}

func bonusAvailableCents(accruedCents, adjustmentCents, settledCents int64) (int64, error) {
	total, err := addPayrollCents(accruedCents, adjustmentCents)
	if err != nil {
		return 0, err
	}
	return addPayrollCents(total, -settledCents)
}

func bonusAvailableAfterChange(summary bonusSummary, accrualDelta, adjustmentDelta int64) (int64, error) {
	accrued, err := addPayrollCents(summary.BonusAccruedCents, accrualDelta)
	if err != nil {
		return 0, err
	}
	adjustment, err := addPayrollCents(summary.BonusAdjustmentCents, adjustmentDelta)
	if err != nil {
		return 0, err
	}
	return bonusAvailableCents(accrued, adjustment, summary.BonusSettledCents)
}

func validateBonusChangeAgainstSettlements(summary bonusSummary, accrualDelta, adjustmentDelta int64) error {
	available, err := bonusAvailableAfterChange(summary, accrualDelta, adjustmentDelta)
	if err != nil {
		return err
	}
	if available < 0 {
		return fail(409, "奖金调整会超过当年尚未结算的余额，请先处理已确认的奖金结算与付款")
	}
	return nil
}

func bonusSettlementAmount(availableCents, amountCents int64) error {
	if err := payrollPositiveAmount(amountCents); err != nil {
		return err
	}
	if availableCents < 0 || amountCents > availableCents {
		return fail(409, "本年度可结算奖金不足，请刷新奖金余额")
	}
	return nil
}

func bonusPaymentRemaining(settlementCents, paidCents int64) (int64, error) {
	if settlementCents < 0 || paidCents < 0 {
		return 0, fail(400, "奖金付款余额无效")
	}
	if paidCents == math.MinInt64 {
		return 0, fail(400, "奖金付款余额超出可处理范围")
	}
	return addPayrollCents(settlementCents, -paidCents)
}

func validateBonusPaymentAmount(settlementCents, paidCents, amountCents int64) (int64, error) {
	if err := payrollPositiveAmount(amountCents); err != nil {
		return 0, err
	}
	remaining, err := bonusPaymentRemaining(settlementCents, paidCents)
	if err != nil {
		return 0, err
	}
	if remaining <= 0 || amountCents > remaining {
		return remaining, fail(409, "本次付款超过奖金结算的剩余金额")
	}
	return remaining - amountCents, nil
}

func bonusSourceKey(orderID, lineID, workerID string, year int, ruleKey string) string {
	return hash([]byte(strings.Join([]string{orderID, lineID, workerID, strconv.Itoa(year), ruleKey}, "\x00")))
}

type bonusDesiredEntry struct {
	SourceKey string
	Entry     bonusEntry
}

type bonusYearKey struct {
	WorkerID string
	Year     int
}

func syncOrderBonusEntries(tx *sql.Tx, u User, orderID, operationID string) error {
	var order bonusEntry
	var workDate string
	var status string
	err := tx.QueryRow(`SELECT number,project_id,project_name,address,DATE_FORMAT(work_date,'%Y-%m-%d'),status
	 FROM orders WHERE id=? FOR UPDATE`, orderID).Scan(&order.OrderNumber, &order.ProjectID, &order.ProjectName, &order.Address, &workDate, &status)
	if err != nil {
		return err
	}
	order.OrderID, order.WorkDate = orderID, workDate
	if year, e := bonusWorkYear(workDate); e == nil {
		order.BonusYear = year
	} else {
		return fmt.Errorf("invalid saved work date for bonus accrual: %w", e)
	}
	desired := map[string]bonusEntry{}
	if status != "已取消" {
		rows, e := tx.Query(`SELECT id,craft_id,category,name,COALESCE(subitem_id,''),COALESCE(subitem_name,''),mode,temporary,qty_micro,worker_id,worker_name
			 FROM order_lines WHERE order_id=? ORDER BY sort_order,id`, orderID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var lineID, craftID, category, name, subitemID, subitemName, mode, workerID, workerName string
			var temporary bool
			var qty int64
			if e = rows.Scan(&lineID, &craftID, &category, &name, &subitemID, &subitemName, &mode, &temporary, &qty, &workerID, &workerName); e != nil {
				rows.Close()
				return e
			}
			rule, ok := automaticBonusRule(category, craftID, name, subitemID, subitemName, mode, temporary)
			if !ok || qty <= 0 || workerID == "" {
				continue
			}
			amount, e := bonusAmountCents(qty, rule.RateCents)
			if e != nil {
				rows.Close()
				return e
			}
			sourceKey := bonusSourceKey(orderID, lineID, workerID, order.BonusYear, rule.Key)
			entry := order
			entry.SourceKey, entry.EntryKind, entry.RuleKey = sourceKey, "automatic", rule.Key
			entry.LineID, entry.Category, entry.LineName = lineID, category, name
			entry.SubitemName = subitemName
			entry.WorkerID, entry.WorkerName = workerID, workerName
			entry.Unit, entry.QtyMicro, entry.RateCents = "平方", qty, rule.RateCents
			entry.AmountCents, entry.Reason = amount, rule.Reason
			desired[sourceKey] = entry
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		if e = rows.Close(); e != nil {
			return e
		}
	}
	existing := map[string]bonusEntry{}
	rows, err := tx.Query(`SELECT id,source_key,rule_key,worker_id,worker_name,order_id,line_id,order_number,project_id,project_name,address,
		DATE_FORMAT(work_date,'%Y-%m-%d'),bonus_year,category,line_name,subitem_name,unit,qty_micro,rate_cents,amount_cents,reason,actor_name,version
		FROM payroll_bonus_entries WHERE order_id=? AND entry_kind='automatic' ORDER BY created_at,id`, orderID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row bonusEntry
		if err = rows.Scan(&row.ID, &row.SourceKey, &row.RuleKey, &row.WorkerID, &row.WorkerName, &row.OrderID, &row.LineID, &row.OrderNumber, &row.ProjectID, &row.ProjectName, &row.Address,
			&row.WorkDate, &row.BonusYear, &row.Category, &row.LineName, &row.SubitemName, &row.Unit, &row.QtyMicro, &row.RateCents, &row.AmountCents, &row.Reason, &row.ActorName, &row.Version); err != nil {
			rows.Close()
			return err
		}
		if old, ok := existing[row.SourceKey]; ok {
			total, e := addPayrollCents(old.AmountCents, row.AmountCents)
			if e != nil {
				rows.Close()
				return e
			}
			row.AmountCents = total
		}
		existing[row.SourceKey] = row
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	workerIDs := map[string]bool{}
	for _, entry := range desired {
		workerIDs[entry.WorkerID] = true
	}
	for _, entry := range existing {
		workerIDs[entry.WorkerID] = true
	}
	lockedWorkers := make([]string, 0, len(workerIDs))
	for workerID := range workerIDs {
		lockedWorkers = append(lockedWorkers, workerID)
	}
	sort.Strings(lockedWorkers)
	for _, workerID := range lockedWorkers {
		if _, lockErr := lockBonusWorker(tx, workerID); lockErr != nil {
			return lockErr
		}
	}
	keys := make([]string, 0, len(existing)+len(desired))
	seen := map[string]bool{}
	for key := range existing {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range desired {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	yearDeltas := map[bonusYearKey]int64{}
	for _, sourceKey := range keys {
		old, hasOld := existing[sourceKey]
		current, hasCurrent := desired[sourceKey]
		oldAmount, desiredAmount := int64(0), int64(0)
		if hasOld {
			oldAmount = old.AmountCents
		}
		if hasCurrent {
			desiredAmount = current.AmountCents
		}
		delta, e := automaticBonusDelta(desiredAmount, oldAmount)
		if e != nil {
			return e
		}
		if delta == 0 {
			continue
		}
		entry := old
		if hasCurrent {
			entry = current
		}
		balanceKey := bonusYearKey{WorkerID: entry.WorkerID, Year: entry.BonusYear}
		total, e := addPayrollCents(yearDeltas[balanceKey], delta)
		if e != nil {
			return e
		}
		yearDeltas[balanceKey] = total
	}
	affectedYears := make([]bonusYearKey, 0, len(yearDeltas))
	for balanceKey := range yearDeltas {
		affectedYears = append(affectedYears, balanceKey)
	}
	sort.Slice(affectedYears, func(i, j int) bool {
		if affectedYears[i].WorkerID != affectedYears[j].WorkerID {
			return affectedYears[i].WorkerID < affectedYears[j].WorkerID
		}
		return affectedYears[i].Year < affectedYears[j].Year
	})
	for _, balanceKey := range affectedYears {
		summary, e := readBonusSummary(tx, balanceKey.WorkerID, balanceKey.Year)
		if e != nil {
			return e
		}
		if e = validateBonusChangeAgainstSettlements(summary, yearDeltas[balanceKey], 0); e != nil {
			return e
		}
	}
	for _, sourceKey := range keys {
		old, hasOld := existing[sourceKey]
		current, hasCurrent := desired[sourceKey]
		oldAmount, desiredAmount := int64(0), int64(0)
		if hasOld {
			oldAmount = old.AmountCents
		}
		if hasCurrent {
			desiredAmount = current.AmountCents
		}
		delta, e := automaticBonusDelta(desiredAmount, oldAmount)
		if e != nil {
			return e
		}
		if delta == 0 {
			continue
		}
		entry := old
		if hasCurrent {
			entry = current
		}
		entry.ID, entry.EntryKind, entry.AmountCents = id(), "automatic", delta
		entry.ActorName, entry.Reason = u.Name, automaticBonusDeltaReason(delta, hasCurrent, oldAmount)
		entry.SourceKey = sourceKey
		if _, err = tx.Exec(`INSERT INTO payroll_bonus_entries(id,source_key,entry_kind,rule_key,worker_id,worker_name,order_id,line_id,order_number,
			project_id,project_name,address,work_date,bonus_year,category,line_name,subitem_name,unit,qty_micro,rate_cents,amount_cents,reason,actor_id,actor_name,operation_id)
			VALUES(?,?,'automatic',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, entry.ID, entry.SourceKey, entry.RuleKey, entry.WorkerID, entry.WorkerName, entry.OrderID, entry.LineID, entry.OrderNumber,
			entry.ProjectID, entry.ProjectName, entry.Address, entry.WorkDate, entry.BonusYear, entry.Category, entry.LineName, entry.SubitemName, entry.Unit, entry.QtyMicro, entry.RateCents, entry.AmountCents, entry.Reason, u.ID, u.Name, operationID); err != nil {
			return err
		}
		after := entry
		if err = payrollHistory(tx, u, operationID, "bonus", entry.ID, "automatic-bonus-adjustment", entry.Reason, oldEntrySnapshot(old, hasOld), after); err != nil {
			return err
		}
	}
	return nil
}

func automaticBonusDeltaReason(delta int64, active bool, oldAmount int64) string {
	if active && oldAmount == 0 {
		return "有效施工记录自动计提奖金"
	}
	if !active {
		return "施工记录取消，自动冲减奖金"
	}
	return "施工记录更正，按差额调整自动奖金"
}

func oldEntrySnapshot(row bonusEntry, ok bool) any {
	if !ok {
		return nil
	}
	return row
}

func validateBonusQuery(q url.Values) (string, int, string, int, error) {
	workerID, err := validatePayrollID(q.Get("worker_id"), "施工人")
	if err != nil {
		return "", 0, "", 0, err
	}
	yearValue := strings.TrimSpace(q.Get("year"))
	if yearValue == "" {
		yearValue = time.Now().In(businessLocation).Format("2006")
	}
	year, err := strconv.Atoi(yearValue)
	if err != nil || year < 1000 || year > 9999 {
		return "", 0, "", 0, fail(400, "奖金年度无效")
	}
	tab := q.Get("tab")
	if tab == "" {
		tab = "ledger"
	}
	if !contains([]string{"ledger", "payments"}, tab) {
		return "", 0, "", 0, fail(400, "奖金明细类型无效")
	}
	page, err := queryPage(q)
	if err != nil {
		return "", 0, "", 0, err
	}
	return workerID, year, tab, page, nil
}

func readBonusSummary(tx *sql.Tx, workerID string, year int) (bonusSummary, error) {
	summary := bonusSummary{WorkerID: workerID, BonusYear: year}
	err := tx.QueryRow(`SELECT COALESCE((SELECT name FROM workers WHERE id=?),
		(SELECT worker_name FROM payroll_bonus_entries WHERE worker_id=? ORDER BY created_at DESC,id DESC LIMIT 1),''),
		COALESCE(SUM(IF(entry_kind='automatic',amount_cents,0)),0),
		COALESCE(SUM(IF(entry_kind IN ('manual','reversal'),amount_cents,0)),0)
		FROM payroll_bonus_entries WHERE worker_id=? AND bonus_year=?`, workerID, workerID, workerID, year).Scan(&summary.WorkerName, &summary.BonusAccruedCents, &summary.BonusAdjustmentCents)
	if err != nil {
		return summary, err
	}
	err = tx.QueryRow(`SELECT COALESCE(SUM(p.amount_cents),0) FROM payroll_bonus_payments p JOIN payroll_bonus_settlements s ON s.id=p.settlement_id
		WHERE s.worker_id=? AND s.bonus_year=? AND s.status='confirmed' AND p.status='active'`, workerID, year).Scan(&summary.BonusPaidCents)
	if err != nil {
		return summary, err
	}
	err = tx.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM payroll_bonus_settlements WHERE worker_id=? AND bonus_year=? AND status='confirmed'`, workerID, year).Scan(&summary.BonusSettledCents)
	if err != nil {
		return summary, err
	}
	summary.BonusBalanceCents, err = bonusBalanceCents(summary.BonusAccruedCents, summary.BonusAdjustmentCents, summary.BonusPaidCents)
	if err != nil {
		return summary, err
	}
	summary.BonusAvailableCents, err = bonusAvailableCents(summary.BonusAccruedCents, summary.BonusAdjustmentCents, summary.BonusSettledCents)
	return summary, err
}

func (a *App) listPayrollBonus(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	workerID, year, tab, page, err := validateBonusQuery(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	summary, err := readBonusSummary(tx, workerID, year)
	if err != nil {
		return err
	}
	var rows any
	var total int64
	if tab == "ledger" {
		if r.URL.Query().Get("export") == "1" {
			return a.exportPayrollBonus(tx, w, r, u, workerID, year, tab)
		}
		rows, total, err = readBonusLedger(tx, workerID, year, page)
	} else {
		if r.URL.Query().Get("export") == "1" {
			return a.exportPayrollBonus(tx, w, r, u, workerID, year, tab)
		}
		rows, total, err = readBonusPayments(tx, workerID, year, page)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, http.StatusOK, bonusPage{Rows: rows, Total: total, Page: page, PageSize: 100, Summary: summary})
	return nil
}

func readBonusLedger(tx *sql.Tx, workerID string, year, page int) ([]bonusEntry, int64, error) {
	var total int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM payroll_bonus_entries WHERE worker_id=? AND bonus_year=?`, workerID, year).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[bonusEntry](tx, `SELECT e.id,e.entry_kind,e.rule_key,e.order_id,e.order_number,e.line_id,e.category,e.line_name,e.subitem_name,e.worker_id,e.worker_name,e.project_id,e.project_name,e.address,
		DATE_FORMAT(e.work_date,'%Y-%m-%d'),e.bonus_year,e.qty_micro,e.unit,e.rate_cents,e.amount_cents,e.reason,e.actor_name,e.version,COALESCE(e.reversal_of,''),
		COALESCE((SELECT r.id FROM payroll_bonus_entries r WHERE r.reversal_of=e.id LIMIT 1),'')
		FROM payroll_bonus_entries e WHERE e.worker_id=? AND e.bonus_year=? ORDER BY e.work_date DESC,e.created_at DESC,e.id DESC LIMIT 100 OFFSET ?`, []any{workerID, year, (page - 1) * 100}, func(r *sql.Rows, row *bonusEntry) error {
		return r.Scan(&row.ID, &row.EntryKind, &row.RuleKey, &row.OrderID, &row.OrderNumber, &row.LineID, &row.Category, &row.LineName, &row.SubitemName, &row.WorkerID, &row.WorkerName, &row.ProjectID, &row.ProjectName, &row.Address,
			&row.WorkDate, &row.BonusYear, &row.QtyMicro, &row.Unit, &row.RateCents, &row.AmountCents, &row.Reason, &row.ActorName, &row.Version, &row.ReversalOf, &row.ReversalID)
	})
	return rows, total, err
}

func readBonusPayments(tx *sql.Tx, workerID string, year, page int) ([]bonusSettlement, int64, error) {
	var total int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM payroll_bonus_settlements WHERE worker_id=? AND bonus_year=?`, workerID, year).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows[bonusSettlement](tx, `SELECT s.id,s.worker_id,s.worker_name,s.bonus_year,DATE_FORMAT(s.settlement_date,'%Y-%m-%d'),s.amount_cents,
		COALESCE(p.paid_cents,0),s.amount_cents-COALESCE(p.paid_cents,0),s.status,s.version,s.note,s.actor_name
		FROM payroll_bonus_settlements s LEFT JOIN (SELECT settlement_id,SUM(amount_cents) paid_cents FROM payroll_bonus_payments WHERE status='active' GROUP BY settlement_id) p ON p.settlement_id=s.id
		WHERE s.worker_id=? AND s.bonus_year=? ORDER BY s.settlement_date DESC,s.created_at DESC,s.id DESC LIMIT 100 OFFSET ?`, []any{workerID, year, (page - 1) * 100}, func(r *sql.Rows, row *bonusSettlement) error {
		row.Payments = []bonusPayment{}
		return r.Scan(&row.ID, &row.WorkerID, &row.WorkerName, &row.BonusYear, &row.SettlementAt, &row.AmountCents, &row.PaidCents, &row.Remaining, &row.Status, &row.Version, &row.Note, &row.ActorName)
	})
	if err != nil || len(rows) == 0 {
		return rows, total, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i := range ids {
		args[i] = ids[i]
	}
	paymentRows, err := tx.Query(`SELECT id,settlement_id,amount_cents,DATE_FORMAT(paid_date,'%Y-%m-%d'),method,note,version,status,reversal_reason
		FROM payroll_bonus_payments WHERE settlement_id IN (`+marks+`) ORDER BY paid_date DESC,created_at DESC,id DESC`, args...)
	if err != nil {
		return nil, 0, err
	}
	bySettlement := map[string][]bonusPayment{}
	for paymentRows.Next() {
		var row bonusPayment
		var settlementID, status string
		if err = paymentRows.Scan(&row.ID, &settlementID, &row.AmountCents, &row.PaidDate, &row.Method, &row.Note, &row.Version, &status, &row.ReversalReason); err != nil {
			paymentRows.Close()
			return nil, 0, err
		}
		row.Reversed = status == "reversed"
		bySettlement[settlementID] = append(bySettlement[settlementID], row)
	}
	if err = paymentRows.Err(); err != nil {
		paymentRows.Close()
		return nil, 0, err
	}
	if err = paymentRows.Close(); err != nil {
		return nil, 0, err
	}
	for i := range rows {
		if values, ok := bySettlement[rows[i].ID]; ok {
			rows[i].Payments = values
		}
	}
	return rows, total, nil
}

func (a *App) exportPayrollBonus(tx *sql.Tx, w http.ResponseWriter, r *http.Request, u User, workerID string, year int, tab string) error {
	filename := fmt.Sprintf("payroll-bonus-%d-%s.csv", year, tab)
	return a.writeBonusCSV(tx, w, r, u, filename, tab, workerID, year)
}

func (a *App) writeBonusCSV(tx *sql.Tx, w http.ResponseWriter, r *http.Request, u User, filename, tab, workerID string, year int) error {
	if tab == "ledger" {
		return a.streamPayrollBonusCSV(w, r, u, filename, []string{"日期", "年度", "类型", "规则", "施工单号", "工艺", "施工人", "工程量", "单位", "每单位奖金（元）", "金额（元）", "原因", "操作人"}, func(writer *csv.Writer) error {
			rows, err := tx.QueryContext(r.Context(), `SELECT DATE_FORMAT(work_date,'%Y-%m-%d'),bonus_year,entry_kind,rule_key,order_number,line_name,worker_name,qty_micro,unit,rate_cents,amount_cents,reason,actor_name
				FROM payroll_bonus_entries WHERE worker_id=? AND bonus_year=? ORDER BY work_date DESC,created_at DESC,id DESC`, workerID, year)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var date, kind, rule, number, line, name, unit, reason, actor string
				var rowYear int
				var qty, rate, amount int64
				if err := rows.Scan(&date, &rowYear, &kind, &rule, &number, &line, &name, &qty, &unit, &rate, &amount, &reason, &actor); err != nil {
					return err
				}
				if err := writer.Write([]string{date, strconv.Itoa(rowYear), kind, rule, number, line, name, scaledDecimal(qty, 6), unit, payrollMoneyCSV(rate), payrollMoneyCSV(amount), reason, actor}); err != nil {
					return err
				}
			}
			return rows.Err()
		})
	}
	return a.streamPayrollBonusCSV(w, r, u, filename, []string{"奖金年度", "施工人", "结算日期", "结算金额（元）", "已发（元）", "剩余（元）", "结算状态", "付款日期", "付款金额（元）", "付款状态", "方式", "付款备注", "操作人"}, func(writer *csv.Writer) error {
		rows, err := tx.QueryContext(r.Context(), `SELECT s.bonus_year,s.worker_name,DATE_FORMAT(s.settlement_date,'%Y-%m-%d'),s.amount_cents,
			COALESCE(t.paid_cents,0),s.status,COALESCE(p.actor_name,s.actor_name),p.id,DATE_FORMAT(p.paid_date,'%Y-%m-%d'),p.amount_cents,p.status,p.method,p.note
			FROM payroll_bonus_settlements s
			LEFT JOIN (SELECT settlement_id,SUM(amount_cents) paid_cents FROM payroll_bonus_payments WHERE status='active' GROUP BY settlement_id) t ON t.settlement_id=s.id
			LEFT JOIN payroll_bonus_payments p ON p.settlement_id=s.id
			WHERE s.worker_id=? AND s.bonus_year=? ORDER BY s.settlement_date DESC,s.id,p.paid_date,p.id`, workerID, year)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rowYear int
			var name, settlementDate, status, actor string
			var amount, paid int64
			var paymentID, paidDate, paymentStatus, method, note sql.NullString
			var paymentAmount sql.NullInt64
			if err := rows.Scan(&rowYear, &name, &settlementDate, &amount, &paid, &status, &actor, &paymentID, &paidDate, &paymentAmount, &paymentStatus, &method, &note); err != nil {
				return err
			}
			remaining := amount - paid
			if err := writer.Write([]string{strconv.Itoa(rowYear), name, settlementDate, payrollMoneyCSV(amount), payrollMoneyCSV(paid), payrollMoneyCSV(remaining), status, paidDate.String, payrollMoneyCSV(paymentAmount.Int64), paymentStatus.String, method.String, note.String, actor}); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

func (a *App) streamPayrollBonusCSV(w http.ResponseWriter, r *http.Request, u User, filename string, headers []string, writeRows func(*csv.Writer) error) error {
	file, err := os.CreateTemp("", "tegu-payroll-bonus-export-*.csv")
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
	if err = writeRows(writer); err != nil {
		return err
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, 0); err != nil {
		return err
	}
	logTx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer logTx.Rollback()
	if err = audit(logTx, u, "导出年终奖金", "", map[string]string{"filename": filename}); err != nil {
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
