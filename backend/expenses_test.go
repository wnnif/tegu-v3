package main

import (
	"errors"
	"testing"
)

func TestExpenseClaimDisplayStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		claim   string
		amount  int64
		applied int64
		paid    int64
		due     int64
		want    string
	}{
		{name: "pending", claim: "pending", amount: 20000, due: 20000, want: "待审核"},
		{name: "confirmed", claim: "confirmed", amount: 20000, due: 20000, want: "已确认"},
		{name: "partially paid", claim: "confirmed", amount: 20000, paid: 5000, due: 15000, want: "部分报销"},
		{name: "partially advanced", claim: "confirmed", amount: 20000, applied: 5000, due: 15000, want: "部分报销"},
		{name: "paid in full", claim: "confirmed", amount: 20000, paid: 20000, due: 0, want: "已付清"},
		{name: "advance plus supplemental payment", claim: "confirmed", amount: 120000, applied: 100000, paid: 20000, due: 0, want: "已付清"},
		{name: "fully covered by advance", claim: "confirmed", amount: 20000, applied: 20000, due: 0, want: "预支已核销"},
		{name: "voided", claim: "voided", amount: 20000, due: 20000, want: "已撤销"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := expenseClaimDisplayStatus(test.claim, test.amount, test.applied, test.paid, test.due); got != test.want {
				t.Fatalf("display status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExpensePersonMatchUsesTrimmedExactName(t *testing.T) {
	if normalizeExpensePerson("  张三   ") != normalizeExpensePerson("张三") {
		t.Fatal("outer and repeated whitespace should normalize")
	}
	if normalizeExpensePerson("张三") == normalizeExpensePerson("李三") {
		t.Fatal("different names must not be treated as the same recipient")
	}
	if normalizeExpensePerson("Alice") == normalizeExpensePerson("alice") {
		t.Fatal("case-different names must remain distinct")
	}
}

func TestExpenseActionRequiresPermissionBeforeDecode(t *testing.T) {
	_, err := (&App{}).expenseAction(nil, User{Role: "employee"}, []byte("not-json"), "expense-denied")
	var f failure
	if !errors.As(err, &f) || f.Code != 403 {
		t.Fatalf("unauthorized expense write = %v, want 403 before body decoding", err)
	}
}

func TestExpenseValidation(t *testing.T) {
	if err := expensePositiveAmount(1); err != nil {
		t.Fatalf("one cent rejected: %v", err)
	}
	for _, amount := range []int64{0, -1, maxExpenseCents + 1} {
		if err := expensePositiveAmount(amount); err == nil {
			t.Errorf("invalid expense amount %d accepted", amount)
		}
	}
	if _, err := expensePersonName("  张三 "); err != nil {
		t.Fatalf("valid claimant rejected: %v", err)
	}
	if _, err := expensePersonName("  "); err == nil {
		t.Fatal("blank claimant accepted")
	}
	if err := expenseDate("2026-02-30", "费用日期"); err == nil {
		t.Fatal("invalid calendar date accepted")
	}
}
