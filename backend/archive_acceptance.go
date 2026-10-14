package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const acceptanceMaxRows = 1048576

type acceptanceOrder struct {
	ID, Number, Address, Date, Company, Contact, Note string
	Total                                             int64
	Lines                                             []archiveLine
	Worker, Status                                    string
	WagePaid                                          bool
	WageTotal                                         int64
	Materials                                         []acceptanceMaterial
	Sales                                             []Sale
	DeliveryWage                                      int64
	Wages                                             []workerWageRecord
}

type acceptanceMaterial struct {
	Product, Warehouse, Unit string
	Quantity                 int64
}

// The cursor is a pair because a page may end in the middle of an order.
func eachAcceptanceOrder(tx *sql.Tx, where string, args []any, companyID string, internal bool, yield func(acceptanceOrder) error) error {
	orderFields, lineFields := "", ""
	if internal {
		orderFields = ",COALESCE(NULLIF(o.worker_name,''),w.name,''),o.status,o.wage_paid,o.wage_cents"
		lineFields = ",COALESCE(l.wage_cents,0),COALESCE(l.labor_cents,0),COALESCE(NULLIF(l.worker_id,''),o.worker_id),COALESCE(NULLIF(l.worker_name,''),o.worker_name)"
	}
	query := `SELECT o.id,o.number,o.address,o.work_date,COALESCE(NULLIF(o.company_name,''),c.name),o.contact,o.note,o.total_cents,
		COALESCE(l.id,''),COALESCE(l.craft_id,''),COALESCE(l.category,''),COALESCE(l.name,''),COALESCE(l.spec,''),COALESCE(l.region,''),
		COALESCE(l.subitem_name,''),COALESCE(l.actual_location,''),COALESCE(l.description,''),COALESCE(l.mode,''),COALESCE(CAST(l.segments AS CHAR),'[]'),
		COALESCE(l.qty_micro,0),COALESCE(l.price_cents,0),COALESCE(l.total_cents,0),COALESCE(l.sort_order,0),COALESCE(l.charge_mode,'normal'),COALESCE(l.charge_note,''),COALESCE(l.use_included_price,0)` + orderFields + lineFields + `
		FROM orders o JOIN companies c ON c.id=o.company_id LEFT JOIN order_lines l ON l.order_id=o.id LEFT JOIN workers w ON w.id=o.worker_id
		WHERE ` + where + ` AND o.company_id=? AND (o.id>? OR (o.id=? AND COALESCE(l.id,'')>?))
		ORDER BY o.id,l.id LIMIT 500`
	orderID, lineID := "", ""
	var current *acceptanceOrder
	emit := func(o acceptanceOrder) error {
		sort.SliceStable(o.Lines, func(i, j int) bool { return o.Lines[i].SortOrder < o.Lines[j].SortOrder })
		var err error
		if o.Sales, err = readOrderSales(tx, o.ID); err != nil {
			return err
		}
		if internal {
			if o.Wages, err = readWorkerWages(tx, o.ID); err != nil {
				return err
			}
			if err = tx.QueryRow("SELECT delivery_wage_cents FROM orders WHERE id=?", o.ID).Scan(&o.DeliveryWage); err != nil {
				return err
			}
			rows, err := tx.Query(`SELECT product_name,warehouse_name,unit,qty_micro FROM order_materials WHERE order_id=? ORDER BY product_id,warehouse_id`, o.ID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var m acceptanceMaterial
				if err = rows.Scan(&m.Product, &m.Warehouse, &m.Unit, &m.Quantity); err != nil {
					break
				}
				o.Materials = append(o.Materials, m)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
		}
		return yield(o)
	}
	for {
		batchArgs := append(append([]any{}, args...), companyID, orderID, orderID, lineID)
		rows, err := tx.Query(query, batchArgs...)
		if err != nil {
			return err
		}
		seen := 0
		completed := []acceptanceOrder{}
		for rows.Next() {
			var o acceptanceOrder
			var l archiveLine
			dest := []any{&o.ID, &o.Number, &o.Address, &o.Date, &o.Company, &o.Contact, &o.Note, &o.Total,
				&l.ID, &l.CraftID, &l.Category, &l.Name, &l.Spec, &l.Region, &l.SubitemName, &l.ActualLocation, &l.Description, &l.Mode, &l.Segments,
				&l.QtyMicro, &l.PriceCents, &l.TotalCents, &l.SortOrder, &l.ChargeMode, &l.ChargeNote, &l.IncludedPrice}
			if internal {
				dest = append(dest, &o.Worker, &o.Status, &o.WagePaid, &o.WageTotal, &l.WageCents, &l.LaborCents, &l.WorkerID, &l.WorkerName)
			}
			if err = rows.Scan(dest...); err != nil {
				break
			}
			seen++
			orderID, lineID = o.ID, l.ID
			if current != nil && current.ID != o.ID {
				completed = append(completed, *current)
				current = nil
			}
			if current == nil {
				current = &o
			}
			if l.ID != "" {
				current.Lines = append(current.Lines, l)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		for _, o := range completed {
			if err = emit(o); err != nil {
				return err
			}
		}
		if seen == 0 {
			break
		}
	}
	if current != nil {
		return emit(*current)
	}
	return nil
}

type acceptanceSheet struct {
	writer io.Writer
	row    int
	merges []string
	breaks []int
}

func xmlText(s string) string {
	var clean strings.Builder
	for _, r := range s {
		if r == 9 || r == 10 || r == 13 || r >= 32 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF {
			clean.WriteRune(r)
		}
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(clean.String()))
	return escaped.String()
}

func (s *acceptanceSheet) begin() error {
	_, err := io.WriteString(s.writer, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetPr><pageSetUpPr fitToPage="1"/></sheetPr><sheetViews><sheetView workbookViewId="0" showGridLines="0"/></sheetViews><sheetFormatPr defaultRowHeight="20"/><cols><col min="1" max="1" width="23" customWidth="1"/><col min="2" max="2" width="20" customWidth="1"/><col min="3" max="4" width="34" customWidth="1"/><col min="5" max="5" width="18" customWidth="1"/><col min="6" max="6" width="18" customWidth="1"/></cols><sheetData>`)
	return err
}

type acceptanceCell struct {
	text   string
	style  int
	number bool
}

func (s *acceptanceSheet) writeRow(height int, cells ...acceptanceCell) error {
	s.row++
	if _, err := fmt.Fprintf(s.writer, `<row r="%d" ht="%d" customHeight="1">`, s.row, height); err != nil {
		return err
	}
	for i, cell := range cells {
		if cell.text == "" {
			continue
		}
		ref := string(rune('A'+i)) + strconv.Itoa(s.row)
		var err error
		if cell.number {
			_, err = fmt.Fprintf(s.writer, `<c r="%s" s="%d"><v>%s</v></c>`, ref, cell.style, cell.text)
		} else {
			_, err = fmt.Fprintf(s.writer, `<c r="%s" s="%d" t="inlineStr"><is><t>%s</t></is></c>`, ref, cell.style, xmlText(cell.text))
		}
		if err != nil {
			return err
		}
	}
	_, err := io.WriteString(s.writer, `</row>`)
	return err
}

func (s *acceptanceSheet) merge(first, last string) {
	s.merges = append(s.merges, first+strconv.Itoa(s.row)+":"+last+strconv.Itoa(s.row))
}

func (s *acceptanceSheet) mergeRows(col string, first, last int) {
	if last > first {
		s.merges = append(s.merges, fmt.Sprintf("%s%d:%s%d", col, first, col, last))
	}
}

func (s *acceptanceSheet) end() error {
	if _, err := io.WriteString(s.writer, `</sheetData>`); err != nil {
		return err
	}
	if len(s.merges) > 0 {
		if _, err := fmt.Fprintf(s.writer, `<mergeCells count="%d">`, len(s.merges)); err != nil {
			return err
		}
		for _, m := range s.merges {
			if _, err := fmt.Fprintf(s.writer, `<mergeCell ref="%s"/>`, m); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(s.writer, `</mergeCells>`); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(s.writer, `<printOptions horizontalCentered="1"/><pageMargins left="0.3" right="0.3" top="0.4" bottom="0.4" header="0.1" footer="0.1"/><pageSetup paperSize="9" orientation="portrait" fitToWidth="1" fitToHeight="0"/>`); err != nil {
		return err
	}
	if len(s.breaks) > 0 {
		if _, err := fmt.Fprintf(s.writer, `<rowBreaks count="%d" manualBreakCount="%d">`, len(s.breaks), len(s.breaks)); err != nil {
			return err
		}
		for _, b := range s.breaks {
			if _, err := fmt.Fprintf(s.writer, `<brk id="%d" man="1" max="16383"/>`, b); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(s.writer, `</rowBreaks>`); err != nil {
			return err
		}
	}
	_, err := io.WriteString(s.writer, `</worksheet>`)
	return err
}

type acceptanceDetail struct{ region, wall, floor, quantity string }
type acceptanceGroup struct {
	label, mode  string
	price, total int64
	rows         []acceptanceDetail
}

func archiveLineLocation(l archiveLine) string {
	parts := []string{}
	if l.SubitemName != "" {
		parts = append(parts, l.SubitemName)
	}
	if l.ActualLocation != "" && l.ActualLocation != l.SubitemName {
		if l.SubitemName != "" {
			parts = append(parts, "实际部位："+l.ActualLocation)
		} else {
			parts = append(parts, l.ActualLocation)
		}
	}
	if l.Region != "" && l.Region != l.ActualLocation && l.Region != l.SubitemName {
		if l.SubitemName != "" || l.ActualLocation != "" {
			parts = append(parts, "原部位："+l.Region)
		} else {
			parts = append(parts, l.Region)
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, "；")
}

func archiveLineChargeNote(l archiveLine) string {
	switch l.ChargeMode {
	case "free":
		return "免费施工"
	case "redo":
		return "补做不收费"
	}
	if l.ChargeNote != "" {
		return l.ChargeNote
	}
	if l.IncludedPrice {
		return "组合含价，不另收费"
	}
	return ""
}

func acceptanceGroups(o acceptanceOrder) ([]acceptanceGroup, error) {
	groups := []acceptanceGroup{}
	index := map[string]int{}
	for _, l := range o.Lines {
		if l.QtyMicro <= 0 && l.TotalCents <= 0 {
			continue
		}
		key := strings.Join([]string{l.CraftID, l.Category, l.Name, l.Mode, strconv.FormatInt(l.PriceCents, 10), l.ChargeMode, l.ChargeNote, strconv.FormatBool(l.IncludedPrice)}, "\x00")
		i, exists := index[key]
		if !exists {
			label := l.Name
			if l.Category != l.Name {
				label = l.Category + " / " + l.Name
			}
			i = len(groups)
			index[key] = i
			groups = append(groups, acceptanceGroup{label: label, mode: l.Mode, price: l.PriceCents})
		}
		if l.TotalCents < 0 || groups[i].total > math.MaxInt64-l.TotalCents {
			return nil, fail(422, "验收单工艺金额超出可计算范围")
		}
		groups[i].total += l.TotalCents
		var segments []Segment
		if err := json.Unmarshal([]byte(l.Segments), &segments); err != nil {
			return nil, err
		}
		wall, floor := []string{}, []string{}
		for _, segment := range segments {
			mode := segment.Mode
			if mode == "" {
				mode = l.Mode
			}
			text := ""
			if mode == "金额" {
				text = "金额 " + scaledDecimal(segment.Amount, 2) + " 元"
			} else if countCraftMode(mode) {
				text = strconv.FormatInt(segment.Count, 10) + " " + mode
			} else if mode == "米" {
				text = strconv.FormatFloat(segment.Length, 'f', -1, 64) + " 米"
			} else {
				text = strconv.FormatFloat(segment.Length, 'f', -1, 64) + " × " + strconv.FormatFloat(segment.Width, 'f', -1, 64)
				if mode == "立方" {
					text += " × " + strconv.FormatFloat(segment.Height, 'f', -1, 64)
				}
			}
			if strings.Contains(segment.Surface, "墙") {
				wall = append(wall, text)
			} else {
				floor = append(floor, text)
			}
		}
		region := archiveLineLocation(l)
		floorText := strings.Join(floor, " + ")
		if floorText == "" && l.Mode == "金额" {
			floorText = l.Description
			if floorText == "" {
				floorText = "按金额计价"
			}
		}
		if floorText == "" {
			floorText = "／"
		}
		if l.Description != "" && l.Mode != "金额" {
			floorText += "\n" + l.Description
		}
		if chargeNote := archiveLineChargeNote(l); chargeNote != "" {
			floorText += "\n" + chargeNote
		}
		wallText := strings.Join(wall, " + ")
		if wallText == "" {
			wallText = "／"
		}
		groups[i].rows = append(groups[i].rows, acceptanceDetail{region, wallText, floorText, segmentQuantityLabel(l.Mode, l.Segments, l.QtyMicro)})
	}
	return groups, nil
}

func acceptanceOrderRows(o acceptanceOrder, internal bool) (int, error) {
	groups, err := acceptanceGroups(o)
	if err != nil {
		return 0, err
	}
	// Title, two metadata rows, headings, details, group totals, grand total, footer and separator.
	rows := 7 + len(o.Sales)
	if len(groups) == 0 {
		rows++
	}
	for _, g := range groups {
		rows += len(g.rows) + 1
	}
	if o.Note != "" {
		rows++
	}
	if internal {
		rows += 5 + max(1, len(o.Lines)) + max(1, len(o.Materials)) + len(o.Wages)
		if o.DeliveryWage != 0 {
			rows++
		}
	}
	return rows, nil
}

func (s *acceptanceSheet) writeOrder(o acceptanceOrder, title, contractor string, internal bool) error {
	groups, err := acceptanceGroups(o)
	if err != nil {
		return err
	}
	if s.row > 0 {
		s.breaks = append(s.breaks, s.row)
	}
	if err := s.writeRow(32, acceptanceCell{text: title, style: 1}); err != nil {
		return err
	}
	s.merge("A", "F")
	if err := s.writeRow(25, acceptanceCell{text: "施工单位", style: 3}, acceptanceCell{text: contractor, style: 2}, acceptanceCell{}, acceptanceCell{text: "客户", style: 3}, acceptanceCell{text: o.Company, style: 2}); err != nil {
		return err
	}
	s.merges = append(s.merges, fmt.Sprintf("B%d:C%d", s.row, s.row), fmt.Sprintf("E%d:F%d", s.row, s.row))
	if err := s.writeRow(25, acceptanceCell{text: "施工地址", style: 3}, acceptanceCell{text: o.Address, style: 2}); err != nil {
		return err
	}
	s.merge("B", "F")
	if err := s.writeRow(27, acceptanceCell{text: "施工项目", style: 3}, acceptanceCell{text: "施工区域", style: 3}, acceptanceCell{text: "墙面尺寸 / 尺寸", style: 3}, acceptanceCell{text: "地面 / 其他尺寸", style: 3}, acceptanceCell{text: "小计", style: 3}, acceptanceCell{text: "合计", style: 3}); err != nil {
		return err
	}
	for _, group := range groups {
		first := s.row + 1
		for rowIndex, detail := range group.rows {
			cells := []acceptanceCell{{}, {text: detail.region, style: 4}, {text: detail.wall, style: 4}, {text: detail.floor, style: 4}, {text: detail.quantity, style: 4}, {}}
			if rowIndex == 0 {
				cells[0] = acceptanceCell{text: group.label, style: 4}
				cells[5] = acceptanceCell{text: scaledDecimal(group.total, 2), style: 5, number: true}
			}
			if err := s.writeRow(43, cells...); err != nil {
				return err
			}
		}
		s.mergeRows("A", first, s.row)
		s.mergeRows("F", first, s.row)
		price := ""
		if group.mode != "金额" {
			price = "单价 " + scaledDecimal(group.price, 2) + " 元；"
			if group.mode == "米" {
				price = "单价 " + scaledDecimal(group.price, 2) + " 元/米；"
			}
		}
		if err := s.writeRow(26, acceptanceCell{text: group.label + "金额", style: 6}, acceptanceCell{}, acceptanceCell{text: price + "金额 " + scaledDecimal(group.total, 2) + " 元", style: 6}); err != nil {
			return err
		}
		s.merges = append(s.merges, fmt.Sprintf("A%d:B%d", s.row, s.row), fmt.Sprintf("C%d:F%d", s.row, s.row))
	}
	if len(groups) == 0 {
		if err := s.writeRow(30, acceptanceCell{text: "本次无施工工艺明细", style: 4}); err != nil {
			return err
		}
		s.merge("A", "F")
	}
	for _, sale := range o.Sales {
		if err := s.writeRow(30, acceptanceCell{text: "材料销售", style: 4}, acceptanceCell{text: sale.ProductName, style: 4}, acceptanceCell{text: scaledDecimal(sale.SaleQty, 6) + " " + sale.PriceUnit, style: 4}, acceptanceCell{text: "单价 " + scaledDecimal(sale.Price, 2) + " 元/" + sale.PriceUnit, style: 4}, acceptanceCell{}, acceptanceCell{text: scaledDecimal(sale.Total, 2), style: 5, number: true}); err != nil {
			return err
		}
	}
	if err := s.writeRow(29, acceptanceCell{text: "合计金额", style: 6}, acceptanceCell{}, acceptanceCell{text: "合计：" + scaledDecimal(o.Total, 2) + " 元", style: 6}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{text: scaledDecimal(o.Total, 2), style: 7, number: true}); err != nil {
		return err
	}
	s.merges = append(s.merges, fmt.Sprintf("A%d:B%d", s.row, s.row), fmt.Sprintf("C%d:E%d", s.row, s.row))
	if err := s.writeRow(25, acceptanceCell{text: "施工时间：" + o.Date, style: 2}, acceptanceCell{}, acceptanceCell{text: "项目经理：" + o.Contact, style: 2}, acceptanceCell{}, acceptanceCell{text: "单号：" + o.Number, style: 2}); err != nil {
		return err
	}
	if o.Note != "" {
		if err := s.writeRow(25, acceptanceCell{text: "备注：" + o.Note, style: 2}); err != nil {
			return err
		}
		s.merge("A", "F")
	}
	if internal {
		if err := s.writeInternalDetails(o); err != nil {
			return err
		}
	}
	return s.writeRow(12)
}

func (s *acceptanceSheet) writeInternalDetails(o acceptanceOrder) error {
	if err := s.writeRow(27, acceptanceCell{text: "公司内部记录（含工人工资）", style: 3}); err != nil {
		return err
	}
	s.merge("A", "F")
	wageState := "未记录付款"
	if o.WagePaid {
		wageState = "已有付款记录（实际金额见工资管理）"
	}
	if err := s.writeRow(25, acceptanceCell{text: "材料/送货人", style: 3}, acceptanceCell{text: o.Worker, style: 2}, acceptanceCell{}, acceptanceCell{text: "单据 / 付款记录", style: 3}, acceptanceCell{text: o.Status + " / " + wageState, style: 2}); err != nil {
		return err
	}
	s.merges = append(s.merges, fmt.Sprintf("B%d:C%d", s.row, s.row), fmt.Sprintf("E%d:F%d", s.row, s.row))
	if err := s.writeRow(27, acceptanceCell{text: "工艺", style: 3}, acceptanceCell{text: "施工区域", style: 3}, acceptanceCell{text: "计费模式", style: 3}, acceptanceCell{text: "工程量", style: 3}, acceptanceCell{text: "人工单价（元）", style: 3}, acceptanceCell{text: "工人工资（元）", style: 3}); err != nil {
		return err
	}
	for _, l := range o.Lines {
		label := l.Name
		if l.Category != l.Name {
			label = l.Category + " / " + l.Name
		}
		if err := s.writeRow(30, acceptanceCell{text: label, style: 4}, acceptanceCell{text: archiveLineLocation(l) + "\n施工人：" + l.WorkerName, style: 4}, acceptanceCell{text: l.Mode, style: 4}, acceptanceCell{text: segmentQuantityLabel(l.Mode, l.Segments, l.QtyMicro), style: 4}, acceptanceCell{text: scaledDecimal(l.WageCents, 2), style: 5, number: true}, acceptanceCell{text: scaledDecimal(l.LaborCents, 2), style: 5, number: true}); err != nil {
			return err
		}
	}
	if len(o.Lines) == 0 {
		if err := s.writeRow(25, acceptanceCell{text: "该历史单无工艺明细", style: 4}); err != nil {
			return err
		}
		s.merge("A", "F")
	}
	if o.DeliveryWage != 0 {
		if err := s.writeRow(26, acceptanceCell{text: "本次带货工资（总额）", style: 4}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{text: scaledDecimal(o.DeliveryWage, 2), style: 5, number: true}); err != nil {
			return err
		}
		s.merge("A", "E")
	}
	if err := s.writeRow(27, acceptanceCell{text: "工资合计", style: 6}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{text: scaledDecimal(o.WageTotal, 2), style: 7, number: true}); err != nil {
		return err
	}
	s.merge("A", "E")
	for _, w := range o.Wages {
		state := "未记录付款"
		if w.WagePaid {
			state = "已有付款记录（实际金额见工资管理）"
		}
		if err := s.writeRow(27, acceptanceCell{text: "分人工资：" + w.WorkerName, style: 4}, acceptanceCell{text: state, style: 4}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{}, acceptanceCell{text: scaledDecimal(w.WageCents, 2), style: 5, number: true}); err != nil {
			return err
		}
		s.merge("B", "E")
	}
	if err := s.writeRow(27, acceptanceCell{text: "材料", style: 3}, acceptanceCell{text: "出库仓库", style: 3}, acceptanceCell{text: "使用数量", style: 3}); err != nil {
		return err
	}
	s.merge("C", "F")
	for _, m := range o.Materials {
		quantity := scaledDecimal(m.Quantity, 6)
		if m.Unit != "" {
			quantity += " " + m.Unit
		}
		if err := s.writeRow(25, acceptanceCell{text: m.Product, style: 4}, acceptanceCell{text: m.Warehouse, style: 4}, acceptanceCell{text: quantity, style: 4}); err != nil {
			return err
		}
		s.merge("C", "F")
	}
	if len(o.Materials) == 0 {
		if err := s.writeRow(25, acceptanceCell{text: "未记录材料", style: 4}); err != nil {
			return err
		}
		s.merge("A", "F")
	}
	return nil
}

const acceptanceStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="3"><font><sz val="11"/><name val="宋体"/></font><font><b/><sz val="16"/><name val="宋体"/></font><font><b/><sz val="11"/><name val="宋体"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFE9EEF2"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left style="thin"/><right style="thin"/><top style="thin"/><bottom style="thin"/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="8"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="center" vertical="center"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="2" fillId="1" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="center" vertical="center" wrapText="1"/></xf><xf numFmtId="2" fontId="0" fillId="0" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf><xf numFmtId="0" fontId="2" fillId="1" borderId="1" xfId="0" applyAlignment="1"><alignment vertical="center"/></xf><xf numFmtId="2" fontId="2" fillId="1" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf></cellXfs></styleSheet>`

func zipText(z *zip.Writer, path, body string) error {
	w, err := z.Create(path)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, body)
	return err
}

func writeAcceptanceWorkbook(dst io.Writer, title, contractor string, internal bool, each func(func(acceptanceOrder) error) error) (count int, total int64, err error) {
	z := zip.NewWriter(dst)
	var sheet *acceptanceSheet
	sheetCount := 0
	openSheet := func() error {
		sheetCount++
		w, e := z.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", sheetCount))
		if e != nil {
			return e
		}
		sheet = &acceptanceSheet{writer: w}
		return sheet.begin()
	}
	if err = openSheet(); err != nil {
		return
	}
	err = each(func(o acceptanceOrder) error {
		needed, rowErr := acceptanceOrderRows(o, internal)
		if rowErr != nil {
			return rowErr
		}
		if needed > acceptanceMaxRows {
			return fail(422, "单张验收单超过 Excel 行数上限")
		}
		if sheet.row+needed > acceptanceMaxRows {
			if e := sheet.end(); e != nil {
				return e
			}
			if e := openSheet(); e != nil {
				return e
			}
		}
		if o.Total < 0 || total > math.MaxInt64-o.Total {
			return fail(422, "归档金额超出可计算范围")
		}
		if e := sheet.writeOrder(o, title, contractor, internal); e != nil {
			return e
		}
		count++
		total += o.Total
		return nil
	})
	if err != nil {
		z.Close()
		return
	}
	if err = sheet.end(); err != nil {
		z.Close()
		return
	}
	workbook := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`
	for i := 1; i <= sheetCount; i++ {
		workbook += fmt.Sprintf(`<sheet name="验收单%d" sheetId="%d" r:id="rId%d"/>`, i, i, i)
		rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i)
		contentTypes += fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
	}
	workbook += `</sheets></workbook>`
	rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, sheetCount+1)
	contentTypes += `</Types>`
	styles := strings.Replace(acceptanceStyles, "</styleSheet>", `<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`, 1)
	for _, part := range [][2]string{{"[Content_Types].xml", contentTypes}, {"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`}, {"xl/workbook.xml", workbook}, {"xl/_rels/workbook.xml.rels", rels}, {"xl/styles.xml", styles}} {
		if err = zipText(z, part[0], part[1]); err != nil {
			z.Close()
			return
		}
	}
	err = z.Close()
	return
}

func writeAcceptanceArchive(tx *sql.Tx, where string, args []any, companyID, companyName, run, dir string, internal bool) (ArchiveFile, error) {
	settings := PrintSettings{Title: "施工验收单", Company: "特固施工"}
	var raw string
	if err := tx.QueryRow("SELECT value FROM settings WHERE setting_key='print'").Scan(&raw); err == nil {
		if err = json.Unmarshal([]byte(raw), &settings); err != nil {
			return ArchiveFile{}, err
		}
	} else if err != sql.ErrNoRows {
		return ArchiveFile{}, err
	}
	name := id() + ".xlsx"
	path := filepath.Join(dir, name)
	f, err := os.Create(path + ".partial")
	if err != nil {
		return ArchiveFile{}, err
	}
	count, total, err := writeAcceptanceWorkbook(f, settings.Title, settings.Company, internal, func(yield func(acceptanceOrder) error) error {
		return eachAcceptanceOrder(tx, where, args, companyID, internal, yield)
	})
	if err != nil {
		f.Close()
		return ArchiveFile{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return ArchiveFile{}, err
	}
	if err = f.Close(); err != nil {
		return ArchiveFile{}, err
	}
	if err = os.Rename(path+".partial", path); err != nil {
		return ArchiveFile{}, err
	}
	kind := "施工验收表"
	if internal {
		kind = "施工内部留档表"
	}
	return ArchiveFile{File: run + "/" + name, CompanyID: companyID, Company: companyName, Kind: kind, Count: count, Amount: total, Available: true}, nil
}
