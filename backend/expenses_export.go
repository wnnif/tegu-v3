package main

import (
	"database/sql"
	"net/http"
)

// Export the complete applied filter under one read snapshot. Advance allocation
// is a balance movement; it is never exported as another actual payment.
func (a *App) exportExpenses(w http.ResponseWriter, r *http.Request, u User, tab string) error {
	if !u.can("expenses") {
		return fail(403, "无报销与费用预支权限")
	}
	var query string
	var headers []string
	var money []bool
	var args []any
	var err error
	switch tab {
	case "claims":
		var where string
		where, args, err = expenseClaimFilters(r.URL.Query())
		headers = []string{"编号", "报销人", "费用发生日期", "类型", "登记金额元", "预支核销元", "实际补付款元", "尚未支付元", "状态", "备注", "关联进货编号", "进货日期", "进货登记金额元"}
		money = []bool{false, false, false, false, true, true, true, true, false, false, false, false, true}
		query = expenseClaimCTE() + ` SELECT id,person_name,expense_date,category,amount_cents,advance_applied_cents,paid_cents,IF(status='voided',0,due_cents),display_status,note,COALESCE(purchase_document_id,''),purchase_date,COALESCE(purchase_total_cents,0) FROM claim_rows WHERE ` + where + ` ORDER BY expense_date DESC,id DESC`
	case "advances":
		var where string
		where, args, err = expenseAdvanceFilters(r.URL.Query())
		headers = []string{"编号", "预支人", "实际付款日期", "费用预支元", "已核销元", "员工退回元", "未核销余额元", "用途", "备注", "状态"}
		money = []bool{false, false, false, true, true, true, true, false, false, false}
		query = expenseAdvanceCTE() + ` SELECT id,person_name,advance_date,amount_cents,applied_cents,returned_cents,remaining_cents,purpose,note,display_status FROM advance_rows WHERE ` + where + ` ORDER BY advance_date DESC,id DESC`
	case "history":
		var where string
		where, args, err = expenseHistoryFilters(r.URL.Query())
		headers = []string{"历史编号", "记录类型", "记录编号", "操作", "原因", "操作人", "操作号", "操作时间", "更正前快照", "更正后快照"}
		money = make([]bool, len(headers))
		query = `SELECT h.id,h.entity_type,h.entity_id,h.event,h.reason,h.actor_name,h.operation_id,DATE_FORMAT(h.created_at,'%Y-%m-%d %H:%i:%s'),COALESCE(CAST(h.before_snapshot AS CHAR),'null'),CAST(h.after_snapshot AS CHAR) FROM expense_history h WHERE ` + where + ` ORDER BY h.created_at DESC,h.id DESC`
	default:
		return fail(400, "报销导出分类无效")
	}
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return exportPartySummary(tx, w, r, "expenses-"+tab, headers, query, args, money)
}
