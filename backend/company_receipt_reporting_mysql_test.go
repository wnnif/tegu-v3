package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func receiptReportingSummary(t *testing.T, f priceMySQLFixture, company string) financeCompanySummary {
	t.Helper()
	w := f.request(t, f.admin, "GET", "/api/finance-companies?company_id="+company, "", nil, 200)
	var page struct {
		Rows        []financeCompanySummary `json:"rows"`
		Amount      string                  `json:"total_cents"`
		Received    string                  `json:"received_cents"`
		Outstanding string                  `json:"outstanding_cents"`
		Delivered   string                  `json:"delivered_cents"`
		Undelivered string                  `json:"undelivered_cents"`
		Allocated   string                  `json:"allocated_cents"`
		Unallocated string                  `json:"unallocated_cents"`
		Discount    string                  `json:"discount_cents"`
		Unsettled   string                  `json:"unsettled_cents"`
		Advance     string                  `json:"advance_cents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Rows) != 1 {
		t.Fatal("company lifetime balances", w.Body.String(), err)
	}
	row := page.Rows[0]
	if !strings.Contains(w.Body.String(), "累计全部月份") {
		t.Fatal("company row and filtered total disagree", w.Body.String())
	}
	for _, field := range []struct {
		name, value string
		amount      int64
	}{
		{"total_cents", page.Amount, row.Total}, {"received_cents", page.Received, row.Received},
		{"outstanding_cents", page.Outstanding, row.Outstanding}, {"delivered_cents", page.Delivered, row.Delivered},
		{"undelivered_cents", page.Undelivered, row.Undelivered}, {"allocated_cents", page.Allocated, row.Allocated},
		{"unallocated_cents", page.Unallocated, row.Unallocated}, {"discount_cents", page.Discount, row.Discount},
		{"unsettled_cents", page.Unsettled, row.Unsettled}, {"advance_cents", page.Advance, row.Advance},
	} {
		if field.value != strconv.FormatInt(field.amount, 10) {
			t.Fatal("company header differs from filtered rows", field.name, field.value, field.amount)
		}
	}
	for _, private := range []string{"wage_cents", "labor_cents", "delivery_wage_cents"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("company cash report leaked wages", private)
		}
	}
	return row
}

func receiptReportingCreate(t *testing.T, f priceMySQLFixture, company, date string, amount int64) companyReceiptDetail {
	t.Helper()
	w := f.request(t, f.admin, "POST", "/api/company-receipt", "", companyReceiptRequest{CompanyID: company, ReceivedAt: date, Amount: amount, Method: "合成转账", VoucherNo: "合成凭证", Note: "合成独立真实到账"}, 200)
	var detail companyReceiptDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func receiptReportingAllocate(t *testing.T, f priceMySQLFixture, detail companyReceiptDetail, allocations ...receiptAllocationInput) companyReceiptDetail {
	t.Helper()
	w := f.request(t, f.admin, "POST", "/api/receipt-allocation", "", receiptAllocationRequest{ReceiptID: detail.Receipt.ID, Version: detail.Receipt.Version, Reason: "合成核对后分配", Allocations: allocations}, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func receiptReportingSource(t *testing.T, f priceMySQLFixture, order orderDetailView) string {
	t.Helper()
	var ticketID string
	if err := f.a.db.QueryRow("SELECT ticket_id FROM project_ticket_items WHERE item_id=? AND item_kind='line'", order.Lines[0].ID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	return ticketID
}

func receiptReportingOrder(t *testing.T, f priceMySQLFixture, address string, amount int64) orderDetailView {
	t.Helper()
	w := f.request(t, f.admin, "POST", "/api/order", "", Order{Company: "loc-co", Address: address, Date: "2026-10-12", Lines: []Line{{Temporary: true, Name: "合成收款施工", Category: "开工", Mode: "金额", Price: amount, Wage: 3000, Worker: "loc-a"}}}, 200)
	return orderLocationsRead(t, f.a, priceTestJSON(t, w)["id"].(string), f.u)
}

func TestCompanyReceiptReportingBalancesMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	receipt := receiptReportingCreate(t, f, "loc-co", "2026-10-12", 100000)
	advance := receiptReportingSummary(t, f, "loc-co")
	if advance.Total != 0 || advance.Received != 100000 || advance.Unallocated != 100000 || advance.Advance != 100000 || advance.Outstanding != 0 || advance.TicketCount != 0 {
		t.Fatal("receipt without any worksite disappeared or became debt", advance)
	}
	first := receiptReportingOrder(t, f, "4B汇总甲工地", 100000)
	second := receiptReportingOrder(t, f, "4B汇总乙工地", 50000)
	firstTicket, secondTicket := receiptReportingSource(t, f, first), receiptReportingSource(t, f, second)
	receipt = receiptReportingAllocate(t, f, receipt, receiptAllocationInput{TicketID: firstTicket, Amount: 40000}, receiptAllocationInput{TicketID: secondTicket, Amount: 30000})
	beforeReads := orderLocationsState(t, f.a.db)
	row := receiptReportingSummary(t, f, "loc-co")
	if row.Total != 150000 || row.Received != 100000 || row.Allocated != 70000 || row.Unallocated != 30000 || row.Unsettled != 80000 || row.Outstanding != 50000 || row.Delivered != 0 || row.Undelivered != 150000 || row.Discount != 0 || row.Advance != 0 {
		t.Fatal("1500 / actual 1000 / allocated 700 balances mixed", row)
	}
	if receipt.Receipt.Amount != 100000 || receipt.Receipt.Allocated != 70000 || receipt.Receipt.Unallocated != 30000 || len(receipt.Allocations) != 2 {
		t.Fatal("single actual receipt duplicated by worksite allocations", receipt)
	}
	for _, project := range []struct {
		id                    string
		total, allocated, due int64
	}{{first.ProjectID, 100000, 40000, 60000}, {second.ProjectID, 50000, 30000, 20000}} {
		w := f.request(t, f.admin, "GET", "/api/projects?id="+project.id, "", nil, 200)
		var detail projectDetailPage
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || detail.Project.TotalCents != project.total || detail.Project.AllocatedCents != project.allocated || detail.Project.UnsettledCents != project.due || detail.Project.OutstandingCents != project.due || detail.Project.DeliveredCents != 0 {
			t.Fatal("worksite balance used company's unallocated cash", w.Body.String(), err)
		}
	}
	f.request(t, f.admin, "GET", "/api/company-receipt?id="+receipt.Receipt.ID, "", nil, 200)
	f.request(t, f.admin, "GET", "/api/company-receipts?company_id=loc-co&month=2026-10&unallocated=1", "", nil, 200)
	f.request(t, f.admin, "GET", "/api/receipt-targets?company_id=loc-co", "", nil, 200)
	orderLocationsUnchanged(t, f.a.db, beforeReads)
	batch := ticketIssueMySQLCreate(t, f, "", ticketIssueMySQLSelection(t, f, first.Lines[0].ID, second.Lines[0].ID))
	row = receiptReportingSummary(t, f, "loc-co")
	if row.Delivered != 150000 || row.Undelivered != 0 || row.Received != 100000 || row.Outstanding != 50000 || batch.Batch.TotalCents != 150000 {
		t.Fatal("paper batch introduced additional receivable", row)
	}
	w := f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": firstTicket, "batch_id": batch.Batch.ID, "amount_cents": 20000, "reason": "合成老板确认少收200元"}, 200)
	var discount customerDiscountDetail
	if err := json.Unmarshal(w.Body.Bytes(), &discount); err != nil {
		t.Fatal(err)
	}
	row = receiptReportingSummary(t, f, "loc-co")
	if row.Total != 150000 || row.Received != 100000 || row.Allocated != 70000 || row.Unallocated != 30000 || row.Discount != 20000 || row.Unsettled != 60000 || row.Outstanding != 30000 || row.Delivered != 150000 {
		t.Fatal("discount was counted as actual cash", row)
	}
	if allCompanies := receiptReportingSummary(t, f, ""); !reflect.DeepEqual(row, allCompanies) {
		t.Fatal("all-company header counted source or allocation joins twice", row, allCompanies)
	}
	w = f.request(t, f.admin, "GET", "/api/project-ticket?id="+firstTicket, "", nil, 200)
	var ticket projectTicketDetail
	if err := json.Unmarshal(w.Body.Bytes(), &ticket); err != nil || ticket.Ticket.TotalCents != 100000 || ticket.Ticket.ReceivedCents != 40000 || ticket.Ticket.DiscountCents != 20000 || ticket.Ticket.OutstandingCents != 40000 {
		t.Fatal("source ticket discount balance", w.Body.String(), err)
	}
	w = f.request(t, f.admin, "GET", "/api/finance-companies?company_id=loc-co&export=1", "", nil, 200)
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 2 || len(records[1]) != 13 || records[1][3] != "1500.00" || records[1][4] != "1000.00" || records[1][5] != "300.00" || records[1][8] != "700.00" || records[1][9] != "300.00" || records[1][10] != "200.00" || records[1][11] != "600.00" {
		t.Fatal("CSV company accounting basis", records, err)
	}
	f.request(t, f.admin, "GET", "/api/finance-companies?month=2026-10", "", nil, 400)
	for _, permissions := range [][]string{{"finance"}, {"customer_prices"}, {"orders"}} {
		actor := f.employee(t, permissions)
		beforeDenied := orderLocationsState(t, f.a.db)
		for _, path := range []string{"/api/finance-companies", "/api/finance-companies?export=1", "/api/company-receipt?id=" + receipt.Receipt.ID, "/api/company-receipts", "/api/receipt-targets?company_id=loc-co", "/api/customer-discount?id=" + discount.Discount.ID, "/api/customer-discounts"} {
			f.request(t, actor, "GET", path, "", nil, 403)
		}
		orderLocationsUnchanged(t, f.a.db, beforeDenied)
	}
	ordersOnly := f.employee(t, []string{"orders", "customer_prices"})
	w = f.request(t, ordersOnly, "GET", "/api/projects?id="+first.ProjectID, "", nil, 200)
	for _, private := range []string{"received_cents", "outstanding_cents", "allocated_cents", "discount_cents", "unsettled_cents"} {
		if strings.Contains(w.Body.String(), `"`+private+`"`) {
			t.Fatal("worksite reader without finance saw cash", private, w.Body.String())
		}
	}
}

func TestCompanyReceiptReportingArchiveMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	first := receiptReportingOrder(t, f, "4B归档甲工地", 100000)
	second := receiptReportingOrder(t, f, "4B归档乙工地", 50000)
	firstTicket, secondTicket := receiptReportingSource(t, f, first), receiptReportingSource(t, f, second)
	receipt := receiptReportingCreate(t, f, "loc-co", "2026-10-12", 100000)
	receipt = receiptReportingAllocate(t, f, receipt, receiptAllocationInput{TicketID: firstTicket, Amount: 40000}, receiptAllocationInput{TicketID: secondTicket, Amount: 30000})
	// An allocation replacement retains history, never creates a new cash head.
	receipt = receiptReportingAllocate(t, f, receipt, receiptAllocationInput{TicketID: firstTicket, Amount: 30000}, receiptAllocationInput{TicketID: secondTicket, Amount: 40000})
	for _, date := range []string{"2026-09-30", "2026-11-01"} {
		receiptReportingCreate(t, f, "loc-co", date, 123)
	}
	f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": firstTicket, "amount_cents": 20000, "reason": "合成归档减免"}, 200)
	if _, err := f.a.db.Exec("UPDATE project_tickets SET updated_at='2026-10-12 12:00:00' WHERE company_id='loc-co'"); err != nil {
		t.Fatal(err)
	}
	backupDir := t.TempDir()
	t.Setenv("BACKUP_DIR", backupDir)
	before := orderLocationsState(t, f.a.db)
	files, err := f.a.archive("2026-10", "loc-co", false, User{Role: "admin"})
	if err != nil {
		t.Fatal("monthly cash archive", err)
	}
	var cash, source *ArchiveFile
	for i := range files {
		switch files[i].Kind {
		case "收款流水":
			cash = &files[i]
		case "票据应收":
			source = &files[i]
		}
	}
	if cash == nil || cash.Amount != 100000 || cash.Count != 1 || source == nil || source.Amount != 150000 || source.Count != 2 || source.Received == nil || *source.Received != 70000 || source.Outstanding == nil || *source.Outstanding != 60000 {
		t.Fatal("month archive counted allocation rows as multiple arrivals", files)
	}
	raw, err := os.ReadFile(filepath.Join(backupDir, "monthly", cash.File))
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 3 || records[1][1] != receipt.Receipt.ID || records[1][2] != "2026-10-12" || records[1][8] != "1000.00" || records[2][8] != "1000.00" {
		t.Fatal("month cash CSV actual receipt identity/boundaries", records, err)
	}
	ticketIssueMySQLTablesUnchanged(t, f.a.db, before, "company_receipts", "company_receipt_versions", "customer_discounts", "customer_discount_versions", "project_ticket_receipts", "orders", "order_worker_wages", "stock_balances", "stock_movements")
}

func TestCompanyReceiptSnapshotRoundTripMySQL(t *testing.T) {
	f := newPriceMySQLFixture(t)
	order := receiptReportingOrder(t, f, "4B新收款恢复工地", 100000)
	ticket := receiptReportingSource(t, f, order)
	receipt := receiptReportingCreate(t, f, "loc-co", "2026-10-12", 80000)
	receiptReportingAllocate(t, f, receipt, receiptAllocationInput{TicketID: ticket, Amount: 60000})
	f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": ticket, "amount_cents": 10000, "reason": "合成恢复减免"}, 200)
	before := orderLocationsState(t, f.a.db)
	target, _ := maintenanceFaultDB(t)
	t.Setenv("BACKUP_DIR", t.TempDir())
	file, err := f.a.backup()
	if err != nil {
		t.Fatal(err)
	}
	if err = target.restore(filepath.Join(os.Getenv("BACKUP_DIR"), file)); err != nil {
		t.Fatal("restore new financial snapshot", err)
	}
	for _, table := range []string{"company_receipts", "company_receipt_versions", "customer_discounts", "customer_discount_versions", "project_ticket_receipts", "project_tickets", "ticket_batches", "ticket_batch_items", "ticket_batch_versions", "orders", "order_worker_wages", "stock_balances", "stock_movements"} {
		if !reflect.DeepEqual(before[table], orderLocationsState(t, target.db)[table]) {
			t.Fatal("receipt backup/restore lost financial identity or history", table)
		}
	}
	if err = backfillCompanyReceipts(target.db); err != nil {
		t.Fatal("new snapshot idempotent compatibility pass", err)
	}
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM company_receipts", 1)
	maintenanceFaultCount(t, target.db, "SELECT COUNT(*) FROM project_ticket_receipts WHERE voided=0 AND amount_cents=60000", 1)
}
