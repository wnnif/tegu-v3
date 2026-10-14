package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type companyReceiptView struct {
	ID            string `json:"id"`
	CompanyID     string `json:"company_id"`
	CompanyName   string `json:"company_name"`
	Amount        int64  `json:"amount_cents"`
	ReceivedAt    string `json:"received_at"`
	Method        string `json:"method"`
	VoucherNo     string `json:"voucher_no"`
	Note          string `json:"note"`
	Status        string `json:"status"`
	Version       int64  `json:"version"`
	CreatedByName string `json:"created_by_name"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	VoidReason    string `json:"void_reason"`
	Allocated     int64  `json:"allocated_cents"`
	Unallocated   int64  `json:"unallocated_cents"`
}

type receiptAllocationView struct {
	ID          string `json:"id"`
	ReceiptID   string `json:"receipt_id"`
	TicketID    string `json:"ticket_id"`
	BatchID     string `json:"batch_id"`
	ProjectID   string `json:"project_id"`
	Address     string `json:"address"`
	Number      string `json:"number"`
	BatchNumber string `json:"batch_number"`
	Amount      int64  `json:"amount_cents"`
	Voided      bool   `json:"voided"`
	CreatedAt   string `json:"created_at"`
}

type receiptHistoryView struct {
	Version   int64           `json:"version"`
	Event     string          `json:"event"`
	Reason    string          `json:"reason"`
	ActorName string          `json:"actor_name"`
	CreatedAt string          `json:"created_at"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

type companyReceiptDetail struct {
	Receipt         companyReceiptView      `json:"receipt"`
	Allocations     []receiptAllocationView `json:"allocations"`
	History         []receiptHistoryView    `json:"history"`
	HistoryTotal    int64                   `json:"history_total"`
	HistoryPage     int                     `json:"history_page"`
	HistoryPageSize int                     `json:"history_page_size"`
}

type customerDiscountView struct {
	ID            string `json:"id"`
	CompanyID     string `json:"company_id"`
	TicketID      string `json:"ticket_id"`
	BatchID       string `json:"batch_id"`
	Address       string `json:"address"`
	Number        string `json:"number"`
	BatchNumber   string `json:"batch_number"`
	Amount        int64  `json:"amount_cents"`
	Reason        string `json:"reason"`
	Version       int64  `json:"version"`
	Voided        bool   `json:"voided"`
	CreatedByName string `json:"created_by_name"`
	CreatedAt     string `json:"created_at"`
	VoidReason    string `json:"void_reason"`
}

type customerDiscountDetail struct {
	Discount        customerDiscountView `json:"discount"`
	History         []receiptHistoryView `json:"history"`
	HistoryTotal    int64                `json:"history_total"`
	HistoryPage     int                  `json:"history_page"`
	HistoryPageSize int                  `json:"history_page_size"`
}

func companyReceiptPermission(u User, sensitive bool) error {
	if !u.can("finance") {
		return fail(403, "无收款管理权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	if sensitive && u.Role != "admin" && u.Role != "operator" {
		return fail(403, "仅管理员可确认减免或更正、作废收款")
	}
	return nil
}

const receiptHeadFrom = ` FROM company_receipts c LEFT JOIN (SELECT receipt_id,SUM(amount_cents) allocated FROM project_ticket_receipts WHERE voided=0 GROUP BY receipt_id) a ON a.receipt_id=c.id `
const receiptHeadSelect = `SELECT c.id,c.company_id,c.company_name,c.amount_cents,DATE_FORMAT(c.received_at,'%Y-%m-%d'),c.method,c.voucher_no,c.note,c.status,c.version,c.created_by_name,DATE_FORMAT(DATE_ADD(c.created_at,INTERVAL 8 HOUR),'%Y-%m-%d %H:%i:%s'),DATE_FORMAT(DATE_ADD(c.updated_at,INTERVAL 8 HOUR),'%Y-%m-%d %H:%i:%s'),c.void_reason,COALESCE(a.allocated,0),IF(c.status='active',c.amount_cents-COALESCE(a.allocated,0),0)`

func scanCompanyReceipt(r interface{ Scan(...any) error }, v *companyReceiptView) error {
	return r.Scan(&v.ID, &v.CompanyID, &v.CompanyName, &v.Amount, &v.ReceivedAt, &v.Method, &v.VoucherNo, &v.Note, &v.Status, &v.Version, &v.CreatedByName, &v.CreatedAt, &v.UpdatedAt, &v.VoidReason, &v.Allocated, &v.Unallocated)
}

func readCompanyReceipt(tx *sql.Tx, receiptID string, lock bool) (companyReceiptView, error) {
	var v companyReceiptView
	if lock {
		var found string
		if err := tx.QueryRow("SELECT id FROM company_receipts WHERE id=? FOR UPDATE", receiptID).Scan(&found); err == sql.ErrNoRows {
			return v, fail(404, "收款记录不存在")
		} else if err != nil {
			return v, err
		}
	}
	err := scanCompanyReceipt(tx.QueryRow(receiptHeadSelect+receiptHeadFrom+" WHERE c.id=?", receiptID), &v)
	if err == sql.ErrNoRows {
		return v, fail(404, "收款记录不存在")
	}
	return v, err
}

func readReceiptAllocations(tx *sql.Tx, receiptID string) ([]receiptAllocationView, error) {
	return scanRows(tx, `SELECT r.id,r.receipt_id,r.ticket_id,r.batch_id,t.project_id,t.address,t.number,COALESCE(b.number,''),r.amount_cents,r.voided,DATE_FORMAT(DATE_ADD(r.created_at,INTERVAL 8 HOUR),'%Y-%m-%d %H:%i:%s') FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id LEFT JOIN ticket_batches b ON b.id=r.batch_id WHERE r.receipt_id=? AND r.voided=0 ORDER BY r.created_at,r.id`, []any{receiptID}, func(r *sql.Rows, v *receiptAllocationView) error {
		return r.Scan(&v.ID, &v.ReceiptID, &v.TicketID, &v.BatchID, &v.ProjectID, &v.Address, &v.Number, &v.BatchNumber, &v.Amount, &v.Voided, &v.CreatedAt)
	})
}

func readMoneyHistory(tx *sql.Tx, table, key, objectID string, page int) ([]receiptHistoryView, int64, error) {
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+key+"=?", objectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := scanRows(tx, "SELECT version,event,reason,actor_name,DATE_FORMAT(DATE_ADD(created_at,INTERVAL 8 HOUR),'%Y-%m-%d %H:%i:%s'),snapshot FROM "+table+" WHERE "+key+"=? ORDER BY version DESC LIMIT 50 OFFSET ?", []any{objectID, (page - 1) * 50}, func(r *sql.Rows, v *receiptHistoryView) error {
		return r.Scan(&v.Version, &v.Event, &v.Reason, &v.ActorName, &v.CreatedAt, &v.Snapshot)
	})
	return rows, total, err
}

func buildCompanyReceiptDetail(tx *sql.Tx, receiptID string, page int) (companyReceiptDetail, error) {
	out := companyReceiptDetail{HistoryPage: page, HistoryPageSize: 50}
	var err error
	if out.Receipt, err = readCompanyReceipt(tx, receiptID, false); err != nil {
		return out, err
	}
	if out.Allocations, err = readReceiptAllocations(tx, receiptID); err != nil {
		return out, err
	}
	out.History, out.HistoryTotal, err = readMoneyHistory(tx, "company_receipt_versions", "receipt_id", receiptID, page)
	return out, err
}

func moneyHistoryPage(r *http.Request) (int, error) {
	if r.URL.Query().Get("history_page") == "" {
		return 1, nil
	}
	v, err := strconv.Atoi(r.URL.Query().Get("history_page"))
	if err != nil || v < 1 || v > 1000000 {
		return 0, fail(400, "历史页码无效")
	}
	return v, nil
}

func businessDateMonthRange(month string) (string, string, error) {
	from, err := time.Parse("2006-01", month)
	if err != nil {
		return "", "", fail(400, "月份无效")
	}
	return from.Format("2006-01-02"), from.AddDate(0, 1, 0).Format("2006-01-02"), nil
}

func (a *App) companyReceiptRead(w http.ResponseWriter, r *http.Request, u User) error {
	if err := companyReceiptPermission(u, false); err != nil {
		return err
	}
	page, err := moneyHistoryPage(r)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out, err := buildCompanyReceiptDetail(tx, r.URL.Query().Get("id"), page)
	if err == nil {
		write(w, 200, out)
	}
	return err
}

func (a *App) companyReceiptsList(w http.ResponseWriter, r *http.Request, u User) error {
	if err := companyReceiptPermission(u, false); err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	where := " WHERE 1=1"
	args := []any{}
	if company := q.Get("company_id"); company != "" {
		where += " AND c.company_id=?"
		args = append(args, company)
	}
	if status := q.Get("status"); status != "" {
		if status != "active" && status != "voided" {
			return fail(400, "收款状态无效")
		}
		where += " AND c.status=?"
		args = append(args, status)
	}
	if q.Get("unallocated") == "1" {
		where += " AND c.status='active' AND c.amount_cents>COALESCE(a.allocated,0)"
	}
	if month := q.Get("month"); month != "" {
		from, to, e := businessDateMonthRange(month)
		if e != nil {
			return e
		}
		where += " AND c.received_at>=? AND c.received_at<?"
		args = append(args, from, to)
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		where += " AND (INSTR(c.company_name,?)>0 OR INSTR(c.voucher_no,?)>0 OR INSTR(c.note,?)>0)"
		args = append(args, search, search, search)
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*)"+receiptHeadFrom+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows(tx, receiptHeadSelect+receiptHeadFrom+where+" ORDER BY c.received_at DESC,c.created_at DESC,c.id DESC LIMIT 50 OFFSET ?", append(args, (page-1)*50), func(r *sql.Rows, v *companyReceiptView) error { return scanCompanyReceipt(r, v) })
	if err == nil {
		write(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "page_size": 50})
	}
	return err
}

const discountReadSelect = `SELECT d.id,d.company_id,d.ticket_id,d.batch_id,t.address,t.number,COALESCE(b.number,''),d.amount_cents,d.reason,d.version,d.voided,d.created_by_name,DATE_FORMAT(DATE_ADD(d.created_at,INTERVAL 8 HOUR),'%Y-%m-%d %H:%i:%s'),d.void_reason FROM customer_discounts d JOIN project_tickets t ON t.id=d.ticket_id LEFT JOIN ticket_batches b ON b.id=d.batch_id `

func scanCustomerDiscount(r interface{ Scan(...any) error }, v *customerDiscountView) error {
	return r.Scan(&v.ID, &v.CompanyID, &v.TicketID, &v.BatchID, &v.Address, &v.Number, &v.BatchNumber, &v.Amount, &v.Reason, &v.Version, &v.Voided, &v.CreatedByName, &v.CreatedAt, &v.VoidReason)
}

func readCustomerDiscount(tx *sql.Tx, discountID string, lock bool) (customerDiscountView, error) {
	var v customerDiscountView
	if lock {
		var found string
		if err := tx.QueryRow("SELECT id FROM customer_discounts WHERE id=? FOR UPDATE", discountID).Scan(&found); err == sql.ErrNoRows {
			return v, fail(404, "客户减免记录不存在")
		} else if err != nil {
			return v, err
		}
	}
	err := scanCustomerDiscount(tx.QueryRow(discountReadSelect+" WHERE d.id=?", discountID), &v)
	if err == sql.ErrNoRows {
		return v, fail(404, "客户减免记录不存在")
	}
	return v, err
}

func buildCustomerDiscountDetail(tx *sql.Tx, discountID string, page int) (customerDiscountDetail, error) {
	out := customerDiscountDetail{HistoryPage: page, HistoryPageSize: 50}
	var err error
	if out.Discount, err = readCustomerDiscount(tx, discountID, false); err != nil {
		return out, err
	}
	out.History, out.HistoryTotal, err = readMoneyHistory(tx, "customer_discount_versions", "discount_id", discountID, page)
	return out, err
}

func (a *App) customerDiscountRead(w http.ResponseWriter, r *http.Request, u User) error {
	if err := companyReceiptPermission(u, false); err != nil {
		return err
	}
	page, err := moneyHistoryPage(r)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out, err := buildCustomerDiscountDetail(tx, r.URL.Query().Get("id"), page)
	if err == nil {
		write(w, 200, out)
	}
	return err
}

func (a *App) customerDiscountsList(w http.ResponseWriter, r *http.Request, u User) error {
	if err := companyReceiptPermission(u, false); err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	where := " WHERE 1=1"
	args := []any{}
	if v := q.Get("company_id"); v != "" {
		where += " AND d.company_id=?"
		args = append(args, v)
	}
	if v := q.Get("project_id"); v != "" {
		where += " AND t.project_id=?"
		args = append(args, v)
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*) FROM customer_discounts d JOIN project_tickets t ON t.id=d.ticket_id"+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows(tx, discountReadSelect+where+" ORDER BY d.created_at DESC,d.id DESC LIMIT 50 OFFSET ?", append(args, (page-1)*50), func(r *sql.Rows, v *customerDiscountView) error { return scanCustomerDiscount(r, v) })
	if err == nil {
		write(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "page_size": 50})
	}
	return err
}

type receiptTargetBatch struct {
	ID        string `json:"id"`
	Number    string `json:"number"`
	Total     int64  `json:"total_cents"`
	Available int64  `json:"available_cents"`
}
type receiptTargetView struct {
	ID          string               `json:"id"`
	ProjectID   string               `json:"project_id"`
	CompanyID   string               `json:"company_id"`
	Number      string               `json:"number"`
	Address     string               `json:"address"`
	Total       int64                `json:"total_cents"`
	Allocated   int64                `json:"allocated_cents"`
	Discount    int64                `json:"discount_cents"`
	Outstanding int64                `json:"outstanding_cents"`
	Batches     []receiptTargetBatch `json:"batches"`
}

func (a *App) receiptTargetsRead(w http.ResponseWriter, r *http.Request, u User) error {
	if err := companyReceiptPermission(u, false); err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	company := q.Get("company_id")
	if company == "" {
		return fail(400, "请先选择付款公司")
	}
	where := " WHERE t.company_id=?"
	args := []any{company}
	if project := q.Get("project_id"); project != "" {
		where += " AND t.project_id=?"
		args = append(args, project)
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRow("SELECT COUNT(*) FROM project_tickets t"+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := scanRows(tx, `SELECT t.id,t.project_id,t.company_id,t.number,t.address,t.total_cents,COALESCE(a.amount,0),COALESCE(d.amount,0),t.total_cents-COALESCE(a.amount,0)-COALESCE(d.amount,0) FROM project_tickets t LEFT JOIN (SELECT ticket_id,SUM(amount_cents) amount FROM project_ticket_receipts WHERE voided=0 GROUP BY ticket_id) a ON a.ticket_id=t.id LEFT JOIN (SELECT ticket_id,SUM(amount_cents) amount FROM customer_discounts WHERE voided=0 GROUP BY ticket_id) d ON d.ticket_id=t.id`+where+" ORDER BY t.updated_at DESC,t.id DESC LIMIT 50 OFFSET ?", append(args, (page-1)*50), func(r *sql.Rows, v *receiptTargetView) error {
		return r.Scan(&v.ID, &v.ProjectID, &v.CompanyID, &v.Number, &v.Address, &v.Total, &v.Allocated, &v.Discount, &v.Outstanding)
	})
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Batches, err = scanRows(tx, `SELECT b.id,b.number,SUM(i.total_cents),SUM(i.total_cents)-COALESCE((SELECT SUM(r.amount_cents) FROM project_ticket_receipts r WHERE r.ticket_id=? AND r.batch_id=b.id AND r.voided=0),0)-COALESCE((SELECT SUM(d.amount_cents) FROM customer_discounts d WHERE d.ticket_id=? AND d.batch_id=b.id AND d.voided=0),0) FROM ticket_batch_items i JOIN ticket_batches b ON b.id=i.batch_id AND b.status='delivered' WHERE i.source_project_ticket_id=? GROUP BY b.id,b.number ORDER BY b.id`, []any{rows[i].ID, rows[i].ID, rows[i].ID}, func(r *sql.Rows, v *receiptTargetBatch) error {
			return r.Scan(&v.ID, &v.Number, &v.Total, &v.Available)
		})
		if err != nil {
			return err
		}
	}
	write(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "page_size": 50})
	return nil
}
