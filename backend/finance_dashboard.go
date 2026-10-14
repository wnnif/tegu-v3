package main

import (
	"database/sql"
	"encoding/csv"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type financeDashboardRange struct {
	From string `json:"from"`
	To   string `json:"to"`
	All  bool   `json:"all"`
}

type financeDashboardMetric struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Cents  string `json:"amount_cents"`
	Basis  string `json:"basis"`
	Source string `json:"source"`
}

type financeDashboardGroup struct {
	Key     string                   `json:"key"`
	Label   string                   `json:"label"`
	Metrics []financeDashboardMetric `json:"metrics"`
}

type financeDashboardResponse struct {
	Range       financeDashboardRange   `json:"range"`
	QueriedAt   string                  `json:"queried_at"`
	BalanceNote string                  `json:"balance_note"`
	Groups      []financeDashboardGroup `json:"groups"`
}

func financeDashboardDates(q url.Values, now time.Time) (financeDashboardRange, error) {
	r := financeDashboardRange{From: strings.TrimSpace(q.Get("from")), To: strings.TrimSpace(q.Get("to"))}
	if !contains([]string{"", "0", "1"}, q.Get("all")) {
		return r, fail(400, "日期范围无效")
	}
	r.All = q.Get("all") == "1"
	today := now.In(businessLocation).Format("2006-01-02")
	if r.All {
		r.From, r.To = "1000-01-01", today
	} else if r.From == "" && r.To == "" {
		r.From, r.To = now.In(businessLocation).Format("2006-01")+"-01", today
	} else if r.From == "" || r.To == "" {
		return r, fail(400, "请选择开始日期和结束日期")
	}
	for _, date := range []string{r.From, r.To} {
		if _, err := time.Parse("2006-01-02", date); err != nil || date < "1000-01-01" {
			return r, fail(400, "请选择有效日期")
		}
	}
	if r.From > r.To {
		return r, fail(400, "开始日期不能晚于结束日期")
	}
	return r, nil
}

// Each source contributes one ledger fact. Delivery and receipt allocation are
// separate classifications, never additional revenue or additional cash.
const financeDashboardCustomerLedger = `
 SELECT o.company_id owner,'construction' kind,o.work_date fact_date,o.total_cents-COALESCE(s.amount,0) amount
 FROM orders o LEFT JOIN (SELECT order_id,SUM(total_cents) amount FROM order_sales GROUP BY order_id) s ON s.order_id=o.id WHERE o.status<>'已取消'
 UNION ALL SELECT t.company_id,'construction',DATE(DATE_ADD(t.created_at,INTERVAL 8 HOUR)),pp.price_cents FROM project_packages pp JOIN project_tickets t ON t.id=pp.charged_ticket_id
 UNION ALL SELECT o.company_id,'sales',o.work_date,s.total_cents FROM order_sales s JOIN orders o ON o.id=s.order_id WHERE o.status<>'已取消'
 UNION ALL SELECT company_id,'received',received_at,amount_cents FROM company_receipts WHERE status='active'
 UNION ALL SELECT t.company_id,'received',r.received_at,r.amount_cents FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id WHERE r.voided=0 AND r.receipt_id=''
 UNION ALL SELECT o.company_id,'received',r.received_at,r.amount_cents FROM payment_receipts r JOIN orders o ON o.id=r.order_id WHERE r.voided=0 AND o.ticket_managed=0 AND o.status<>'已取消'
 UNION ALL SELECT o.company_id,'received_undated','1000-01-01',o.total_cents FROM orders o WHERE o.ticket_managed=0 AND o.status='已收款' AND NOT EXISTS(SELECT 1 FROM payment_receipts r WHERE r.order_id=o.id AND r.voided=0)
 UNION ALL SELECT t.company_id,'allocated',GREATEST(COALESCE(c.received_at,r.received_at),DATE(DATE_ADD(r.created_at,INTERVAL 8 HOUR))),r.amount_cents FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id LEFT JOIN company_receipts c ON c.id=r.receipt_id WHERE r.voided=0 AND (r.receipt_id='' OR c.status='active')
 UNION ALL SELECT o.company_id,'allocated',r.received_at,r.amount_cents FROM payment_receipts r JOIN orders o ON o.id=r.order_id WHERE r.voided=0 AND o.ticket_managed=0 AND o.status<>'已取消'
 UNION ALL SELECT o.company_id,'allocated','1000-01-01',o.total_cents FROM orders o WHERE o.ticket_managed=0 AND o.status='已收款' AND NOT EXISTS(SELECT 1 FROM payment_receipts r WHERE r.order_id=o.id AND r.voided=0)
 UNION ALL SELECT d.company_id,'discount',DATE(DATE_ADD(d.created_at,INTERVAL 8 HOUR)),d.amount_cents FROM customer_discounts d WHERE d.voided=0
 UNION ALL SELECT o.company_id,'delivered',GREATEST(o.work_date,COALESCE(b.delivered_at,t.delivered_at,o.delivered_at,o.work_date)),l.total_cents FROM order_lines l JOIN orders o ON o.id=l.order_id
 LEFT JOIN ticket_batches b ON b.id=l.ticket_batch_id AND b.status='delivered'
 LEFT JOIN (SELECT i.item_id,i.order_id,t.company_id,t.project_id,MIN(t.delivered_at) delivered_at FROM project_ticket_items i JOIN project_tickets t ON t.id=i.ticket_id WHERE i.item_kind='line' AND t.delivered_at IS NOT NULL AND t.status<>'草稿' GROUP BY i.item_id,i.order_id,t.company_id,t.project_id) t ON t.item_id=l.id AND t.order_id=o.id AND t.company_id=o.company_id AND t.project_id=o.project_id
 WHERE o.status<>'已取消' AND (b.id IS NOT NULL OR t.delivered_at IS NOT NULL OR (o.ticket_managed=0 AND (o.delivered_at IS NOT NULL OR o.status IN ('已交单','部分收款','已收款'))))
 UNION ALL SELECT o.company_id,'delivered',GREATEST(o.work_date,COALESCE(b.delivered_at,t.delivered_at,o.delivered_at,o.work_date)),s.total_cents FROM order_sales s JOIN orders o ON o.id=s.order_id
 LEFT JOIN ticket_batches b ON b.id=s.ticket_batch_id AND b.status='delivered'
 LEFT JOIN (SELECT i.item_id,i.order_id,t.company_id,t.project_id,MIN(t.delivered_at) delivered_at FROM project_ticket_items i JOIN project_tickets t ON t.id=i.ticket_id WHERE i.item_kind='sale' AND t.delivered_at IS NOT NULL AND t.status<>'草稿' GROUP BY i.item_id,i.order_id,t.company_id,t.project_id) t ON t.item_id=s.id AND t.order_id=o.id AND t.company_id=o.company_id AND t.project_id=o.project_id
 WHERE o.status<>'已取消' AND (b.id IS NOT NULL OR t.delivered_at IS NOT NULL OR (o.ticket_managed=0 AND (o.delivered_at IS NOT NULL OR o.status IN ('已交单','部分收款','已收款'))))
 UNION ALL SELECT o.company_id,'delivered',GREATEST(o.work_date,COALESCE(o.delivered_at,o.work_date)),o.total_cents FROM orders o
 WHERE o.ticket_managed=0 AND o.status<>'已取消' AND (o.delivered_at IS NOT NULL OR o.status IN ('已交单','部分收款','已收款'))
   AND NOT EXISTS(SELECT 1 FROM order_lines l WHERE l.order_id=o.id)
   AND NOT EXISTS(SELECT 1 FROM order_sales s WHERE s.order_id=o.id)
 UNION ALL SELECT t.company_id,'delivered',GREATEST(DATE(DATE_ADD(t.created_at,INTERVAL 8 HOUR)),COALESCE(t.delivered_at,pbd.delivered_at)),pp.price_cents
 FROM project_packages pp JOIN project_tickets t ON t.id=pp.charged_ticket_id
 LEFT JOIN (
  SELECT i.source_project_ticket_id ticket_id,MIN(b.delivered_at) delivered_at
  FROM ticket_batch_items i JOIN ticket_batches b ON b.id=i.batch_id AND b.status='delivered'
  JOIN orders o ON o.id=i.order_id AND o.status<>'已取消'
  LEFT JOIN order_lines l ON i.item_kind='line' AND l.id=i.item_id AND l.ticket_batch_id=i.batch_id
  LEFT JOIN order_sales s ON i.item_kind='sale' AND s.id=i.item_id AND s.ticket_batch_id=i.batch_id
  WHERE l.id IS NOT NULL OR s.id IS NOT NULL GROUP BY i.source_project_ticket_id
 ) pbd ON pbd.ticket_id=t.id
 WHERE t.delivered_at IS NOT NULL OR pbd.delivered_at IS NOT NULL`

const financeDashboardPayrollLedger = `
 SELECT ww.worker_id owner,'gross' kind,o.work_date fact_date,ww.wage_cents amount FROM ` + workerWageSource + ` ww JOIN orders o ON o.id=ww.order_id WHERE o.status<>'已取消'
 UNION ALL SELECT worker_id,'adjustment',effective_date,amount_cents FROM payroll_adjustments
 UNION ALL SELECT worker_id,'deduction',deduction_date,amount_cents FROM payroll_deductions WHERE status='confirmed'
 UNION ALL SELECT worker_id,'advance',advance_date,amount_cents FROM payroll_advances WHERE status='active'
 UNION ALL SELECT s.worker_id,IF(src.source_type='advance','advance_applied','deduction_applied'),COALESCE(s.settlement_date,DATE(DATE_ADD(s.created_at,INTERVAL 8 HOUR))),src.amount_cents
 FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type IN ('advance','deduction') AND s.status='confirmed'
 UNION ALL SELECT s.worker_id,IF(p.paid_date IS NULL,'paid_undated','paid_final'),COALESCE(p.paid_date,'1000-01-01'),p.amount_cents FROM payroll_payments p JOIN payroll_settlements s ON s.id=p.settlement_id WHERE p.status='active' AND s.status='confirmed'
 UNION ALL SELECT worker_id,'paid_undated','1000-01-01',payable_cents FROM payroll_settlements WHERE status='confirmed' AND legacy_paid=1
 UNION ALL SELECT worker_id,IF(entry_kind='automatic','bonus_accrued','bonus_adjustment'),work_date,amount_cents FROM payroll_bonus_entries
 UNION ALL SELECT s.worker_id,'bonus_paid',p.paid_date,p.amount_cents FROM payroll_bonus_payments p JOIN payroll_bonus_settlements s ON s.id=p.settlement_id WHERE p.status='active' AND s.status='confirmed'`

const financeDashboardExpenseLedger = `
 SELECT id owner,'registered' kind,expense_date fact_date,amount_cents amount FROM expense_claims WHERE status<>'voided'
 UNION ALL SELECT id,'confirmed',expense_date,amount_cents FROM expense_claims WHERE status='confirmed'
 UNION ALL SELECT id,'pending',expense_date,amount_cents FROM expense_claims WHERE status='pending'
 UNION ALL SELECT p.claim_id,'paid',p.paid_date,p.amount_cents FROM expense_payments p JOIN expense_claims c ON c.id=p.claim_id WHERE p.status='active' AND c.status='confirmed'
 UNION ALL SELECT a.claim_id,'applied',a.applied_date,a.amount_cents FROM expense_advance_allocations a JOIN expense_claims c ON c.id=a.claim_id JOIN expense_advances v ON v.id=a.advance_id WHERE a.status='active' AND c.status='confirmed' AND v.status='active'
 UNION ALL SELECT id,'advance',advance_date,amount_cents FROM expense_advances WHERE status='active'
 UNION ALL SELECT r.advance_id,'returned',r.returned_date,r.amount_cents FROM expense_advance_returns r JOIN expense_advances a ON a.id=r.advance_id WHERE r.status='active' AND a.status='active'`

type financeDashboardAmounts struct {
	Period map[string]string
	Cutoff map[string]string
}

func financeDashboardAggregate(tx *sql.Tx, r financeDashboardRange, ledger string) (financeDashboardAmounts, error) {
	result := financeDashboardAmounts{Period: map[string]string{}, Cutoff: map[string]string{}}
	rows, err := tx.Query(`SELECT kind,CAST(COALESCE(SUM(IF(fact_date>=? AND fact_date<=?,amount,0)),0) AS CHAR),CAST(COALESCE(SUM(IF(fact_date<=?,amount,0)),0) AS CHAR) FROM (`+ledger+`) facts GROUP BY kind`, r.From, r.To, r.To)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, period, cutoff string
		if err := rows.Scan(&key, &period, &cutoff); err != nil {
			return result, err
		}
		result.Period[key], result.Cutoff[key] = period, cutoff
	}
	return result, rows.Err()
}

func financeDashboardSum(values map[string]string, terms ...string) string {
	result := new(big.Int)
	for _, term := range terms {
		sign, key := int64(1), term
		if strings.HasPrefix(term, "-") {
			sign, key = -1, term[1:]
		}
		value, ok := new(big.Int).SetString(values[key], 10)
		if ok {
			result.Add(result, value.Mul(value, big.NewInt(sign)))
		}
	}
	return result.String()
}

// Compute debt per owner before summing; one company's prepayment must never
// cancel another company's unpaid invoices, nor a worker's unpaid wages.
func financeDashboardOwnerBalance(tx *sql.Tx, r financeDashboardRange, ledger, expression string) (string, error) {
	var result string
	err := tx.QueryRow(`SELECT CAST(COALESCE(SUM(GREATEST(`+expression+`,0)),0) AS CHAR) FROM (
 SELECT owner,SUM(IF(kind IN ('construction','sales'),amount,0)) charge,SUM(IF(kind IN ('received','received_undated'),amount,0)) received,
 SUM(IF(kind='discount',amount,0)) discount,SUM(IF(kind='gross',amount,0)) gross,SUM(IF(kind='adjustment',amount,0)) adjustment,
 SUM(IF(kind='deduction_applied',amount,0)) deduction_applied,SUM(IF(kind='advance_applied',amount,0)) advance_applied,
 SUM(IF(kind IN ('paid_final','paid_undated'),amount,0)) paid,SUM(IF(kind='bonus_accrued',amount,0)) bonus_accrued,
 SUM(IF(kind='bonus_adjustment',amount,0)) bonus_adjustment,SUM(IF(kind='bonus_paid',amount,0)) bonus_paid
 FROM (`+ledger+`) facts WHERE fact_date<=? GROUP BY owner) balances`, r.To).Scan(&result)
	return result, err
}

func financeDashboardExpenseUnpaid(tx *sql.Tx, r financeDashboardRange) (string, error) {
	var result string
	err := tx.QueryRow(`SELECT CAST(COALESCE(SUM(GREATEST(c.amount_cents-COALESCE(p.paid,0)-COALESCE(a.applied,0),0)),0) AS CHAR)
 FROM expense_claims c
 LEFT JOIN (SELECT claim_id,SUM(amount_cents) paid FROM expense_payments WHERE status='active' AND paid_date<=? GROUP BY claim_id) p ON p.claim_id=c.id
 LEFT JOIN (SELECT x.claim_id,SUM(x.amount_cents) applied FROM expense_advance_allocations x JOIN expense_advances v ON v.id=x.advance_id WHERE x.status='active' AND v.status='active' AND x.applied_date<=? GROUP BY x.claim_id) a ON a.claim_id=c.id
 WHERE c.status='confirmed' AND c.expense_date<=?`, r.To, r.To, r.To).Scan(&result)
	return result, err
}

func financeDashboardAdvanceBalance(tx *sql.Tx, r financeDashboardRange, payroll bool) (string, error) {
	query := `SELECT CAST(COALESCE(SUM(GREATEST(v.amount_cents-COALESCE(a.applied,0)-COALESCE(r.returned,0),0)),0) AS CHAR)
 FROM expense_advances v
 LEFT JOIN (SELECT advance_id,SUM(x.amount_cents) applied FROM expense_advance_allocations x JOIN expense_claims c ON c.id=x.claim_id WHERE x.status='active' AND c.status='confirmed' AND x.applied_date<=? GROUP BY advance_id) a ON a.advance_id=v.id
 LEFT JOIN (SELECT advance_id,SUM(amount_cents) returned FROM expense_advance_returns WHERE status='active' AND returned_date<=? GROUP BY advance_id) r ON r.advance_id=v.id
 WHERE v.status='active' AND v.advance_date<=?`
	args := []any{r.To, r.To, r.To}
	if payroll {
		query = `SELECT CAST(COALESCE(SUM(GREATEST(v.amount_cents-COALESCE(a.applied,0),0)),0) AS CHAR)
 FROM payroll_advances v
 LEFT JOIN (SELECT src.source_id,SUM(src.amount_cents) applied FROM payroll_settlement_sources src JOIN payroll_settlements s ON s.id=src.settlement_id WHERE src.source_type='advance' AND s.status='confirmed' AND COALESCE(s.settlement_date,DATE(DATE_ADD(s.created_at,INTERVAL 8 HOUR)))<=? GROUP BY src.source_id) a ON a.source_id=v.id
 WHERE v.status='active' AND v.advance_date<=?`
		args = []any{r.To, r.To}
	}
	var result string
	err := tx.QueryRow(query, args...).Scan(&result)
	return result, err
}

func (a *App) financeDashboard(w http.ResponseWriter, req *http.Request, u User) error {
	if !u.can("finance") {
		return fail(403, "无财务汇总权限")
	}
	now := time.Now()
	r, err := financeDashboardDates(req.URL.Query(), now)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(req.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result := financeDashboardResponse{Range: r, QueriedAt: now.In(businessLocation).Format("2006-01-02 15:04:05"), Groups: []financeDashboardGroup{}, BalanceNote: "余额累计至结束日期，依据当前有效记录；撤销、更正后的历史状态不重建。库存为查询时当前参考货值，不是历史库存。"}
	add := func(key, label string, metrics ...financeDashboardMetric) {
		result.Groups = append(result.Groups, financeDashboardGroup{Key: key, Label: label, Metrics: metrics})
	}
	metric := func(key, label, cents, basis, source string) financeDashboardMetric {
		return financeDashboardMetric{Key: key, Label: label, Cents: cents, Basis: basis, Source: source}
	}
	period, cutoff := "期间", "截至 "+r.To
	if u.can("customer_prices") {
		v, err := financeDashboardAggregate(tx, r, financeDashboardCustomerLedger)
		if err != nil {
			return err
		}
		outstanding, err := financeDashboardOwnerBalance(tx, r, financeDashboardCustomerLedger, "charge-discount-received")
		if err != nil {
			return err
		}
		advance, err := financeDashboardOwnerBalance(tx, r, financeDashboardCustomerLedger, "received+discount-charge")
		if err != nil {
			return err
		}
		add("customer", "客户业务与收款",
			metric("construction", "施工收费", financeDashboardSum(v.Period, "construction"), period+"施工日期；含特殊收费，免费项为0", "orders"),
			metric("sales", "单独售料收费", financeDashboardSum(v.Period, "sales"), period+"销售施工单日期", "orders"),
			metric("received", "实际客户到账", financeDashboardSum(v.Period, "received"), period+"实际到账日期；不含分配或减免", "finance"),
			metric("outstanding", "公司真正待收款", outstanding, cutoff+"；逐公司收费－减免－真实到账，含未分配款", "finance"),
			metric("charge_balance", "累计施工及售料收费", financeDashboardSum(v.Cutoff, "construction", "sales"), cutoff+"；交票不增加收费", "finance"),
			metric("delivered", "累计已交票金额", financeDashboardSum(v.Cutoff, "delivered"), cutoff+"；按当前有效实际行交票；旧无明细整单仅计保存总额一次；旧整单交票日期未记录时按施工日期纳入余额", "ticket-issue"),
			metric("undelivered", "累计未交票金额", financeDashboardSum(v.Cutoff, "construction", "sales", "-delivered"), cutoff+"；累计收费－已交票", "ticket-issue"),
			metric("unallocated", "尚未分配客户收款", financeDashboardSum(v.Cutoff, "received", "received_undated", "-allocated"), cutoff+"；分配不产生新收款", "finance"),
			metric("discount", "客户减免", financeDashboardSum(v.Cutoff, "discount"), cutoff+"；减少欠款，不计实收", "finance"),
			metric("customer_advance", "客户预收余额", advance, cutoff+"；逐公司超出收费的到账及减免", "finance"),
			metric("received_undated", "历史已收款（日期未记录）", financeDashboardSum(v.Cutoff, "received_undated"), "只参与余额核对，不计期间到账", "finance"))
	}
	if u.can("warehouse") {
		v, err := financeDashboardAggregate(tx, r, `SELECT '' owner,'purchase' kind,work_date fact_date,COALESCE(total_cents,0) amount FROM stock_documents WHERE kind='进货' AND voided=0`)
		if err != nil {
			return err
		}
		from, args := inventorySummaryQuery(url.Values{"show_zero": {"1"}})
		var inventory string
		if err := tx.QueryRow("SELECT CAST(COALESCE(SUM("+inventorySummaryValueSQL+"),0) AS CHAR)"+from, args...).Scan(&inventory); err != nil {
			return err
		}
		var unknown int64
		if err := tx.QueryRow("SELECT COUNT(*)"+from+" AND COALESCE(s.qty_quantum,0)<>0 AND COALESCE(cost.purchased_quantum,0)=0", args...).Scan(&unknown); err != nil {
			return err
		}
		basis := "查询时当前库存；含总仓和工人仓，调拨不减少总额；沿累计进货均价估值"
		if unknown > 0 {
			basis += "；存在无采购价格商品，其参考货值为0"
		}
		add("inventory", "采购与库存",
			metric("purchase", "商品进货金额", financeDashboardSum(v.Period, "purchase"), period+"进货日期；代买报销不再次计进货", "inventory"),
			metric("purchase_balance", "商品累计进货金额", financeDashboardSum(v.Cutoff, "purchase"), cutoff+"；仅有效进货单", "inventory"),
			metric("inventory_value", "当前剩余库存参考货值", inventory, basis, "inventory"))
	}
	if u.can("wages") {
		if err := validateWorkerAccountScope(tx, "o.work_date<=?", []any{r.To}); err != nil {
			return err
		}
		v, err := financeDashboardAggregate(tx, r, financeDashboardPayrollLedger)
		if err != nil {
			return err
		}
		unpaid, err := financeDashboardOwnerBalance(tx, r, financeDashboardPayrollLedger, "gross+adjustment-deduction_applied-advance_applied-paid")
		if err != nil {
			return err
		}
		bonusBalance, err := financeDashboardOwnerBalance(tx, r, financeDashboardPayrollLedger, "bonus_accrued+bonus_adjustment-bonus_paid")
		if err != nil {
			return err
		}
		advanceBalance, err := financeDashboardAdvanceBalance(tx, r, true)
		if err != nil {
			return err
		}
		add("payroll", "工人工资",
			metric("wage", "期间施工工资", financeDashboardSum(v.Period, "gross"), period+"施工日期；含带货工资", "wages"),
			metric("wage_balance", "累计施工工资", financeDashboardSum(v.Cutoff, "gross"), cutoff, "wages"),
			metric("wage_adjustment", "累计工资调整", financeDashboardSum(v.Cutoff, "adjustment"), cutoff+"；单独列示，不覆盖施工工资", "wages"),
			metric("wage_paid", "实际支付工资款", financeDashboardSum(v.Period, "advance", "paid_final"), period+"付款日期；工资预支＋最终实际发放，只计现金一次", "wages"),
			metric("wage_final_paid", "其中最终实际发放", financeDashboardSum(v.Period, "paid_final"), period+"实际发薪日期；不重复已抵扣预支", "wages"),
			metric("wage_unpaid", "尚未支付工资", unpaid, cutoff+"；按人员扣除已结算扣款、抵扣预支及最终已发", "wages"),
			metric("wage_deduction", "已确认罚款及扣款", financeDashboardSum(v.Cutoff, "deduction"), cutoff+"扣款日期；未确认不计", "wages"),
			metric("wage_deduction_applied", "其中已结算扣款", financeDashboardSum(v.Cutoff, "deduction_applied"), cutoff+"结算日期；此金额扣减未发工资", "wages"),
			metric("wage_advance", "期间工资预支", financeDashboardSum(v.Period, "advance"), period+"实际预支付款日期", "wages"),
			metric("wage_advance_balance", "工资预支尚未抵扣", advanceBalance, cutoff+"；仅截至日已支付的预支及其实际抵扣", "wages"),
			metric("wage_paid_undated", "历史已发工资（日期未记录）", financeDashboardSum(v.Cutoff, "paid_undated"), "只参与余额核对，不计期间付款", "wages"))
		add("bonus", "工人年终奖金",
			metric("bonus_accrued", "期间自动累计奖金", financeDashboardSum(v.Period, "bonus_accrued"), period+"有效施工日期；按已确认奖金规则独立计提", "wages"),
			metric("bonus_accrued_balance", "累计自动奖金", financeDashboardSum(v.Cutoff, "bonus_accrued"), cutoff+"；订单更正或取消保留差额冲销记录", "wages"),
			metric("bonus_adjustment", "累计手工奖金调整", financeDashboardSum(v.Cutoff, "bonus_adjustment"), cutoff+"；老板手工登记及冲销，不按百分比推算", "wages"),
			metric("bonus_paid", "期间实际已发奖金", financeDashboardSum(v.Period, "bonus_paid"), period+"奖金付款日期；与工资付款分开", "wages"),
			metric("bonus_unpaid", "剩余未发奖金", bonusBalance, cutoff+"；按人员累计奖金及有效实际付款计算", "wages"))
	}
	if u.can("expenses") {
		v, err := financeDashboardAggregate(tx, r, financeDashboardExpenseLedger)
		if err != nil {
			return err
		}
		unpaid, err := financeDashboardExpenseUnpaid(tx, r)
		if err != nil {
			return err
		}
		advanceBalance, err := financeDashboardAdvanceBalance(tx, r, false)
		if err != nil {
			return err
		}
		add("expenses", "报销与报账预支",
			metric("expense_registered", "已登记报销金额", financeDashboardSum(v.Period, "registered"), period+"费用发生日期；含待审核，不含撤销", "expenses"),
			metric("expense_paid", "实际已付报销款", financeDashboardSum(v.Period, "paid"), period+"实际付款日期；预支核销不再次计付款", "expenses"),
			metric("expense_unpaid", "尚未支付已确认报销", unpaid, cutoff+"；已发生的已确认费用－对应预支核销－对应实际付款", "expenses"),
			metric("expense_pending", "待审核报销", financeDashboardSum(v.Cutoff, "pending"), cutoff+"；未计入已确认待付款", "expenses"),
			metric("expense_advance", "期间报账预支", financeDashboardSum(v.Period, "advance"), period+"实际预支付款日期", "expenses"),
			metric("expense_advance_applied", "期间预支核销", financeDashboardSum(v.Period, "applied"), period+"核销日期；不产生再次付款", "expenses"),
			metric("expense_advance_returned", "期间退回预支", financeDashboardSum(v.Period, "returned"), period+"实际退回日期；单独列示退回款", "expenses"),
			metric("expense_advance_balance", "报账预支尚未核销", advanceBalance, cutoff+"；截至日已支付的预支－对应核销－对应退回", "expenses"),
			metric("expense_cash", "报销及预支净付款", financeDashboardSum(v.Period, "advance", "paid", "-returned"), period+"；预支＋补付报销－退回，只计现金一次", "expenses"))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	if req.URL.Query().Get("export") == "1" {
		return writeFinanceDashboardCSV(w, result)
	}
	write(w, http.StatusOK, result)
	return nil
}

// The export is generated from the same completed read snapshot and permission
// projection as the page. No audit or other database writes occur on this GET.
func writeFinanceDashboardCSV(w http.ResponseWriter, result financeDashboardResponse) error {
	var content strings.Builder
	content.WriteString("\ufeff")
	c := csv.NewWriter(&content)
	c.UseCRLF = true
	if err := c.Write([]string{"分类", "指标", "金额（元）", "金额（分）", "统计口径", "开始日期", "结束日期", "查询时间"}); err != nil {
		return err
	}
	for _, group := range result.Groups {
		for _, m := range group.Metrics {
			if err := c.Write([]string{csvSafe(group.Label), csvSafe(m.Label), financeDashboardYuan(m.Cents), m.Cents, csvSafe(m.Basis), result.Range.From, result.Range.To, result.QueriedAt}); err != nil {
				return err
			}
		}
	}
	c.Flush()
	if err := c.Error(); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="finance-summary.csv"`)
	_, err := w.Write([]byte(content.String()))
	return err
}

func financeDashboardYuan(cents string) string {
	v, ok := new(big.Int).SetString(cents, 10)
	if !ok {
		return ""
	}
	negative := v.Sign() < 0
	if negative {
		v.Neg(v)
	}
	whole := new(big.Int).Quo(v, big.NewInt(100)).String()
	fraction := new(big.Int).Mod(v, big.NewInt(100)).String()
	if len(fraction) == 1 {
		fraction = "0" + fraction
	}
	if negative {
		return "-" + whole + "." + fraction
	}
	return whole + "." + fraction
}
