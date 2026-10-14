package main

import "encoding/json"

// This is a top-level projection for the explicit customer-money DTOs below.
// It never walks arbitrary business maps: stock costs and wage amounts retain
// their existing permissions and exact integer representation.
func marshalSecondaryCustomerView(v any, customerHidden, financeHidden bool, customerFields, financeFields []string) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil || (!customerHidden && !financeHidden) {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if customerHidden {
		for _, field := range customerFields {
			delete(fields, field)
		}
	}
	if customerHidden || financeHidden {
		for _, field := range financeFields {
			delete(fields, field)
		}
	}
	return json.Marshal(fields)
}

func (v craftsRecord) MarshalJSON() ([]byte, error) {
	type plain craftsRecord
	return marshalSecondaryCustomerView(plain(v), v.customerPricesHidden, false,
		[]string{"price_cents"}, nil)
}

func (v projectRecord) MarshalJSON() ([]byte, error) {
	type plain projectRecord
	return marshalSecondaryCustomerView(plain(v), v.customerPricesHidden, v.financeHidden,
		[]string{"total_cents"},
		[]string{"received_cents", "undelivered_cents", "delivered_outstanding_cents", "outstanding_cents", "allocated_cents", "discount_cents", "delivered_cents", "unsettled_cents"})
}

func (v projectOrderRecord) MarshalJSON() ([]byte, error) {
	type plain projectOrderRecord
	return marshalSecondaryCustomerView(plain(v), v.customerPricesHidden, v.financeHidden,
		[]string{"total_cents", "package_charge_cents"}, []string{"received_cents", "outstanding_cents", "discount_cents"})
}

func (v unassignedProjectOrderRecord) MarshalJSON() ([]byte, error) {
	type plain unassignedProjectOrderRecord
	return marshalSecondaryCustomerView(plain(v), v.customerPricesHidden, false,
		[]string{"total_cents"}, nil)
}

func (v projectTicketView) MarshalJSON() ([]byte, error) {
	type plain projectTicketView
	return marshalSecondaryCustomerView(plain(v), v.customerPricesHidden, v.financeHidden,
		[]string{"total_cents", "package_charge_cents", "package_charge"}, []string{"received_cents", "outstanding_cents", "discount_cents"})
}
