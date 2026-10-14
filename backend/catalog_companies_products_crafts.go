package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

type companyEntityInput struct {
	ID                 string          `json:"id"`
	Version            int64           `json:"version"`
	Name               string          `json:"name"`
	Contact            string          `json:"contact"`
	Phone              string          `json:"phone"`
	CustomerType       string          `json:"customer_type"`
	Note               string          `json:"note"`
	TicketMode         string          `json:"ticket_mode"`
	CraftCategoryOrder json.RawMessage `json:"craft_category_order,omitempty"`
	FixedCrafts        json.RawMessage `json:"fixed_crafts"`
}

type productEntityInput struct {
	ID       string          `json:"id"`
	Version  int64           `json:"version"`
	Name     string          `json:"name"`
	Unit     string          `json:"unit"`
	AuxUnit  string          `json:"aux_unit"`
	AuxRatio json.RawMessage `json:"aux_ratio"`
	Active   catalogBoolean  `json:"active"`
	Color    string          `json:"color"`
	WeightKG json.RawMessage `json:"weight_kg"`
	Brand    string          `json:"brand"`
	Spec     string          `json:"spec"`
	Note     string          `json:"note"`
	Category string          `json:"category"`
}

type craftEntityInput struct {
	ID           string         `json:"id"`
	Version      int64          `json:"version"`
	Name         string         `json:"name"`
	Spec         string         `json:"spec"`
	CompanyID    string         `json:"company_id"`
	Category     string         `json:"category"`
	Mode         string         `json:"mode"`
	PriceCents   catalogCents   `json:"price_cents"`
	WageCents    catalogCents   `json:"wage_cents"`
	Active       catalogBoolean `json:"active"`
	Subitems     []CraftSubitem `json:"subitems"`
	DisplayOrder *int           `json:"display_order"`
}

// Existing catalog forms can submit numeric fields as JSON numbers or text.
type catalogCents int64

func (v *catalogCents) UnmarshalJSON(data []byte) error {
	var number int64
	if json.Unmarshal(data, &number) == nil {
		*v = catalogCents(number)
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return strconv.ErrSyntax
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return err
	}
	*v = catalogCents(n)
	return nil
}

func catalogFloat(raw json.RawMessage, emptyIsZero bool) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, emptyIsZero
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		value = strings.TrimSpace(value)
		if value == "" && emptyIsZero {
			return 0, true
		}
		n, err := strconv.ParseFloat(value, 64)
		return n, err == nil
	}
	return 0, emptyIsZero
}

func saveCompanyEntity(tx *sql.Tx, u User, body []byte) (any, error) {
	if !u.can("customers") {
		return nil, fail(403, "没有修改公司资料的权限")
	}
	var input companyEntityInput
	if err := decode(body, &input); err != nil {
		return nil, err
	}
	order, err := parseCompanyCraftCategoryOrder(input.CraftCategoryOrder)
	if err != nil {
		return nil, err
	}
	var categoryOrder any
	if order != nil {
		encoded := jsonText(order)
		categoryOrder = encoded
		input.CraftCategoryOrder = json.RawMessage(encoded)
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Phone = strings.TrimSpace(input.Phone)
	input.CustomerType = strings.TrimSpace(input.CustomerType)
	input.Note = strings.TrimSpace(input.Note)
	if input.TicketMode == "" {
		input.TicketMode = "single"
		if input.ID != "" {
			if err := tx.QueryRow("SELECT ticket_mode FROM companies WHERE id=?", input.ID).Scan(&input.TicketMode); err != nil {
				return nil, fail(404, "公司不存在")
			}
		}
	}
	if !contains([]string{"single", "split"}, input.TicketMode) {
		return nil, fail(400, "请选择统一交票或分两次交票")
	}
	if err := validEntityName(input.Name); err != nil {
		return nil, err
	}
	if len([]rune(input.Phone)) > 60 || len([]rune(input.Contact)) > 100 || len([]rune(input.CustomerType)) > 100 || len([]rune(input.Note)) > 1000 {
		return nil, fail(400, "客户资料文字过长")
	}
	fresh := input.ID == ""
	if fresh {
		input.ID = id()
	}
	var result sql.Result
	if fresh {
		result, err = tx.Exec("INSERT INTO companies(id,name,contact,phone,customer_type,note,ticket_mode,craft_category_order) VALUES(?,?,?,?,?,?,?,?)", input.ID, input.Name, input.Contact, input.Phone, input.CustomerType, input.Note, input.TicketMode, categoryOrder)
	} else {
		// SQL NULL means omitted, not reset. Preserve the previous JSON in the
		// same version-checked update; explicit [] is a non-null JSON value.
		result, err = tx.Exec("UPDATE companies SET name=?,contact=?,phone=?,customer_type=?,note=?,ticket_mode=?,craft_category_order=COALESCE(?,craft_category_order),version=version+1 WHERE id=? AND version=?", input.Name, input.Contact, input.Phone, input.CustomerType, input.Note, input.TicketMode, categoryOrder, input.ID, input.Version)
	}
	if err != nil {
		return nil, err
	}
	if err = entitySaved(result); err != nil {
		return nil, err
	}
	if len(input.FixedCrafts) > 0 {
		if err = saveCompanyCrafts(tx, u, input.ID, input.TicketMode, input.FixedCrafts); err != nil {
			return nil, err
		}
	} else if input.TicketMode == "single" {
		var invalid int
		if err = tx.QueryRow("SELECT COUNT(*) FROM crafts WHERE company_id=? AND active=1 AND ticket_slot<>1", input.ID).Scan(&invalid); err != nil {
			return nil, err
		}
		if invalid != 0 {
			return nil, fail(400, "变更为统一交票时，请同时将全部固定工艺设置为第一张票")
		}
	}
	if err = audit(tx, u, "保存companies", input.ID, input); err != nil {
		return nil, err
	}
	return map[string]any{"id": input.ID, "ok": true}, nil
}

func saveProductEntity(tx *sql.Tx, u User, body []byte) (any, error) {
	var input productEntityInput
	if err := decode(body, &input); err != nil {
		return nil, err
	}
	input, ratio, weight, err := validateProductEntityInput(input)
	if err != nil {
		return nil, err
	}
	if input.ID != "" {
		var oldUnit, oldAux string
		var oldRatio string
		if err := tx.QueryRow("SELECT unit,aux_unit,CAST(aux_ratio AS CHAR) FROM products WHERE id=? FOR UPDATE", input.ID).Scan(&oldUnit, &oldAux, &oldRatio); err != nil {
			if err == sql.ErrNoRows {
				return nil, fail(409, "资料已变更，请刷新后重新打开")
			}
			return nil, err
		}
		canonicalOldRatio, err := parseProductUnitRatio(json.RawMessage(strconv.Quote(oldRatio)))
		if err != nil {
			return nil, err
		}
		if oldUnit != input.Unit || oldAux != input.AuxUnit || canonicalOldRatio != ratio {
			// A locking read sees a first movement committed while we waited for
			// the product lock, even if this transaction already has an RR snapshot.
			var movementID int64
			err := tx.QueryRow("SELECT id FROM stock_movements FORCE INDEX (idx_stock_movement_product) WHERE product_id=? LIMIT 1 FOR SHARE", input.ID).Scan(&movementID)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if err == nil {
				return nil, fail(400, "有库存流水的商品不能修改单位换算")
			}
		}
	}
	fresh := input.ID == ""
	if fresh {
		input.ID = id()
	}
	var result sql.Result
	if fresh {
		result, err = tx.Exec("INSERT INTO products(id,name,unit,aux_unit,aux_ratio,active,color,weight_kg,brand,spec,note) VALUES(?,?,?,?,?,?,?,?,?,?,?)", input.ID, input.Name, input.Unit, input.AuxUnit, ratio, bool(input.Active), input.Color, weight, input.Brand, input.Spec, input.Note)
	} else {
		result, err = tx.Exec("UPDATE products SET name=?,unit=?,aux_unit=?,aux_ratio=?,active=?,color=?,weight_kg=?,brand=?,spec=?,note=?,version=version+1 WHERE id=? AND version=?", input.Name, input.Unit, input.AuxUnit, ratio, bool(input.Active), input.Color, weight, input.Brand, input.Spec, input.Note, input.ID, input.Version)
	}
	if err != nil {
		return nil, err
	}
	if err = entitySaved(result); err != nil {
		return nil, err
	}
	if err = syncProductUnitDefinition(tx, input.ID, input.Unit, input.AuxUnit, ratio); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "保存products", input.ID, input); err != nil {
		return nil, err
	}
	return map[string]any{"id": input.ID, "ok": true}, nil
}

func validateProductEntityInput(input productEntityInput) (productEntityInput, string, float64, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.Unit = strings.TrimSpace(input.Unit)
	input.AuxUnit = strings.TrimSpace(input.AuxUnit)
	input.Color = strings.TrimSpace(input.Color)
	input.Brand = strings.TrimSpace(input.Brand)
	input.Spec = strings.TrimSpace(input.Spec)
	input.Note = strings.TrimSpace(input.Note)
	if err := validEntityName(input.Name); err != nil {
		return input, "", 0, err
	}
	for _, field := range []struct {
		value string
		limit int
	}{{input.Brand, 100}, {input.Spec, 200}, {input.Note, 1000}, {input.Category, 100}} {
		if len([]rune(field.value)) > field.limit {
			return input, "", 0, fail(400, "资料文字过长，请缩短后保存")
		}
	}
	if input.Unit == "" {
		return input, "", 0, fail(400, "请填写基本单位")
	}
	if len([]rune(input.Unit)) > 30 || len([]rune(input.AuxUnit)) > 30 {
		return input, "", 0, fail(400, "商品单位不能超过30字")
	}
	ratio, ratioErr := parseProductUnitRatio(input.AuxRatio)
	if ratioErr != nil {
		return input, "", 0, ratioErr
	}
	weight, ok := catalogFloat(input.WeightKG, true)
	if !ok || math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > 1000000 {
		return input, "", 0, fail(400, "重量须为0至1000000之间的公斤数")
	}
	if len([]rune(input.Color)) > 100 {
		return input, "", 0, fail(400, "颜色不能超过100字")
	}
	return input, ratio, weight, nil
}

func saveCraftEntity(tx *sql.Tx, u User, body []byte) (any, error) {
	var input craftEntityInput
	if err := decode(body, &input); err != nil {
		return nil, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.Spec = strings.TrimSpace(input.Spec)
	input.CompanyID = strings.TrimSpace(input.CompanyID)
	input.Category = strings.TrimSpace(input.Category)
	input.Mode = strings.TrimSpace(input.Mode)
	if err := validEntityName(input.Name); err != nil {
		return nil, err
	}
	if len([]rune(input.Spec)) > 200 {
		return nil, fail(400, "工艺规格最多200字")
	}
	if !contains(categories, input.Category) || !validCraftMode(input.Mode) {
		return nil, fail(400, "请选择有效工艺和计算方式")
	}
	if input.DisplayOrder != nil && !validSortOrder(*input.DisplayOrder) {
		return nil, fail(400, "工艺显示顺序须为0至10000的整数")
	}
	if input.CompanyID != "" {
		return nil, fail(400, "公司固定工艺请在公司资料中选择公共工艺并设置收费")
	}
	if input.PriceCents != 0 {
		return nil, fail(400, "公共工艺不设置客户收费，请在公司固定工艺中设置")
	}
	wage := int64(input.WageCents)
	if wage < 0 || wage > 100000000 {
		return nil, fail(400, "单价超出范围")
	}
	var err error
	input.Subitems, err = normalizeCraftSubitems(input.Subitems)
	if err != nil {
		return nil, err
	}
	fresh := input.ID == ""
	if fresh {
		input.ID = id()
	}
	var result sql.Result
	if fresh {
		displayOrder := 0
		if input.DisplayOrder != nil {
			displayOrder = *input.DisplayOrder
		}
		result, err = tx.Exec("INSERT INTO crafts(id,name,spec,company_id,category,mode,price_cents,wage_cents,active,subitems,display_order) VALUES(?,?,?,'',?,?,0,?,?,?,?)", input.ID, input.Name, input.Spec, input.Category, input.Mode, wage, bool(input.Active), jsonText(input.Subitems), displayOrder)
	} else {
		result, err = tx.Exec("UPDATE crafts SET name=?,spec=?,category=?,mode=?,price_cents=0,wage_cents=?,active=?,subitems=?,display_order=COALESCE(?,display_order),version=version+1 WHERE id=? AND version=? AND company_id=''", input.Name, input.Spec, input.Category, input.Mode, wage, bool(input.Active), jsonText(input.Subitems), input.DisplayOrder, input.ID, input.Version)
	}
	if err != nil {
		return nil, err
	}
	if err = entitySaved(result); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "保存crafts", input.ID, input); err != nil {
		return nil, err
	}
	return map[string]any{"id": input.ID, "ok": true}, nil
}
