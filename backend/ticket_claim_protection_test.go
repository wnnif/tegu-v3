package main

import "testing"

func TestTicketClaimProtectionIsPerActualRow(t *testing.T) {
	claimed := Line{ID: "line-claimed", CraftID: "craft", Name: "防水", Mode: "平方", Segments: []Segment{{Length: 2, Width: 3}}, Qty: 6000000, Price: 1000, Total: 6000, ChargeMode: "normal", ticketBatchID: "batch-a"}
	other := Line{ID: "line-other", CraftID: "craft", Name: "防水", Mode: "平方", Segments: []Segment{{Length: 1, Width: 2}}, Qty: 2000000, Price: 1000, Total: 2000, ChargeMode: "normal"}
	if !sameTicketClaimedLine([]Line{claimed, other}, claimed) {
		t.Fatal("unchanged claimed row should remain valid alongside another row")
	}
	changed := claimed
	changed.Worker = "another-worker"
	if sameTicketClaimedLine([]Line{changed, other}, claimed) {
		t.Fatal("a claimed construction row cannot change its worker")
	}
	changed = claimed
	changed.Qty++
	if sameTicketClaimedLine([]Line{changed, other}, claimed) {
		t.Fatal("a changed claimed row must require its owning batch to be voided")
	}
	if !sameTicketClaimedLine([]Line{other}, other) {
		t.Fatal("an unrelated row must remain independently editable")
	}
	if sameTicketClaimedLine([]Line{other}, claimed) {
		t.Fatal("a missing claimed row must not be treated as an unchanged row")
	}
}

func TestTicketClaimProtectionPreservesSalesOwnership(t *testing.T) {
	stored := Sale{ID: "sale-claimed", Product: "product", Warehouse: "warehouse", Qty: 2000000, QtyQuantum: "2000000", SaleQty: 2000000, InputQty: "2", InputUnit: "base", Price: 500, Total: 1000, Unit: "桶", PriceUnit: "桶", SortOrder: 1, ConversionRatio: "1", ticketBatchID: "batch-a"}
	if !sameTicketClaimedSale(stored, stored) {
		t.Fatal("unchanged claimed sale should retain its active owner")
	}
	changed := stored
	changed.Total += 100
	if sameTicketClaimedSale(changed, stored) {
		t.Fatal("a changed claimed sale must require its owning batch to be voided")
	}
	changed = stored
	changed.ticketBatchID = "batch-b"
	if sameTicketClaimedSale(changed, stored) {
		t.Fatal("a claimed sale cannot be reassigned to a different batch")
	}
}

func TestTicketClaimedMaterialChangesAreScopedToOwningRow(t *testing.T) {
	claimed := Material{LineID: "claimed", Product: "product", Warehouse: "warehouse", QtyQuantum: "2000000"}
	other := Material{LineID: "other", Product: "product", Warehouse: "warehouse", QtyQuantum: "1000000"}
	changedOther := other
	changedOther.QtyQuantum = "3000000"
	if !sameTicketClaimedMaterials([]Material{claimed, other}, []Material{claimed, changedOther}, claimed.LineID) {
		t.Fatal("changing another row's materials must not require voiding this row's batch")
	}
	changedClaimed := claimed
	changedClaimed.QtyQuantum = "4000000"
	if sameTicketClaimedMaterials([]Material{claimed, other}, []Material{changedClaimed, other}, claimed.LineID) ||
		sameTicketClaimedMaterials([]Material{claimed, other}, []Material{other}, claimed.LineID) {
		t.Fatal("changing/removing claimed materials must require voiding their own batch")
	}
}

func TestTicketClaimedSaleKeepsSavedOrderAfterOtherSaleIsRemoved(t *testing.T) {
	removed := Sale{ID: "voided-sale", SortOrder: 0}
	claimed := Sale{ID: "valid-sale", SortOrder: 1, ticketBatchID: "valid-batch"}
	previous := map[string]Sale{removed.ID: removed, claimed.ID: claimed}
	current := claimed
	current.SortOrder = orderSaleSortOrder(current, 0, previous)
	if !sameTicketClaimedSale(current, claimed) {
		t.Fatal("removing an earlier voided sale must not renumber another batch's claimed row")
	}
	if sort := orderSaleSortOrder(Sale{}, 2, previous); sort != 2 {
		t.Fatalf("new sale should receive its input position, got %d", sort)
	}
}
