package main

func effectiveOrderLineWage(subitemID string, craftWage int64, companyOverride, legacyCompanyOverride *int64, subitems []CraftSubitem) (string, int64, error) {
	name, wage, _, err := effectiveOrderLineWageStatus(subitemID, craftWage, true, companyOverride, legacyCompanyOverride, subitems)
	return name, wage, err
}

func effectiveOrderLineWageStatus(subitemID string, craftWage int64, craftWageConfigured bool, companyOverride, legacyCompanyOverride *int64, subitems []CraftSubitem) (string, int64, bool, error) {
	var selected *CraftSubitem
	if subitemID != "" {
		for i := range subitems {
			if subitems[i].ID == subitemID {
				selected = &subitems[i]
				break
			}
		}
		if selected == nil {
			return "", 0, false, fail(400, "工艺小项已调整，请重新打开制单")
		}
	}
	name := ""
	if selected != nil {
		name = selected.Name
	}
	if companyOverride != nil {
		return name, *companyOverride, true, nil
	}
	if legacyCompanyOverride != nil {
		return name, *legacyCompanyOverride, true, nil
	}
	if selected != nil && selected.WageCents != nil {
		return name, *selected.WageCents, true, nil
	}
	return name, craftWage, craftWageConfigured, nil
}
