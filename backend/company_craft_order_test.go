package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCompanyCraftCategoryOrderValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"omitted-preserves", "", nil},
		{"empty-resets", "[]", []string{}},
		{"partial-exact-order", `["丙纶","室外防水","开工"]`, []string{"丙纶", "室外防水", "开工"}},
		{"all-canonical-categories", jsonText(categories), append([]string{}, categories...)},
		{"JSON-whitespace", " \n [\"维修\",\"回填\"] \t", []string{"维修", "回填"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCompanyCraftCategoryOrder(json.RawMessage(tc.raw))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse order got %#v, want %#v: %v", got, tc.want, err)
			}
		})
	}
	for _, tc := range []struct{ name, raw string }{
		{"null-is-not-reset", `null`},
		{"string", `"开工"`},
		{"object", `{"category":"开工"}`},
		{"boolean", `true`},
		{"number", `1`},
		{"numeric-item", `["开工",1]`},
		{"boolean-item", `[false]`},
		{"null-item", `[null]`},
		{"object-item", `[{"id":"开工"}]`},
		{"nested-array", `[["开工"]]`},
		{"duplicate", `["开工","开工"]`},
		{"unknown-category", `["防水"]`},
		{"category-ID-is-not-a-key", `["category-1"]`},
		{"craft-name-is-not-a-key", `["开工墙面"]`},
		{"substring-is-not-a-key", `["防水涂料及室外防水"]`},
		{"key-whitespace-is-not-normalized", `[" 开工 "]`},
		{"empty-key", `[""]`},
		{"over-limit", jsonText(append(append([]string{}, categories...), "开工"))},
		{"oversized-key", jsonText([]string{strings.Repeat("工", 201)})},
		{"invalid-JSON", `["开工"`},
		{"trailing-JSON", `[] []`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCompanyCraftCategoryOrder(json.RawMessage(tc.raw))
			var f failure
			if !errors.As(err, &f) || f.Code != 400 {
				t.Fatalf("invalid category order accepted or wrong failure: %v", err)
			}
		})
	}
}

func TestCompanyCraftCategoryOrderInputPresence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
		code       int
	}{
		{"omitted", `{"name":"合成公司"}`, nil, 0},
		{"reset", `{"craft_category_order":[]}`, []string{}, 0},
		{"partial", `{"craft_category_order":["室外防水","防水涂料"]}`, []string{"室外防水", "防水涂料"}, 0},
		{"folded-field", `{"CRAFT_CATEGORY_ORDER":["包管"]}`, []string{"包管"}, 0},
		{"explicit-null", `{"craft_category_order":null}`, nil, 400},
		{"folded-null", `{"Craft_Category_Order":null}`, nil, 400},
		{"wrong-type", `{"craft_category_order":{}}`, nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input companyEntityInput
			if err := decode([]byte(tc.body), &input); err != nil {
				t.Fatal(err)
			}
			got, err := parseCompanyCraftCategoryOrder(input.CraftCategoryOrder)
			secondaryPriceFailure(t, err, tc.code)
			if tc.code == 0 && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("field presence lost: got %#v, want %#v", got, tc.want)
			}
			// Omitted input remains omitted in the existing audit projection.
			fields := secondaryPriceObject(t, input)
			if tc.name == "omitted" {
				if _, ok := fields["craft_category_order"]; ok {
					t.Fatal("omitted category order became an explicit audit reset")
				}
			} else if len(fields["craft_category_order"]) == 0 {
				t.Fatal("explicit category order disappeared from audit input")
			}
		})
	}
}

func TestCompanyCraftCategoryOrderReadArrays(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"SQL-null", "", []string{}},
		{"JSON-null", " null \n", []string{}},
		{"reset", "[]", []string{}},
		{"stored-partial", `["室外防水","丙纶"]`, []string{"室外防水", "丙纶"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order, err := readCompanyCraftCategoryOrder([]byte(tc.raw))
			if err != nil || order == nil || !reflect.DeepEqual(order, tc.want) {
				t.Fatalf("read array got %#v, want %#v: %v", order, tc.want, err)
			}
			fields := secondaryPriceObject(t, companiesRecord{CraftCategoryOrder: order})
			if string(fields["craft_category_order"]) != jsonText(tc.want) {
				t.Fatalf("company JSON omitted array or exposed null: %s", fields["craft_category_order"])
			}
		})
	}
	for _, raw := range []string{`{}`, `[1]`, `["未知"]`, `["开工","开工"]`, `[`} {
		if _, err := readCompanyCraftCategoryOrder([]byte(raw)); err == nil {
			t.Fatalf("invalid stored order silently replaced with default: %s", raw)
		}
	}
}

func TestCompanyCraftCategoryOrderRequiresCustomersOnly(t *testing.T) {
	// All failures below happen before using a transaction: no MySQL is needed.
	body := []byte(`{"entity":"companies","name":"合成公司","craft_category_order":[]}`)
	for _, permissions := range [][]string{nil, {"customer_prices"}, {"orders", "combo_pricing"}, {"wages"}, {"finance", "rates"}} {
		u := User{Role: "employee", Permissions: permissions}
		_, err := saveCompanyEntity(nil, u, body)
		secondaryPriceFailure(t, err, 403)
		_, err = (&App{}).saveEntity(nil, u, body)
		secondaryPriceFailure(t, err, 403)
		secondaryPriceFailure(t, customerPriceMutationAllowed("/api/entity", u, body), 403)
		secondaryPriceFailure(t, customerPriceMutationAllowed("/api/entity", u, []byte(`{"entity":" companies ","craft_category_order":[]}`)), 403)
	}
	u := User{Role: "employee", Permissions: []string{"customers"}}
	if u.can("customer_prices") || u.can("wages") || u.can("combo_pricing") {
		t.Fatal("customers permission widened")
	}
	invalid := []byte(`{"entity":"companies","name":"合成公司","craft_category_order":null}`)
	_, err := saveCompanyEntity(nil, u, invalid)
	secondaryPriceFailure(t, err, 400)
	_, err = (&App{}).saveEntity(nil, u, invalid)
	secondaryPriceFailure(t, err, 400)
	secondaryPriceFailure(t, customerPriceMutationAllowed("/api/entity", u, body), 0)
}
