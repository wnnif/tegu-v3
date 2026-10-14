package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type companyProductPriceFixture struct {
	id, companyID, productID, inputUnit string
	priceCents                         int64
}

type companyProductPriceTestStore struct {
	rates      []companyProductPriceFixture
	writes     []string
	writeArgs  [][]driver.NamedValue
}

type companyProductPriceTestConnector struct{ store *companyProductPriceTestStore }
type companyProductPriceTestDriver struct{}
type companyProductPriceTestConn struct{ store *companyProductPriceTestStore }
type companyProductPriceTestTx struct{}
type companyProductPriceTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (c companyProductPriceTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &companyProductPriceTestConn{store: c.store}, nil
}
func (c companyProductPriceTestConnector) Driver() driver.Driver { return companyProductPriceTestDriver{} }
func (companyProductPriceTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("offline company price connector does not accept a DSN")
}
func (c *companyProductPriceTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("offline company price connector does not prepare statements")
}
func (c *companyProductPriceTestConn) Close() error { return nil }
func (c *companyProductPriceTestConn) Begin() (driver.Tx, error) {
	return companyProductPriceTestTx{}, nil
}
func (c *companyProductPriceTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return companyProductPriceTestTx{}, nil
}
func (companyProductPriceTestTx) Commit() error   { return nil }
func (companyProductPriceTestTx) Rollback() error { return nil }

func (c *companyProductPriceTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "FROM company_product_prices WHERE company_id=? AND product_id IN (") {
		return nil, fmt.Errorf("unexpected company product price query: %s", query)
	}
	companies := fmt.Sprint(args[0].Value)
	products := map[string]bool{}
	for _, arg := range args[1:] {
		products[fmt.Sprint(arg.Value)] = true
	}
	rows := &companyProductPriceTestRows{columns: []string{"id", "product_id", "input_unit", "price_cents"}}
	for _, rate := range c.store.rates {
		if rate.companyID == companies && products[rate.productID] {
			rows.values = append(rows.values, []driver.Value{rate.id, rate.productID, rate.inputUnit, rate.priceCents})
		}
	}
	return rows, nil
}

func (c *companyProductPriceTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "product_purchase_costs") {
		return nil, errors.New("customer price path must not write supplier purchase costs")
	}
	if !strings.Contains(query, "order_sales") {
		return nil, fmt.Errorf("unexpected company price write: %s", query)
	}
	c.store.writes = append(c.store.writes, query)
	c.store.writeArgs = append(c.store.writeArgs, append([]driver.NamedValue(nil), args...))
	return driver.RowsAffected(1), nil
}

func (r *companyProductPriceTestRows) Columns() []string { return r.columns }
func (r *companyProductPriceTestRows) Close() error     { return nil }
func (r *companyProductPriceTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func openCompanyProductPriceTestDB(t *testing.T, store *companyProductPriceTestStore) *sql.DB {
	t.Helper()
	db := sql.OpenDB(companyProductPriceTestConnector{store: store})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCompanyProductPriceLookupScopesCompanyProductAndUnit(t *testing.T) {
	store := &companyProductPriceTestStore{rates: []companyProductPriceFixture{
		{id: "yiban-toilet-base", companyID: "company-yiban", productID: "temporary-toilet", inputUnit: "base", priceCents: 1500},
		{id: "yiban-box-base", companyID: "company-yiban", productID: "electric-box", inputUnit: "base", priceCents: 10000},
		{id: "other-toilet-base", companyID: "company-other", productID: "temporary-toilet", inputUnit: "base", priceCents: 99000},
	}}
	db := openCompanyProductPriceTestDB(t, store)

	prices, err := loadCompanyProductPrices(db, "company-yiban", []string{"temporary-toilet"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := prices["temporary-toilet"]["base"]; got.PriceCents != 1500 || got.ID != "yiban-toilet-base" {
		t.Fatalf("wrong company/product price returned: %+v", got)
	}
	if _, exists := prices["temporary-toilet"]["aux"]; exists {
		t.Fatal("an unconfigured input unit received a fixed price")
	}
	otherCompany, err := loadCompanyProductPrices(db, "company-other", []string{"temporary-toilet"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := otherCompany["temporary-toilet"]["base"].PriceCents; got != 99000 {
		t.Fatalf("company-scoped lookup returned %d, want 99000", got)
	}
}

func TestCompanyProductSalePriceAppliesExactUnitAndRejectsMissingRate(t *testing.T) {
	sale := Sale{Product: "temporary-toilet", InputUnit: "base", Price: 1}
	configured := map[string]companyProductPrice{"base": {ID: "yiban-toilet-base", ProductID: "temporary-toilet", InputUnit: "base", PriceCents: 1500}}
	if err := applyCompanyProductSalePrice(&sale, nil, configured); err != nil {
		t.Fatal(err)
	}
	if sale.Price != 1500 || sale.CompanyProductPriceID != "yiban-toilet-base" {
		t.Fatalf("sale did not adopt the fixed company rate: %+v", sale)
	}
	sale.InputUnit = "aux"
	if err := applyCompanyProductSalePrice(&sale, nil, configured); err == nil {
		t.Fatal("missing auxiliary-unit rate was accepted")
	}
	old := Sale{Product: "temporary-toilet", InputUnit: "base", Price: 1400, CompanyProductPriceID: "old-rate"}
	sale.InputUnit = "base"
	sale.Price = 9999
	if err := applyCompanyProductSalePrice(&sale, &old, configured); err != nil {
		t.Fatal(err)
	}
	if sale.Price != 1400 || sale.CompanyProductPriceID != "old-rate" {
		t.Fatalf("editing an existing fixed-rate sale must retain its saved price: %+v", sale)
	}
}

func TestCompanyProductSaleTotalSnapshotsRateWithoutTouchingWageOrPurchaseCost(t *testing.T) {
	store := &companyProductPriceTestStore{rates: []companyProductPriceFixture{{
		id: "yiban-toilet-base", companyID: "company-yiban", productID: "temporary-toilet", inputUnit: "base", priceCents: 1500,
	}}}
	db := openCompanyProductPriceTestDB(t, store)
	prices, err := loadCompanyProductPrices(db, "company-yiban", []string{"temporary-toilet"}, false)
	if err != nil {
		t.Fatal(err)
	}
	sale := Sale{ID: "sale-1", Product: "temporary-toilet", Warehouse: "warehouse-1", Qty: 2000000, QtyQuantum: "2000000", SaleQty: 2000000, InputQty: "2", InputUnit: "base", Unit: "个", PriceUnit: "个"}
	if err := applyCompanyProductSalePrice(&sale, nil, prices[sale.Product]); err != nil {
		t.Fatal(err)
	}
	sale.Total = scaledAmount(sale.SaleQty, sale.Price)
	if sale.Price != 1500 || sale.Total != 3000 {
		t.Fatalf("2 items at 15 yuan should total 3000 cents; sale=%+v", sale)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = persistOrderSales(tx, Order{ID: "order-1", Sales: []Sale{sale}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 2 || len(store.writeArgs) != 2 {
		t.Fatalf("expected sale replacement statements, got %d", len(store.writes))
	}
	for _, statement := range store.writes {
		if !strings.Contains(statement, "order_sales") || strings.Contains(statement, "product_purchase_costs") {
			t.Fatalf("sale-price persistence crossed into supplier cost tables: %s", statement)
		}
	}
	args := store.writeArgs[1]
	if len(args) != 17 || args[7].Value != int64(1500) || args[8].Value != int64(3000) || args[16].Value != "yiban-toilet-base" {
		t.Fatalf("sale snapshot did not persist fixed rate and total: %#v", args)
	}
}

func TestCompanyProductPriceEndpointRequiresPricePermission(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/company-product-prices?company_id=company-yiban&product_id=temporary-toilet", nil)
	response := httptest.NewRecorder()
	app := &App{}
	err := app.companyProductPrices(response, request, User{ID: "worker", Permissions: []string{"orders"}})
	if err == nil {
		t.Fatalf("price endpoint must reject a user without customer_prices: status=%d", response.Code)
	}
}
