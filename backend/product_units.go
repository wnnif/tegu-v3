package main

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Phase 1 defines exact unit quantities; existing stock posting stays unchanged
// until phase 2 switches all input, snapshot and ledger paths together.
const productQuantityScale int64 = 1000000

var productQuantityDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)

type productUnitDefinition struct {
	BaseUnit   string `json:"base_unit"`
	AuxUnit    string `json:"aux_unit"`
	BaseFactor int64  `json:"base_factor,string"`
	AuxFactor  int64  `json:"aux_factor,string"`
}

// This snapshot is the phase-2 persistence contract. Input and internal quantity
// travel as strings so a browser cannot lose integer precision.
type productQuantitySnapshot struct {
	productUnitDefinition
	InputQuantity   string `json:"input_quantity"`
	InputUnit       string `json:"input_unit"`
	QuantityQuantum string `json:"quantity_quantum"`
}

func parseProductUnitRatio(raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		value = strings.TrimSpace(value)
		if !productQuantityDecimal.MatchString(value) {
			return "", fail(400, "换算比例须为最多6位小数的十进制数")
		}
	} else {
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return "", fail(400, "换算比例必须是大于零、最多6位小数的数量")
		}
		value = number.String()
	}
	value = strings.TrimSpace(value)
	if len(value) > 40 {
		return "", fail(400, "换算比例超出范围")
	}
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(value[index+1:])
		if err != nil || exponent < -12 || exponent > 12 {
			return "", fail(400, "换算比例超出范围")
		}
	}
	// Numeric JSON may use exponent notation, but no rounding is allowed.
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Sign() <= 0 || ratio.Cmp(big.NewRat(1, productQuantityScale)) < 0 || ratio.Cmp(big.NewRat(1000000, 1)) > 0 {
		return "", fail(400, "换算比例须为0.000001至1000000，最多6位小数")
	}
	scaled := new(big.Rat).Mul(ratio, big.NewRat(productQuantityScale, 1))
	if !scaled.IsInt() || !scaled.Num().IsInt64() {
		return "", fail(400, "换算比例最多6位小数，不能舍入保存")
	}
	return scaledDecimal(scaled.Num().Int64(), 6), nil
}

func newProductUnitDefinition(baseUnit, auxUnit, ratioText string) (productUnitDefinition, error) {
	d := productUnitDefinition{BaseUnit: baseUnit, AuxUnit: auxUnit, BaseFactor: 1, AuxFactor: 1}
	canonical, err := parseProductUnitRatio(json.RawMessage(strconv.Quote(ratioText)))
	if err != nil {
		return d, err
	}
	if auxUnit == "" {
		return d, nil
	}
	ratio, _ := new(big.Rat).SetString(canonical)
	d.BaseFactor, d.AuxFactor = ratio.Num().Int64(), ratio.Denom().Int64()
	return d, nil
}

func (d productUnitDefinition) quantityQuantum(inputQuantity, inputUnit string) (int64, error) {
	inputQuantity = strings.TrimSpace(inputQuantity)
	if len(inputQuantity) > 30 || !productQuantityDecimal.MatchString(inputQuantity) {
		return 0, fail(400, "数量须为非负数，最多6位小数")
	}
	factor := d.BaseFactor
	if inputUnit == "aux" && d.AuxUnit != "" {
		factor = d.AuxFactor
	} else if inputUnit != "base" {
		return 0, fail(400, "数量单位无效")
	}
	if factor <= 0 {
		return 0, fail(400, "商品单位定义无效")
	}
	quantity, _ := new(big.Rat).SetString(inputQuantity)
	quantum := new(big.Rat).Mul(quantity, new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(productQuantityScale), big.NewInt(factor))))
	if !quantum.IsInt() || !quantum.Num().IsInt64() {
		return 0, fail(400, "数量超过精确换算范围")
	}
	return quantum.Num().Int64(), nil
}

func syncProductUnitDefinition(tx *sql.Tx, productID, baseUnit, auxUnit, ratioText string) error {
	d, err := newProductUnitDefinition(baseUnit, auxUnit, ratioText)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO product_unit_definitions(product_id,base_unit,aux_unit,base_factor,aux_factor,quantity_scale) VALUES(?,?,?,?,?,6)
		ON DUPLICATE KEY UPDATE version=version+IF(base_unit<>VALUES(base_unit) OR aux_unit<>VALUES(aux_unit) OR base_factor<>VALUES(base_factor) OR aux_factor<>VALUES(aux_factor),1,0),base_unit=VALUES(base_unit),aux_unit=VALUES(aux_unit),base_factor=VALUES(base_factor),aux_factor=VALUES(aux_factor)`, productID, d.BaseUnit, d.AuxUnit, d.BaseFactor, d.AuxFactor)
	return err
}
