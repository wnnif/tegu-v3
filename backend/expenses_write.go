package main

import (
	"database/sql"
	"strings"
	"time"
)

const maxExpenseCents int64 = 100000000000000

var expenseCategories = []string{"材料采购", "油费", "停车费", "餐费", "其他"}

type expenseActionRequest struct {
	Action             string `json:"action"`
	ID                 string `json:"id"`
	ClaimID            string `json:"claim_id"`
	AdvanceID          string `json:"advance_id"`
	PaymentID          string `json:"payment_id"`
	AllocationID       string `json:"allocation_id"`
	ReturnID           string `json:"return_id"`
	PersonName         string `json:"person_name"`
	Category           string `json:"category"`
	ExpenseDate        string `json:"expense_date"`
	AdvanceDate        string `json:"advance_date"`
	PaidDate           string `json:"paid_date"`
	AppliedDate        string `json:"applied_date"`
	ReturnedDate       string `json:"returned_date"`
	AmountCents        int64  `json:"amount_cents"`
	Note               string `json:"note"`
	Purpose            string `json:"purpose"`
	PurchaseDocumentID string `json:"purchase_document_id"`
	Reason             string `json:"reason"`
	Version            int64  `json:"version"`
	ClaimVersion       int64  `json:"claim_version"`
	AdvanceVersion     int64  `json:"advance_version"`
}

type expenseClaimSource struct {
	ID                 string
	PersonName         string
	ExpenseDate        string
	Category           string
	AmountCents        int64
	Note               string
	PurchaseDocumentID sql.NullString
	PurchaseNumber     string
	PurchaseDate       sql.NullString
	PurchaseTotalCents sql.NullInt64
	Status             string
	Version            int64
}

type expenseAdvanceSource struct {
	ID          string
	PersonName  string
	AdvanceDate string
	AmountCents int64
	Purpose     string
	Note        string
	Status      string
	Version     int64
}

type expensePurchaseSource struct {
	ID       string
	Kind     string
	WorkDate string
	Note     string
	Total    sql.NullInt64
	Version  int64
	Voided   bool
}

func (a *App) expenseAction(tx *sql.Tx, u User, body []byte, key string) (any, error) {
	if !u.can("expenses") {
		return nil, fail(403, "无报销与费用预支权限")
	}
	var p expenseActionRequest
	if err := decode(body, &p); err != nil {
		return nil, err
	}
	p.Action = strings.TrimSpace(p.Action)
	switch p.Action {
	case "create-claim":
		return createExpenseClaim(tx, u, p, key)
	case "update-claim":
		return updateExpenseClaim(tx, u, p, key)
	case "confirm-claim":
		return confirmExpenseClaim(tx, u, p, key)
	case "void-claim":
		return voidExpenseClaim(tx, u, p, key)
	case "pay-claim":
		return payExpenseClaim(tx, u, p, key)
	case "reverse-payment":
		return reverseExpensePayment(tx, u, p, key)
	case "create-advance":
		return createExpenseAdvance(tx, u, p, key)
	case "correct-advance":
		return correctExpenseAdvance(tx, u, p, key)
	case "reverse-advance":
		return reverseExpenseAdvance(tx, u, p, key)
	case "allocate-advance":
		return allocateExpenseAdvance(tx, u, p, key)
	case "reverse-allocation":
		return reverseExpenseAllocation(tx, u, p, key)
	case "return-advance":
		return returnExpenseAdvance(tx, u, p, key)
	case "reverse-return":
		return reverseExpenseReturn(tx, u, p, key)
	default:
		return nil, fail(400, "费用操作类型无效")
	}
}

func expensePersonName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 100 {
		return "", fail(400, "请填写1至100字的人员姓名")
	}
	return value, nil
}

func normalizeExpensePerson(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func expenseDate(value, label string) error {
	if len(value) != 10 || strings.TrimSpace(value) != value {
		return fail(400, label+"无效")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fail(400, label+"无效")
	}
	return nil
}

func expensePositiveAmount(value int64) error {
	if value < 1 || value > maxExpenseCents {
		return fail(400, "金额必须大于0且不超过1万亿元")
	}
	return nil
}

func expenseNote(value, label string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	runes := len([]rune(value))
	if runes > 1000 || (required && runes == 0) {
		if required {
			return "", fail(400, label+"不能超过1000字且不能为空")
		}
		return "", fail(400, label+"不能超过1000字")
	}
	return value, nil
}

func expenseIdentifier(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "", fail(400, "请选择有效的"+label)
	}
	return value, nil
}

func expenseReason(value string) (string, error) {
	return expenseNote(value, "更正原因", true)
}

func expensePurchaseID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 64 {
		return "", fail(400, "关联进货单编号无效")
	}
	return value, nil
}

func expenseHistory(tx *sql.Tx, u User, key, entityType, entityID, event, reason string, before, after any) error {
	var beforeJSON any
	if before != nil {
		beforeJSON = jsonText(before)
	}
	if _, err := tx.Exec(`INSERT INTO expense_history(id,entity_type,entity_id,event,reason,before_snapshot,after_snapshot,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,?,?,?,?)`, id(), entityType, entityID, event, reason, beforeJSON, jsonText(after), u.ID, u.Name, key); err != nil {
		return err
	}
	return audit(tx, u, "expenses."+event, entityID, map[string]any{"entity_type": entityType, "reason": reason, "before": before, "after": after, "operation_id": key})
}

func lockExpenseClaim(tx *sql.Tx, claimID string) (expenseClaimSource, error) {
	var row expenseClaimSource
	err := tx.QueryRow(`SELECT id,person_name,DATE_FORMAT(expense_date,'%Y-%m-%d'),category,amount_cents,note,purchase_document_id,purchase_number,
	 DATE_FORMAT(purchase_date,'%Y-%m-%d'),purchase_total_cents,status,version FROM expense_claims WHERE id=? FOR UPDATE`, claimID).Scan(
		&row.ID, &row.PersonName, &row.ExpenseDate, &row.Category, &row.AmountCents, &row.Note, &row.PurchaseDocumentID,
		&row.PurchaseNumber, &row.PurchaseDate, &row.PurchaseTotalCents, &row.Status, &row.Version)
	if err == sql.ErrNoRows {
		return row, fail(404, "报销记录不存在")
	}
	return row, err
}

func lockExpenseAdvance(tx *sql.Tx, advanceID string) (expenseAdvanceSource, error) {
	var row expenseAdvanceSource
	err := tx.QueryRow(`SELECT id,person_name,DATE_FORMAT(advance_date,'%Y-%m-%d'),amount_cents,purpose,note,status,version
	 FROM expense_advances WHERE id=? FOR UPDATE`, advanceID).Scan(
		&row.ID, &row.PersonName, &row.AdvanceDate, &row.AmountCents, &row.Purpose, &row.Note, &row.Status, &row.Version)
	if err == sql.ErrNoRows {
		return row, fail(404, "费用预支记录不存在")
	}
	return row, err
}

func expenseClaimBalances(tx *sql.Tx, claimID string) (applied, paid, due int64, err error) {
	err = tx.QueryRow(`SELECT COALESCE((SELECT SUM(amount_cents) FROM expense_advance_allocations WHERE claim_id=? AND status='active'),0),
	 COALESCE((SELECT SUM(amount_cents) FROM expense_payments WHERE claim_id=? AND status='active'),0),c.amount_cents-
	 COALESCE((SELECT SUM(amount_cents) FROM expense_advance_allocations WHERE claim_id=? AND status='active'),0)-
	 COALESCE((SELECT SUM(amount_cents) FROM expense_payments WHERE claim_id=? AND status='active'),0)
	 FROM expense_claims c WHERE c.id=?`, claimID, claimID, claimID, claimID, claimID).Scan(&applied, &paid, &due)
	return
}

func expenseAdvanceBalances(tx *sql.Tx, advanceID string) (applied, returned, remaining int64, err error) {
	err = tx.QueryRow(`SELECT COALESCE((SELECT SUM(amount_cents) FROM expense_advance_allocations WHERE advance_id=? AND status='active'),0),
	 COALESCE((SELECT SUM(amount_cents) FROM expense_advance_returns WHERE advance_id=? AND status='active'),0),a.amount_cents-
	 COALESCE((SELECT SUM(amount_cents) FROM expense_advance_allocations WHERE advance_id=? AND status='active'),0)-
	 COALESCE((SELECT SUM(amount_cents) FROM expense_advance_returns WHERE advance_id=? AND status='active'),0)
	 FROM expense_advances a WHERE a.id=?`, advanceID, advanceID, advanceID, advanceID, advanceID).Scan(&applied, &returned, &remaining)
	return
}

func lockExpensePurchase(tx *sql.Tx, purchaseID string, claimID string, claimAmount int64) (expensePurchaseSource, error) {
	var purchase expensePurchaseSource
	err := tx.QueryRow(`SELECT id,kind,DATE_FORMAT(work_date,'%Y-%m-%d'),note,total_cents,version,voided
	 FROM stock_documents WHERE id=? FOR UPDATE`, purchaseID).Scan(
		&purchase.ID, &purchase.Kind, &purchase.WorkDate, &purchase.Note, &purchase.Total, &purchase.Version, &purchase.Voided)
	if err == sql.ErrNoRows {
		return purchase, fail(404, "关联进货单不存在")
	}
	if err != nil {
		return purchase, err
	}
	if purchase.Kind != "进货" || purchase.Voided || !purchase.Total.Valid || purchase.Total.Int64 <= 0 {
		return purchase, fail(409, "只能关联有效且金额大于0的进货单")
	}
	var reserved int64
	if err = tx.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM expense_claims
	 WHERE purchase_document_id=? AND status<>'voided' AND id<>?`, purchaseID, claimID).Scan(&reserved); err != nil {
		return purchase, err
	}
	if claimAmount > purchase.Total.Int64-reserved {
		return purchase, fail(409, "本次材料报销超过该进货单可关联余额")
	}
	return purchase, nil
}

func (p expensePurchaseSource) snapshot() map[string]any {
	var total any
	if p.Total.Valid {
		total = p.Total.Int64
	}
	return map[string]any{"id": p.ID, "kind": p.Kind, "work_date": p.WorkDate, "note": p.Note, "total_cents": total, "version": p.Version, "voided": p.Voided}
}

func (c expenseClaimSource) snapshot() map[string]any {
	return map[string]any{
		"id": c.ID, "person_name": c.PersonName, "expense_date": c.ExpenseDate, "category": c.Category, "amount_cents": c.AmountCents,
		"note": c.Note, "purchase_document_id": nullableString(c.PurchaseDocumentID), "purchase_number": c.PurchaseNumber,
		"purchase_date": nullableString(c.PurchaseDate), "purchase_total_cents": nullableInt(c.PurchaseTotalCents), "status": c.Status, "version": c.Version,
	}
}

func (a expenseAdvanceSource) snapshot() map[string]any {
	return map[string]any{"id": a.ID, "person_name": a.PersonName, "advance_date": a.AdvanceDate, "amount_cents": a.AmountCents, "purpose": a.Purpose, "note": a.Note, "status": a.Status, "version": a.Version}
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func expenseClaimFields(p expenseActionRequest) (string, string, string, int64, string, string, error) {
	person, err := expensePersonName(p.PersonName)
	if err != nil {
		return "", "", "", 0, "", "", err
	}
	if !contains(expenseCategories, strings.TrimSpace(p.Category)) {
		return "", "", "", 0, "", "", fail(400, "费用类型无效")
	}
	if err = expenseDate(p.ExpenseDate, "费用日期"); err != nil {
		return "", "", "", 0, "", "", err
	}
	if err = expensePositiveAmount(p.AmountCents); err != nil {
		return "", "", "", 0, "", "", err
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return "", "", "", 0, "", "", err
	}
	purchaseID, err := expensePurchaseID(p.PurchaseDocumentID)
	if err != nil {
		return "", "", "", 0, "", "", err
	}
	if purchaseID != "" && strings.TrimSpace(p.Category) != "材料采购" {
		return "", "", "", 0, "", "", fail(400, "只有材料采购报销可以关联进货单")
	}
	return person, strings.TrimSpace(p.Category), p.ExpenseDate, p.AmountCents, note, purchaseID, nil
}

func createExpenseClaim(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	person, category, date, amount, note, purchaseID, err := expenseClaimFields(p)
	if err != nil {
		return nil, err
	}
	claimID := id()
	var purchase any
	var purchaseNumber string
	var purchaseDate any
	var purchaseTotal any
	var purchaseSnapshot any
	if purchaseID != "" {
		locked, lockErr := lockExpensePurchase(tx, purchaseID, "", amount)
		if lockErr != nil {
			return nil, lockErr
		}
		purchase = locked.ID
		purchaseNumber = locked.ID
		purchaseDate = locked.WorkDate
		purchaseTotal = locked.Total.Int64
		purchaseSnapshot = locked.snapshot()
	}
	if _, err = tx.Exec(`INSERT INTO expense_claims(id,person_name,expense_date,category,amount_cents,note,purchase_document_id,purchase_number,purchase_date,purchase_total_cents,status,version,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,?,?,? ,?,'pending',1,?,?,?)`, claimID, person, date, category, amount, note, purchase, purchaseNumber, purchaseDate, purchaseTotal, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	after := map[string]any{"id": claimID, "person_name": person, "expense_date": date, "category": category, "amount_cents": amount, "note": note,
		"purchase_document_id": nullableAny(purchase), "purchase_number": purchaseNumber, "purchase_date": nullableAny(purchaseDate), "purchase_total_cents": nullableAny(purchaseTotal), "status": "pending", "version": 1}
	if err = expenseHistory(tx, u, key, "claim", claimID, "create-claim", "", nil, map[string]any{"claim": after, "purchase": purchaseSnapshot}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": claimID, "version": int64(1), "status": "待审核"}, nil
}

func nullableAny(value any) any {
	if value == nil {
		return nil
	}
	return value
}

func updateExpenseClaim(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	claimID, err := expenseIdentifier(p.ID, "报销记录")
	if err != nil {
		return nil, err
	}
	person, category, date, amount, note, purchaseID, err := expenseClaimFields(p)
	if err != nil {
		return nil, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	if err != nil {
		return nil, err
	}
	if claim.Version != p.Version || (claim.Status != "pending" && claim.Status != "confirmed") {
		return nil, fail(409, "报销记录已变化或不能更正")
	}
	allocated, paid, _, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	if allocated != 0 || paid != 0 {
		return nil, fail(409, "请先逐笔撤销有效付款和预支核销，再更正报销")
	}
	reason := ""
	if claim.Status == "confirmed" {
		reason, err = expenseReason(p.Reason)
		if err != nil {
			return nil, err
		}
	}
	var purchase any
	var purchaseNumber string
	var purchaseDate any
	var purchaseTotal any
	var purchaseSnapshot any
	if purchaseID != "" {
		locked, lockErr := lockExpensePurchase(tx, purchaseID, claim.ID, amount)
		if lockErr != nil {
			return nil, lockErr
		}
		purchase, purchaseNumber, purchaseDate, purchaseTotal, purchaseSnapshot = locked.ID, locked.ID, locked.WorkDate, locked.Total.Int64, locked.snapshot()
	}
	before := claim.snapshot()
	newVersion := claim.Version + 1
	if _, err = tx.Exec(`UPDATE expense_claims SET person_name=?,expense_date=?,category=?,amount_cents=?,note=?,purchase_document_id=?,purchase_number=?,purchase_date=?,purchase_total_cents=?,status='pending',version=?,updated_at=UTC_TIMESTAMP()
	 WHERE id=? AND version=?`, person, date, category, amount, note, purchase, purchaseNumber, purchaseDate, purchaseTotal, newVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	after := map[string]any{"id": claim.ID, "person_name": person, "expense_date": date, "category": category, "amount_cents": amount, "note": note,
		"purchase_document_id": nullableAny(purchase), "purchase_number": purchaseNumber, "purchase_date": nullableAny(purchaseDate), "purchase_total_cents": nullableAny(purchaseTotal), "status": "pending", "version": newVersion}
	if err = expenseHistory(tx, u, key, "claim", claim.ID, "update-claim", reason, before, map[string]any{"claim": after, "purchase": purchaseSnapshot}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": claim.ID, "version": newVersion, "status": "待审核"}, nil
}

func confirmExpenseClaim(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	claimID, err := expenseIdentifier(p.ID, "报销记录")
	if err != nil {
		return nil, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	if err != nil {
		return nil, err
	}
	if claim.Version != p.Version || claim.Status != "pending" {
		return nil, fail(409, "报销记录已变化或不在待审核状态")
	}
	newVersion := claim.Version + 1
	if _, err = tx.Exec("UPDATE expense_claims SET status='confirmed',version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='pending'", newVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	after := claim.snapshot()
	after["status"], after["version"] = "confirmed", newVersion
	if err = expenseHistory(tx, u, key, "claim", claim.ID, "confirm-claim", "", claim.snapshot(), after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": claim.ID, "version": newVersion, "status": "已确认"}, nil
}

func voidExpenseClaim(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	claimID, err := expenseIdentifier(p.ID, "报销记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	if err != nil {
		return nil, err
	}
	if claim.Version != p.Version || (claim.Status != "pending" && claim.Status != "confirmed") {
		return nil, fail(409, "报销记录已变化或已经撤销")
	}
	allocated, paid, _, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	if allocated != 0 || paid != 0 {
		return nil, fail(409, "请先逐笔撤销有效付款和预支核销，再撤销报销")
	}
	newVersion := claim.Version + 1
	if _, err = tx.Exec("UPDATE expense_claims SET status='voided',version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	after := claim.snapshot()
	after["status"], after["version"], after["reason"] = "voided", newVersion, reason
	if err = expenseHistory(tx, u, key, "claim", claim.ID, "void-claim", reason, claim.snapshot(), after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": claim.ID, "version": newVersion, "status": "已撤销"}, nil
}

func payExpenseClaim(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	claimID, err := expenseIdentifier(p.ID, "报销记录")
	if err != nil {
		return nil, err
	}
	if err = expenseDate(p.PaidDate, "实际付款日期"); err != nil {
		return nil, err
	}
	if err = expensePositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return nil, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	if err != nil {
		return nil, err
	}
	if claim.Version != p.Version || claim.Status != "confirmed" {
		return nil, fail(409, "报销记录已变化或尚未确认")
	}
	applied, paid, due, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	if due <= 0 || p.AmountCents > due {
		return nil, fail(409, "本次付款超过员工尚欠报销金额")
	}
	paymentID := id()
	if _, err = tx.Exec(`INSERT INTO expense_payments(id,claim_id,amount_cents,paid_date,note,status,version,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,'active',1,?,?,?)`, paymentID, claim.ID, p.AmountCents, p.PaidDate, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	newVersion := claim.Version + 1
	if _, err = tx.Exec("UPDATE expense_claims SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	newPaid, newDue := paid+p.AmountCents, due-p.AmountCents
	status := expenseClaimDisplayStatus(claim.Status, claim.AmountCents, applied, newPaid, newDue)
	after := map[string]any{"id": paymentID, "claim_id": claim.ID, "amount_cents": p.AmountCents, "paid_date": p.PaidDate, "note": note, "status": "active", "version": int64(1), "paid_cents": newPaid, "due_cents": newDue}
	if err = expenseHistory(tx, u, key, "payment", paymentID, "pay-claim", "", nil, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": paymentID, "claim_id": claim.ID, "version": newVersion, "paid_cents": newPaid, "due_cents": newDue, "status": status}, nil
}

func expenseClaimDisplayStatus(claimStatus string, amount, applied, paid, due int64) string {
	if claimStatus == "voided" {
		return "已撤销"
	}
	if claimStatus == "pending" {
		return "待审核"
	}
	if due == 0 {
		if paid == 0 {
			return "预支已核销"
		}
		return "已付清"
	}
	if paid > 0 || applied > 0 {
		return "部分报销"
	}
	return "已确认"
}

func reverseExpensePayment(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	paymentID, err := expenseIdentifier(p.PaymentID, "报销付款记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	var claimID string
	if err = tx.QueryRow("SELECT claim_id FROM expense_payments WHERE id=?", paymentID).Scan(&claimID); err == sql.ErrNoRows {
		return nil, fail(404, "报销付款记录不存在")
	} else if err != nil {
		return nil, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	if err != nil {
		return nil, err
	}
	var amount int64
	var status string
	var version int64
	err = tx.QueryRow("SELECT amount_cents,status,version FROM expense_payments WHERE id=? AND claim_id=? FOR UPDATE", paymentID, claim.ID).Scan(&amount, &status, &version)
	if err == sql.ErrNoRows {
		return nil, fail(404, "报销付款记录不存在")
	}
	if err != nil {
		return nil, err
	}
	if version != p.Version || status != "active" || claim.Status != "confirmed" {
		return nil, fail(409, "报销付款已变化、撤销或报销已撤销")
	}
	if _, err = tx.Exec("UPDATE expense_payments SET status='reversed',version=version+1,reversal_reason=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='active'", reason, paymentID, version); err != nil {
		return nil, err
	}
	newClaimVersion := claim.Version + 1
	if _, err = tx.Exec("UPDATE expense_claims SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newClaimVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	_, paid, due, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	after := map[string]any{"id": paymentID, "claim_id": claim.ID, "amount_cents": amount, "status": "reversed", "version": version + 1, "reversal_reason": reason, "paid_cents": paid, "due_cents": due}
	if err = expenseHistory(tx, u, key, "payment", paymentID, "reverse-payment", reason, map[string]any{"id": paymentID, "claim_id": claim.ID, "amount_cents": amount, "status": "active", "version": version}, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": paymentID, "claim_id": claim.ID, "version": newClaimVersion, "payment_version": version + 1, "paid_cents": paid, "due_cents": due}, nil
}

func createExpenseAdvance(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	person, err := expensePersonName(p.PersonName)
	if err != nil {
		return nil, err
	}
	if err = expenseDate(p.AdvanceDate, "实际预支日期"); err != nil {
		return nil, err
	}
	if err = expensePositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	purpose, err := expenseNote(p.Purpose, "预支用途", true)
	if err != nil || len([]rune(purpose)) > 200 {
		if err != nil {
			return nil, err
		}
		return nil, fail(400, "预支用途不能超过200字")
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return nil, err
	}
	advanceID := id()
	if _, err = tx.Exec(`INSERT INTO expense_advances(id,person_name,advance_date,amount_cents,purpose,note,status,version,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,'active',1,?,?,?)`, advanceID, person, p.AdvanceDate, p.AmountCents, purpose, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	after := map[string]any{"id": advanceID, "person_name": person, "advance_date": p.AdvanceDate, "amount_cents": p.AmountCents, "purpose": purpose, "note": note, "status": "active", "version": 1, "remaining_cents": p.AmountCents}
	if err = expenseHistory(tx, u, key, "advance", advanceID, "create-advance", "", nil, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": advanceID, "version": int64(1), "remaining_cents": p.AmountCents, "status": "未核销"}, nil
}

func correctExpenseAdvance(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	advanceID, err := expenseIdentifier(p.ID, "费用预支记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	if err = expenseDate(p.AdvanceDate, "实际预支日期"); err != nil {
		return nil, err
	}
	purpose, err := expenseNote(p.Purpose, "预支用途", true)
	if err != nil || len([]rune(purpose)) > 200 {
		if err != nil {
			return nil, err
		}
		return nil, fail(400, "预支用途不能超过200字")
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return nil, err
	}
	advance, err := lockExpenseAdvance(tx, advanceID)
	if err != nil {
		return nil, err
	}
	if advance.Version != p.Version || advance.Status != "active" {
		return nil, fail(409, "费用预支已变化或已经撤销")
	}
	allocated, returned, _, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	if allocated != 0 || returned != 0 {
		return nil, fail(409, "已有报账或退回记录的预支不能更正")
	}
	newVersion := advance.Version + 1
	if _, err = tx.Exec("UPDATE expense_advances SET advance_date=?,purpose=?,note=?,version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", p.AdvanceDate, purpose, note, newVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	after := advance.snapshot()
	after["advance_date"], after["purpose"], after["note"], after["version"] = p.AdvanceDate, purpose, note, newVersion
	if err = expenseHistory(tx, u, key, "advance", advance.ID, "correct-advance", reason, advance.snapshot(), after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": advance.ID, "version": newVersion, "remaining_cents": advance.AmountCents}, nil
}

func reverseExpenseAdvance(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	advanceID, err := expenseIdentifier(p.ID, "费用预支记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	advance, err := lockExpenseAdvance(tx, advanceID)
	if err != nil {
		return nil, err
	}
	if advance.Version != p.Version || advance.Status != "active" {
		return nil, fail(409, "费用预支已变化或已经撤销")
	}
	allocated, returned, remaining, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	if allocated != 0 || returned != 0 || remaining != advance.AmountCents {
		return nil, fail(409, "已有报账或退回记录的预支不能撤销")
	}
	newVersion := advance.Version + 1
	if _, err = tx.Exec("UPDATE expense_advances SET status='voided',version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='active'", newVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	after := advance.snapshot()
	after["status"], after["version"], after["reason"] = "voided", newVersion, reason
	if err = expenseHistory(tx, u, key, "advance", advance.ID, "reverse-advance", reason, advance.snapshot(), after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": advance.ID, "version": newVersion, "remaining_cents": int64(0), "status": "已撤销"}, nil
}

func lockExpenseParents(tx *sql.Tx, claimID, advanceID string) (expenseAdvanceSource, expenseClaimSource, error) {
	advance, err := lockExpenseAdvance(tx, advanceID)
	if err != nil {
		return advance, expenseClaimSource{}, err
	}
	claim, err := lockExpenseClaim(tx, claimID)
	return advance, claim, err
}

func allocateExpenseAdvance(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	claimID, err := expenseIdentifier(p.ClaimID, "报销记录")
	if err != nil {
		return nil, err
	}
	advanceID, err := expenseIdentifier(p.AdvanceID, "费用预支记录")
	if err != nil {
		return nil, err
	}
	if err = expensePositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	if err = expenseDate(p.AppliedDate, "核销日期"); err != nil {
		return nil, err
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return nil, err
	}
	advance, claim, err := lockExpenseParents(tx, claimID, advanceID)
	if err != nil {
		return nil, err
	}
	if advance.Version != p.AdvanceVersion || claim.Version != p.ClaimVersion || advance.Status != "active" || claim.Status != "confirmed" {
		return nil, fail(409, "报销或预支已变化，或报销尚未确认")
	}
	if normalizeExpensePerson(advance.PersonName) != normalizeExpensePerson(claim.PersonName) {
		return nil, fail(409, "预支人员与报销人员不一致，不能核销")
	}
	claimApplied, claimPaid, claimDue, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	advanceApplied, advanceReturned, advanceRemaining, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	if p.AmountCents > claimDue || p.AmountCents > advanceRemaining {
		return nil, fail(409, "本次核销超过报销未覆盖金额或预支余额")
	}
	allocationID := id()
	if _, err = tx.Exec(`INSERT INTO expense_advance_allocations(id,claim_id,advance_id,amount_cents,applied_date,note,status,version,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,?,'active',1,?,?,?)`, allocationID, claim.ID, advance.ID, p.AmountCents, p.AppliedDate, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	newClaimVersion, newAdvanceVersion := claim.Version+1, advance.Version+1
	if _, err = tx.Exec("UPDATE expense_claims SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newClaimVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE expense_advances SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newAdvanceVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	claimApplied += p.AmountCents
	advanceApplied += p.AmountCents
	claimDue -= p.AmountCents
	advanceRemaining -= p.AmountCents
	status := expenseClaimDisplayStatus(claim.Status, claim.AmountCents, claimApplied, claimPaid, claimDue)
	after := map[string]any{"id": allocationID, "claim_id": claim.ID, "advance_id": advance.ID, "amount_cents": p.AmountCents, "applied_date": p.AppliedDate,
		"note": note, "status": "active", "version": 1, "claim_due_cents": claimDue, "advance_remaining_cents": advanceRemaining}
	if err = expenseHistory(tx, u, key, "allocation", allocationID, "allocate-advance", "", nil, after); err != nil {
		return nil, err
	}
	_ = advanceReturned
	return map[string]any{"ok": true, "id": allocationID, "claim_id": claim.ID, "advance_id": advance.ID, "version": int64(1), "claim_version": newClaimVersion,
		"advance_version": newAdvanceVersion, "advance_applied_cents": claimApplied, "due_cents": claimDue, "advance_remaining_cents": advanceRemaining, "status": status}, nil
}

func reverseExpenseAllocation(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	allocationID, err := expenseIdentifier(p.AllocationID, "预支核销记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	var claimID, advanceID string
	if err = tx.QueryRow("SELECT claim_id,advance_id FROM expense_advance_allocations WHERE id=?", allocationID).Scan(&claimID, &advanceID); err == sql.ErrNoRows {
		return nil, fail(404, "预支核销记录不存在")
	} else if err != nil {
		return nil, err
	}
	advance, claim, err := lockExpenseParents(tx, claimID, advanceID)
	if err != nil {
		return nil, err
	}
	var amount int64
	var status string
	var version int64
	err = tx.QueryRow("SELECT amount_cents,status,version FROM expense_advance_allocations WHERE id=? AND claim_id=? AND advance_id=? FOR UPDATE", allocationID, claim.ID, advance.ID).Scan(&amount, &status, &version)
	if err == sql.ErrNoRows {
		return nil, fail(404, "预支核销记录不存在")
	}
	if err != nil {
		return nil, err
	}
	if version != p.Version || status != "active" {
		return nil, fail(409, "预支核销已变化或已经撤销")
	}
	if _, err = tx.Exec("UPDATE expense_advance_allocations SET status='reversed',version=version+1,reversal_reason=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='active'", reason, allocationID, version); err != nil {
		return nil, err
	}
	newClaimVersion, newAdvanceVersion := claim.Version+1, advance.Version+1
	if _, err = tx.Exec("UPDATE expense_claims SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newClaimVersion, claim.ID, claim.Version); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE expense_advances SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newAdvanceVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	claimApplied, claimPaid, claimDue, err := expenseClaimBalances(tx, claim.ID)
	if err != nil {
		return nil, err
	}
	advanceApplied, advanceReturned, advanceRemaining, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	statusText := expenseClaimDisplayStatus(claim.Status, claim.AmountCents, claimApplied, claimPaid, claimDue)
	after := map[string]any{"id": allocationID, "claim_id": claim.ID, "advance_id": advance.ID, "amount_cents": amount, "status": "reversed", "version": version + 1,
		"reversal_reason": reason, "claim_due_cents": claimDue, "advance_remaining_cents": advanceRemaining}
	if err = expenseHistory(tx, u, key, "allocation", allocationID, "reverse-allocation", reason, map[string]any{"id": allocationID, "claim_id": claim.ID, "advance_id": advance.ID, "amount_cents": amount, "status": "active", "version": version}, after); err != nil {
		return nil, err
	}
	_ = advanceApplied
	_ = advanceReturned
	return map[string]any{"ok": true, "id": allocationID, "claim_id": claim.ID, "advance_id": advance.ID, "version": version + 1,
		"claim_version": newClaimVersion, "advance_version": newAdvanceVersion, "due_cents": claimDue, "advance_remaining_cents": advanceRemaining, "status": statusText}, nil
}

func returnExpenseAdvance(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	advanceID, err := expenseIdentifier(p.AdvanceID, "费用预支记录")
	if err != nil {
		return nil, err
	}
	if err = expenseDate(p.ReturnedDate, "实际退回日期"); err != nil {
		return nil, err
	}
	if err = expensePositiveAmount(p.AmountCents); err != nil {
		return nil, err
	}
	note, err := expenseNote(p.Note, "备注", false)
	if err != nil {
		return nil, err
	}
	advance, err := lockExpenseAdvance(tx, advanceID)
	if err != nil {
		return nil, err
	}
	if advance.Version != p.AdvanceVersion || advance.Status != "active" {
		return nil, fail(409, "费用预支已变化或已经撤销")
	}
	applied, returned, remaining, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	if remaining <= 0 || p.AmountCents > remaining {
		return nil, fail(409, "实际退回金额超过未核销预支余额")
	}
	returnID := id()
	if _, err = tx.Exec(`INSERT INTO expense_advance_returns(id,advance_id,amount_cents,returned_date,note,status,version,actor_id,actor_name,operation_id)
	 VALUES(?,?,?,?,?,'active',1,?,?,?)`, returnID, advance.ID, p.AmountCents, p.ReturnedDate, note, u.ID, u.Name, key); err != nil {
		return nil, err
	}
	newVersion := advance.Version + 1
	if _, err = tx.Exec("UPDATE expense_advances SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	remaining -= p.AmountCents
	after := map[string]any{"id": returnID, "advance_id": advance.ID, "amount_cents": p.AmountCents, "returned_date": p.ReturnedDate, "note": note,
		"status": "active", "version": int64(1), "remaining_cents": remaining}
	if err = expenseHistory(tx, u, key, "return", returnID, "return-advance", "", nil, after); err != nil {
		return nil, err
	}
	_ = applied
	_ = returned
	return map[string]any{"ok": true, "id": returnID, "advance_id": advance.ID, "version": newVersion, "return_version": int64(1), "remaining_cents": remaining}, nil
}

func reverseExpenseReturn(tx *sql.Tx, u User, p expenseActionRequest, key string) (any, error) {
	returnID, err := expenseIdentifier(p.ReturnID, "费用退回记录")
	if err != nil {
		return nil, err
	}
	reason, err := expenseReason(p.Reason)
	if err != nil {
		return nil, err
	}
	var advanceID string
	if err = tx.QueryRow("SELECT advance_id FROM expense_advance_returns WHERE id=?", returnID).Scan(&advanceID); err == sql.ErrNoRows {
		return nil, fail(404, "费用退回记录不存在")
	} else if err != nil {
		return nil, err
	}
	advance, err := lockExpenseAdvance(tx, advanceID)
	if err != nil {
		return nil, err
	}
	var amount int64
	var status string
	var version int64
	err = tx.QueryRow("SELECT amount_cents,status,version FROM expense_advance_returns WHERE id=? AND advance_id=? FOR UPDATE", returnID, advance.ID).Scan(&amount, &status, &version)
	if err == sql.ErrNoRows {
		return nil, fail(404, "费用退回记录不存在")
	}
	if err != nil {
		return nil, err
	}
	if version != p.Version || status != "active" || advance.Status != "active" {
		return nil, fail(409, "费用退回已变化、撤销或预支已经撤销")
	}
	if _, err = tx.Exec("UPDATE expense_advance_returns SET status='reversed',version=version+1,reversal_reason=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=? AND status='active'", reason, returnID, version); err != nil {
		return nil, err
	}
	newVersion := advance.Version + 1
	if _, err = tx.Exec("UPDATE expense_advances SET version=?,updated_at=UTC_TIMESTAMP() WHERE id=? AND version=?", newVersion, advance.ID, advance.Version); err != nil {
		return nil, err
	}
	_, _, remaining, err := expenseAdvanceBalances(tx, advance.ID)
	if err != nil {
		return nil, err
	}
	after := map[string]any{"id": returnID, "advance_id": advance.ID, "amount_cents": amount, "status": "reversed", "version": version + 1, "reversal_reason": reason, "remaining_cents": remaining}
	if err = expenseHistory(tx, u, key, "return", returnID, "reverse-return", reason, map[string]any{"id": returnID, "advance_id": advance.ID, "amount_cents": amount, "status": "active", "version": version}, after); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": returnID, "advance_id": advance.ID, "version": version + 1, "advance_version": newVersion, "remaining_cents": remaining}, nil
}
