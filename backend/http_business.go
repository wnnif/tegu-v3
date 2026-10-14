package main

import (
	"database/sql"
	"net/http"
)

func (a *App) get(w http.ResponseWriter, r *http.Request, u User) error {
	p := r.URL.Path
	switch p {
	case "/api/dashboard":
		return a.dashboard(w, r, u)
	case "/api/materials":
		return a.materialHistory(w, r, u)
	case "/api/export":
		return a.exportFiltered(w, r, u)
	case "/api/backup-file":
		return a.backupFile(w, r, u)
	case "/api/print-settings":
		if !u.can("orders") && !u.can("finance") && !u.can("wages") && !u.can("rates") {
			return fail(403, "无打印权限")
		}
		s, e := a.printSettings()
		if e == nil {
			write(w, 200, s)
		}
		return e
	case "/api/backup-settings":
		if !u.can("backups") {
			return fail(403, "仅系统管理员可查看备份设置")
		}
		s, e := a.backupSettings()
		if e != nil {
			return e
		}
		s.HasSecret = s.Secret != ""
		s.Secret = ""
		write(w, 200, s)
		return nil
	case "/api/reference":
		return a.reference(w, r, u)
	case "/api/reference-options":
		return a.referenceOptions(w, r, u)
	case "/api/company-product-prices":
		return a.companyProductPrices(w, r, u)
	case "/api/catalog":
		return a.listCatalog(w, r, u)
	case "/api/company-packages":
		return a.listCompanyPackages(w, r, u)
	case "/api/users":
		return a.listUsers(w, r, u)
	case "/api/orders":
		return a.listOrders(w, r, u)
	case "/api/projects":
		return a.listProjects(w, r, u)
	case "/api/project-work":
		return a.readProjectWork(w, r, u)
	case "/api/project-combinations":
		return a.readProjectCombinations(w, r, u)
	case "/api/project-ticket":
		return a.readProjectTicket(w, r, u)
	case "/api/ticket-issue":
		return a.ticketIssueRead(w, r, u)
	case "/api/ticket-batch":
		return a.ticketBatchRead(w, r, u)
	case "/api/project-tickets":
		return a.listProjectTickets(w, r, u)
	case "/api/order":
		return a.readOrder(w, r, u)
	case "/api/wage":
		return a.readWage(w, r, u)
	case "/api/wage-workers":
		return a.listWageWorkers(w, r, u)
	case "/api/payroll-workers":
		return a.listPayrollWorkers(w, r, u)
	case "/api/payroll-projects":
		return a.listPayrollProjects(w, r, u)
	case "/api/payroll":
		return a.listPayroll(w, r, u)
	case "/api/payroll-history":
		return a.listPayrollHistory(w, r, u)
	case "/api/payroll-bonus":
		return a.listPayrollBonus(w, r, u)
	case "/api/expenses":
		return a.listExpenses(w, r, u)
	case "/api/expense-purchases":
		return a.listExpensePurchases(w, r, u)
	case "/api/finance-companies":
		return a.listFinanceCompanies(w, r, u)
	case "/api/finance-summary":
		return a.financeDashboard(w, r, u)
	case "/api/company-receipts":
		return a.companyReceiptsList(w, r, u)
	case "/api/company-receipt":
		return a.companyReceiptRead(w, r, u)
	case "/api/receipt-targets":
		return a.receiptTargetsRead(w, r, u)
	case "/api/customer-discounts":
		return a.customerDiscountsList(w, r, u)
	case "/api/customer-discount":
		return a.customerDiscountRead(w, r, u)
	case "/api/inventory":
		return a.inventoryList(w, r, u)
	case "/api/stock-movements":
		return a.listStockMovements(w, r, u)
	case "/api/stock-documents":
		return a.listHistory(w, r, u, true)
	case "/api/audit":
		return a.listHistory(w, r, u, false)
	case "/api/stock-document":
		return a.readStockDocument(w, r, u)
	case "/api/backups":
		return a.backupList(w, r, u)
	case "/api/maintenance/jobs":
		return a.listMaintenanceJobs(w, r, u)
	case "/api/archives":
		return a.listArchives(w, r, u)
	case "/api/archive-companies":
		return a.listArchiveCompanies(w, r, u)
	case "/api/archive-company-download":
		return a.downloadArchiveCompany(w, r, u)
	}
	return fail(404, "接口不存在")
}
func (a *App) post(tx *sql.Tx, p string, u User, b []byte, key string) (any, error) {
	switch p {
	case "/api/account-password":
		return a.changePassword(tx, u, b)
	case "/api/warehouse-delete":
		return a.deleteWarehouse(tx, u, b)
	case "/api/print-settings":
		return a.savePrintSettings(tx, u, b)
	case "/api/order":
		return a.saveOrder(tx, u, b, key)
	case "/api/project-combination":
		return a.saveProjectCombination(tx, u, b)
	case "/api/project":
		return a.saveProject(tx, u, b)
	case "/api/project-associate":
		return a.associateProject(tx, u, b)
	case "/api/project-ticket-action":
		return a.projectTicketAction(tx, u, b, key)
	case "/api/project-ticket-batch":
		return a.projectTicketBatch(tx, u, b, key)
	case "/api/ticket-batch":
		return a.createTicketBatch(tx, u, b, key)
	case "/api/ticket-batch-action":
		return a.ticketBatchAction(tx, u, b, key)
	case "/api/company-receipt":
		return a.createCompanyReceipt(tx, u, b, key)
	case "/api/company-receipt-action":
		return a.companyReceiptAction(tx, u, b, key)
	case "/api/receipt-allocation":
		return a.replaceReceiptAllocation(tx, u, b, key)
	case "/api/customer-discount":
		return a.createCustomerDiscount(tx, u, b, key)
	case "/api/customer-discount-action":
		return a.customerDiscountAction(tx, u, b, key)
	case "/api/stock":
		return a.saveStock(tx, u, b, key)
	case "/api/stock-state":
		return a.stockState(tx, u, b, key)
	case "/api/finance-edit":
		return a.financeEdit(tx, u, b)
	case "/api/wage-settlement":
		return a.settleWorkerWages(tx, u, b, key)
	case "/api/payroll-action":
		return a.payrollAction(tx, u, b, key)
	case "/api/expense-action":
		return a.expenseAction(tx, u, b, key)
	case "/api/settle":
		return a.settle(tx, u, b, key)
	case "/api/user":
		return a.saveUser(tx, u, b)
	case "/api/entity":
		return a.saveEntity(tx, u, b)
	}
	return nil, fail(404, "接口不存在")
}
