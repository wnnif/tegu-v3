package main

import "reflect"

func sameTicketClaimedLine(lines []Line, stored Line) bool {
	for _, line := range lines {
		if line.ID != stored.ID {
			continue
		}
		return line.ticketBatchID == stored.ticketBatchID && line.CraftID == stored.CraftID &&
			line.Worker == stored.Worker && line.WorkerName == stored.WorkerName &&
			line.Category == stored.Category && line.Name == stored.Name && line.Spec == stored.Spec &&
			line.Region == stored.Region && line.SubitemName == stored.SubitemName &&
			line.ActualLocation == stored.ActualLocation && line.Description == stored.Description &&
			line.Mode == stored.Mode && reflect.DeepEqual(line.Segments, stored.Segments) &&
			line.Qty == stored.Qty && line.Price == stored.Price && line.Wage == stored.Wage &&
			line.Total == stored.Total && line.Labor == stored.Labor && line.Temporary == stored.Temporary &&
			line.SubitemID == stored.SubitemID && line.SortOrder == stored.SortOrder &&
			line.UseIncludedPrice == stored.UseIncludedPrice && line.IncludedByCraftID == stored.IncludedByCraftID &&
			line.ChargeNote == stored.ChargeNote && normalizeChargeMode(line.ChargeMode) == normalizeChargeMode(stored.ChargeMode) &&
			line.WaivedQty == stored.WaivedQty && sameOptionalCents(line.WageOverrideCents, stored.WageOverrideCents) &&
			line.WageAdjustmentReason == stored.WageAdjustmentReason
	}
	return false
}

func sameTicketClaimedSale(current, stored Sale) bool {
	return current.ID == stored.ID && current.ticketBatchID == stored.ticketBatchID &&
		current.Product == stored.Product && current.Warehouse == stored.Warehouse &&
		current.Qty == stored.Qty && current.QtyQuantum == stored.QtyQuantum &&
		current.SaleQty == stored.SaleQty && current.InputQty == stored.InputQty &&
		current.InputUnit == stored.InputUnit && current.Price == stored.Price &&
		current.Total == stored.Total && current.ProductName == stored.ProductName &&
		current.Unit == stored.Unit && current.PriceUnit == stored.PriceUnit &&
		current.SortOrder == stored.SortOrder && current.ConversionRatio == stored.ConversionRatio
}

func sameOptionalCents(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameTicketClaimedMaterials(previous, current []Material, lineID string) bool {
	selectLine := func(materials []Material) []Material {
		result := []Material{}
		for _, material := range materials {
			if material.LineID == lineID {
				result = append(result, material)
			}
		}
		return result
	}
	return sameOrderStock(selectLine(previous), selectLine(current), nil, nil)
}
