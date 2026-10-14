package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
)

// A missing field preserves the company setting; an explicit [] resets the
// display order. Category identity is the exact canonical registry key, never a
// craft name, source ID or inferred waterproof/other grouping.
func parseCompanyCraftCategoryOrder(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var order []string
	if err := json.Unmarshal(raw, &order); err != nil || order == nil || len(order) > len(categories) {
		return nil, fail(400, "工艺大类排序须为有效的大类数组，恢复默认请提交空数组")
	}
	seen := make(map[string]bool, len(order))
	for _, category := range order {
		if !contains(categories, category) || seen[category] {
			return nil, fail(400, "工艺大类排序不能包含重复或未配置的大类")
		}
		seen[category] = true
	}
	return order, nil
}

// Nullable rows and snapshots made before migration 438 use the default display
// order. API reads always expose an array; invalid stored data is not silently
// replaced with a default or used to rewrite historical work.
func readCompanyCraftCategoryOrder(raw []byte) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []string{}, nil
	}
	order, err := parseCompanyCraftCategoryOrder(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid stored company craft category order: %v", err)
	}
	return order, nil
}

// Version 438 runs in initSchema's existing database-scoped migration lock.
// Additive and resumable: no historical UPDATE, category guesses, price/wage
// recalculation, permission changes or non-null backfill.
func migrateCompanyCraftCategoryOrder(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='companies' AND COLUMN_NAME='craft_category_order'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE companies ADD COLUMN craft_category_order JSON NULL"); err != nil {
			return err
		}
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(438)")
	return err
}
