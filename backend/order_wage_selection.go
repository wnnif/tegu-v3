package main

func effectiveOrderLineWage(subitemID string, craftWage int64, companyOverride, legacyCompanyOverride *int64, subitems []CraftSubitem) (string, int64, error) {
	var selected *CraftSubitem
	if subitemID != "" {
		for i := range subitems {
			if subitems[i].ID == subitemID {
				selected = &subitems[i]
				break
			}
		}
		if selected == nil {
			return "", 0, fail(400, "工艺小项已调整，请重新打开制单")
		}
	}
	name := ""
	if selected != nil {
		name = selected.Name
	}
	if companyOverride != nil {
		return name, *companyOverride, nil
	}
	if legacyCompanyOverride != nil {
		return name, *legacyCompanyOverride, nil
	}
	if selected != nil && selected.WageCents != nil {
		return name, *selected.WageCents, nil
	}
	return name, craftWage, nil
}
