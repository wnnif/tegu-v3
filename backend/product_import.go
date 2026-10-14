package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxProductCatalogFileBytes = 4 * 1024 * 1024

type productCatalogImport struct {
	Format   string                       `json:"format"`
	Products []productCatalogImportEntry `json:"products"`
}

type productCatalogImportEntry struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Unit     string          `json:"unit"`
	AuxUnit  string          `json:"aux_unit"`
	AuxRatio json.RawMessage `json:"aux_ratio"`
	Active   json.RawMessage `json:"active"`
	Color    string          `json:"color"`
	WeightKG json.RawMessage `json:"weight_kg"`
	Brand    string          `json:"brand"`
	Spec     string          `json:"spec"`
	Note     string          `json:"note"`
}

type validatedProductCatalogEntry struct {
	input  productEntityInput
	ratio  string
	weight float64
}

func parseProductCatalog(data []byte) ([]validatedProductCatalogEntry, error) {
	if !utf8.Valid(data) {
		return nil, fail(400, "商品目录文件必须是有效UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog productCatalogImport
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fail(400, "商品目录文件格式不正确")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fail(400, "商品目录文件包含多余内容")
	}
	if catalog.Format != "tegu-product-catalog-v1" {
		return nil, fail(400, "商品目录格式版本无效")
	}
	if len(catalog.Products) == 0 {
		return nil, fail(400, "商品目录不能为空")
	}

	validated := make([]validatedProductCatalogEntry, 0, len(catalog.Products))
	seenIDs := make(map[string]struct{}, len(catalog.Products))
	for _, entry := range catalog.Products {
		if strings.TrimSpace(entry.ID) == "" || entry.ID != strings.TrimSpace(entry.ID) || len([]rune(entry.ID)) > 64 {
			return nil, fail(400, "商品目录包含无效商品编号")
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, fail(400, "商品目录包含重复商品编号")
		}
		seenIDs[entry.ID] = struct{}{}
		if len(entry.AuxRatio) == 0 || len(entry.Active) == 0 || len(entry.WeightKG) == 0 {
			return nil, fail(400, "商品目录缺少单位比例、启用状态或重量")
		}
		active, err := parseProductCatalogActive(entry.Active)
		if err != nil {
			return nil, err
		}
		weight, err := parseProductCatalogWeight(entry.WeightKG)
		if err != nil {
			return nil, err
		}

		input := productEntityInput{
			ID: entry.ID, Name: entry.Name, Unit: entry.Unit, AuxUnit: entry.AuxUnit,
			AuxRatio: entry.AuxRatio, Active: catalogBoolean(active),
			Color: entry.Color, WeightKG: entry.WeightKG, Brand: entry.Brand,
			Spec: entry.Spec, Note: entry.Note,
		}
		input, ratio, validatedWeight, err := validateProductEntityInput(input)
		if err != nil {
			return nil, err
		}
		if validatedWeight != weight {
			return nil, fail(400, "商品重量无法精确解析")
		}
		definition, err := newProductUnitDefinition(input.Unit, input.AuxUnit, ratio)
		if err != nil {
			return nil, err
		}
		if input.AuxUnit == "" && ratio != "1.000000" {
			return nil, fail(400, "未设置辅助单位时换算比例必须为1")
		}
		if definition.BaseFactor <= 0 || definition.AuxFactor <= 0 {
			return nil, fail(400, "商品单位换算无效")
		}
		validated = append(validated, validatedProductCatalogEntry{input: input, ratio: ratio, weight: weight})
	}
	return validated, nil
}

func parseProductCatalogActive(raw json.RawMessage) (bool, error) {
	switch strings.TrimSpace(string(raw)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fail(400, "商品启用状态必须为true、false、1或0")
	}
}

func parseProductCatalogWeight(raw json.RawMessage) (float64, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return 0, fail(400, "商品重量必须是有效的公斤数")
		}
		text = number.String()
	}
	if len(text) > 30 || !productQuantityDecimal.MatchString(text) {
		return 0, fail(400, "商品重量必须是非负、最多6位小数的公斤数")
	}
	weight, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > 1000000 {
		return 0, fail(400, "商品重量须为0至1000000之间的公斤数")
	}
	return weight, nil
}

func (a *App) importProducts(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxProductCatalogFileBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxProductCatalogFileBytes {
		return fail(413, "商品目录文件过大")
	}
	products, err := parseProductCatalog(data)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(SUBSTRING(SHA2(LOWER(DATABASE()),256),1,32),':import-products'),30)").Scan(&locked); err != nil {
		discardProductImportConnection(conn)
		return fail(409, "商品目录导入锁获取失败，请核对数据库后重试")
	}
	if !locked.Valid || locked.Int64 != 1 {
		return fail(409, "商品目录导入锁定失败，请稍后重试")
	}
	defer releaseProductImportLock(conn, ctx)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM products ORDER BY id LIMIT 1 FOR UPDATE").Scan(&existingID)
	if err == nil {
		return fail(409, "商品库非空，拒绝导入以避免覆盖现有资料")
	}
	if err != sql.ErrNoRows {
		return err
	}
	for _, product := range products {
		input := product.input
		if _, err = tx.Exec(`INSERT INTO products(id,name,unit,aux_unit,aux_ratio,active,color,weight_kg,brand,spec,note,version)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, input.ID, input.Name, input.Unit, input.AuxUnit,
			product.ratio, bool(input.Active), input.Color, product.weight, input.Brand, input.Spec, input.Note); err != nil {
			return err
		}
		if err = syncProductUnitDefinition(tx, input.ID, input.Unit, input.AuxUnit, product.ratio); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func releaseProductImportLock(conn *sql.Conn, parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(CONCAT(SUBSTRING(SHA2(LOWER(DATABASE()),256),1,32),':import-products'))").Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
		discardProductImportConnection(conn)
	}
}

func discardProductImportConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
