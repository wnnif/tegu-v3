package main

// Resolve company IDs through real public sources inside the same read/save
// transaction. Replacing the map prevents a previous company's configuration
// from being reused, and a submitted source_id cannot influence authorization.
func withCompanyCraftSources(q rowQuery, u User, companyID string) (User, error) {
	u.companyCraftSources = map[string]string{}
	if u.Role != "employee" || !u.can("orders") || len(u.Crafts) == 0 {
		return u, nil
	}
	rows, err := q.Query(`SELECT c.id,c.source_id FROM crafts c JOIN crafts p ON p.id=c.source_id AND p.company_id='' WHERE c.company_id=? AND c.company_id<>'' ORDER BY c.id`, companyID)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var fixed, source string
		if err = rows.Scan(&fixed, &source); err != nil {
			return u, err
		}
		u.companyCraftSources[fixed] = source
	}
	return u, rows.Err()
}
