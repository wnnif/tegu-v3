package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const expensePageSize = 100

type expenseClaimRow struct {
	ID                  string `json:"id"`
	PersonName          string `json:"person_name"`
	ExpenseDate         string `json:"expense_date"`
	Category            string `json:"category"`
	AmountCents         int64  `json:"amount_cents"`
	Note                string `json:"note"`
	Status              string `json:"status"`
	AdvanceAppliedCents int64  `json:"advance_applied_cents"`
	PaidCents           int64  `json:"paid_cents"`
	DueCents            int64  `json:"due_cents"`
	PurchaseDocumentID  string `json:"purchase_document_id"`
	PurchaseNumber      string `json:"purchase_number"`
	PurchaseDate        string `json:"purchase_date"`
	PurchaseTotalCents  *int64 `json:"purchase_total_cents"`
	Version             int64  `json:"version"`
	ActorName           string `json:"actor_name"`
	CreatedAt           string `json:"created_at"`
}

type expenseAdvanceRow struct {
	ID             string `json:"id"`
	PersonName     string `json:"person_name"`
	AdvanceDate    string `json:"advance_date"`
	AmountCents    int64  `json:"amount_cents"`
	Purpose        string `json:"purpose"`
	Note           string `json:"note"`
	AppliedCents   int64  `json:"applied_cents"`
	ReturnedCents  int64  `json:"returned_cents"`
	RemainingCents int64  `json:"remaining_cents"`
	Status         string `json:"status"`
	Version        int64  `json:"version"`
	ActorName      string `json:"actor_name"`
	CreatedAt      string `json:"created_at"`
}

type expenseHistoryRow struct {
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
	CurrentStatus  string          `json:"current_status,omitempty"`
	CurrentVersion int64           `json:"current_version,omitempty"`
	ClaimID        string          `json:"claim_id,omitempty"`
	AdvanceID      string          `json:"advance_id,omitempty"`
	AmountCents    *int64          `json:"amount_cents,omitempty"`
}

type expensePurchaseRow struct {
	ID             string          `json:"id"`
	WorkDate       string          `json:"work_date"`
	TotalCents     int64           `json:"total_cents"`
	Note           string          `json:"note"`
	LineItems      json.RawMessage `json:"line_items"`
	Version        int64           `json:"version"`
	AvailableCents int64           `json:"available_cents"`
}

type expensePage struct {
	Rows     any   `json:"rows"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func expenseDateFilter(q url.Values) (string, string, error) {
	from, to := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	for _, value := range []string{from, to} {
		if value != "" {
			if err := expenseDate(value, "日期范围"); err != nil {
				return "", "", err
			}
		}
	}
	if from != "" && to != "" && from > to {
		return "", "", fail(400, "开始日期不能晚于结束日期")
	}
	return from, to, nil
}

func expenseClaimCTE() string {
	return `WITH claim_balances AS (
	 SELECT c.id,c.person_name,DATE_FORMAT(c.expense_date,'%Y-%m-%d') expense_date,c.category,c.amount_cents,c.note,c.status,c.version,
	  c.purchase_document_id,c.purchase_number,COALESCE(DATE_FORMAT(c.purchase_date,'%Y-%m-%d'),'') purchase_date,c.purchase_total_cents,
	  c.actor_name,DATE_FORMAT(c.created_at,'%Y-%m-%d %H:%i:%s') created_at,
	  COALESCE(a.applied,0) advance_applied_cents,COALESCE(p.paid,0) paid_cents,
	  c.amount_cents-COALESCE(a.applied,0)-COALESCE(p.paid,0) due_cents
	 FROM expense_claims c
	 LEFT JOIN (SELECT claim_id,SUM(amount_cents) applied FROM expense_advance_allocations WHERE status='active' GROUP BY claim_id) a ON a.claim_id=c.id
	 LEFT JOIN (SELECT claim_id,SUM(amount_cents) paid FROM expense_payments WHERE status='active' GROUP BY claim_id) p ON p.claim_id=c.id
	), claim_rows AS (
	 SELECT claim_balances.*,
	  CASE WHEN status='voided' THEN '已撤销' WHEN status='pending' THEN '待审核'
	   WHEN due_cents=0 AND paid_cents=0 THEN '预支已核销'
	   WHEN due_cents=0 THEN '已付清'
	   WHEN paid_cents>0 OR advance_applied_cents>0 THEN '部分报销' ELSE '已确认' END display_status
	 FROM claim_balances
	)`
}

func expenseClaimFilters(q url.Values) (string, []any, error) {
	from, to, err := expenseDateFilter(q)
	if err != nil {
		return "", nil, err
	}
	status := strings.TrimSpace(q.Get("status"))
	if len([]rune(status)) > 30 {
		return "", nil, fail(400, "报销状态无效")
	}
	needle := strings.TrimSpace(q.Get("q"))
	if len([]rune(needle)) > 100 {
		return "", nil, fail(400, "搜索内容不能超过100字")
	}
	conditions, args := []string{}, []any{}
	if from != "" {
		conditions = append(conditions, "expense_date>=?")
		args = append(args, from)
	}
	if to != "" {
		conditions = append(conditions, "expense_date<=?")
		args = append(args, to)
	}
	if status != "" {
		conditions = append(conditions, "display_status=?")
		args = append(args, status)
	}
	if needle != "" {
		conditions = append(conditions, "(LOCATE(?,person_name)>0 OR LOCATE(?,category)>0 OR LOCATE(?,note)>0 OR LOCATE(?,purchase_number)>0)")
		args = append(args, needle, needle, needle, needle)
	}
	if len(conditions) == 0 {
		return "1=1", args, nil
	}
	return strings.Join(conditions, " AND "), args, nil
}

func (a *App) listExpenses(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("expenses") {
		return fail(403, "无报销与费用预支权限")
	}
	q := r.URL.Query()
	tab := strings.TrimSpace(q.Get("tab"))
	if tab == "" {
		tab = "claims"
	}
	if tab != "claims" && tab != "advances" && tab != "history" {
		return fail(400, "报销查询分类无效")
	}
	if q.Get("export") == "1" {
		return a.exportExpenses(w, r, u, tab)
	}
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result expensePage
	result.Page, result.PageSize = page, expensePageSize
	switch tab {
	case "claims":
		where, args, filterErr := expenseClaimFilters(q)
		if filterErr != nil {
			return filterErr
		}
		base := expenseClaimCTE()
		if err = tx.QueryRowContext(r.Context(), base+" SELECT COUNT(*) FROM claim_rows WHERE "+where, args...).Scan(&result.Total); err != nil {
			return err
		}
		rows, queryErr := scanRows[expenseClaimRow](tx, base+` SELECT id,person_name,expense_date,category,amount_cents,note,display_status,advance_applied_cents,paid_cents,due_cents,
			COALESCE(purchase_document_id,''),purchase_number,purchase_date,purchase_total_cents,version,actor_name,created_at
			FROM claim_rows WHERE `+where+` ORDER BY expense_date DESC,id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), expensePageSize, (page-1)*expensePageSize), func(rows *sql.Rows, row *expenseClaimRow) error {
			var purchaseTotal sql.NullInt64
			if scanErr := rows.Scan(&row.ID, &row.PersonName, &row.ExpenseDate, &row.Category, &row.AmountCents, &row.Note, &row.Status,
				&row.AdvanceAppliedCents, &row.PaidCents, &row.DueCents, &row.PurchaseDocumentID, &row.PurchaseNumber,
				&row.PurchaseDate, &purchaseTotal, &row.Version, &row.ActorName, &row.CreatedAt); scanErr != nil {
				return scanErr
			}
			if purchaseTotal.Valid {
				row.PurchaseTotalCents = &purchaseTotal.Int64
			}
			return nil
		})
		if queryErr != nil {
			return queryErr
		}
		result.Rows = rows
	case "advances":
		where, args, filterErr := expenseAdvanceFilters(q)
		if filterErr != nil {
			return filterErr
		}
		base := expenseAdvanceCTE()
		if err = tx.QueryRowContext(r.Context(), base+" SELECT COUNT(*) FROM advance_rows WHERE "+where, args...).Scan(&result.Total); err != nil {
			return err
		}
		rows, queryErr := scanRows[expenseAdvanceRow](tx, base+` SELECT id,person_name,advance_date,amount_cents,purpose,note,applied_cents,returned_cents,remaining_cents,display_status,version,actor_name,created_at
			FROM advance_rows WHERE `+where+` ORDER BY advance_date DESC,id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), expensePageSize, (page-1)*expensePageSize), func(rows *sql.Rows, row *expenseAdvanceRow) error {
			return rows.Scan(&row.ID, &row.PersonName, &row.AdvanceDate, &row.AmountCents, &row.Purpose, &row.Note,
				&row.AppliedCents, &row.ReturnedCents, &row.RemainingCents, &row.Status, &row.Version, &row.ActorName, &row.CreatedAt)
		})
		if queryErr != nil {
			return queryErr
		}
		result.Rows = rows
	case "history":
		where, args, filterErr := expenseHistoryFilters(q)
		if filterErr != nil {
			return filterErr
		}
		if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM expense_history h WHERE "+where, args...).Scan(&result.Total); err != nil {
			return err
		}
		rows, queryErr := scanRows[expenseHistoryRow](tx, `SELECT h.id,h.entity_type,h.entity_id,h.event,h.reason,h.before_snapshot,h.after_snapshot,h.actor_name,h.operation_id,
			DATE_FORMAT(h.created_at,'%Y-%m-%d %H:%i:%s'),
			CASE h.entity_type WHEN 'claim' THEN COALESCE((SELECT c.status FROM expense_claims c WHERE c.id=h.entity_id),'')
			 WHEN 'advance' THEN COALESCE((SELECT a.status FROM expense_advances a WHERE a.id=h.entity_id),'')
			 WHEN 'payment' THEN COALESCE((SELECT p.status FROM expense_payments p WHERE p.id=h.entity_id),'')
			 WHEN 'allocation' THEN COALESCE((SELECT x.status FROM expense_advance_allocations x WHERE x.id=h.entity_id),'')
			 WHEN 'return' THEN COALESCE((SELECT z.status FROM expense_advance_returns z WHERE z.id=h.entity_id),'') ELSE '' END,
			CASE h.entity_type WHEN 'claim' THEN COALESCE((SELECT c.version FROM expense_claims c WHERE c.id=h.entity_id),0)
			 WHEN 'advance' THEN COALESCE((SELECT a.version FROM expense_advances a WHERE a.id=h.entity_id),0)
			 WHEN 'payment' THEN COALESCE((SELECT p.version FROM expense_payments p WHERE p.id=h.entity_id),0)
			 WHEN 'allocation' THEN COALESCE((SELECT x.version FROM expense_advance_allocations x WHERE x.id=h.entity_id),0)
			 WHEN 'return' THEN COALESCE((SELECT z.version FROM expense_advance_returns z WHERE z.id=h.entity_id),0) ELSE 0 END,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.claim_id')),JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.ClaimID')),''),
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.advance_id')),''),
			CAST(JSON_UNQUOTE(JSON_EXTRACT(h.after_snapshot,'$.amount_cents')) AS SIGNED)
		 FROM expense_history h WHERE `+where+` ORDER BY h.created_at DESC,h.id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), expensePageSize, (page-1)*expensePageSize), func(rows *sql.Rows, row *expenseHistoryRow) error {
			var before, after []byte
			var amount sql.NullInt64
			if scanErr := rows.Scan(&row.ID, &row.EntityType, &row.EntityID, &row.Event, &row.Reason, &before, &after,
				&row.ActorName, &row.OperationID, &row.CreatedAt, &row.CurrentStatus, &row.CurrentVersion, &row.ClaimID, &row.AdvanceID, &amount); scanErr != nil {
				return scanErr
			}
			if len(before) == 0 {
				before = []byte("null")
			}
			if !json.Valid(before) || !json.Valid(after) {
				return fail(500, "费用历史快照无效")
			}
			row.BeforeSnapshot = append(json.RawMessage(nil), before...)
			row.AfterSnapshot = append(json.RawMessage(nil), after...)
			if amount.Valid {
				row.AmountCents = &amount.Int64
			}
			return nil
		})
		if queryErr != nil {
			return queryErr
		}
		result.Rows = rows
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, result)
	return nil
}

func expenseAdvanceCTE() string {
	return `WITH advance_balances AS (
	 SELECT a.id,a.person_name,DATE_FORMAT(a.advance_date,'%Y-%m-%d') advance_date,a.amount_cents,a.purpose,a.note,a.status,a.version,a.actor_name,
	  DATE_FORMAT(a.created_at,'%Y-%m-%d %H:%i:%s') created_at,COALESCE(x.applied,0) applied_cents,COALESCE(r.returned,0) returned_cents,
	  CASE WHEN a.status='voided' THEN 0 ELSE a.amount_cents-COALESCE(x.applied,0)-COALESCE(r.returned,0) END remaining_cents
	 FROM expense_advances a
	 LEFT JOIN (SELECT advance_id,SUM(amount_cents) applied FROM expense_advance_allocations WHERE status='active' GROUP BY advance_id) x ON x.advance_id=a.id
	 LEFT JOIN (SELECT advance_id,SUM(amount_cents) returned FROM expense_advance_returns WHERE status='active' GROUP BY advance_id) r ON r.advance_id=a.id
	), advance_rows AS (
	 SELECT advance_balances.*,CASE WHEN status='voided' THEN '已撤销' WHEN remaining_cents=0 THEN '已结清' ELSE '未核销' END display_status FROM advance_balances
	)`
}

func expenseAdvanceFilters(q url.Values) (string, []any, error) {
	from, to, err := expenseDateFilter(q)
	if err != nil {
		return "", nil, err
	}
	status, needle := strings.TrimSpace(q.Get("status")), strings.TrimSpace(q.Get("q"))
	if len([]rune(status)) > 30 || len([]rune(needle)) > 100 {
		return "", nil, fail(400, "费用预支筛选无效")
	}
	conditions, args := []string{}, []any{}
	if from != "" {
		conditions, args = append(conditions, "advance_date>=?"), append(args, from)
	}
	if to != "" {
		conditions, args = append(conditions, "advance_date<=?"), append(args, to)
	}
	if status != "" {
		if status == "active" {
			conditions = append(conditions, "status='active'")
		} else {
			conditions, args = append(conditions, "display_status=?"), append(args, status)
		}
	}
	if needle != "" {
		conditions = append(conditions, "(LOCATE(?,person_name)>0 OR LOCATE(?,purpose)>0 OR LOCATE(?,note)>0)")
		args = append(args, needle, needle, needle)
	}
	if len(conditions) == 0 {
		return "1=1", args, nil
	}
	return strings.Join(conditions, " AND "), args, nil
}

func expenseHistoryFilters(q url.Values) (string, []any, error) {
	from, to, err := expenseDateFilter(q)
	if err != nil {
		return "", nil, err
	}
	status, needle := strings.TrimSpace(q.Get("status")), strings.TrimSpace(q.Get("q"))
	if len([]rune(status)) > 100 || len([]rune(needle)) > 100 {
		return "", nil, fail(400, "费用历史筛选无效")
	}
	conditions, args := []string{}, []any{}
	if from != "" {
		conditions, args = append(conditions, "h.created_at>=?"), append(args, from)
	}
	if to != "" {
		conditions, args = append(conditions, "h.created_at<DATE_ADD(?,INTERVAL 1 DAY)"), append(args, to)
	}
	if status != "" {
		conditions, args = append(conditions, "h.event=?"), append(args, status)
	}
	if needle != "" {
		conditions = append(conditions, "(LOCATE(?,h.entity_id)>0 OR LOCATE(?,h.entity_type)>0 OR LOCATE(?,h.actor_name)>0 OR LOCATE(?,h.reason)>0)")
		args = append(args, needle, needle, needle, needle)
	}
	if len(conditions) == 0 {
		return "1=1", args, nil
	}
	return strings.Join(conditions, " AND "), args, nil
}

func (a *App) listExpensePurchases(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("expenses") {
		return fail(403, "无报销与费用预支权限")
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	needle := strings.TrimSpace(q.Get("q"))
	if len([]rune(needle)) > 100 {
		return fail(400, "搜索内容不能超过100字")
	}
	where := "d.kind='进货' AND d.voided=0 AND d.total_cents>0"
	args := []any{}
	if needle != "" {
		where += " AND (LOCATE(?,d.id)>0 OR LOCATE(?,d.note)>0)"
		args = append(args, needle, needle)
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM stock_documents d WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows[expensePurchaseRow](tx, `SELECT d.id,DATE_FORMAT(d.work_date,'%Y-%m-%d'),d.total_cents,d.note,d.line_items,d.version,
		GREATEST(0,d.total_cents-COALESCE((SELECT SUM(c.amount_cents) FROM expense_claims c WHERE c.purchase_document_id=d.id AND c.status<>'voided'),0))
		FROM stock_documents d WHERE `+where+` ORDER BY d.work_date DESC,d.id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), expensePageSize, (page-1)*expensePageSize), func(rows *sql.Rows, row *expensePurchaseRow) error {
		var lines []byte
		if scanErr := rows.Scan(&row.ID, &row.WorkDate, &row.TotalCents, &row.Note, &lines, &row.Version, &row.AvailableCents); scanErr != nil {
			return scanErr
		}
		if len(lines) == 0 {
			lines = []byte("[]")
		}
		if !json.Valid(lines) {
			return fail(500, "进货单明细无效")
		}
		row.LineItems = append(json.RawMessage(nil), lines...)
		return nil
	})
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, expensePage{Rows: rows, Total: total, Page: page, PageSize: expensePageSize})
	return nil
}
