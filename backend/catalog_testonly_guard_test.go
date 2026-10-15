package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateCompanyCraftTestOnlyPriceRequiresExplicitConfirmedReplacement(t *testing.T) {
	authorized := User{Role: "employee", Permissions: []string{"customer_prices"}}
	unauthorized := User{Role: "employee"}
	confirmedPrice := catalogCents(250)
	placeholderPrice := catalogCents(100)
	zeroPrice := catalogCents(0)

	tests := []struct {
		name       string
		user       User
		current    int64
		price      *catalogCents
		explicit   bool
		wantStatus int
	}{
		{name: "no customer price permission", user: unauthorized, current: 100, price: &confirmedPrice, explicit: true, wantStatus: 403},
		{name: "manually changed current charge", user: authorized, current: 200, price: &confirmedPrice, explicit: true, wantStatus: 409},
		{name: "omitted price", user: authorized, current: 100, explicit: false, wantStatus: 400},
		{name: "unchanged one yuan placeholder", user: authorized, current: 100, price: &placeholderPrice, explicit: true, wantStatus: 400},
		{name: "confirmed replacement", user: authorized, current: 100, price: &confirmedPrice, explicit: true},
		{name: "explicit zero price", user: authorized, current: 100, price: &zeroPrice, explicit: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCompanyCraftTestOnlyPrice(test.user, test.current, test.price, test.explicit)
			if test.wantStatus == 0 {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			var got failure
			if !errors.As(err, &got) || got.Code != test.wantStatus {
				t.Fatalf("got %v, want HTTP %d", err, test.wantStatus)
			}
		})
	}
}

func TestManualCompanyCraftPriceProvenanceRetainsSeedSource(t *testing.T) {
	actor := User{ID: "operator-1", Name: "确认人", Role: "employee", Permissions: []string{"customer_prices"}}
	provenance := companyCraftTestOnlyPriceProvenance{
		seedID: "v3-seed-old", sourceID: "excel-prices", sourceFile: "报价表.xlsx",
		sourceSheet: "开工报价", sourceRow: 17, classification: "test_only", detail: `{"note":"旧占位"}`,
	}

	data, err := manualCompanyCraftPriceProvenanceDetail(actor, catalogCents(350), provenance)
	if err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Kind                string `json:"kind"`
		ActorID             string `json:"actor_id"`
		ActorName           string `json:"actor_name"`
		ConfirmedPriceCents int64  `json:"confirmed_price_cents"`
		PreviousSeed        struct {
			SeedID         string          `json:"seed_id"`
			Classification string          `json:"classification"`
			SourceID       string          `json:"source_id"`
			SourceFile     string          `json:"source_file"`
			SourceSheet    string          `json:"source_sheet"`
			SourceRow      int             `json:"source_row"`
			Detail         json.RawMessage `json:"detail"`
		} `json:"previous_seed"`
	}
	if err := json.Unmarshal(data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Kind != "manual" || detail.ActorID != actor.ID || detail.ActorName != actor.Name || detail.ConfirmedPriceCents != 350 {
		t.Fatalf("manual confirmation metadata missing: %+v", detail)
	}
	previous := detail.PreviousSeed
	if previous.SeedID != provenance.seedID || previous.Classification != "test_only" || previous.SourceID != provenance.sourceID || previous.SourceFile != provenance.sourceFile || previous.SourceSheet != provenance.sourceSheet || previous.SourceRow != provenance.sourceRow || string(previous.Detail) != provenance.detail {
		t.Fatalf("original seed provenance not retained: %+v", previous)
	}
}
