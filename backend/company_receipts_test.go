package main

import "testing"

func TestCompanyReceiptValidationAndPermission(t *testing.T) {
	for _, actor := range []User{{Role: "employee", Permissions: []string{"finance"}}, {Role: "employee", Permissions: []string{"customer_prices"}}, {Role: "employee", Permissions: []string{"orders"}}} {
		maintenanceFaultError(t, companyReceiptPermission(actor, false), 403)
	}
	finance := User{Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	if err := companyReceiptPermission(finance, false); err != nil {
		t.Fatal(err)
	}
	maintenanceFaultError(t, companyReceiptPermission(finance, true), 403)
	for _, role := range []string{"admin", "operator"} {
		if err := companyReceiptPermission(User{Role: role}, true); err != nil {
			t.Fatal(err)
		}
	}
	valid := companyReceiptRequest{CompanyID: "company", ReceivedAt: "2026-10-12", Amount: 100, Method: "转账"}
	if err := validateCompanyReceipt(&valid); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []int64{0, -1, maxTicketBatchCents + 1} {
		p := valid
		p.Amount = amount
		maintenanceFaultError(t, validateCompanyReceipt(&p), 400)
	}
	for _, date := range []string{"2026-02-30", "", "2026-1-1"} {
		p := valid
		p.ReceivedAt = date
		maintenanceFaultError(t, validateCompanyReceipt(&p), 400)
	}
	p := receiptAllocationRequest{ReceiptID: "cash", Version: 1, Reason: "分配", Allocations: []receiptAllocationInput{{TicketID: "a", Amount: 1}, {TicketID: "a", Amount: 1}}}
	maintenanceFaultError(t, validateAllocationInputs(&p), 400)
	if ticketStatus(100, 50, false) != "草稿" || ticketStatus(100, 100, false) != "草稿" {
		t.Fatal("receipt changes paper delivery")
	}
	for _, path := range []string{"/api/company-receipt", "/api/receipt-allocation", "/api/company-receipt-action", "/api/customer-discount", "/api/customer-discount-action"} {
		maintenanceFaultError(t, customerPriceMutationAllowed(path, User{Role: "employee"}, []byte(`{}`)), 403)
	}
}

func TestCompanyReceiptRetiredWritersRejectBeforeSQL(t *testing.T) {
	for _, action := range []string{"receive", "void_receipt"} {
		_, err := (&App{}).projectTicketAction(nil, User{Role: "admin"}, []byte(jsonText(ProjectTicketActionRequest{ID: "old-ticket", Version: 1, Action: action, Amount: 100})), "old-writer-operation")
		maintenanceFaultError(t, err, 409)
	}
	_, err := (&App{}).projectTicketBatch(nil, User{Role: "admin"}, []byte(`{"action":"receive_full"}`), "old-writer-operation")
	maintenanceFaultError(t, err, 409)
}
