package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
)

type CraftSubitem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	WageCents *int64 `json:"wage_cents,omitempty"`
}

const craftCatalogOrder = "display_order,name,id"

func validCraftMode(mode string) bool {
	return contains([]string{"平方", "立方", "金额", "个", "家", "米", "根", "处", "孔位", "副", "张", "套"}, mode)
}

func countCraftMode(mode string) bool {
	return contains([]string{"个", "家", "根", "处", "孔位", "副", "张", "套"}, mode)
}

func normalizeCraftSubitems(items []CraftSubitem) ([]CraftSubitem, error) {
	if len(items) > 100 {
		return nil, fail(400, "每项工艺最多设置100个小项")
	}
	if items == nil {
		items = []CraftSubitem{}
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for i := range items {
		items[i].ID = strings.TrimSpace(items[i].ID)
		items[i].Name = strings.TrimSpace(items[i].Name)
		if items[i].Name == "" || len([]rune(items[i].Name)) > 100 {
			return nil, fail(400, "工艺小项名称须为1至100字")
		}
		if items[i].ID == "" {
			items[i].ID = id()
		}
		if len(items[i].ID) > 64 || ids[items[i].ID] || names[items[i].Name] {
			return nil, fail(400, "工艺小项编号或名称不能重复")
		}
		if items[i].WageCents != nil && (*items[i].WageCents < 0 || *items[i].WageCents > 100000000) {
			return nil, fail(400, "工艺小项工资须为0至1000000元")
		}
		ids[items[i].ID], names[items[i].Name] = true, true
	}
	return items, nil
}

// Catalog metadata for new work follows its explicit public source. Historical
// order lines keep their own snapshots and never use this projection.
const craftConfigurationSource = `(SELECT c.id,c.company_id,
	IF(p.id IS NULL,c.category,p.category) AS category,
	IF(p.id IS NULL,c.name,p.name) AS name,
	IF(p.id IS NULL,c.mode,p.mode) AS mode,
	IF(c.company_id='',0,c.price_cents) AS price_cents,
	IF(c.company_id='',c.wage_cents,COALESCE(wo.wage_cents,c.wage_override_cents,p.wage_cents,0)) AS wage_cents,
	IF(c.company_id='',c.wage_cents,COALESCE(p.wage_cents,0)) AS public_wage_cents,
	IF(c.company_id='',c.wage_configured,CASE WHEN p.id IS NULL THEN c.wage_configured WHEN wo.company_id IS NOT NULL OR c.wage_override_cents IS NOT NULL THEN 1 ELSE p.wage_configured END) AS wage_configured,
	IF(c.company_id='',NULL,COALESCE(wo.wage_cents,c.wage_override_cents)) AS wage_override_cents,
	c.active,c.version,IF(p.id IS NULL,c.spec,p.spec) AS spec,
	c.source_id,IF(c.company_id='',c.subitems,p.subitems) AS subitems,
	c.ticket_slot,c.included_by_craft_id,
	c.display_order,
	EXISTS(SELECT 1 FROM catalog_seed_provenance cp WHERE cp.target_kind='company_craft' AND cp.target_id=c.id AND cp.component='customer_price' AND cp.test_only=1) AS customer_price_test_only,
	CASE WHEN c.company_id='' THEN EXISTS(SELECT 1 FROM catalog_seed_provenance wp WHERE wp.target_kind='craft' AND wp.target_id=c.id AND wp.component='wage' AND wp.test_only=1)
		WHEN wo.company_id IS NOT NULL THEN wo.classification='test_only'
		WHEN c.wage_override_cents IS NOT NULL THEN EXISTS(SELECT 1 FROM catalog_seed_provenance wp WHERE wp.target_kind='company_craft' AND wp.target_id=c.id AND wp.component='wage_override' AND wp.test_only=1)
		ELSE EXISTS(SELECT 1 FROM catalog_seed_provenance wp WHERE wp.target_kind='craft' AND wp.target_id=p.id AND wp.component='wage' AND wp.test_only=1) END AS wage_test_only,
	(c.company_id<>'' AND p.id IS NULL) AS source_missing,
	IF(c.company_id='',c.active,COALESCE(p.active,0)) AS source_active
	FROM crafts c LEFT JOIN crafts p ON p.id=c.source_id AND p.company_id=''
	LEFT JOIN company_wage_overrides wo ON wo.company_id=c.company_id AND wo.craft_id=c.source_id) craft_configuration`

// parseCompanyCraftWageOverride preserves the distinction between an omitted
// field (retain an existing override), explicit null (inherit the public rate),
// and an integer-cent override (including explicit zero).
func parseCompanyCraftWageOverride(raw json.RawMessage) (present bool, value *int64, err error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true, nil, nil
	}
	var amount catalogCents
	if err := json.Unmarshal(raw, &amount); err != nil {
		return true, nil, fail(400, "公司特殊工资须为以分计的整数或null")
	}
	cents := int64(amount)
	if cents < 0 || cents > 100000000 {
		return true, nil, fail(400, "公司特殊工资须为0至1000000元")
	}
	return true, &cents, nil
}

func effectiveCraftWage(publicWage int64, companyOverride sql.NullInt64) int64 {
	if companyOverride.Valid {
		return companyOverride.Int64
	}
	return publicWage
}

func migrateCraftConfiguration(db *sql.DB) error {
	for index, column := range []struct{ table, name, definition string }{
		{"crafts", "source_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"crafts", "subitems", "JSON NULL"},
		{"crafts", "ticket_slot", "INT NOT NULL DEFAULT 1"},
		{"crafts", "included_by_craft_id", "VARCHAR(64) NOT NULL DEFAULT ''"},
		{"companies", "ticket_mode", "VARCHAR(20) NOT NULL DEFAULT 'single'"},
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
		if _, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", 400+index); err != nil {
			return err
		}
	}
	// Do not guess public sources or expand legacy category grants. Retain old
	// stored prices for recovery; public prices are suppressed by the projection.
	return nil
}

func waterproofCategory(category string) bool {
	return category == "防水涂料" || category == "室外防水"
}

func validateWaterproofCombination(child, parent string) error {
	if child != "丙纶" || !waterproofCategory(parent) {
		return fail(400, "组合只允许丙纶费用包含在防水涂料或室外防水中")
	}
	return nil
}

func validateCompanyCraftLinks(items []companyCraftInput, ticketMode string, categories map[string]string) error {
	byID := map[string]companyCraftInput{}
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, item := range items {
		if item.TicketSlot < 1 || item.TicketSlot > 2 || ticketMode == "single" && item.TicketSlot != 1 {
			return fail(400, "固定工艺交票分组与公司交票方式不一致")
		}
		if item.IncludedByCraftID == "" {
			continue
		}
		parent, ok := byID[item.IncludedByCraftID]
		if !ok || parent.ID == item.ID || parent.IncludedByCraftID != "" {
			return fail(400, "含价工艺须选择本公司已选的独立收费主工艺，不能自身或循环包含")
		}
		if err := validateWaterproofCombination(categories[item.ID], categories[parent.ID]); err != nil {
			return err
		}
		if parent.TicketSlot != item.TicketSlot {
			return fail(400, "组合内工艺必须安排在同一张票")
		}
	}
	return nil
}
