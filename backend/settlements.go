package main

import "database/sql"

func (a *App) settle(_ *sql.Tx, u User, body []byte, _ string) (any, error) {
	var request SettlementRequest
	if err := decode(body, &request); err != nil {
		return nil, err
	}
	if request.Action == "pay" || request.Action == "undo-pay" {
		if !u.can("wages") {
			return nil, fail(403, "无工资结算权限")
		}
		return nil, fail(409, "旧工资发放入口已停用，请在工资管理登记结算和实际付款")
	}
	if !u.can("finance") {
		return nil, fail(403, "无财务权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return nil, err
	}
	return nil, fail(409, "请在交票管理或收款管理处理财务；旧结算入口仅保留读取")
}
