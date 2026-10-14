package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCustomerPricePermissionIsIndependent(t *testing.T) {
	for _, role := range []string{"admin", "operator"} {
		if !(User{Role: role}).can(customerPricePermission) {
			t.Fatalf("%s lost existing role access", role)
		}
	}
	for _, permissions := range [][]string{nil, {"orders"}, {"wages"}, {"finance"}, {"rates", "customers"}} {
		u := User{Role: "employee", Permissions: permissions}
		if u.can(customerPricePermission) {
			t.Fatalf("legacy employee automatically granted price access: %v", permissions)
		}
	}
	u := User{Role: "employee", Permissions: []string{customerPricePermission}}
	if !u.can(customerPricePermission) || u.can("orders") || u.can("finance") || u.can("wages") || u.canFillCraft("fixed") {
		t.Fatal("price access incorrectly grants other business access")
	}
	if !contains(allPermissions, customerPricePermission) {
		t.Fatal("price permission is not accepted by account management")
	}
}

func TestOrderCustomerPriceJSONIsolation(t *testing.T) {
	wage, labor := int64(321), int64(654)
	v := orderDetailView{
		customerPricesHidden: true, financeHidden: true,
		ID: "saved", Address: "普通施工信息", TotalCents: 12345, ReceivedCents: 300, OutstandingCents: 12045,
		WageCents: &wage,
		Lines: []orderLineView{{ID: "line", SubitemName: "墙面", ActualLocation: "厨房", ChargeMode: "free", PackageIncluded: true, Qty: 9007199254740993,
			Price: 12345, Total: 12345, Wage: &wage, Labor: &labor,
			Segments: []Segment{{Mode: "金额", Amount: 12345, Surface: "保留表面"}}}},
		Sales: []Sale{{ID: "sale", ProductName: "普通商品", Price: 7654, Total: 15308, Qty: 2000000}},
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"price_cents", "total_cents", "amount_cents", "received_cents", "outstanding_cents", "charge_mode", "package_included"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("hidden order exposed %s: %s", field, data)
		}
	}
	for _, text := range []string{"墙面", "厨房", "普通商品", "9007199254740993", `"wage_cents":321`, `"labor_cents":654`} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("ordinary construction / independent wages changed: %s", data)
		}
	}
	if v.Lines[0].Price != 12345 || v.Lines[0].Segments[0].Amount != 12345 || v.Sales[0].Price != 7654 {
		t.Fatal("serialization redacted the server-side stored snapshot")
	}
	v.customerPricesHidden = false
	data, err = json.Marshal(v)
	if err != nil || !strings.Contains(string(data), `"total_cents":12345`) || !strings.Contains(string(data), `"charge_mode":"free"`) || strings.Contains(string(data), `"received_cents"`) {
		t.Fatal("view prices must not imply finance access", string(data), err)
	}
	v.financeHidden = false
	v.TotalCents = 0
	data, err = json.Marshal(v)
	if err != nil || !strings.Contains(string(data), `"total_cents":0`) || !strings.Contains(string(data), `"received_cents":300`) {
		t.Fatal("authorized zero amount / finance fields missing", string(data), err)
	}
}

func TestOrderCustomerPriceListJSONIsolation(t *testing.T) {
	wage := int64(100)
	row := orderListItem{customerPricesHidden: true, financeHidden: true, ID: "a", TotalCents: 800, ReceivedCents: 200, OutstandingCents: 600, WageCents: &wage}
	page := orderListPage{customerPricesHidden: true, Rows: []orderListItem{row}, Total: 1, TotalCents: 800, WageCents: &wage, Page: 1}
	data, err := json.Marshal(page)
	if err != nil || strings.Contains(string(data), "total_cents") || strings.Contains(string(data), "received_cents") || !strings.Contains(string(data), `"wage_cents":100`) || !strings.Contains(string(data), `"total":1`) {
		t.Fatal("list totals must be omitted without losing count/wages", string(data), err)
	}
}

func TestOrderSaveCachedResultUsesCurrentPermissions(t *testing.T) {
	cached := map[string]any{"id": "saved", "version": int64(2), "total_cents": int64(987654321), "wage_cents": int64(456)}
	u := User{Role: "employee", Permissions: []string{"orders"}}
	v, err := orderSaveResultForUser(cached, u)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil || strings.Contains(string(data), "total_cents") || strings.Contains(string(data), "wage_cents") || !strings.Contains(string(data), "saved") {
		t.Fatal("cached save response bypassed current permissions", string(data), err)
	}
	if cached["total_cents"] != int64(987654321) {
		t.Fatal("filter changed cached storage")
	}
	u.Permissions = []string{"orders", customerPricePermission}
	v, err = orderSaveResultForUser(cached, u)
	data, marshalErr := json.Marshal(v)
	if err != nil || marshalErr != nil || !strings.Contains(string(data), `"total_cents":987654321`) || strings.Contains(string(data), "wage_cents") {
		t.Fatal("price permission leaked wages / lost price", string(data), err, marshalErr)
	}
}

func TestHiddenCustomerPriceTemporaryLinePreservesSnapshot(t *testing.T) {
	previous := Line{ID: "line", Category: "开工", Name: "历史临时项", Mode: "金额", Price: 4321, Wage: 300, Temporary: true, Segments: []Segment{{Mode: "金额", Amount: 4321}}}
	u := User{Role: "employee", Permissions: []string{"orders", "orders_temporary"}}
	for _, forged := range []int64{0, 1, 99999} {
		line := previous
		line.Price, line.Wage = forged, 99999
		line.Segments = []Segment{{Mode: "金额", Amount: forged}}
		line.Description, line.ActualLocation = "改普通备注", "厨房"
		if err := prepareOrderLine(nil, u, Order{}, &line, &previous); err != nil {
			t.Fatal(err)
		}
		if line.Price != 4321 || line.Total != 4321 || line.Wage != 300 || line.Labor != 300 || line.Segments[0].Amount != 4321 || line.ActualLocation != "厨房" {
			t.Fatalf("omitted/forged hidden amount overwrote saved price: %#v", line)
		}
	}
	line := previous
	line.ID = ""
	if err := prepareOrderLine(nil, u, Order{}, &line, nil); err == nil {
		t.Fatal("unpriced temporary line unexpectedly accepted")
	}
}

func TestHiddenCustomerSalePricesPreserveSnapshot(t *testing.T) {
	u := User{Role: "employee", Permissions: []string{"orders"}}
	previous := []Sale{{ID: "saved", Product: "product", InputUnit: "base", Price: 1800}}
	for _, forged := range []int64{0, 1, 99999} {
		o := Order{Sales: []Sale{{ID: "saved", Product: "product", InputUnit: "base", InputQty: "3", Price: forged}}}
		if err := protectOrderSalesPrices(u, &o, previous); err != nil || o.Sales[0].Price != 1800 || o.Sales[0].InputQty != "3" {
			t.Fatal("ordinary sale edit lost stored price", o.Sales, err)
		}
	}
	for _, sale := range []Sale{{}, {ID: "foreign"}, {ID: "saved", InputUnit: "aux"}} {
		o := Order{Sales: []Sale{sale}}
		if err := protectOrderSalesPrices(u, &o, previous); err == nil {
			t.Fatal("new sale / changed price unit accepted without permission", sale)
		}
	}
}
