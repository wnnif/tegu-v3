package main

import (
	"database/sql"
	"encoding/csv"
	"net/http"
	"net/url"
	"strings"
)

type ticketFinanceRow struct {
	TicketMode  string `json:"ticket_mode_snapshot"`
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	ProjectName string `json:"project_name"`
	Address     string `json:"address"`
	Number      string `json:"number"`
	Kind        string `json:"kind"`
	Slot        int    `json:"slot"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
	Total       int64  `json:"total_cents"`
	Received    int64  `json:"received_cents"`
	Outstanding int64  `json:"outstanding_cents"`
	Date        string `json:"work_date"`
	DeliveredAt string `json:"delivered_at"`
	Recipient   string `json:"recipient"`
	TicketNo    string `json:"ticket_no"`
	Discount    int64  `json:"discount_cents"`
	Delivered   int64  `json:"delivered_cents"`
}

// Active actual-row claims define current paper delivery; old delivered
// source tickets remain delivered for compatibility. Batch snapshots add no debt.
const ticketDeliveryRowsSQL = `SELECT i.source_project_ticket_id ticket_id,SUM(i.total_cents) delivered,COUNT(*) delivered_rows
 ,SUM(CASE WHEN i.item_kind='package' THEN 1 ELSE 0 END) package_rows
 FROM ticket_batch_items i JOIN ticket_batches b ON b.id=i.batch_id AND b.status='delivered'
 LEFT JOIN orders o ON i.item_kind IN('line','sale') AND o.id=i.order_id AND o.status<>'已取消'
 LEFT JOIN order_lines l ON i.item_kind='line' AND l.id=i.item_id AND l.ticket_batch_id=i.batch_id
 LEFT JOIN order_sales s ON i.item_kind='sale' AND s.id=i.item_id AND s.ticket_batch_id=i.batch_id
 WHERE i.item_kind='package' OR (o.id IS NOT NULL AND (l.id IS NOT NULL OR s.id IS NOT NULL)) GROUP BY i.source_project_ticket_id`
const ticketDeliveredCentsSQL = `IF(t.delivered_at IS NOT NULL,t.total_cents,LEAST(t.total_cents,COALESCE(tb.delivered,0)+IF(COALESCE(tb.delivered_rows,0)>0 AND COALESCE(tb.package_rows,0)=0,COALESCE(pp.price_cents,0),0)))`
const ticketFinanceFrom = ` FROM project_tickets t JOIN projects p ON p.id=t.project_id JOIN companies c ON c.id=t.company_id
 LEFT JOIN (SELECT ticket_id,SUM(amount_cents) received FROM project_ticket_receipts WHERE voided=0 GROUP BY ticket_id) tr ON tr.ticket_id=t.id
 LEFT JOIN (SELECT ticket_id,SUM(amount_cents) discount FROM customer_discounts WHERE voided=0 GROUP BY ticket_id) td ON td.ticket_id=t.id
 LEFT JOIN (` + ticketDeliveryRowsSQL + `) tb ON tb.ticket_id=t.id
 LEFT JOIN project_packages pp ON pp.charged_ticket_id=t.id `
const ticketFinanceSelect = `SELECT t.id,t.project_id,t.company_id,t.company_name,t.project_name,t.address,t.number,t.kind,COALESCE(t.slot,0),t.status,t.version,t.total_cents,COALESCE(tr.received,0),t.total_cents-COALESCE(tr.received,0)-COALESCE(td.discount,0),DATE_FORMAT(t.created_at,'%Y-%m-%d'),COALESCE(DATE_FORMAT(t.delivered_at,'%Y-%m-%d'),''),t.recipient,t.ticket_no,p.ticket_mode_snapshot,COALESCE(td.discount,0),` + ticketDeliveredCentsSQL

func projectTicketLabel(kind, mode string, slot int) string {
	switch kind {
	case "summary":
		return "全工地累计"
	case "append":
		return "追加票"
	}
	if mode == "single" {
		return "统一票"
	}
	if mode == "split" {
		if slot == 2 {
			return "第二票"
		}
		return "第一票"
	}
	return "正常票"
}

func csvSafe(v string) string {
	t := strings.TrimSpace(v)
	if t != "" && strings.ContainsAny(t[:1], "=+-@") {
		return "'" + v
	}
	return v
}

func addProjectTicketBalances(tx *sql.Tx, projects []projectRecord) error {
	if len(projects) == 0 {
		return nil
	}
	args := []any{}
	for i := range projects {
		projects[i].AllocatedCents = projects[i].ReceivedCents
		projects[i].UnsettledCents = projects[i].OutstandingCents
	}
	positions := map[string]int{}
	for i, p := range projects {
		args = append(args, p.ID)
		positions[p.ID] = i
	}
	rows, err := tx.Query("SELECT t.project_id,COALESCE(SUM(tr.received),0),COALESCE(SUM("+ticketDeliveredCentsSQL+"),0),COALESCE(SUM(td.discount),0),COALESCE(SUM(t.total_cents-COALESCE(tr.received,0)-COALESCE(td.discount,0)),0),COALESCE(SUM(t.total_cents),0)"+ticketFinanceFrom+"WHERE t.project_id IN (?"+strings.Repeat(",?", len(args)-1)+") GROUP BY t.project_id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var allocated, delivered, discount, outstanding, total int64
		if err = rows.Scan(&id, &allocated, &delivered, &discount, &outstanding, &total); err != nil {
			return err
		}
		p := &projects[positions[id]]
		p.ReceivedCents += allocated
		p.AllocatedCents += allocated
		p.DeliveredCents += delivered
		p.DiscountCents += discount
		p.UndeliveredCents += total - delivered
		p.OutstandingCents += outstanding
		p.UnsettledCents += outstanding
		p.DeliveredOutstandingCents += max(delivered-allocated-discount, 0)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	packageRows, err := tx.Query("SELECT project_id,COALESCE(SUM(price_cents),0) FROM project_packages WHERE project_id IN (?"+strings.Repeat(",?", len(args)-1)+") GROUP BY project_id", args...)
	if err != nil {
		return err
	}
	defer packageRows.Close()
	for packageRows.Next() {
		var id string
		var amount int64
		if err = packageRows.Scan(&id, &amount); err != nil {
			return err
		}
		projects[positions[id]].TotalCents += amount
	}
	return packageRows.Err()
}

func ticketFinanceFilter(q url.Values) (string, []any, error) {
	where := " WHERE 1=1"
	args := []any{}
	if company := q.Get("company_id"); company != "" {
		where += " AND t.company_id=?"
		args = append(args, company)
	} else {
		where += " AND 1=0"
	}
	if month := q.Get("month"); month != "" {
		from, to, err := businessMonthRangeUTC(month)
		if err != nil {
			return "", nil, err
		}
		// A ticket can span construction months. Month means its most recent
		// business activity, so later corrections remain visible.
		where += " AND t.updated_at>=? AND t.updated_at<?"
		args = append(args, from, to)
	}
	if state := q.Get("status"); state != "" {
		if !contains([]string{"草稿", "已交票", "部分收款", "已收款"}, state) {
			return "", nil, fail(400, "票据状态无效")
		}
		where += " AND t.status=?"
		args = append(args, state)
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where += " AND (INSTR(t.number,?)>0 OR INSTR(t.project_name,?)>0 OR INSTR(t.address,?)>0)"
		args = append(args, search, search, search)
	}
	return where, args, nil
}

func scanTicketFinance(q rowQuery, sqlText string, args []any) ([]ticketFinanceRow, error) {
	return scanRows[ticketFinanceRow](q, sqlText, args, func(r *sql.Rows, v *ticketFinanceRow) error {
		return r.Scan(&v.ID, &v.ProjectID, &v.CompanyID, &v.CompanyName, &v.ProjectName, &v.Address, &v.Number, &v.Kind, &v.Slot, &v.Status, &v.Version, &v.Total, &v.Received, &v.Outstanding, &v.Date, &v.DeliveredAt, &v.Recipient, &v.TicketNo, &v.TicketMode, &v.Discount, &v.Delivered)
	})
}

func (a *App) listProjectTickets(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("finance") {
		return fail(403, "无应收款权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	page, err := queryPage(r.URL.Query())
	if err != nil {
		return err
	}
	where, args, err := ticketFinanceFilter(r.URL.Query())
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, total, received, discount int64
	if err = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(t.total_cents),0),COALESCE(SUM(tr.received),0),COALESCE(SUM(td.discount),0)"+ticketFinanceFrom+where, args...).Scan(&count, &total, &received, &discount); err != nil {
		return err
	}
	if r.URL.Query().Get("export") == "1" {
		// Read every matching page before emitting headers; no silent 100-row cap.
		all := []ticketFinanceRow{}
		for offset := 0; ; offset += 500 {
			batch, e := scanTicketFinance(tx, ticketFinanceSelect+ticketFinanceFrom+where+" ORDER BY t.updated_at DESC,t.id DESC LIMIT 500 OFFSET ?", append(append([]any{}, args...), offset))
			if e != nil {
				return e
			}
			all = append(all, batch...)
			if len(batch) < 500 {
				break
			}
		}
		var buf strings.Builder
		buf.WriteString("\ufeff")
		cw := csv.NewWriter(&buf)
		cw.Write([]string{"公司", "工地", "地址", "票号", "票类", "状态", "累计收费元", "已分配收款元", "工地尚未核销元", "原交票日期", "原收票人", "客户减免元", "有效已交票元"})
		for _, v := range all {
			kind := projectTicketLabel(v.Kind, v.TicketMode, v.Slot)
			cw.Write([]string{csvSafe(v.CompanyName), csvSafe(v.ProjectName), csvSafe(v.Address), csvSafe(v.Number), kind, v.Status, scaledDecimal(v.Total, 2), scaledDecimal(v.Received, 2), scaledDecimal(v.Outstanding, 2), v.DeliveredAt, csvSafe(v.Recipient), scaledDecimal(v.Discount, 2), scaledDecimal(v.Delivered, 2)})
		}
		cw.Flush()
		if err = cw.Error(); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=project-tickets.csv")
		_, err = w.Write([]byte(buf.String()))
		return err
	}
	rows, err := scanTicketFinance(tx, ticketFinanceSelect+ticketFinanceFrom+where+" ORDER BY t.updated_at DESC,t.id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100))
	if err != nil {
		return err
	}
	write(w, 200, map[string]any{"rows": rows, "total": count, "page": page, "total_cents": total, "received_cents": received, "outstanding_cents": total - received - discount, "discount_cents": discount})
	return nil
}

func (a *App) projectTicketBatch(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if !u.can("finance") {
		return nil, fail(403, "无应收款权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return nil, err
	}
	var p struct {
		Action string `json:"action"`
	}
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	if p.Action == "deliver" {
		return nil, fail(409, "请在交票管理中选择实际明细并确认纸票交付")
	}
	return nil, fail(409, "请在收款管理中登记真实到账，再分配款项；此票内批量收款入口已停用")
}
