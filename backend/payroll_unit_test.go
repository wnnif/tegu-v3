package main

import (
	"strings"
	"testing"
)

func TestValidatePayrollSettlementProjectScope(t *testing.T) {
	for _, tt := range []struct {
		name       string
		projectIDs []string
		wantError  bool
	}{
		{name: "same project", projectIDs: []string{"project-a", "project-a"}},
		{name: "all unlinked sources", projectIDs: []string{"", ""}},
		{name: "single source", projectIDs: []string{"project-a"}},
		{name: "unlinked and linked sources", projectIDs: []string{"", "project-a"}, wantError: true},
		{name: "mixed linked projects", projectIDs: []string{"project-a", "project-b"}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePayrollSettlementProjectScope(tt.projectIDs)
			if (err != nil) != tt.wantError {
				t.Fatalf("scope error = %v, want error %v", err, tt.wantError)
			}
			if tt.wantError {
				if f, ok := err.(failure); !ok || f.Code != 400 || !strings.Contains(f.Message, "请按工地或未关联范围拆分") {
					t.Fatalf("mixed-worksite error = %v, want actionable 400", err)
				}
			}
		})
	}
}

func TestPayrollSupplementAmount(t *testing.T) {
	for _, tt := range []struct {
		name      string
		amount    int64
		wantError bool
	}{
		{name: "positive supplement", amount: 1},
		{name: "zero adjustment", amount: 0, wantError: true},
		{name: "negative adjustment must use confirmed deduction", amount: -1, wantError: true},
		{name: "amount above limit", amount: maxPayrollCents + 1, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := payrollSupplementAmount(tt.amount)
			if (err != nil) != tt.wantError {
				t.Fatalf("supplement error = %v, want error %v", err, tt.wantError)
			}
			if tt.name == "negative adjustment must use confirmed deduction" {
				if f, ok := err.(failure); !ok || f.Code != 400 || !strings.Contains(f.Message, "扣款依据") {
					t.Fatalf("negative supplement error = %v, want confirmed-deduction guidance", err)
				}
			}
		})
	}
}

func TestPayrollActionRequiresWagePermissionBeforeDecode(t *testing.T) {
	_, err := (&App{}).payrollAction(nil, User{Role: "employee"}, []byte("not-json"), "permission-denied")
	if f, ok := err.(failure); !ok || f.Code != 403 {
		t.Fatalf("unauthorized payroll write = %v, want 403 before request decoding", err)
	}
}

func TestPayrollActionRouteChecksPermissionBeforeIdempotentReplay(t *testing.T) {
	if err := customerPriceMutationAllowed("/api/payroll-action", User{Role: "employee"}, []byte(`{}`)); err == nil {
		t.Fatal("revoked payroll permission passed the route preflight")
	} else if f, ok := err.(failure); !ok || f.Code != 403 {
		t.Fatalf("revoked payroll permission preflight = %v, want 403", err)
	}
	if err := customerPriceMutationAllowed("/api/payroll-action", User{Role: "employee", Permissions: []string{"wages"}}, []byte(`{}`)); err != nil {
		t.Fatalf("authorized payroll permission preflight = %v, want success", err)
	}
}

func TestCalculatePayrollPayable(t *testing.T) {
	tests := []struct {
		name           string
		gross          int64
		adjustment     int64
		deduction      int64
		advance        int64
		hasPayrollSource bool
		want           int64
		wantError      bool
	}{
		{name: "example remaining wage", gross: 100000, deduction: 5000, advance: 20000, hasPayrollSource: true, want: 75000},
		{name: "advance fully offsets wage", gross: 100000, advance: 100000, hasPayrollSource: true, want: 0},
		{name: "deduction fully offsets wage", gross: 100000, deduction: 100000, hasPayrollSource: true, want: 0},
		{name: "adjustment and deduction net to zero", gross: 100000, adjustment: 5000, deduction: 105000, hasPayrollSource: true, want: 0},
		{name: "negative final balance rejected", gross: 100000, deduction: 100001, hasPayrollSource: true, wantError: true},
		{name: "advance above positive source rejected", gross: 100000, deduction: 5000, advance: 95001, hasPayrollSource: true, wantError: true},
		{name: "advance only is not a settlement", advance: 1, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculatePayrollPayable(tt.gross, tt.adjustment, tt.deduction, tt.advance, tt.hasPayrollSource)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, want error %v", err, tt.wantError)
			}
			if err == nil && got != tt.want {
				t.Fatalf("payable = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLegacyWageSettlementHandlerDisabled(t *testing.T) {
	_, err := (&App{}).settleWorkerWages(nil, User{Role: "admin"}, nil, "")
	f, ok := err.(failure)
	if !ok || f.Code != 409 {
		t.Fatalf("legacy wage handler returned %v, want 409", err)
	}
}
