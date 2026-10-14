package main

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func TestCompanyWageOverrideMigrationAndBackupRegistration(t *testing.T) {
	if companyWageOverrideMigrationVersion != 453 {
		t.Fatalf("company wage override migration version = %d, want 453", companyWageOverrideMigrationVersion)
	}
	if !contains(backupTables, "company_wage_overrides") {
		t.Fatal("company wage overrides must be included in backup and restore")
	}
	if got := companyWageOverrideProvenanceID("东箭", "craft-poly"); len(got) != 64 {
		t.Fatalf("provenance key length = %d, want SHA-256 hex", len(got))
	}
	first := companyWageOverrideProvenanceID("东箭", "craft-poly")
	if got := companyWageOverrideProvenanceID("东箭", "craft-poly"); got != first {
		t.Fatal("provenance key must be deterministic")
	}
	if first == companyWageOverrideProvenanceID("宜仁", "craft-poly") || first == companyWageOverrideProvenanceID("东箭", "craft-other") {
		t.Fatal("provenance key must distinguish company and craft pairs")
	}
}

func TestCompanyCraftWageOverridePresenceAndValidation(t *testing.T) {
	if present, value, err := parseCompanyCraftWageOverride(nil); err != nil || present || value != nil {
		t.Fatalf("omitted field must mean preserve: present=%v value=%v err=%v", present, value, err)
	}
	if present, value, err := parseCompanyCraftWageOverride(json.RawMessage("null")); err != nil || !present || value != nil {
		t.Fatalf("null must mean inherit: present=%v value=%v err=%v", present, value, err)
	}
	for _, raw := range []string{"550", "\"550\"", "0"} {
		present, value, err := parseCompanyCraftWageOverride(json.RawMessage(raw))
		if err != nil || !present || value == nil {
			t.Fatalf("valid cents override rejected: %s: %v", raw, err)
		}
		want := int64(550)
		if raw == "0" {
			want = 0
		}
		if *value != want {
			t.Fatalf("override %s became %d, want %d", raw, *value, want)
		}
	}
	for _, raw := range []string{"-1", "100000001", "1.5", "true", "{}"} {
		if _, _, err := parseCompanyCraftWageOverride(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid company wage override accepted: %s", raw)
		}
	}
}

func TestCompanyCraftWageOverrideAuthorizationAndPreservation(t *testing.T) {
	stored := sql.NullInt64{Int64: 550, Valid: true}
	previous := savedCompanyCraft{legacyWageOverride: stored}
	priceOnly := User{Role: "employee", Permissions: []string{"customers", "customer_prices"}}
	item := companyCraftInput{WageOverrideCents: json.RawMessage("0")}
	if err := protectCompanyCraftWageOverride(priceOnly, &item, previous, true); err != nil {
		t.Fatal(err)
	}
	if item.wageOverrideChange != companyWageOverridePreserve || item.legacyWageOverrideValue == nil || *item.legacyWageOverrideValue != 550 {
		t.Fatalf("customer-price-only edit changed special wage: change=%d legacy=%v", item.wageOverrideChange, item.legacyWageOverrideValue)
	}
	item = companyCraftInput{}
	if err := protectCompanyCraftWageOverride(priceOnly, &item, previous, true); err != nil || item.wageOverrideChange != companyWageOverridePreserve || item.legacyWageOverrideValue == nil || *item.legacyWageOverrideValue != 550 {
		t.Fatalf("ordinary company edit did not retain omitted special wage: change=%d legacy=%v %v", item.wageOverrideChange, item.legacyWageOverrideValue, err)
	}
	normalized := savedCompanyCraft{companyWageOverride: stored}
	item = companyCraftInput{}
	if err := protectCompanyCraftWageOverride(User{Role: "employee", Permissions: []string{"customers", "rates"}}, &item, normalized, true); err != nil || item.wageOverrideChange != companyWageOverridePreserve {
		t.Fatalf("omitted field must preserve normalized override: change=%d err=%v", item.wageOverrideChange, err)
	}
	item = companyCraftInput{WageOverrideCents: json.RawMessage("null")}
	if err := protectCompanyCraftWageOverride(User{Role: "employee", Permissions: []string{"customers", "rates"}}, &item, normalized, true); err != nil || item.wageOverrideChange != companyWageOverrideClear || item.legacyWageOverrideValue != nil {
		t.Fatalf("rates editor could not clear override to inherited wage: change=%d legacy=%v err=%v", item.wageOverrideChange, item.legacyWageOverrideValue, err)
	}
	item = companyCraftInput{WageOverrideCents: json.RawMessage("0")}
	if err := protectCompanyCraftWageOverride(User{Role: "employee", Permissions: []string{"customers", "wages"}}, &item, normalized, true); err != nil || item.wageOverrideChange != companyWageOverrideSet || item.wageOverrideValue == nil || *item.wageOverrideValue != 0 || item.legacyWageOverrideValue != nil {
		t.Fatalf("explicit zero wage override was not stored distinctly: change=%d value=%v legacy=%v err=%v", item.wageOverrideChange, item.wageOverrideValue, item.legacyWageOverrideValue, err)
	}
	item = companyCraftInput{WageOverrideCents: json.RawMessage("550")}
	if err := protectCompanyCraftWageOverride(priceOnly, &item, savedCompanyCraft{}, false); err != nil || item.wageOverrideChange != companyWageOverridePreserve || item.wageOverrideValue != nil {
		t.Fatalf("new unauthorized fixed craft accepted an override: change=%d value=%v err=%v", item.wageOverrideChange, item.wageOverrideValue, err)
	}
}

func TestEffectiveCompanyCraftWageOverrideUsesNormalizedThenLegacy(t *testing.T) {
	legacy := sql.NullInt64{Int64: 525, Valid: true}
	if got := effectiveCompanyWageOverride(legacy, sql.NullInt64{}); !got.Valid || got.Int64 != 525 {
		t.Fatalf("legacy override was not retained as fallback: %+v", got)
	}
	independent := sql.NullInt64{Int64: 550, Valid: true}
	if got := effectiveCompanyWageOverride(legacy, independent); !got.Valid || got.Int64 != 550 {
		t.Fatalf("normalized override did not take precedence: %+v", got)
	}
	zero := sql.NullInt64{Int64: 0, Valid: true}
	if got := effectiveCompanyWageOverride(legacy, zero); !got.Valid || got.Int64 != 0 {
		t.Fatalf("explicit zero override was treated as inheritance: %+v", got)
	}
}
