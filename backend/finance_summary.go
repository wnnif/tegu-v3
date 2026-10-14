package main

import (
	"database/sql"
	"encoding/csv"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type wageWorkerSummary struct {
	WorkerID   string `json:"worker_id"`
	WorkerName string `json:"worker_name"`
	OrderCount int64  `json:"order_count"`
	Wage       int64  `json:"wage_cents"`
	Paid       int64  `json:"paid_cents"`
	Unpaid     int64  `json:"unpaid_cents"`
}

type financeCompanySummary struct {
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	TicketCount int64  `json:"ticket_count"`
	Total       int64  `json:"total_cents,string"`
	Received    int64  `json:"received_cents,string"`
	Outstanding int64  `json:"outstanding_cents,string"`
	Delivered   int64  `json:"delivered_cents,string"`
	Undelivered int64  `json:"undelivered_cents,string"`
	Allocated   int64  `json:"allocated_cents,string"`
	Unallocated int64  `json:"unallocated_cents,string"`
	Discount    int64  `json:"discount_cents,string"`
	Unsettled   int64  `json:"unsettled_cents,string"`
	Advance     int64  `json:"advance_cents,string"`
}

const wageWorkerNameSQL = "COALESCE(NULLIF(w.name,''),ww.worker_name)"
const financeCompanyNameSQL = "COALESCE(NULLIF(c.name,''),t.company_name)"

func wageWorkerSummaryQuery(q url.Values) (string, []any, string, []any, error) {
	if !contains([]string{"", "0", "1"}, q.Get("wage_paid")) {
		return "", nil, "", nil, fail(400, "工资发放状态无效")
	}
	filters := url.Values{}
	for _, k := range []string{"month", "worker_id", "company_id", "wage_paid"} {
		filters.Set(k, q.Get(k))
	}
	filters.Set("all_months", "1")
	where, args, err := workerWageFilter(filters)
	if err != nil {
		return "", nil, "", nil, err
	}
	scope, scopeArgs, err := workerWageOrderFilter(filters)
	if err != nil {
		return "", nil, "", nil, err
	}
	// Validate the entire selected order scope before name/status grouping, so a
	// damaged partial account never disappears behind a convenient name filter.
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where += " AND INSTR(" + wageWorkerNameSQL + ",?)>0"
		args = append(args, search)
	}
	grouped := "SELECT ww.worker_id,MAX(" + wageWorkerNameSQL + ") worker_name,COUNT(DISTINCT o.id) order_count,SUM(ww.wage_cents) wage_cents,SUM(IF(ww.wage_paid=1,ww.wage_cents,0)) paid_cents,SUM(IF(ww.wage_paid=0,ww.wage_cents,0)) unpaid_cents FROM " + workerWageSource + " ww JOIN orders o ON o.id=ww.order_id LEFT JOIN workers w ON w.id=ww.worker_id WHERE " + where + " GROUP BY ww.worker_id"
	return grouped, args, scope, scopeArgs, nil
}

// Company balances are lifetime balances. A receipt-month filter belongs to
// the receipt list; filtering only one side of a balance invents false debt.
func financeCompanySummaryQuery(q url.Values) (string, []any, error) {
	if q.Get("month") != "" {
		return "", nil, fail(400, "公司欠款按累计全部月份核对；到账月份请在收款列表筛选")
	}
	state := q.Get("status")
	if !contains([]string{"", "欠款", "结清", "预收"}, state) {
		return "", nil, fail(400, "公司欠款状态无效")
	}
	where := " WHERE 1=1"
	args := []any{}
	if id := q.Get("company_id"); id != "" {
		where += " AND c.id=?"
		args = append(args, id)
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where += " AND INSTR(c.name,?)>0"
		args = append(args, search)
	}
	query := `SELECT c.id company_id,c.name company_name,COALESCE(f.ticket_count,0)+COALESCE(l.order_count,0) ticket_count,
 (COALESCE(f.total,0)+COALESCE(l.total,0)) total_cents,(COALESCE(r.received,0)+COALESCE(l.received,0)) received_cents,
 GREATEST((COALESCE(f.total,0)+COALESCE(l.total,0))-COALESCE(f.discount,0)-(COALESCE(r.received,0)+COALESCE(l.received,0)),0) outstanding_cents,
 (COALESCE(f.delivered,0)+COALESCE(l.delivered,0)) delivered_cents,(COALESCE(f.total,0)+COALESCE(l.total,0))-(COALESCE(f.delivered,0)+COALESCE(l.delivered,0)) undelivered_cents,
 (COALESCE(f.allocated,0)+COALESCE(l.received,0)) allocated_cents,(COALESCE(r.received,0)+COALESCE(l.received,0))-(COALESCE(f.allocated,0)+COALESCE(l.received,0)) unallocated_cents,
 COALESCE(f.discount,0) discount_cents,(COALESCE(f.total,0)+COALESCE(l.total,0))-COALESCE(f.discount,0)-(COALESCE(f.allocated,0)+COALESCE(l.received,0)) unsettled_cents,
 GREATEST((COALESCE(r.received,0)+COALESCE(l.received,0))+COALESCE(f.discount,0)-(COALESCE(f.total,0)+COALESCE(l.total,0)),0) advance_cents
 FROM companies c LEFT JOIN (
 SELECT t.company_id,COUNT(*) ticket_count,SUM(t.total_cents) total,SUM(COALESCE(tr.received,0)) allocated,
 SUM(COALESCE(td.discount,0)) discount,SUM(` + ticketDeliveredCentsSQL + `) delivered` + ticketFinanceFrom + ` GROUP BY t.company_id
 ) f ON f.company_id=c.id LEFT JOIN (
 SELECT company_id,SUM(amount_cents) received FROM (
 SELECT company_id,amount_cents FROM company_receipts WHERE status='active'
 UNION ALL SELECT t.company_id,r.amount_cents FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id WHERE r.voided=0 AND r.receipt_id=''
 ) cash GROUP BY company_id
 ) r ON r.company_id=c.id LEFT JOIN (
 SELECT o.company_id,COUNT(*) order_count,SUM(o.total_cents) total,
 SUM(IF(o.status='已收款' AND pr.received IS NULL,o.total_cents,COALESCE(pr.received,0))) received,
 SUM(IF(o.status IN ('已交单','部分收款','已收款'),o.total_cents,0)) delivered
 FROM orders o LEFT JOIN(SELECT order_id,SUM(amount_cents) received FROM payment_receipts WHERE voided=0 GROUP BY order_id) pr ON pr.order_id=o.id
 WHERE o.ticket_managed=0 AND o.status<>'已取消' GROUP BY o.company_id
 ) l ON l.company_id=c.id` + where
	if state != "" {
		predicate := "outstanding_cents>0"
		if state == "结清" {
			predicate = "outstanding_cents=0 AND advance_cents=0"
		}
		if state == "预收" {
			predicate = "advance_cents>0"
		}
		query = "SELECT * FROM (" + query + ") balances WHERE " + predicate
	}
	return query, args, nil
}

func (a *App) listWageWorkers(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("wages") {
		return fail(403, "无工资权限")
	}
	return fail(409, "旧工资余额汇总已停用，请改用工资管理查看累计工资、实际发放和未发余额")
}

func (a *App) listFinanceCompanies(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("finance") {
		return fail(403, "无应收款权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	grouped, args, err := financeCompanySummaryQuery(q)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int64
	var totals financeCompanySummary
	from := " FROM (" + grouped + ") s"
	if err = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(total_cents),0),COALESCE(SUM(received_cents),0),COALESCE(SUM(outstanding_cents),0),COALESCE(SUM(delivered_cents),0),COALESCE(SUM(undelivered_cents),0),COALESCE(SUM(allocated_cents),0),COALESCE(SUM(unallocated_cents),0),COALESCE(SUM(discount_cents),0),COALESCE(SUM(unsettled_cents),0),COALESCE(SUM(advance_cents),0)"+from, args...).Scan(&count, &totals.Total, &totals.Received, &totals.Outstanding, &totals.Delivered, &totals.Undelivered, &totals.Allocated, &totals.Unallocated, &totals.Discount, &totals.Unsettled, &totals.Advance); err != nil {
		return err
	}
	query := "SELECT company_id,company_name,ticket_count,total_cents,received_cents,outstanding_cents,delivered_cents,undelivered_cents,allocated_cents,unallocated_cents,discount_cents,unsettled_cents,advance_cents" + from + " ORDER BY outstanding_cents DESC,company_name,company_id"
	if q.Get("export") == "1" {
		return exportPartySummary(tx, w, r, "finance-companies", []string{"公司ID", "公司", "收费来源票数", "累计收费元", "实际到账元", "公司实际待收元", "已交票元", "尚未交票元", "已分配元", "未分配元", "客户减免元", "工地尚未核销元", "公司预收余额元"}, query, args, []bool{false, false, false, true, true, true, true, true, true, true, true, true, true})
	}
	rows, err := scanRows[financeCompanySummary](tx, query+" LIMIT 100 OFFSET ?", append(append([]any{}, args...), (page-1)*100), func(r *sql.Rows, v *financeCompanySummary) error {
		return r.Scan(&v.CompanyID, &v.CompanyName, &v.TicketCount, &v.Total, &v.Received, &v.Outstanding, &v.Delivered, &v.Undelivered, &v.Allocated, &v.Unallocated, &v.Discount, &v.Unsettled, &v.Advance)
	})
	if err != nil {
		return err
	}
	write(w, 200, map[string]any{"rows": rows, "total": count, "page": page, "page_size": 100, "total_cents": strconv.FormatInt(totals.Total, 10), "received_cents": strconv.FormatInt(totals.Received, 10), "outstanding_cents": strconv.FormatInt(totals.Outstanding, 10), "delivered_cents": strconv.FormatInt(totals.Delivered, 10), "undelivered_cents": strconv.FormatInt(totals.Undelivered, 10), "allocated_cents": strconv.FormatInt(totals.Allocated, 10), "unallocated_cents": strconv.FormatInt(totals.Unallocated, 10), "discount_cents": strconv.FormatInt(totals.Discount, 10), "unsettled_cents": strconv.FormatInt(totals.Unsettled, 10), "advance_cents": strconv.FormatInt(totals.Advance, 10), "balance_scope": "累计全部月份"})
	return nil
}

// Spool all matching groups under the same read snapshot before sending headers.
// SQL, overflow or disk errors must return an error, never a partial success CSV.
func exportPartySummary(tx *sql.Tx, w http.ResponseWriter, r *http.Request, name string, headers []string, query string, args []any, money []bool) error {
	rows, err := tx.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	f, err := os.CreateTemp("", "tegu-party-summary-*.csv")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write([]byte("\ufeff")); err != nil {
		return err
	}
	cw := csv.NewWriter(f)
	cw.UseCRLF = true
	if err = cw.Write(headers); err != nil {
		return err
	}
	values := make([]string, len(headers))
	dest := make([]any, len(headers))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err = rows.Scan(dest...); err != nil {
			return err
		}
		line := make([]string, len(headers))
		for i, v := range values {
			if money[i] {
				cents, e := strconv.ParseInt(v, 10, 64)
				if e != nil {
					return e
				}
				line[i] = scaledDecimal(cents, 2)
			} else {
				line[i] = csvSafe(v)
			}
		}
		if err = cw.Write(line); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	cw.Flush()
	if err = cw.Error(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name+".csv")
	http.ServeContent(w, r, name+".csv", stat.ModTime(), f)
	return nil
}
