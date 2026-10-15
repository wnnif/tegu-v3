package main

import (
	"database/sql"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"os"
	"strings"
	"time"
)

func checkRef(tx *sql.Tx, table, id string) error {
	var n int
	if e := tx.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return fail(400, "关联资料不存在，请刷新后重新选择")
	}
	return nil
}
func (a *App) saveEntity(tx *sql.Tx, u User, b []byte) (any, error) {
	var request struct {
		Entity string `json:"entity"`
	}
	if e := decode(b, &request); e != nil {
		return nil, e
	}
	table := strings.TrimSpace(request.Entity)
	permission := map[string]string{"companies": "customers", "workers": "workers", "warehouses": "warehouse", "products": "warehouse", "crafts": "rates"}[table]
	if permission == "" || !u.can(permission) {
		return nil, fail(403, "没有修改此资料的权限")
	}
	switch table {
	case "companies":
		return saveCompanyEntity(tx, u, b)
	case "workers":
		return saveWorkerEntity(tx, u, b)
	case "warehouses":
		return saveWarehouseEntity(tx, u, b)
	case "products":
		return saveProductEntity(tx, u, b)
	case "crafts":
		return saveCraftEntity(tx, u, b)
	}
	return nil, fail(403, "没有修改此资料的权限")
}

type companyCraftInput struct {
	ID                      string          `json:"id"`
	SourceID                string          `json:"source_id"`
	Version                 int64           `json:"version"`
	PriceCents              *catalogCents   `json:"price_cents"`
	WageOverrideCents       json.RawMessage `json:"wage_override_cents,omitempty"`
	TicketSlot              int             `json:"ticket_slot"`
	IncludedByCraftID       string          `json:"included_by_craft_id"`
	wageOverrideValue       *int64
	legacyWageOverrideValue *int64
	wageOverrideChange      companyWageOverrideChange
}

type savedCompanyCraft struct {
	source, includedBy  string
	version, price      int64
	legacyWageOverride  sql.NullInt64
	companyWageOverride sql.NullInt64
}

type companyCraftTestOnlyPriceProvenance struct {
	seedID, sourceID, sourceFile, sourceSheet, classification, detail string
	sourceRow                                                         int
	testOnly                                                          bool
}

func validateCompanyCraftTestOnlyPrice(u User, currentPrice int64, submittedPrice *catalogCents, explicitlySubmitted bool) error {
	if !u.can("customer_prices") {
		return fail(403, "重新启用测试占位收费需要客户价格权限")
	}
	if currentPrice != 100 {
		return fail(409, "测试占位报价已发生变化，请先核对当前收费后再确认")
	}
	if !explicitlySubmitted || submittedPrice == nil || int64(*submittedPrice) == 100 {
		return fail(400, "重新启用测试占位收费前，请明确填写并确认非1元新报价")
	}
	return nil
}

func lockCompanyCraftTestOnlyPriceProvenance(tx *sql.Tx, craftID string) (*companyCraftTestOnlyPriceProvenance, error) {
	rows, err := tx.Query(`SELECT seed_id,classification,source_id,source_file,source_sheet,source_row,CAST(detail AS CHAR),test_only
		FROM catalog_seed_provenance WHERE target_kind='company_craft' AND target_id=? AND component='customer_price' AND (test_only=1 OR classification='test_only')
		ORDER BY seed_id FOR UPDATE`, craftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found *companyCraftTestOnlyPriceProvenance
	for rows.Next() {
		var provenance companyCraftTestOnlyPriceProvenance
		if err := rows.Scan(&provenance.seedID, &provenance.classification, &provenance.sourceID, &provenance.sourceFile, &provenance.sourceSheet, &provenance.sourceRow, &provenance.detail, &provenance.testOnly); err != nil {
			return nil, err
		}
		if found != nil || provenance.classification != "test_only" || !provenance.testOnly {
			return nil, fail(409, "测试占位报价来源记录不唯一或不一致，请先核对工艺来源")
		}
		found = &provenance
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return found, nil
}

func manualCompanyCraftPriceProvenanceDetail(u User, price catalogCents, provenance companyCraftTestOnlyPriceProvenance) ([]byte, error) {
	return json.Marshal(map[string]any{
		"kind":                  "manual",
		"actor_id":              u.ID,
		"actor_name":            u.Name,
		"confirmed_price_cents": int64(price),
		"previous_seed": map[string]any{
			"seed_id":        provenance.seedID,
			"classification": provenance.classification,
			"source_id":      provenance.sourceID,
			"source_file":    provenance.sourceFile,
			"source_sheet":   provenance.sourceSheet,
			"source_row":     provenance.sourceRow,
			"detail":         json.RawMessage(provenance.detail),
		},
	})
}

func confirmCompanyCraftTestOnlyPrice(tx *sql.Tx, u User, company string, item companyCraftInput, provenance companyCraftTestOnlyPriceProvenance) error {
	if item.PriceCents == nil {
		return fail(400, "请填写确认后的公司收费单价")
	}
	detail, err := manualCompanyCraftPriceProvenanceDetail(u, *item.PriceCents, provenance)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE catalog_seed_provenance SET classification='confirmed',source_id='manual',source_file='',source_sheet='人工确认报价',source_row=0,test_only=0,detail=?
		WHERE seed_id=? AND target_kind='company_craft' AND target_id=? AND component='customer_price' AND classification='test_only' AND test_only=1`, string(detail), provenance.seedID, item.ID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed != 1 {
		return fail(409, "测试占位报价来源已变化，请刷新后重新确认")
	}
	return audit(tx, u, "确认公司测试占位报价", item.ID, map[string]any{
		"company_id":            company,
		"source_id":             item.SourceID,
		"previous_price_cents":  int64(100),
		"price_cents":           int64(*item.PriceCents),
		"seed_id":               provenance.seedID,
		"previous_source_id":    provenance.sourceID,
		"previous_source_sheet": provenance.sourceSheet,
		"previous_source_row":   provenance.sourceRow,
	})
}

type companyWageOverrideChange uint8

const (
	companyWageOverridePreserve companyWageOverrideChange = iota
	companyWageOverrideSet
	companyWageOverrideClear
)

// Hidden input never overwrites the saved charge or changes its included-price
// relationship. Newly priced fixed items need explicit price access.
func protectCompanyCraftPrice(u User, item *companyCraftInput, previous savedCompanyCraft, existed bool) error {
	if !u.can("customer_prices") {
		if !existed {
			return fail(403, "新增公司收费工艺需要查看客户价格权限")
		}
		item.IncludedByCraftID = previous.includedBy
	}
	if existed && (item.PriceCents == nil || !u.can("customer_prices")) {
		value := catalogCents(previous.price)
		item.PriceCents = &value
	}
	return nil
}

// Wage overrides are separate from customer charges. A missing field retains
// the old value; users without rates or wages rights cannot change it.
func protectCompanyCraftWageOverride(u User, item *companyCraftInput, previous savedCompanyCraft, existed bool) error {
	present, value, err := parseCompanyCraftWageOverride(item.WageOverrideCents)
	if err != nil {
		return err
	}
	item.wageOverrideChange = companyWageOverridePreserve
	if previous.legacyWageOverride.Valid {
		legacy := previous.legacyWageOverride.Int64
		item.legacyWageOverrideValue = &legacy
	}
	if !u.can("rates") && !u.can("wages") {
		return nil
	}
	if !present {
		return nil
	}
	if value == nil {
		item.wageOverrideChange = companyWageOverrideClear
		item.legacyWageOverrideValue = nil
		return nil
	}
	if existed && previous.companyWageOverride.Valid && previous.companyWageOverride.Int64 == *value {
		return nil
	}
	item.wageOverrideChange = companyWageOverrideSet
	item.wageOverrideValue = value
	item.legacyWageOverrideValue = nil
	return nil
}

func effectiveCompanyWageOverride(legacy, independent sql.NullInt64) sql.NullInt64 {
	if independent.Valid {
		return independent
	}
	return legacy
}

func persistCompanyCraftWageOverride(tx *sql.Tx, u User, companyID, craftID string, item *companyCraftInput) error {
	switch item.wageOverrideChange {
	case companyWageOverridePreserve:
		return nil
	case companyWageOverrideClear:
		_, err := tx.Exec("DELETE FROM company_wage_overrides WHERE company_id=? AND craft_id=?", companyID, craftID)
		return err
	case companyWageOverrideSet:
		source, err := json.Marshal(map[string]any{"kind": "manual", "actor_id": u.ID, "actor_name": u.Name})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO company_wage_overrides(company_id,craft_id,wage_cents,classification,source)
			VALUES(?,?,?,'confirmed',?) ON DUPLICATE KEY UPDATE wage_cents=VALUES(wage_cents),classification='confirmed',source=VALUES(source),updated_at=UTC_TIMESTAMP()`, companyID, craftID, *item.wageOverrideValue, string(source))
		return err
	default:
		return fail(500, "公司工资覆盖操作无效")
	}
}

// Company details and its fixed crafts are committed by the same entity transaction.
func saveCompanyCrafts(tx *sql.Tx, u User, company, ticketMode string, raw json.RawMessage) error {
	var items []companyCraftInput
	if e := json.Unmarshal(raw, &items); e != nil || items == nil || len(items) > 200 {
		return fail(400, "公司固定工艺格式不正确或超过200项")
	}
	existing, e := tx.Query(`SELECT c.id,c.source_id,c.version,c.price_cents,c.included_by_craft_id,c.wage_override_cents,wo.wage_cents
		FROM crafts c LEFT JOIN company_wage_overrides wo ON wo.company_id=c.company_id AND wo.craft_id=c.source_id
		WHERE c.company_id=? ORDER BY c.id FOR UPDATE`, company)
	if e != nil {
		return e
	}
	old := map[string]savedCompanyCraft{}
	oldBySource := map[string]string{}
	for existing.Next() {
		var craftID string
		var row savedCompanyCraft
		if e = existing.Scan(&craftID, &row.source, &row.version, &row.price, &row.includedBy, &row.legacyWageOverride, &row.companyWageOverride); e != nil {
			existing.Close()
			return e
		}
		old[craftID] = row
		if row.source != "" {
			if oldBySource[row.source] != "" {
				existing.Close()
				return fail(409, "公司工艺来源重复，请核对基础资料")
			}
			oldBySource[row.source] = craftID
		}
	}
	e = existing.Err()
	existing.Close()
	if e != nil {
		return e
	}
	kept, bySource := map[string]bool{}, map[string]string{}
	testOnlyPriceChanges := map[string]companyCraftTestOnlyPriceProvenance{}
	for i := range items {
		item := &items[i]
		explicitPrice := item.PriceCents != nil
		item.ID, item.SourceID = strings.TrimSpace(item.ID), strings.TrimSpace(item.SourceID)
		item.IncludedByCraftID = strings.TrimSpace(item.IncludedByCraftID)
		if item.TicketSlot == 0 {
			item.TicketSlot = 1
		}
		if item.ID != "" {
			previous, ok := old[item.ID]
			if !ok {
				return fail(400, "固定工艺不属于该公司")
			}
			if previous.version != item.Version {
				return fail(409, "公司工艺已被修改，请重新打开")
			}
			if item.SourceID == "" {
				item.SourceID = previous.source
			}
			if previous.source != "" && item.SourceID != previous.source {
				return fail(400, "已关联的固定工艺不能更换来源，请移除后重新选择")
			}
			provenance, err := lockCompanyCraftTestOnlyPriceProvenance(tx, item.ID)
			if err != nil {
				return err
			}
			if provenance != nil {
				if err = validateCompanyCraftTestOnlyPrice(u, previous.price, item.PriceCents, explicitPrice); err != nil {
					return err
				}
				testOnlyPriceChanges[item.ID] = *provenance
			}
			if e = protectCompanyCraftPrice(u, item, previous, true); e != nil {
				return e
			}
			if e = protectCompanyCraftWageOverride(u, item, previous, true); e != nil {
				return e
			}
		} else {
			// Re-selecting an inactive company/public association keeps its
			// identity, historical references and previous hidden price.
			oldID := oldBySource[item.SourceID]
			previous, existed := old[oldID]
			if existed {
				provenance, err := lockCompanyCraftTestOnlyPriceProvenance(tx, oldID)
				if err != nil {
					return err
				}
				if provenance != nil {
					if err = validateCompanyCraftTestOnlyPrice(u, previous.price, item.PriceCents, explicitPrice); err != nil {
						return err
					}
					testOnlyPriceChanges[oldID] = *provenance
				}
			}
			if e = protectCompanyCraftPrice(u, item, previous, existed); e != nil {
				return e
			}
			if e = protectCompanyCraftWageOverride(u, item, previous, existed); e != nil {
				return e
			}
			if existed {
				item.ID, item.Version = oldID, previous.version
			} else {
				item.ID = id()
			}
		}
		if item.SourceID == "" {
			return fail(400, "历史固定工艺尚未关联公共工艺，请逐项选择来源后保存")
		}
		if kept[item.ID] || bySource[item.SourceID] != "" {
			return fail(400, "固定工艺不能重复选择")
		}
		kept[item.ID], bySource[item.SourceID] = true, item.ID
	}
	for i := range items {
		if strings.HasPrefix(items[i].IncludedByCraftID, "source:") {
			source := strings.TrimPrefix(items[i].IncludedByCraftID, "source:")
			resolved := bySource[source]
			if resolved == "" {
				return fail(400, "含价主工艺未选择，请重新选择组合")
			}
			items[i].IncludedByCraftID = resolved
		}
	}
	categories := map[string]string{}
	for _, item := range items {
		var category string
		if e = tx.QueryRow("SELECT category FROM crafts WHERE id=? AND company_id='' FOR SHARE", item.SourceID).Scan(&category); e != nil {
			return fail(400, "请选择有效的公共工艺来源")
		}
		categories[item.ID] = category
	}
	if e = validateCompanyCraftLinks(items, ticketMode, categories); e != nil {
		return e
	}
	for displayOrder, item := range items {
		var category, name, mode, spec string
		var wage int64
		var active bool
		if e = tx.QueryRow("SELECT category,name,mode,spec,wage_cents,active FROM crafts WHERE id=? AND company_id='' FOR SHARE", item.SourceID).Scan(&category, &name, &mode, &spec, &wage, &active); e != nil {
			return fail(400, "请选择有效的公共工艺来源")
		}
		previous, existed := old[item.ID]
		if !active && (!existed || previous.source != item.SourceID) {
			return fail(400, "请选择已启用的公共工艺")
		}
		if item.PriceCents == nil {
			return fail(400, "请填写公司的收费单价")
		}
		price := int64(*item.PriceCents)
		if price < 0 || price > 100000000 {
			return fail(400, "公司收费单价超出范围")
		}
		if !existed {
			_, e = tx.Exec("INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,active,spec,source_id,ticket_slot,included_by_craft_id,display_order,wage_override_cents) VALUES(?,?,?,?,?,?,0,1,?,?,?,?,?,?)", item.ID, company, category, name, mode, price, spec, item.SourceID, item.TicketSlot, item.IncludedByCraftID, displayOrder, item.legacyWageOverrideValue)
		} else {
			_, e = tx.Exec("UPDATE crafts SET category=?,name=?,mode=?,spec=?,source_id=?,price_cents=?,wage_cents=0,ticket_slot=?,included_by_craft_id=?,display_order=?,active=1,wage_override_cents=?,version=version+1 WHERE id=?", category, name, mode, spec, item.SourceID, price, item.TicketSlot, item.IncludedByCraftID, displayOrder, item.legacyWageOverrideValue, item.ID)
		}
		if e != nil {
			return e
		}
		if e = persistCompanyCraftWageOverride(tx, u, company, item.SourceID, &item); e != nil {
			return e
		}
		if provenance, ok := testOnlyPriceChanges[item.ID]; ok {
			if e = confirmCompanyCraftTestOnlyPrice(tx, u, company, item, provenance); e != nil {
				return e
			}
		}
	}
	for craftID := range old {
		if !kept[craftID] {
			if _, e = tx.Exec("UPDATE crafts SET active=0,version=version+1 WHERE id=? AND active=1", craftID); e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *App) saveUser(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("users") {
		return nil, fail(403, "没有账号管理权限")
	}
	var input struct {
		User
		Password string `json:"password"`
		Delete   bool   `json:"delete"`
	}
	if e := decode(b, &input); e != nil {
		return nil, e
	}
	oldCombo := false
	oldCrafts := []string{}
	if input.ID != "" {
		var role, oldPermissions, storedCrafts string
		if e := tx.QueryRow("SELECT role,permissions,crafts FROM users WHERE id=? AND deleted=0 FOR UPDATE", input.ID).Scan(&role, &oldPermissions, &storedCrafts); e != nil {
			return nil, fail(404, "账号不存在")
		}
		var old []string
		if err := json.Unmarshal([]byte(oldPermissions), &old); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(storedCrafts), &oldCrafts); err != nil {
			return nil, err
		}
		oldCombo = contains(old, combinationPermission)
		if role == "admin" && (u.Role != "admin" || input.Role != "admin" || input.Delete || !input.Active) {
			return nil, fail(403, "不能删除、停用或降级系统管理员")
		}
		if role != "admin" && input.Role == "admin" {
			return nil, fail(403, "系统管理员只允许一个，不能提升其他账号为系统管理员")
		}
		if input.Delete {
			if input.ID == u.ID {
				return nil, fail(400, "不能删除当前账号")
			}
			result, e := tx.Exec("UPDATE users SET deleted=1,active=0,username=CONCAT('deleted-',id),version=version+1 WHERE id=? AND version=?", input.ID, input.Version)
			if e != nil {
				return nil, e
			}
			if n, _ := result.RowsAffected(); n != 1 {
				return nil, fail(409, "账号已被修改，请刷新")
			}
			if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", input.ID); e != nil {
				return nil, e
			}
			if e = audit(tx, u, "删除账号", input.ID, map[string]any{"username": input.Username}); e != nil {
				return nil, e
			}
			return map[string]any{"ok": true}, nil
		}
	} else {
		if input.Role == "admin" {
			return nil, fail(400, "系统管理员只允许一个")
		}
		input.ID = id()
		input.Version = 0
	}
	if u.Role != "admin" && oldCombo != contains(input.Permissions, combinationPermission) {
		return nil, fail(403, "组合免收权限只能由系统管理员授予或撤销，普通管理员不能自授或转授")
	}
	username, err := normalizeUsername(input.Username)
	if err != nil {
		return nil, err
	}
	input.Username = username
	if !contains([]string{"admin", "operator", "employee"}, input.Role) || input.Name == "" {
		return nil, fail(400, "请填写姓名、账号及角色")
	}
	for _, p := range input.Permissions {
		if input.Role == "employee" && (!contains(allPermissions, p) || p == "users" || p == "backups") {
			return nil, fail(400, "员工权限不正确")
		}
	}
	if len(input.Crafts) > 5000 {
		return nil, fail(400, "工艺授权数量超出范围")
	}
	seenCrafts := map[string]bool{}
	for _, c := range input.Crafts {
		if len(c) > 64 || seenCrafts[c] || contains(categories, c) {
			return nil, fail(400, "请选择具体公共工艺，不能按公司固定工艺或大类授权")
		}
		var active bool
		if err := tx.QueryRow("SELECT active FROM crafts WHERE id=? AND company_id='' FOR SHARE", c).Scan(&active); err != nil {
			if err != sql.ErrNoRows {
				return nil, err
			}
			return nil, fail(400, "工艺授权必须选择公共工艺")
		}
		if !active && !contains(oldCrafts, c) {
			return nil, fail(400, "不能新增授权已停用的公共工艺")
		}
		seenCrafts[c] = true
	}
	if input.Permissions == nil {
		input.Permissions = []string{}
	}
	if input.Crafts == nil {
		input.Crafts = []string{}
	}
	if len([]byte(input.Password)) > 72 {
		return nil, fail(400, "密码过长")
	}
	pw := ""
	if input.Password != "" {
		v, e := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
		pw = string(v)
	}
	if input.Version == 0 {
		if pw == "" {
			return nil, fail(400, "请设置初始密码")
		}
		_, e := tx.Exec("INSERT INTO users(id,username,name,password_hash,role,permissions,crafts,active) VALUES(?,?,?,?,?,?,?,?)", input.ID, input.Username, input.Name, pw, input.Role, jsonText(input.Permissions), jsonText(input.Crafts), input.Active)
		if e != nil {
			return nil, e
		}
	} else {
		result, e := tx.Exec("UPDATE users SET username=?,name=?,role=?,permissions=?,crafts=?,active=?,password_hash=IF(?='',password_hash,?),version=version+1 WHERE id=? AND version=?", input.Username, input.Name, input.Role, jsonText(input.Permissions), jsonText(input.Crafts), input.Active, pw, pw, input.ID, input.Version)
		if e != nil {
			return nil, e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return nil, fail(409, "账号已被修改")
		}
		if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", input.ID); e != nil {
			return nil, e
		}
	}
	if e := audit(tx, u, "保存账号", input.ID, input.User); e != nil {
		return nil, e
	}
	return map[string]any{"ok": true}, nil
}
func (a *App) importCrafts(file string) error {
	b, e := os.ReadFile(file)
	if e != nil {
		return e
	}
	var data struct {
		Format    string                      `json:"format"`
		Customers []struct{ ID, Name string } `json:"customers"`
		Rates     []struct {
			ID, Name, Category, CustomerID, CalcMode string
			Spec                                     string
			PriceCents, WageCents                    int64
			Version                                  int64
			Price, Wage                              float64
			Active                                   bool
			Subitems                                 []CraftSubitem `json:"subitems"`
		} `json:"rates"`
	}
	if e = json.Unmarshal(b, &data); e != nil {
		return e
	}
	if data.Format != "tegu-craft-export-v1" && data.Format != "tegu-craft-export-v2" {
		return fail(400, "工艺备份格式无效")
	}
	if len(data.Customers) != 0 {
		return fail(400, "本独立项目仅允许导入公共工艺，不允许导入既有公司资料")
	}
	for _, r := range data.Rates {
		if r.CustomerID != "" {
			return fail(400, "本独立项目不得导入公司专属工艺")
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT COUNT(*) FROM crafts").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return fail(409, "工艺库非空，拒绝重复导入")
	}
	for _, r := range data.Rates {
		if !contains(categories, r.Category) {
			return fail(400, "备份工艺类别无效")
		}
		mode := r.CalcMode
		if mode == "" {
			mode = "平方"
		}
		if !validCraftMode(mode) {
			return fail(400, "备份计量方式无效")
		}
		wage := int64(r.Wage*100 + 0.5)
		if data.Format == "tegu-craft-export-v2" {
			wage = r.WageCents
		}
		if wage < 0 || wage > 100000000 {
			return fail(400, "备份工资单价超出范围")
		}
		if r.Version < 1 {
			r.Version = 1
		}
		subitems, err := normalizeCraftSubitems(r.Subitems)
		if err != nil {
			return err
		}
		if _, e = tx.Exec("INSERT INTO crafts(id,company_id,category,name,mode,price_cents,wage_cents,active,spec,version,subitems) VALUES(?,'',?,?,?,0,?,?,?,?,?)", r.ID, r.Category, r.Name, mode, wage, r.Active, r.Spec, r.Version, jsonText(subitems)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func monthRange(month string) (string, string, error) {
	t, e := time.Parse("2006-01", month)
	if e != nil {
		return "", "", fail(400, "请选择有效月份")
	}
	return t.Format("2006-01-02"), t.AddDate(0, 1, 0).Format("2006-01-02"), nil
}
