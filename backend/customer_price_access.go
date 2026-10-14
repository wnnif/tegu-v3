package main

import (
	"encoding/json"
	"strings"
)

const customerPricePermission = "customer_prices"

func requireCustomerPrices(u User) error {
	if !u.can(customerPricePermission) {
		return fail(403, "没有查看客户价格权限")
	}
	return nil
}

// These views still hold full server-side snapshots for business calculations.
// Only their JSON representation hides customer money; storage is never redacted.
func (v orderLineView) MarshalJSON() ([]byte, error) {
	type plain orderLineView
	return marshalOrderPriceView(plain(v), v.customerPricesHidden, false)
}

func (v orderDetailView) MarshalJSON() ([]byte, error) {
	type plain orderDetailView
	return marshalOrderPriceView(plain(v), v.customerPricesHidden, v.financeHidden)
}

func (v orderListItem) MarshalJSON() ([]byte, error) {
	type plain orderListItem
	return marshalOrderPriceView(plain(v), v.customerPricesHidden, v.financeHidden)
}

func (v orderListPage) MarshalJSON() ([]byte, error) {
	type plain orderListPage
	return marshalOrderPriceView(plain(v), v.customerPricesHidden, false)
}

func marshalOrderPriceView(v any, customerHidden, financeHidden bool) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if customerHidden {
		data, err = omitOrderCustomerMoney(data)
		if err != nil {
			return nil, err
		}
	}
	if financeHidden {
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		delete(fields, "received_cents")
		delete(fields, "outstanding_cents")
		return json.Marshal(fields)
	}
	return data, nil
}

// Scope: explicit order API views / order save responses only. In particular this
// must not be applied to stock costs or wage settlement amounts. RawMessage keeps
// exact integers and quantities intact (no float64 round-trip).
func omitOrderCustomerMoney(data []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if len(data) > 0 && data[0] == '{' {
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, err
		}
		for key, value := range object {
			switch strings.ToLower(key) {
			case "price_cents", "total_cents", "amount_cents", "received_cents", "outstanding_cents", "charge_mode", "package_included":
				delete(object, key)
			default:
				filtered, err := omitOrderCustomerMoney(value)
				if err != nil {
					return nil, err
				}
				object[key] = filtered
			}
		}
		return json.Marshal(object)
	}
	if len(data) > 0 && data[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		for i := range items {
			filtered, err := omitOrderCustomerMoney(items[i])
			if err != nil {
				return nil, err
			}
			items[i] = filtered
		}
		return json.Marshal(items)
	}
	return data, nil
}

// Re-evaluate the current user even for a previously cached idempotent response.
func orderSaveResultForUser(result any, u User) (any, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if !u.can(customerPricePermission) {
		data, err = omitOrderCustomerMoney(data)
		if err != nil {
			return nil, err
		}
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if !u.can("wages") {
		delete(fields, "wage_cents")
		delete(fields, "delivery_wage_cents")
		delete(fields, "labor_cents")
	}
	return fields, nil
}

// Employees editing ordinary sale quantities must use the saved selling price.
// Creating a free-priced sale or changing its pricing unit needs price access.
func protectOrderSalesPrices(u User, o *Order, previous []Sale) error {
	if u.can(customerPricePermission) || o.Delete {
		return nil
	}
	byID := make(map[string]Sale, len(previous))
	for _, sale := range previous {
		byID[sale.ID] = sale
	}
	for i := range o.Sales {
		sale := &o.Sales[i]
		old, found := byID[sale.ID]
		if sale.ID == "" || !found {
			return fail(403, "新增售料收费项需要查看客户价格权限")
		}
		unit, oldUnit := sale.InputUnit, old.InputUnit
		if unit == "" {
			unit = "base"
		}
		if oldUnit == "" {
			oldUnit = "base"
		}
		if unit != oldUnit {
			return fail(403, "调整售料计价单位需要查看客户价格权限")
		}
		sale.Price = old.Price
	}
	return nil
}
