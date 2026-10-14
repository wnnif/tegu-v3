package main

import "strings"

// Call before mutate: an idempotent replay returns before the POST handler runs.
// Ordinary company/order saves have their own server-side price preservation and
// safe result projections; they must not be blanket-denied for missing prices.
// Order and company replays must still honor their current module permissions.
func customerPriceMutationAllowed(path string, u User, body []byte) error {
	switch path {
	case "/api/company-receipt", "/api/receipt-allocation":
		return companyReceiptPermission(u, false)
	case "/api/company-receipt-action", "/api/customer-discount", "/api/customer-discount-action":
		return companyReceiptPermission(u, true)
	case "/api/entity":
		var request struct {
			Entity string `json:"entity"`
		}
		if err := decode(body, &request); err != nil {
			return err
		}
		// Company sorting shares the existing company save. Check its current
		// management right before mutate can return a cached idempotent success;
		// customer-price, wage or combination rights are not substitutes.
		if strings.TrimSpace(request.Entity) == "companies" && !u.can("customers") {
			return fail(403, "没有修改公司资料的权限")
		}
		if strings.TrimSpace(request.Entity) == "crafts" && !u.can("rates") {
			return fail(403, "没有修改公共工艺资料的权限")
		}
		return nil
	case "/api/order":
		if !u.can("orders") {
			return fail(403, "无制单权限")
		}
		var request struct {
			Delete       bool                  `json:"delete"`
			Combinations *[]ProjectCombination `json:"combinations"`
		}
		if err := decode(body, &request); err != nil {
			return err
		}
		if request.Combinations != nil {
			if err := requireCombinationOperation(u); err != nil {
				return err
			}
		}
		if request.Delete && !u.can("order_delete") {
			return fail(403, "无删除施工记录权限")
		}
		return nil
	case "/api/project-combination":
		if !u.can("orders") {
			return fail(403, "无制单权限")
		}
		return requireCombinationOperation(u)
	case "/api/project-ticket-action", "/api/project-ticket-batch", "/api/finance-edit":
		if !u.can("finance") {
			return fail(403, "无财务权限")
		}
		return requireCustomerPrices(u)
	case "/api/ticket-batch", "/api/ticket-batch-action":
		return ticketIssuePermission(u)
	case "/api/wage-settlement":
		if !u.can("wages") {
			return fail(403, "无工资结算权限")
		}
		return fail(409, "旧工资发放入口已停用，请在工资管理登记结算和实际付款")
	case "/api/payroll-action":
		if !u.can("wages") {
			return fail(403, "无工资权限")
		}
		return nil
	case "/api/expense-action":
		if !u.can("expenses") {
			return fail(403, "无报销与费用预支权限")
		}
		return nil
	case "/api/settle":
		var request struct {
			Action string `json:"action"`
		}
		if err := decode(body, &request); err != nil {
			return err
		}
		if request.Action == "pay" || request.Action == "undo-pay" {
			if !u.can("wages") {
				return fail(403, "无工资结算权限")
			}
			return fail(409, "旧工资发放入口已停用，请在工资管理登记结算和实际付款")
		}
		if !u.can("finance") {
			return fail(403, "无财务权限")
		}
		return requireCustomerPrices(u)
	}
	return nil
}
