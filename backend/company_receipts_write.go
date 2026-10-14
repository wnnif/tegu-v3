package main

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

type companyReceiptRequest struct {
	CompanyID  string `json:"company_id"`
	ReceivedAt string `json:"received_at"`
	Amount     int64  `json:"amount_cents"`
	Method     string `json:"method"`
	VoucherNo  string `json:"voucher_no"`
	Note       string `json:"note"`
}
type receiptAllocationInput struct {
	TicketID string `json:"ticket_id"`
	BatchID  string `json:"batch_id"`
	Amount   int64  `json:"amount_cents"`
}
type receiptAllocationRequest struct {
	ReceiptID   string                   `json:"receipt_id"`
	Version     int64                    `json:"version"`
	Reason      string                   `json:"reason"`
	Allocations []receiptAllocationInput `json:"allocations"`
}

func validMoneyReason(reason string) error {
	if strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return fail(400, "请填写1至1000字的操作原因")
	}
	return nil
}
func validateCompanyReceipt(p *companyReceiptRequest) error {
	p.CompanyID, p.Method, p.VoucherNo, p.Note = strings.TrimSpace(p.CompanyID), strings.TrimSpace(p.Method), strings.TrimSpace(p.VoucherNo), strings.TrimSpace(p.Note)
	if p.CompanyID == "" || len(p.CompanyID) > 64 || p.Amount < 1 || p.Amount > maxTicketBatchCents {
		return fail(400, "请选择公司并填写大于0且有效的到账金额")
	}
	if _, err := time.Parse("2006-01-02", p.ReceivedAt); err != nil {
		return fail(400, "实际到账日期无效")
	}
	if p.Method == "" || len([]rune(p.Method)) > 40 || len([]rune(p.VoucherNo)) > 100 || len([]rune(p.Note)) > 1000 {
		return fail(400, "收款方式须填写，方式、凭证或备注超出长度限制")
	}
	return nil
}

func saveCompanyReceiptVersion(tx *sql.Tx, u User, receiptID, event, reason string) error {
	r, err := readCompanyReceipt(tx, receiptID, false)
	if err != nil {
		return err
	}
	allocations, err := readReceiptAllocations(tx, receiptID)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO company_receipt_versions(receipt_id,version,event,reason,snapshot,actor_id,actor_name) VALUES(?,?,?,?,?,?,?)", r.ID, r.Version, event, reason, jsonText(map[string]any{"receipt": r, "allocations": allocations}), u.ID, u.Name)
	return err
}
func saveCustomerDiscountVersion(tx *sql.Tx, u User, discountID, event, reason string) error {
	d, err := readCustomerDiscount(tx, discountID, false)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO customer_discount_versions(discount_id,version,event,reason,snapshot,actor_id,actor_name) VALUES(?,?,?,?,?,?,?)", d.ID, d.Version, event, reason, jsonText(d), u.ID, u.Name)
	return err
}

func (a *App) createCompanyReceipt(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := companyReceiptPermission(u, false); err != nil {
		return nil, err
	}
	var p companyReceiptRequest
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	if err := validateCompanyReceipt(&p); err != nil {
		return nil, err
	}
	var name string
	if err := tx.QueryRow("SELECT name FROM companies WHERE id=?", p.CompanyID).Scan(&name); err == sql.ErrNoRows {
		return nil, fail(404, "付款公司不存在")
	} else if err != nil {
		return nil, err
	}
	receiptID := id()
	_, err := tx.Exec(`INSERT INTO company_receipts(id,company_id,company_name,amount_cents,received_at,method,voucher_no,note,created_by,created_by_name) VALUES(?,?,?,?,?,?,?,?,?,?)`, receiptID, p.CompanyID, name, p.Amount, p.ReceivedAt, p.Method, p.VoucherNo, p.Note, u.ID, u.Name)
	if err != nil {
		return nil, err
	}
	if err = saveCompanyReceiptVersion(tx, u, receiptID, "登记实际收款", ""); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "登记公司实际收款", receiptID, map[string]any{"receipt": p, "operation_id": key}); err != nil {
		return nil, err
	}
	return buildCompanyReceiptDetail(tx, receiptID, 1)
}

// Read references before locking, then re-read after the receipt lock. If another
// editor changed the set, reject instead of writing an unlocked worksite.
func lockReceiptProjects(tx *sql.Tx, receiptID string, desired []receiptAllocationInput) (map[string]bool, error) {
	set := map[string]bool{}
	rows, err := tx.Query(`SELECT DISTINCT t.project_id FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id WHERE r.receipt_id=? AND r.voided=0`, receiptID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		set[v] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, d := range desired {
		var project string
		if err = tx.QueryRow("SELECT project_id FROM project_tickets WHERE id=?", d.TicketID).Scan(&project); err == sql.ErrNoRows {
			return nil, fail(404, "分配工地票不存在")
		} else if err != nil {
			return nil, err
		}
		set[project] = true
	}
	for _, project := range sortedKeys(set) {
		var found string
		if err = tx.QueryRow("SELECT id FROM projects WHERE id=? FOR UPDATE", project).Scan(&found); err != nil {
			return nil, err
		}
	}
	return set, nil
}
func assertReceiptProjectLocks(tx *sql.Tx, receiptID string, locked map[string]bool) error {
	rows, err := tx.Query(`SELECT DISTINCT t.project_id FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id WHERE r.receipt_id=? AND r.voided=0`, receiptID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return err
		}
		if !locked[v] {
			return fail(409, "收款分配已变化，请刷新后重新核对")
		}
	}
	return rows.Err()
}

func validateAllocationInputs(p *receiptAllocationRequest) error {
	p.ReceiptID, p.Reason = strings.TrimSpace(p.ReceiptID), strings.TrimSpace(p.Reason)
	if p.ReceiptID == "" || len(p.ReceiptID) > 64 || p.Version < 1 || len(p.Allocations) > 100 {
		return fail(400, "收款版本或分配清单无效，一笔最多分配100处")
	}
	if err := validMoneyReason(p.Reason); err != nil {
		return err
	}
	seen := map[string]bool{}
	var total int64
	for i := range p.Allocations {
		d := &p.Allocations[i]
		d.TicketID, d.BatchID = strings.TrimSpace(d.TicketID), strings.TrimSpace(d.BatchID)
		key := d.TicketID + "/" + d.BatchID
		if d.TicketID == "" || len(d.TicketID) > 64 || len(d.BatchID) > 64 || seen[key] || d.Amount < 1 || d.Amount > maxTicketBatchCents || total > maxTicketBatchCents-d.Amount {
			return fail(400, "分配目标重复或金额无效")
		}
		seen[key] = true
		total += d.Amount
	}
	sort.Slice(p.Allocations, func(i, j int) bool {
		if p.Allocations[i].TicketID != p.Allocations[j].TicketID {
			return p.Allocations[i].TicketID < p.Allocations[j].TicketID
		}
		return p.Allocations[i].BatchID < p.Allocations[j].BatchID
	})
	return nil
}

func ticketMoneyBalance(tx *sql.Tx, ticketID string) (int64, int64, int64, error) {
	var total, allocated, discount int64
	err := tx.QueryRow(`SELECT t.total_cents,COALESCE((SELECT SUM(r.amount_cents) FROM project_ticket_receipts r WHERE r.ticket_id=t.id AND r.voided=0),0),COALESCE((SELECT SUM(d.amount_cents) FROM customer_discounts d WHERE d.ticket_id=t.id AND d.voided=0),0) FROM project_tickets t WHERE t.id=?`, ticketID).Scan(&total, &allocated, &discount)
	return total, allocated, discount, err
}
func batchMoneyBalance(tx *sql.Tx, ticketID, batchID, companyID string) (int64, error) {
	var status, company string
	if err := tx.QueryRow("SELECT company_id,status FROM ticket_batches WHERE id=? FOR UPDATE", batchID).Scan(&company, &status); err == sql.ErrNoRows {
		return 0, fail(404, "交票批次不存在")
	} else if err != nil {
		return 0, err
	}
	if status != "delivered" || company != companyID {
		return 0, fail(409, "只能分配到同公司的有效交票批次")
	}
	var total, allocated, discount int64
	var count int
	if err := tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(total_cents),0) FROM ticket_batch_items WHERE batch_id=? AND source_project_ticket_id=?", batchID, ticketID).Scan(&count, &total); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, fail(400, "该交票批次不包含所选工地收费来源")
	}
	if err := tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM project_ticket_receipts WHERE ticket_id=? AND batch_id=? AND voided=0", ticketID, batchID).Scan(&allocated); err != nil {
		return 0, err
	}
	if err := tx.QueryRow("SELECT COALESCE(SUM(amount_cents),0) FROM customer_discounts WHERE ticket_id=? AND batch_id=? AND voided=0", ticketID, batchID).Scan(&discount); err != nil {
		return 0, err
	}
	return total - allocated - discount, nil
}

func refreshTicketFinancialVersion(tx *sql.Tx, u User, ticketID, event string) error {
	total, allocated, discount, err := ticketMoneyBalance(tx, ticketID)
	if err != nil {
		return err
	}
	var status string
	if err = tx.QueryRow("SELECT status FROM project_tickets WHERE id=? FOR UPDATE", ticketID).Scan(&status); err != nil {
		return err
	}
	if allocated+discount > total {
		return fail(409, "工地已分配款与减免超出有效收费")
	}
	if _, err = tx.Exec("UPDATE project_tickets SET status=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", ticketStatus(total, allocated+discount, status != "草稿"), ticketID); err != nil {
		return err
	}
	return saveProjectTicketVersion(tx, u, ticketID, event)
}

func (a *App) replaceReceiptAllocation(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := companyReceiptPermission(u, false); err != nil {
		return nil, err
	}
	var p receiptAllocationRequest
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	if err := validateAllocationInputs(&p); err != nil {
		return nil, err
	}
	projects, err := lockReceiptProjects(tx, p.ReceiptID, p.Allocations)
	if err != nil {
		return nil, err
	}
	receipt, err := readCompanyReceipt(tx, p.ReceiptID, true)
	if err != nil {
		return nil, err
	}
	if receipt.Status != "active" || receipt.Version != p.Version {
		return nil, fail(409, "收款已变化或作废，请刷新后重新核对")
	}
	if err = assertReceiptProjectLocks(tx, p.ReceiptID, projects); err != nil {
		return nil, err
	}
	before, err := readReceiptAllocations(tx, p.ReceiptID)
	if err != nil {
		return nil, err
	}
	tickets := map[string]bool{}
	for _, d := range before {
		tickets[d.TicketID] = true
	}
	for _, d := range p.Allocations {
		tickets[d.TicketID] = true
	}
	for _, ticketID := range sortedKeys(tickets) {
		var found string
		if err = tx.QueryRow("SELECT id FROM project_tickets WHERE id=? FOR UPDATE", ticketID).Scan(&found); err != nil {
			return nil, err
		}
	}
	var sum int64
	for _, d := range p.Allocations {
		sum += d.Amount
	}
	if sum > receipt.Amount {
		return nil, fail(400, "分配合计不能超过该笔实际到账金额")
	}
	if _, err = tx.Exec("UPDATE project_ticket_receipts SET voided=1 WHERE receipt_id=? AND voided=0", p.ReceiptID); err != nil {
		return nil, err
	}
	for _, d := range p.Allocations {
		var company string
		if err = tx.QueryRow("SELECT company_id FROM project_tickets WHERE id=?", d.TicketID).Scan(&company); err != nil {
			return nil, err
		}
		if company != receipt.CompanyID {
			return nil, fail(400, "不同公司的收款不能混合分配")
		}
		total, allocated, discount, e := ticketMoneyBalance(tx, d.TicketID)
		if e != nil {
			return nil, e
		}
		if d.Amount > total-allocated-discount {
			return nil, fail(400, "分配金额超过工地尚未核销金额")
		}
		if d.BatchID != "" {
			available, e := batchMoneyBalance(tx, d.TicketID, d.BatchID, company)
			if e != nil {
				return nil, e
			}
			if d.Amount > available {
				return nil, fail(400, "分配金额超过该交票批次尚未核销金额")
			}
		}
		_, err = tx.Exec(`INSERT INTO project_ticket_receipts(id,ticket_id,receipt_id,batch_id,amount_cents,received_at,method,voucher_no,note,created_by) VALUES(?,?,?,?,?,?,?,?,?,?)`, id(), d.TicketID, p.ReceiptID, d.BatchID, d.Amount, receipt.ReceivedAt, receipt.Method, receipt.VoucherNo, p.Reason, u.ID)
		if err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec("UPDATE company_receipts SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", p.ReceiptID); err != nil {
		return nil, err
	}
	for _, ticketID := range sortedKeys(tickets) {
		if err = refreshTicketFinancialVersion(tx, u, ticketID, "修改收款分配"); err != nil {
			return nil, err
		}
	}
	if err = saveCompanyReceiptVersion(tx, u, p.ReceiptID, "修改收款分配", p.Reason); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "修改公司收款分配", p.ReceiptID, map[string]any{"before": before, "after": p.Allocations, "reason": p.Reason, "operation_id": key}); err != nil {
		return nil, err
	}
	return buildCompanyReceiptDetail(tx, p.ReceiptID, 1)
}

func resizeReceiptAllocation(tx *sql.Tx, u User, a receiptAllocationView, keep int64, reason string) error {
	if keep < 0 || keep > a.Amount {
		return fail(409, "款项自动调整金额无效")
	}
	if _, err := tx.Exec("UPDATE project_ticket_receipts SET voided=1 WHERE id=? AND voided=0", a.ID); err != nil {
		return err
	}
	if keep > 0 {
		_, err := tx.Exec(`INSERT INTO project_ticket_receipts(id,ticket_id,receipt_id,batch_id,amount_cents,received_at,method,voucher_no,note,created_by) SELECT ?,ticket_id,receipt_id,batch_id,?,received_at,method,voucher_no,?,? FROM project_ticket_receipts WHERE id=?`, id(), keep, reason, u.ID, a.ID)
		if err != nil {
			return err
		}
	}
	return audit(tx, u, "调整收款分配", a.ReceiptID, map[string]any{"allocation_id": a.ID, "ticket_id": a.TicketID, "before_cents": a.Amount, "after_cents": keep, "released_cents": a.Amount - keep, "reason": reason})
}

func (a *App) companyReceiptAction(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := companyReceiptPermission(u, true); err != nil {
		return nil, err
	}
	var p struct {
		companyReceiptRequest
		ID      string `json:"id"`
		Version int64  `json:"version"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	}
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	p.Reason = strings.TrimSpace(p.Reason)
	if p.ID == "" || len(p.ID) > 64 || p.Version < 1 || (p.Action != "correct" && p.Action != "void") {
		return nil, fail(400, "收款纠错信息无效")
	}
	if err := validMoneyReason(p.Reason); err != nil {
		return nil, err
	}
	if p.Action == "correct" {
		if err := validateCompanyReceipt(&p.companyReceiptRequest); err != nil {
			return nil, err
		}
	}
	projects, err := lockReceiptProjects(tx, p.ID, nil)
	if err != nil {
		return nil, err
	}
	receipt, err := readCompanyReceipt(tx, p.ID, true)
	if err != nil {
		return nil, err
	}
	if receipt.Status != "active" || receipt.Version != p.Version {
		return nil, fail(409, "收款已变化或作废，请刷新后重新核对")
	}
	if err = assertReceiptProjectLocks(tx, p.ID, projects); err != nil {
		return nil, err
	}
	allocations, err := readReceiptAllocations(tx, p.ID)
	if err != nil {
		return nil, err
	}
	tickets := map[string]bool{}
	for _, d := range allocations {
		tickets[d.TicketID] = true
	}
	for _, ticketID := range sortedKeys(tickets) {
		var found string
		if err = tx.QueryRow("SELECT id FROM project_tickets WHERE id=? FOR UPDATE", ticketID).Scan(&found); err != nil {
			return nil, err
		}
	}
	var name string
	desired := int64(0)
	if p.Action == "correct" {
		if err = tx.QueryRow("SELECT name FROM companies WHERE id=?", p.CompanyID).Scan(&name); err == sql.ErrNoRows {
			return nil, fail(404, "更正后的付款公司不存在")
		} else if err != nil {
			return nil, err
		}
		if p.CompanyID == receipt.CompanyID {
			desired = p.Amount
		}
	}
	allocated := receipt.Allocated
	for i := len(allocations) - 1; i >= 0 && allocated > desired; i-- {
		d := allocations[i]
		release := allocated - desired
		if release > d.Amount {
			release = d.Amount
		}
		if err = resizeReceiptAllocation(tx, u, d, d.Amount-release, p.Reason); err != nil {
			return nil, err
		}
		allocated -= release
	}
	if p.Action == "void" {
		_, err = tx.Exec("UPDATE company_receipts SET status='voided',void_reason=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", p.Reason, p.ID)
	} else {
		_, err = tx.Exec(`UPDATE company_receipts SET company_id=?,company_name=?,amount_cents=?,received_at=?,method=?,voucher_no=?,note=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?`, p.CompanyID, name, p.Amount, p.ReceivedAt, p.Method, p.VoucherNo, p.Note, p.ID)
	}
	if err != nil {
		return nil, err
	}
	for _, ticketID := range sortedKeys(tickets) {
		if err = refreshTicketFinancialVersion(tx, u, ticketID, "收款纠错调整分配"); err != nil {
			return nil, err
		}
	}
	event := "更正实际收款"
	if p.Action == "void" {
		event = "作废误登记收款"
	}
	if err = saveCompanyReceiptVersion(tx, u, p.ID, event, p.Reason); err != nil {
		return nil, err
	}
	if err = audit(tx, u, event, p.ID, map[string]any{"before": receipt, "request": p, "operation_id": key}); err != nil {
		return nil, err
	}
	return buildCompanyReceiptDetail(tx, p.ID, 1)
}

func (a *App) createCustomerDiscount(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := companyReceiptPermission(u, true); err != nil {
		return nil, err
	}
	var p struct {
		CompanyID string `json:"company_id"`
		TicketID  string `json:"ticket_id"`
		BatchID   string `json:"batch_id"`
		Amount    int64  `json:"amount_cents"`
		Reason    string `json:"reason"`
	}
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	p.Reason = strings.TrimSpace(p.Reason)
	if err := validMoneyReason(p.Reason); err != nil {
		return nil, err
	}
	if p.CompanyID == "" || p.TicketID == "" || len(p.CompanyID) > 64 || len(p.TicketID) > 64 || len(p.BatchID) > 64 || p.Amount < 1 || p.Amount > maxTicketBatchCents {
		return nil, fail(400, "客户减免金额或目标无效")
	}
	_, err := lockReceiptProjects(tx, "", []receiptAllocationInput{{TicketID: p.TicketID}})
	if err != nil {
		return nil, err
	}
	var company string
	if err = tx.QueryRow("SELECT company_id FROM project_tickets WHERE id=? FOR UPDATE", p.TicketID).Scan(&company); err != nil {
		return nil, err
	}
	if company != p.CompanyID {
		return nil, fail(400, "减免须属于同一公司工地")
	}
	total, allocated, discount, err := ticketMoneyBalance(tx, p.TicketID)
	if err != nil {
		return nil, err
	}
	if p.Amount > total-allocated-discount {
		return nil, fail(400, "减免金额不能超过工地尚未核销收费，已免收内容不能再次减免")
	}
	if p.BatchID != "" {
		available, e := batchMoneyBalance(tx, p.TicketID, p.BatchID, company)
		if e != nil {
			return nil, e
		}
		if p.Amount > available {
			return nil, fail(400, "减免金额超过交票批次尚未核销金额")
		}
	}
	discountID := id()
	_, err = tx.Exec(`INSERT INTO customer_discounts(id,company_id,ticket_id,batch_id,amount_cents,reason,created_by,created_by_name) VALUES(?,?,?,?,?,?,?,?)`, discountID, p.CompanyID, p.TicketID, p.BatchID, p.Amount, p.Reason, u.ID, u.Name)
	if err != nil {
		return nil, err
	}
	if err = refreshTicketFinancialVersion(tx, u, p.TicketID, "老板确认客户减免"); err != nil {
		return nil, err
	}
	if err = saveCustomerDiscountVersion(tx, u, discountID, "老板确认客户减免", p.Reason); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "老板确认客户减免", discountID, map[string]any{"request": p, "operation_id": key}); err != nil {
		return nil, err
	}
	return buildCustomerDiscountDetail(tx, discountID, 1)
}

func (a *App) customerDiscountAction(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := companyReceiptPermission(u, true); err != nil {
		return nil, err
	}
	var p struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	}
	if err := decode(b, &p); err != nil {
		return nil, err
	}
	p.Reason = strings.TrimSpace(p.Reason)
	if p.ID == "" || len(p.ID) > 64 || p.Version < 1 || p.Action != "void" {
		return nil, fail(400, "减免作废信息无效")
	}
	if err := validMoneyReason(p.Reason); err != nil {
		return nil, err
	}
	d, err := readCustomerDiscount(tx, p.ID, false)
	if err != nil {
		return nil, err
	}
	if _, err = lockReceiptProjects(tx, "", []receiptAllocationInput{{TicketID: d.TicketID}}); err != nil {
		return nil, err
	}
	d, err = readCustomerDiscount(tx, p.ID, true)
	if err != nil {
		return nil, err
	}
	if d.Voided || d.Version != p.Version {
		return nil, fail(409, "客户减免已变化或作废，请刷新核对")
	}
	if _, err = tx.Exec("UPDATE customer_discounts SET voided=1,void_reason=?,version=version+1 WHERE id=?", p.Reason, p.ID); err != nil {
		return nil, err
	}
	if err = refreshTicketFinancialVersion(tx, u, d.TicketID, "撤销客户减免"); err != nil {
		return nil, err
	}
	if err = saveCustomerDiscountVersion(tx, u, p.ID, "撤销客户减免", p.Reason); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "撤销客户减免", p.ID, map[string]any{"before": d, "reason": p.Reason, "operation_id": key}); err != nil {
		return nil, err
	}
	return buildCustomerDiscountDetail(tx, p.ID, 1)
}
