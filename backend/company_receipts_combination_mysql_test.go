package main

import "testing"

// Focused Phase 4 linkage only: effective combination waivers are already out
// of the receivable. Cash must survive void/edit, and cannot be waived twice.
func TestCompanyReceiptCombinationChargeDecreaseMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "收款组合更正合成工地", Date: "2026-10-12", Lines: combinationActualLines()})
	ticket := companyReceiptTestTicket(t, f.priceMySQLFixture, d.ProjectID)
	batch := ticketIssueMySQLCreate(t, f.priceMySQLFixture, id(), ticketIssueMySQLSelection(t, f.priceMySQLFixture, d.Lines[0].ID, d.Lines[1].ID))
	r := companyReceiptTestCreate(t, f.priceMySQLFixture, 500000)
	r = companyReceiptTestAllocate(t, f.priceMySQLFixture, r, receiptAllocationInput{TicketID: ticket, BatchID: batch.Batch.ID, Amount: 500000})
	f.request(t, f.admin, "POST", "/api/ticket-batch-action", "", ticketBatchActionRequest{ID: batch.Batch.ID, Version: batch.Batch.Version, Action: "void", Reason: "合成组合收费更正"}, 200)
	d = f.read(t, d.ID)
	d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 100000000)}, 200)
	f.assertWages(t, d, 120000, 150000)
	f.assertDraftTicket(t, d.ProjectID, 300000)
	r = companyReceiptTestDetail(t, f.request(t, f.admin, "GET", "/api/company-receipt?id="+r.Receipt.ID, "", nil, 200))
	if r.Receipt.Amount != 500000 || r.Receipt.Allocated != 300000 || r.Receipt.Unallocated != 200000 {
		t.Fatal("combination decrease did not release cash", r)
	}
	f.request(t, f.admin, "POST", "/api/customer-discount", "", map[string]any{"company_id": "loc-co", "ticket_id": ticket, "amount_cents": 300000, "reason": "试图再次免掉组合免收部分"}, 400)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM customer_discounts", 0)
	companyReceiptTestScalar(t, f.a.db, "SELECT COUNT(*) FROM company_receipts", 1)
	companyReceiptTestScalar(t, f.a.db, "SELECT SUM(amount_cents) FROM company_receipts WHERE status='active'", 500000)
}
