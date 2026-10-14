package main

import (
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

type Segment struct {
	Length  float64 `json:"length"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Surface string  `json:"surface"`
	Count   int64   `json:"count,omitempty"`
	Mode    string  `json:"mode,omitempty"`
	Amount  int64   `json:"amount_cents,omitempty"`
}
type Line struct {
	Worker               string    `json:"worker_id"`
	WorkerName           string    `json:"worker_name"`
	Spec                 string    `json:"spec"`
	Region               string    `json:"region"` // Legacy mixed snapshot; never interpreted as an actual location.
	SubitemName          string    `json:"subitem_name"`
	ActualLocation       string    `json:"actual_location"`
	Description          string    `json:"description"`
	ID                   string    `json:"id"`
	ClientRef            string    `json:"client_ref,omitempty"`
	PackageIncluded      bool      `json:"package_included"`
	WaivedQty            int64     `json:"waived_qty_micro"`
	CraftID              string    `json:"craft_id"`
	Category             string    `json:"category"`
	Name                 string    `json:"name"`
	Mode                 string    `json:"mode"`
	Segments             []Segment `json:"segments"`
	Qty                  int64     `json:"qty_micro"`
	Price                int64     `json:"price_cents"`
	Wage                 int64     `json:"wage_cents"`
	WageConfigured       bool      `json:"wage_configured"`
	Total                int64     `json:"total_cents"`
	Labor                int64     `json:"labor_cents"`
	Temporary            bool      `json:"temporary"`
	SubitemID            string    `json:"subitem_id"`
	SortOrder            int       `json:"sort_order"`
	Editable             bool      `json:"editable"`
	UseIncludedPrice     bool      `json:"use_included_price"`
	IncludedByCraftID    string    `json:"included_by_craft_id"`
	ChargeNote           string    `json:"charge_note"`
	ChargeMode           string    `json:"charge_mode"`
	WageOverrideCents    *int64    `json:"wage_override_cents,omitempty"`
	WageAdjustmentReason string    `json:"wage_adjustment_reason,omitempty"`

	// Older clients omit actual_location. An explicit empty string still clears it.
	actualLocationOmitted bool
	wageOverrideOmitted   bool
	standardLaborCents    int64
	ticketBatchID         string `json:"-"`
}

func (l *Line) UnmarshalJSON(data []byte) error {
	type plainLine Line
	var parsed plainLine
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Match encoding/json's case-insensitive struct-field lookup.
	parsed.actualLocationOmitted = true
	parsed.wageOverrideOmitted = true
	for key := range fields {
		if strings.EqualFold(key, "actual_location") {
			parsed.actualLocationOmitted = false
		}
		if strings.EqualFold(key, "wage_override_cents") {
			parsed.wageOverrideOmitted = false
		}
	}
	*l = Line(parsed)
	return nil
}

type Material struct {
	ProductName     string `json:"product_name"`
	Unit            string `json:"unit"`
	AuxUnit         string `json:"aux_unit"`
	WarehouseName   string `json:"warehouse_name"`
	LineID          string `json:"line_id"`
	Product         string `json:"product_id"`
	Warehouse       string `json:"warehouse_id"`
	Qty             int64  `json:"qty_micro"`
	QtyQuantum      string `json:"qty_quantum"`
	InputUnit       string `json:"input_unit"`
	InputQty        string `json:"input_qty"`
	ConversionRatio string `json:"conversion_ratio"`
}
type Order struct {
	ID                 string                `json:"id"`
	Number             string                `json:"number"`
	Company            string                `json:"company_id"`
	Project            string                `json:"project_id"`
	PackageID          string                `json:"package_id"`
	Address            string                `json:"address"`
	Date               string                `json:"work_date"`
	Worker             string                `json:"worker_id"`
	Warehouse          string                `json:"warehouse_id"`
	Contact            string                `json:"contact"`
	Phone              string                `json:"phone"`
	Note               string                `json:"note"`
	TicketNo           string                `json:"ticket_no"`
	DeliveredAt        string                `json:"delivered_at"`
	Recipient          string                `json:"recipient"`
	TicketAttachment   string                `json:"ticket_attachment"`
	ReturnNote         string                `json:"return_note"`
	Version            int64                 `json:"version"`
	Lines              []Line                `json:"lines"`
	Materials          []Material            `json:"materials"`
	Sales              []Sale                `json:"sales"`
	DeliveryWage       int64                 `json:"delivery_wage_cents"`
	TicketAction       string                `json:"ticket_action"`
	TicketID           string                `json:"ticket_id"`
	TicketVersion      int64                 `json:"ticket_version"`
	SalesTicketSlot    int                   `json:"sales_ticket_slot"`
	Delete             bool                  `json:"delete"`
	Combinations       *[]ProjectCombination `json:"combinations,omitempty"`
	CombinationVersion int64                 `json:"combination_version"`
	legacyIncluded     bool
}

func nullDate(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func calcLine(l *Line) error {
	if !contains(categories, l.Category) || l.Name == "" || !validCraftMode(l.Mode) {
		return fail(400, "请填写正确工艺、名称和计算方式")
	}
	if l.Price < 0 || l.Wage < 0 || l.Price > 100000000 || l.Wage > 100000000 {
		return fail(400, "单价超出范围")
	}
	qty := new(big.Rat)
	l.Total, l.Labor, l.Qty = 0, 0, 0
	if l.Mode == "金额" {
		l.Qty = 1000000
		l.Total = l.Price
		l.Labor = l.Wage
		return nil
	}
	if countCraftMode(l.Mode) {
		var count int64
		if len(l.Segments) == 0 || len(l.Segments) > 100 {
			return fail(400, "请填写整数数量")
		}
		for _, s := range l.Segments {
			if (s.Mode != "" && s.Mode != l.Mode) || s.Count <= 0 || s.Count > 1000000 {
				return fail(400, l.Mode+"数量须为1至1000000的整数")
			}
			count += s.Count
		}
		if count > 1000000 {
			return fail(400, "合计"+l.Mode+"数量超出范围")
		}
		l.Qty, l.Total, l.Labor = count*1000000, count*l.Price, count*l.Wage
		return nil
	}
	if len(l.Segments) == 0 || len(l.Segments) > 100 {
		return fail(400, "请填写施工尺寸")
	}
	for _, s := range l.Segments {
		segmentMode := s.Mode
		if segmentMode == "" {
			segmentMode = l.Mode
		}
		if segmentMode != l.Mode {
			return fail(400, "同一收费项不能混用计量方式，请分别录入")
		}
		if segmentMode == "米" {
			if s.Length <= 0 || s.Length > 10000 || s.Width != 0 || s.Height != 0 || s.Count != 0 || s.Amount != 0 {
				return fail(400, "按米只填写大于零且不超过10000米的长度，不填写宽、高、个数或金额")
			}
			length, _ := new(big.Rat).SetString(strconv.FormatFloat(s.Length, 'f', -1, 64))
			if length == nil {
				return fail(400, "长度无效")
			}
			qty.Add(qty, length)
			continue
		}
		if s.Length <= 0 || s.Width <= 0 || s.Length > 10000 || s.Width > 10000 {
			return fail(400, "长宽必须大于零且在合理范围")
		}
		length, _ := new(big.Rat).SetString(strconv.FormatFloat(s.Length, 'f', -1, 64))
		width, _ := new(big.Rat).SetString(strconv.FormatFloat(s.Width, 'f', -1, 64))
		if length == nil || width == nil {
			return fail(400, "尺寸无效")
		}
		v := new(big.Rat).Mul(length, width)
		if segmentMode == "立方" {
			if s.Height <= 0 || s.Height > 10000 {
				return fail(400, "请填写有效高度")
			}
			height, _ := new(big.Rat).SetString(strconv.FormatFloat(s.Height, 'f', -1, 64))
			if height == nil {
				return fail(400, "高度无效")
			}
			v.Mul(v, height)
		}
		qty.Add(qty, v)
	}
	if qty.Cmp(big.NewRat(1000000, 1)) > 0 {
		return fail(400, "施工数量过大")
	}
	measuredQty := roundPositive(new(big.Rat).Mul(qty, big.NewRat(1000000, 1)))
	l.Qty = measuredQty
	if l.Qty <= 0 && (qty.Sign() > 0 || l.Total == 0) {
		return fail(400, "施工数量小于最小精度0.000001")
	}
	l.Total += scaledAmount(measuredQty, l.Price)
	l.Labor += scaledAmount(measuredQty, l.Wage)
	return nil
}

func validChargeMode(mode string) bool {
	return mode == "normal" || mode == "free" || mode == "redo"
}

func normalizeChargeMode(mode string) string {
	if mode == "" {
		return "normal"
	}
	return mode
}
func roundPositive(r *big.Rat) int64 {
	r = new(big.Rat).Add(r, big.NewRat(1, 2))
	return new(big.Int).Quo(r.Num(), r.Denom()).Int64()
}
func scaledAmount(q, p int64) int64 {
	return roundPositive(new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(q), big.NewInt(p)), big.NewInt(1000000)))
}

// Mixed dimensions must not be presented as one physical unit in monthly exports.
func segmentQuantityLabel(mode, raw string, fallback int64) string {
	if countCraftMode(mode) {
		return strconv.FormatInt(fallback/1000000, 10) + " " + mode
	}
	if mode == "金额" {
		return "1 项"
	}
	fallbackLabel := scaledDecimal(fallback, 6)
	if mode == "米" {
		fallbackLabel += " 米"
	}
	var segments []Segment
	if json.Unmarshal([]byte(raw), &segments) != nil || len(segments) == 0 {
		return fallbackLabel
	}
	parts := []string{}
	for _, m := range []string{"平方", "立方", "米"} {
		selected := []Segment{}
		for _, s := range segments {
			sm := s.Mode
			if sm == "" {
				sm = mode
			}
			if sm == m {
				s.Mode = m
				selected = append(selected, s)
			}
		}
		if len(selected) == 0 {
			continue
		}
		l := Line{Category: "开工", Name: "工程量", Mode: m, Segments: selected}
		if calcLine(&l) != nil {
			return fallbackLabel
		}
		unit := "㎡"
		if m == "立方" {
			unit = "m³"
		} else if m == "米" {
			unit = "米"
		}
		parts = append(parts, scaledDecimal(l.Qty, 6)+" "+unit)
	}
	if len(parts) == 0 {
		return "按金额计费"
	}
	return strings.Join(parts, " + ")
}
func (a *App) listOrders(w http.ResponseWriter, r *http.Request, u User) error {
	q := r.URL.Query()
	view := q.Get("view")
	permission := "orders"
	if view == "finance" {
		permission = "finance"
	}
	if view == "wages" {
		permission = "wages"
	}
	if !u.can(permission) {
		return fail(403, "没有查看权限")
	}
	if view == "finance" {
		if e := requireCustomerPrices(u); e != nil {
			return e
		}
	}
	if q.Get("wage_paid") != "" && !u.can("wages") {
		return fail(403, "无工资筛选权限")
	}
	if view == "wages" {
		return a.listWorkerWages(w, r, u)
	}
	where, args, e := orderFilter(q)
	if e != nil {
		return e
	}
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count, total, wages, wagePendingCount int64
	if e = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(CASE WHEN o.status<>'已取消' THEN o.total_cents ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.status<>'已取消' AND NOT EXISTS(SELECT 1 FROM order_lines l WHERE l.order_id=o.id AND l.wage_configured=0 AND l.wage_override_cents IS NULL) THEN o.wage_cents ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.status<>'已取消' AND EXISTS(SELECT 1 FROM order_lines l WHERE l.order_id=o.id AND l.wage_configured=0 AND l.wage_override_cents IS NULL) THEN 1 ELSE 0 END),0) FROM orders o WHERE "+where, args...).Scan(&count, &total, &wages, &wagePendingCount); e != nil {
		return e
	}
	v, e := readOrderList(tx, where, args, page, u)
	if e != nil {
		return e
	}
	if !u.can("wages") {
		wages, wagePendingCount = 0, 0
	}
	writeOrderList(w, v, count, total, wages, wagePendingCount, page, u)
	return nil
}
