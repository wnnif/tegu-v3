package main

import "testing"

func TestAutomaticBonusRulesMatchOnlyApprovedIdentities(t *testing.T) {
	approved := []struct {
		category, craftID, name, subitemID, subitem, key string
		rate                                             int64
	}{
		{"防水涂料", "craft-v3-waterproof-split-two-coats", "分开两遍防水工艺", "", "", "waterproof_split_two_coats", 50},
		{"开工", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-wall", "墙", "fine_start_wall_ground_wall", 15},
		{"防水涂料", "", "分开刷连二遍防水涂料", "", "", "waterproof_split_two_coats", 50},
		{"开工", "", "精开工·墙地固", "", "墙", "fine_start_wall_ground_wall", 15},
	}
	for _, tc := range approved {
		rule, ok := automaticBonusRule(tc.category, tc.craftID, tc.name, tc.subitemID, tc.subitem, "平方", false)
		if !ok || rule.Key != tc.key || rule.RateCents != tc.rate {
			t.Errorf("automaticBonusRule(%q, %q, %q, %q) = (%+v, %t), want key %q rate %d", tc.category, tc.craftID, tc.name, tc.subitem, rule, ok, tc.key, tc.rate)
		}
	}
	unapproved := []struct {
		category, craftID, name, subitemID, subitem, mode string
		temporary                                         bool
	}{
		{"丙纶", "", "普通丙纶", "", "", "平方", false},
		{"开工", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-ceiling", "顶", "平方", false},
		{"开工", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-floor", "地", "平方", false},
		{"开工", "craft-v3-fine-slot-moisture", "精开工·线槽防潮加固", "fine-living-slots", "卧室客厅槽子＋门边框＋地角", "平方", false},
		{"开工", "craft-v3-fine-marking", "精开工·弹线喷字", "fine-marking-five-lines", "标准五线弹线喷字", "平方", false},
		{"开工", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-wall", "墙", "金额", false},
		{"开工", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-wall", "墙", "平方", true},
		{"开工", "", "墙地固", "", "墙", "平方", false},
		{"其它", "craft-v3-fine-wall-ground", "精开工·墙地固", "fine-wall", "墙", "平方", false},
	}
	for _, tc := range unapproved {
		if rule, ok := automaticBonusRule(tc.category, tc.craftID, tc.name, tc.subitemID, tc.subitem, tc.mode, tc.temporary); ok {
			t.Errorf("unexpected automatic rule for %+v: %+v", tc, rule)
		}
	}
}

func TestBonusAmountUsesIntegerCentRounding(t *testing.T) {
	got, err := bonusAmountCents(1_500_000, 15)
	if err != nil || got != 23 {
		t.Fatalf("bonusAmountCents(1.5 square meters, 0.15 yuan) = %d, %v; want 23 cents", got, err)
	}
	got, err = bonusAmountCents(1_500_000, 50)
	if err != nil || got != 75 {
		t.Fatalf("bonusAmountCents(1.5 square meters, 0.50 yuan) = %d, %v; want 75 cents", got, err)
	}
	if _, err := bonusAmountCents(0, 15); err == nil {
		t.Fatal("zero quantity must not create a bonus entry")
	}
}

func TestAutomaticBonusDeltaTracksAccrualCorrectionAndCancellation(t *testing.T) {
	created, err := automaticBonusDelta(1000, 0)
	if err != nil || created != 1000 || automaticBonusDeltaReason(created, true, 0) != "有效施工记录自动计提奖金" {
		t.Fatalf("initial accrual = %d, %v", created, err)
	}

	corrected, err := automaticBonusDelta(700, 1000)
	if err != nil || corrected != -300 || automaticBonusDeltaReason(corrected, true, 1000) != "施工记录更正，按差额调整自动奖金" {
		t.Fatalf("correction delta = %d, %v", corrected, err)
	}
	unchanged, err := automaticBonusDelta(700, 700)
	if err != nil || unchanged != 0 {
		t.Fatalf("saving the same effective record must not accrue twice: %d, %v", unchanged, err)
	}

	cancelled, err := automaticBonusDelta(0, 700)
	if err != nil || cancelled != -700 || automaticBonusDeltaReason(cancelled, false, 700) != "施工记录取消，自动冲减奖金" {
		t.Fatalf("cancellation delta = %d, %v", cancelled, err)
	}

	net, err := addPayrollCents(created, corrected)
	if err != nil {
		t.Fatal(err)
	}
	net, err = addPayrollCents(net, cancelled)
	if err != nil || net != 0 {
		t.Fatalf("accrual, correction, and cancellation should net to zero, got %d, %v", net, err)
	}
}

func TestAutomaticBonusWorkYearBoundaryCreatesSeparateSources(t *testing.T) {
	oldYear, err := bonusWorkYear("2025-12-31")
	if err != nil || oldYear != 2025 {
		t.Fatalf("year for prior work date = %d, %v", oldYear, err)
	}
	newYear, err := bonusWorkYear("2026-01-01")
	if err != nil || newYear != 2026 {
		t.Fatalf("year for corrected work date = %d, %v", newYear, err)
	}
	if _, err = bonusWorkYear("2026-02-30"); err == nil {
		t.Fatal("invalid saved work date must not silently select a bonus year")
	}

	oldSource := bonusSourceKey("order", "line", "worker", oldYear, "waterproof_split_two_coats")
	newSource := bonusSourceKey("order", "line", "worker", newYear, "waterproof_split_two_coats")
	if oldSource == newSource {
		t.Fatal("a work-date year change must reverse the old yearly source and accrue into the new year")
	}
	oldAmount, _ := bonusAmountCents(10_000_000, 50)
	newAmount, _ := bonusAmountCents(8_000_000, 50)
	oldYearDelta, err := automaticBonusDelta(0, oldAmount)
	if err != nil || oldYearDelta != -500 {
		t.Fatalf("prior year reversal = %d, %v", oldYearDelta, err)
	}
	newYearDelta, err := automaticBonusDelta(newAmount, 0)
	if err != nil || newYearDelta != 400 {
		t.Fatalf("new year accrual = %d, %v", newYearDelta, err)
	}
}

func TestBonusBalancesAndSettlementRemainSeparateFromWages(t *testing.T) {
	wageBalance, err := calculatePayrollPayable(200_000, 0, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	bonusBalance, err := bonusBalanceCents(5_000, 1_000, 2_000)
	if err != nil || bonusBalance != 4_000 {
		t.Fatalf("bonus balance should use only bonus accrual, adjustment, and payment: %d, %v", bonusBalance, err)
	}
	if wageBalance != 200_000 || wageBalance == bonusBalance {
		t.Fatalf("wage and bonus balances must remain independent: wages=%d bonus=%d", wageBalance, bonusBalance)
	}

	available, err := bonusAvailableCents(10_000, 2_000, 3_000)
	if err != nil || available != 9_000 {
		t.Fatalf("unsettled bonus available = %d, %v", available, err)
	}
	if err = bonusSettlementAmount(available, available); err != nil {
		t.Fatalf("settling the full available bonus should be allowed: %v", err)
	}
	if err = bonusSettlementAmount(available, available+1); err == nil {
		t.Fatal("settlement above the bonus balance must be rejected")
	}
	available, err = bonusAvailableCents(10_000, 2_000, 7_000)
	if err != nil || available != 5_000 {
		t.Fatalf("partially settling 4000 cents should leave 5000 unsettled, got %d, %v", available, err)
	}
	if err = bonusSettlementAmount(available, 5_001); err == nil {
		t.Fatal("a second settlement above the remaining bonus must be rejected")
	}
}

func TestBonusChangesCannotOverdrawConfirmedSettlements(t *testing.T) {
	summary := bonusSummary{BonusAccruedCents: 10_000, BonusAdjustmentCents: 500, BonusSettledCents: 8_000}
	if err := validateBonusChangeAgainstSettlements(summary, -2_500, 0); err != nil {
		t.Fatalf("correction that leaves zero unsettled bonus should be allowed: %v", err)
	}
	if err := validateBonusChangeAgainstSettlements(summary, -2_501, 0); err == nil {
		t.Fatal("automatic correction must not reduce annual bonus below confirmed settlements")
	}
	if err := validateBonusChangeAgainstSettlements(summary, 0, -2_501); err == nil {
		t.Fatal("manual negative adjustment must not reduce annual bonus below confirmed settlements")
	}
	if err := validateBonusChangeAgainstSettlements(summary, 2_000, 0); err != nil {
		t.Fatalf("positive automatic bonus correction should remain available: %v", err)
	}
}

func TestBonusPartialPaymentBalanceAndOverpayment(t *testing.T) {
	remaining, err := bonusPaymentRemaining(6_000, 2_000)
	if err != nil || remaining != 4_000 {
		t.Fatalf("partial payment remaining = %d, %v", remaining, err)
	}
	remaining, err = validateBonusPaymentAmount(6_000, 2_000, 2_500)
	if err != nil || remaining != 1_500 {
		t.Fatalf("second partial payment should leave 1500 cents, got %d, %v", remaining, err)
	}
	if _, err = validateBonusPaymentAmount(6_000, 2_000, 4_001); err == nil {
		t.Fatal("payment above the settlement's remaining amount must be rejected")
	}
	if _, err = validateBonusPaymentAmount(6_000, 6_000, 1); err == nil {
		t.Fatal("payment after the settlement is fully paid must be rejected")
	}
	remaining, err = bonusPaymentRemaining(6_000, 0)
	if err != nil || remaining != 6_000 {
		t.Fatalf("reversing all prior payments should restore the full payment balance: %d, %v", remaining, err)
	}
}
