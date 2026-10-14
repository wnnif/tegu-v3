package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTicketPrintSnapshotKeepsSavedCustomerFactsAndOldOmissions(t *testing.T) {
	price := int64(0)
	item := ticketBatchItemView{ItemID: "saved", ItemKind: "line", TotalCents: 0, Quantity: 1.234567, Unit: "平方", ticketPrintFields: ticketPrintFields{Category: "维修", QtyMicro: "1234567", PriceCents: &price, Segments: `[{"surface":"墙面","length":1,"width":1.234567}]`, Region: "原部位", ChargeMode: "free", ChargeNote: "已保存免收说明"}}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var restored ticketBatchItemView
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.QtyMicro != "1234567" || restored.PriceCents == nil || *restored.PriceCents != 0 || restored.Segments != item.Segments || restored.ChargeMode != "free" || restored.Region != item.Region {
		t.Fatalf("print facts changed: %+v", restored)
	}
	for _, forbidden := range []string{"wage_cents", "labor_cents", "penalty", "inventory_value"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("customer snapshot leaked", forbidden)
		}
	}
	var old ticketBatchItemView
	if err = json.Unmarshal([]byte(`{"item_id":"old","item_kind":"line","quantity":2,"unit":"平方","total_cents":100}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.PriceCents != nil || old.QtyMicro != "" || old.Segments != "" || old.ChargeMode != "" {
		t.Fatalf("old missing facts were fabricated: %+v", old)
	}
}

func TestTicketSelectionRequiresWholeRowIdentity(t *testing.T) {
	var selected ticketSelection
	if err := json.Unmarshal([]byte(`{"item_id":"line-a","item_kind":"line","order_version":3}`), &selected); err != nil {
		t.Fatal("valid row selection rejected:", err)
	}
	if selected.ItemID != "line-a" || selected.ItemKind != "line" || selected.OrderVersion != 3 {
		t.Fatalf("selection identity changed: %+v", selected)
	}
	if err := json.Unmarshal([]byte(`{"item_id":"line-a","item_kind":"line","order_version":3,"quantity":0.5}`), &selected); err == nil {
		t.Fatal("partial-quantity selection was accepted")
	}
	if err := json.Unmarshal([]byte(`{"item_id":"line-a","item_kind":"line","order_version":3,"total_cents":1}`), &selected); err == nil {
		t.Fatal("client-provided amount was accepted")
	}
	for _, input := range []string{`{"item_id":"line-a","item_kind":"line"}`, `{"item_id":"line-a","item_kind":"line","order_version":0}`, `{"item_id":"line-a","item_kind":"line","order_version":-1}`} {
		if err := json.Unmarshal([]byte(input), &selected); err == nil {
			t.Fatal("missing or invalid preview version was accepted:", input)
		}
	}
	if _, err := validateTicketSelection([]ticketSelection{{ItemID: "line-a", ItemKind: "line"}}); err == nil {
		t.Fatal("programmatic selection without preview version was accepted")
	}
	if _, err := validateTicketSelection([]ticketSelection{{ItemID: "line-a", ItemKind: "line", OrderVersion: 1}, {ItemID: "line-a", ItemKind: "line", OrderVersion: 2}}); err == nil {
		t.Fatal("duplicate exact row was accepted")
	}
	items, err := validateTicketSelection([]ticketSelection{{ItemID: "sale-a", ItemKind: "sale", OrderVersion: 2}, {ItemID: "line-b", ItemKind: "line", OrderVersion: 1}})
	if err != nil {
		t.Fatal("distinct complete rows rejected:", err)
	}
	if items[0].ItemKind != "line" || items[1].ItemKind != "sale" {
		t.Fatalf("rows were not normalized for stable locking: %+v", items)
	}
}

func TestTicketBatchTotalKeepsFreeRowsWithoutCharging(t *testing.T) {
	total, err := ticketBatchTotal([]ticketBatchItemView{
		{ItemID: "free-line", TotalCents: 0},
		{ItemID: "charged-line", TotalCents: 12550},
	})
	if err != nil || total != 12550 {
		t.Fatalf("zero-charge row changed batch amount: total=%d err=%v", total, err)
	}
	if ticketIssueAmountValid(-1) || !ticketIssueAmountValid(0) || !ticketIssueAmountValid(maxTicketBatchCents) || ticketIssueAmountValid(maxTicketBatchCents+1) {
		t.Fatal("ticket amount range or zero-charge rule is incorrect")
	}
	if _, err = ticketBatchTotal([]ticketBatchItemView{{TotalCents: maxTicketBatchCents}, {TotalCents: 1}}); err == nil {
		t.Fatal("overflowing batch total was accepted")
	}
}

func TestTicketReminderDistinguishesNoWorkPendingDeliveredAndMixed(t *testing.T) {
	cases := []struct {
		name             string
		total, delivered int64
		want             string
	}{
		{name: "no work", total: 0, delivered: 0, want: "no_work"},
		{name: "pending", total: 3, delivered: 0, want: "pending"},
		{name: "delivered", total: 2, delivered: 2, want: "delivered"},
		{name: "mixed", total: 4, delivered: 1, want: "mixed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ticketReminderStatus(tc.total, tc.delivered); got != tc.want {
				t.Fatalf("status=%q want %q", got, tc.want)
			}
		})
	}
}

func TestTicketIssueRequiresFinanceAndCustomerPricePermissions(t *testing.T) {
	cases := []struct {
		name string
		user User
		want bool
	}{
		{name: "finance only", user: User{Role: "employee", Permissions: []string{"finance"}}},
		{name: "customer prices only", user: User{Role: "employee", Permissions: []string{customerPricePermission}}},
		{name: "both", user: User{Role: "employee", Permissions: []string{"finance", customerPricePermission}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ticketIssuePermission(tc.user) == nil
			if got != tc.want {
				t.Fatalf("permission result=%v want %v", got, tc.want)
			}
		})
	}
}

func TestTicketSourceRequiresExactUniqueReceivableIdentity(t *testing.T) {
	ref := ticketOrderRef{Kind: "line", ItemID: "line", OrderID: "order", CompanyID: "company", ProjectID: "project"}
	valid := ticketSourceRef{TicketID: "ticket", Status: "草稿", OrderID: ref.OrderID, CompanyID: ref.CompanyID, ProjectID: ref.ProjectID}
	if sourceID, status, err := exactTicketSource(ref, []ticketSourceRef{valid}); err != nil || sourceID != valid.TicketID || status != valid.Status {
		t.Fatalf("exact unique source rejected: id=%q status=%q err=%v", sourceID, status, err)
	}
	cases := []struct {
		name    string
		sources []ticketSourceRef
	}{
		{name: "missing"},
		{name: "duplicate", sources: []ticketSourceRef{valid, valid}},
	}
	for _, field := range []string{"ticket", "status", "order", "company", "project"} {
		changed := valid
		switch field {
		case "ticket":
			changed.TicketID = ""
		case "status":
			changed.Status = ""
		case "order":
			changed.OrderID = "other-order"
		case "company":
			changed.CompanyID = "other-company"
		case "project":
			changed.ProjectID = "other-project"
		}
		cases = append(cases, struct {
			name    string
			sources []ticketSourceRef
		}{name: field, sources: []ticketSourceRef{changed}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := exactTicketSource(ref, tc.sources); err == nil {
				t.Fatal("invalid receivable source was accepted")
			}
		})
	}
}

func TestTicketRemindersKeepChangedConfigurationAsSeparateActualWork(t *testing.T) {
	projects := []ticketReminderProject{{ID: "project", Name: "工地", Address: "地址"}}
	crafts := []ticketReminderCraft{{ID: "craft-current", Name: "防水", Subitems: `[{"id":"part-current","name":"厨房"}]`}}
	actual := ticketReminderWork{ProjectID: "project", CraftID: "craft-old", CraftName: "防水", SubitemID: "part-old", SubitemName: "厨房", Count: ticketWorkCount{Total: 3, Delivered: 1}}
	counts := map[string]ticketReminderWork{ticketReminderKey(actual.ProjectID, actual.CraftID, actual.SubitemID): actual}
	reminders, err := buildTicketReminders(crafts, projects, counts)
	if err != nil || len(reminders) != 2 {
		t.Fatalf("expected current configuration and changed actual identity, got %+v err=%v", reminders, err)
	}
	current, changed := reminders[0], reminders[1]
	if current.Status != "no_work" || current.Unmapped || current.TotalCount != 0 {
		t.Fatalf("same display names must not map distinct identities: %+v", current)
	}
	if !changed.Unmapped || changed.CraftID != actual.CraftID || changed.SubitemID != actual.SubitemID || changed.Status != "mixed" || changed.PendingCount != 2 || changed.DeliveredCount != 1 || changed.TotalCount != 3 {
		t.Fatalf("saved work or delivery counts were lost after configuration change: %+v", changed)
	}
	matched := ticketReminderWork{ProjectID: "project", CraftID: "craft-current", SubitemID: "part-current", Count: ticketWorkCount{Total: 2, Delivered: 2}}
	counts[ticketReminderKey(matched.ProjectID, matched.CraftID, matched.SubitemID)] = matched
	reminders, err = buildTicketReminders(crafts, projects, counts)
	if err != nil || reminders[0].Status != "delivered" || reminders[0].Unmapped || len(reminders) != 2 {
		t.Fatalf("exact configuration match must stay distinct from changed work: %+v err=%v", reminders, err)
	}
}

func TestTicketBatchHistoryPageIsBoundedIndependently(t *testing.T) {
	page, size, err := ticketBatchPage("", "")
	if err != nil || page != 1 || size != 20 {
		t.Fatalf("unexpected history default page=%d size=%d err=%v", page, size, err)
	}
	page, size, err = ticketBatchPage("3", "100")
	if err != nil || page != 3 || size != 100 {
		t.Fatalf("valid bounded history page rejected: page=%d size=%d err=%v", page, size, err)
	}
	for _, input := range [][2]string{{"0", "20"}, {"1", "101"}, {"1", "0"}, {"bad", "20"}} {
		if _, _, err = ticketBatchPage(input[0], input[1]); err == nil {
			t.Fatalf("invalid history pagination accepted: %v", input)
		}
	}
	_, size, err = ticketPage("", "")
	if err != nil || size != 100 {
		t.Fatalf("batch pagination changed candidate default: size=%d err=%v", size, err)
	}
}
