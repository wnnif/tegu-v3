package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxTicketBatchItems = 500
const maxTicketBatchCents int64 = 100000000000000

type ticketIssueCompany struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Additive customer-facing print facts live in the existing immutable JSON
// snapshot. Old batches omit these fields; they never read today's order back.
type ticketPrintFields struct {
	Category      string `json:"category,omitempty"`
	QtyMicro      string `json:"qty_micro,omitempty"`
	PriceCents    *int64 `json:"price_cents,omitempty"`
	Segments      string `json:"segments,omitempty"`
	Region        string `json:"region,omitempty"`
	Description   string `json:"description,omitempty"`
	ChargeMode    string `json:"charge_mode,omitempty"`
	ChargeNote    string `json:"charge_note,omitempty"`
	IncludedPrice bool   `json:"use_included_price,omitempty"`
}

type ticketIssueRow struct {
	ticketPrintFields
	ItemID       string  `json:"item_id"`
	ItemKind     string  `json:"item_kind"`
	OrderID      string  `json:"order_id"`
	WorkDate     string  `json:"work_date"`
	OrderNumber  string  `json:"order_number"`
	ProjectID    string  `json:"project_id"`
	ProjectName  string  `json:"project_name"`
	Address      string  `json:"address"`
	CraftID      string  `json:"craft_id,omitempty"`
	SubitemID    string  `json:"subitem_id,omitempty"`
	ItemName     string  `json:"item_name"`
	SubitemName  string  `json:"subitem_name"`
	Location     string  `json:"location"`
	WorkerName   string  `json:"worker_name"`
	Quantity     float64 `json:"quantity"`
	Unit         string  `json:"unit"`
	TotalCents   int64   `json:"total_cents"`
	Status       string  `json:"status"`
	BatchID      string  `json:"ticket_batch_id,omitempty"`
	OrderVersion int64   `json:"order_version"`
}

type ticketReminder struct {
	ProjectID      string `json:"project_id"`
	ProjectName    string `json:"project_name"`
	Address        string `json:"address"`
	CraftID        string `json:"craft_id"`
	CraftName      string `json:"craft_name"`
	SubitemID      string `json:"subitem_id"`
	SubitemName    string `json:"subitem_name"`
	Status         string `json:"status"`
	TotalCount     int64  `json:"total_count"`
	PendingCount   int64  `json:"pending_count"`
	DeliveredCount int64  `json:"delivered_count"`
	Unmapped       bool   `json:"unmapped,omitempty"`
}

type ticketBatchView struct {
	ID            string `json:"id"`
	CompanyID     string `json:"company_id"`
	CompanyName   string `json:"company_name"`
	Number        string `json:"number"`
	TicketNo      string `json:"ticket_no"`
	DeliveredAt   string `json:"delivered_at"`
	Recipient     string `json:"recipient"`
	Status        string `json:"status"`
	Version       int64  `json:"version"`
	TotalCents    int64  `json:"total_cents"`
	VoidReason    string `json:"void_reason"`
	VoidedByName  string `json:"voided_by_name"`
	CreatedByName string `json:"created_by_name"`
	CreatedAt     string `json:"created_at"`
}

type ticketBatchItemView struct {
	ticketPrintFields
	ItemID                string  `json:"item_id"`
	ItemKind              string  `json:"item_kind"`
	OrderID               string  `json:"order_id"`
	WorkDate              string  `json:"work_date"`
	OrderNumber           string  `json:"order_number"`
	ProjectID             string  `json:"project_id"`
	ProjectName           string  `json:"project_name"`
	Address               string  `json:"address"`
	ItemName              string  `json:"item_name"`
	CraftID               string  `json:"craft_id,omitempty"`
	SubitemID             string  `json:"subitem_id,omitempty"`
	SubitemName           string  `json:"subitem_name"`
	Location              string  `json:"location"`
	WorkerName            string  `json:"worker_name"`
	Quantity              float64 `json:"quantity"`
	Unit                  string  `json:"unit"`
	TotalCents            int64   `json:"total_cents"`
	SourceProjectTicketID string  `json:"source_project_ticket_id"`
	PackageID             string  `json:"package_id,omitempty"`
	PackageType           string  `json:"package_type,omitempty"`
	PackageRoomCount      int     `json:"package_room_count,omitempty"`
}

type ticketBatchResponse struct {
	Batch ticketBatchView       `json:"batch"`
	Items []ticketBatchItemView `json:"items"`
}

type ticketIssueResponse struct {
	Company       ticketIssueCompany `json:"company"`
	Rows          []ticketIssueRow   `json:"rows"`
	Reminders     []ticketReminder   `json:"reminders"`
	Batches       []ticketBatchView  `json:"batches"`
	Total         int64              `json:"total"`
	Page          int                `json:"page"`
	PageSize      int                `json:"page_size"`
	BatchTotal    int64              `json:"batch_total"`
	BatchPage     int                `json:"batch_page"`
	BatchPageSize int                `json:"batch_page_size"`
}

type ticketSelection struct {
	ItemID       string `json:"item_id"`
	ItemKind     string `json:"item_kind"`
	OrderVersion int64  `json:"order_version"`
}

func (s *ticketSelection) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name := range fields {
		if name != "item_id" && name != "item_kind" && name != "order_version" {
			return fmt.Errorf("交票明细只接受完整行编号、类型和预览版本")
		}
	}
	type plain ticketSelection
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.OrderVersion < 1 {
		return fmt.Errorf("交票明细须携带有效的预览施工版本")
	}
	*s = ticketSelection(decoded)
	return nil
}

type ticketBatchCreateRequest struct {
	CompanyID   string            `json:"company_id"`
	Items       []ticketSelection `json:"items"`
	TicketNo    string            `json:"ticket_no"`
	DeliveredAt string            `json:"delivered_at"`
	Recipient   string            `json:"recipient"`
}

type ticketBatchActionRequest struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Action  string `json:"action"`
	Reason  string `json:"reason"`
}

type ticketOrderMeta struct {
	ID            string
	CompanyID     string
	ProjectID     string
	Status        string
	Number        string
	WorkDate      string
	ProjectName   string
	Address       string
	WorkerName    string
	TicketManaged bool
	Version       int64
}

type ticketOrderRef struct {
	Kind      string
	ItemID    string
	OrderID   string
	ProjectID string
	CompanyID string
}

type ticketBatchHeader struct {
	Batch     ticketBatchView
	CompanyID string
	CreatedBy string
	VoidedBy  string
}

func ticketIssuePermission(u User) error {
	if !u.can("finance") {
		return fail(403, "无交票查看或操作权限")
	}
	return requireCustomerPrices(u)
}

func (a *App) ticketIssueRead(w http.ResponseWriter, r *http.Request, u User) error {
	if err := ticketIssuePermission(u); err != nil {
		return err
	}
	companyID := strings.TrimSpace(r.URL.Query().Get("company_id"))
	if companyID == "" || len(companyID) > 64 {
		return fail(400, "请选择公司")
	}
	page, pageSize, err := ticketPage(r.URL.Query().Get("page"), r.URL.Query().Get("page_size"))
	if err != nil {
		return err
	}
	batchPage, batchPageSize, err := ticketBatchPage(r.URL.Query().Get("batch_page"), r.URL.Query().Get("batch_page_size"))
	if err != nil {
		return err
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 100 {
		return fail(400, "搜索内容不能超过100字")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out := ticketIssueResponse{Rows: []ticketIssueRow{}, Reminders: []ticketReminder{}, Batches: []ticketBatchView{}, Page: page, PageSize: pageSize, BatchPage: batchPage, BatchPageSize: batchPageSize}
	if err = tx.QueryRow("SELECT id,name FROM companies WHERE id=?", companyID).Scan(&out.Company.ID, &out.Company.Name); err == sql.ErrNoRows {
		return fail(404, "公司不存在")
	} else if err != nil {
		return err
	}
	out.Total, out.Rows, err = readTicketIssueRows(tx, companyID, query, page, pageSize)
	if err != nil {
		return err
	}
	out.Reminders, err = readTicketReminders(tx, companyID)
	if err != nil {
		return err
	}
	out.BatchTotal, out.Batches, err = readTicketBatchSummaries(tx, companyID, batchPage, batchPageSize)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, out)
	return nil
}

func ticketPage(rawPage, rawSize string) (int, int, error) {
	return ticketPageWithLimits(rawPage, rawSize, 100, 500)
}

func ticketBatchPage(rawPage, rawSize string) (int, int, error) {
	return ticketPageWithLimits(rawPage, rawSize, 20, 100)
}

func ticketPageWithLimits(rawPage, rawSize string, defaultSize, maxSize int) (int, int, error) {
	page, size := 1, defaultSize
	if rawPage != "" {
		v, err := strconv.Atoi(rawPage)
		if err != nil || v < 1 || v > 1000000 {
			return 0, 0, fail(400, "页码无效")
		}
		page = v
	}
	if rawSize != "" {
		v, err := strconv.Atoi(rawSize)
		if err != nil || v < 1 || v > maxSize {
			return 0, 0, fail(400, fmt.Sprintf("每页条数须为1至%d", maxSize))
		}
		size = v
	}
	return page, size, nil
}

func ticketCandidateSQL(search bool) string {
	lineSearch, saleSearch := "", ""
	if search {
		lineSearch = ` AND CONCAT_WS(' ',l.name,l.subitem_name,l.actual_location,l.description,o.number,o.project_name,o.address,p.name) LIKE ?`
		saleSearch = ` AND CONCAT_WS(' ',s.product_name,o.number,o.project_name,o.address,p.name) LIKE ?`
	}
	// Both branches project saved customer facts only. Quantity stays compatible
	// with the existing list while qty_micro carries its exact printable value.
	line := `SELECT l.id AS item_id,'line' AS item_kind,o.id AS order_id,DATE_FORMAT(o.work_date,'%Y-%m-%d') AS work_date,o.number AS order_number,o.project_id,COALESCE(NULLIF(o.project_name,''),p.name,'') AS project_name,o.address AS address,l.craft_id,l.subitem_id,l.name AS item_name,COALESCE(l.subitem_name,'') AS subitem_name,COALESCE(l.actual_location,'') AS location,COALESCE(l.worker_name,'') AS worker_name,CAST(l.qty_micro AS DECIMAL(18,6))/1000000 AS quantity,l.mode AS unit,l.total_cents,` + ticketCandidateStatusSQL("l", "line") + ` AS status,COALESCE(l.ticket_batch_id,'') AS ticket_batch_id,o.version AS order_version,l.category AS category,CAST(l.qty_micro AS CHAR) AS qty_micro,l.price_cents AS price_cents,CAST(l.segments AS CHAR) AS segments,l.region AS region,l.description AS description,l.charge_mode AS charge_mode,l.charge_note AS charge_note,l.use_included_price AS use_included_price FROM order_lines l JOIN orders o ON o.id=l.order_id JOIN projects p ON p.id=o.project_id LEFT JOIN ticket_batches tb ON tb.id=l.ticket_batch_id WHERE o.company_id=? AND o.ticket_managed=1 AND o.status<>'已取消'` + lineSearch
	sale := `SELECT s.id AS item_id,'sale' AS item_kind,o.id AS order_id,DATE_FORMAT(o.work_date,'%Y-%m-%d') AS work_date,o.number AS order_number,o.project_id,COALESCE(NULLIF(o.project_name,''),p.name,'') AS project_name,o.address AS address,'' AS craft_id,'' AS subitem_id,s.product_name AS item_name,'' AS subitem_name,'' AS location,COALESCE(o.worker_name,'') AS worker_name,CAST(s.sale_qty_micro AS DECIMAL(18,6))/1000000 AS quantity,s.price_unit AS unit,s.total_cents,` + ticketCandidateStatusSQL("s", "sale") + ` AS status,COALESCE(s.ticket_batch_id,'') AS ticket_batch_id,o.version AS order_version,'' AS category,CAST(s.sale_qty_micro AS CHAR) AS qty_micro,s.price_cents AS price_cents,'[]' AS segments,'' AS region,'' AS description,'normal' AS charge_mode,'' AS charge_note,0 AS use_included_price FROM order_sales s JOIN orders o ON o.id=s.order_id JOIN projects p ON p.id=o.project_id LEFT JOIN ticket_batches tb ON tb.id=s.ticket_batch_id WHERE o.company_id=? AND o.ticket_managed=1 AND o.status<>'已取消'` + saleSearch
	return `SELECT * FROM (` + line + ` UNION ALL ` + sale + `) issue_rows`
}

// Only exact persisted identities establish the receivable source. A broken
// association remains visible for diagnosis but must never become selectable.
func ticketCandidateStatusSQL(alias, kind string) string {
	identity := `pi.item_id=` + alias + `.id AND pi.item_kind='` + kind + `'`
	matching := identity + ` AND pi.order_id=o.id AND pt.company_id=o.company_id AND pt.project_id=o.project_id`
	return `CASE WHEN tb.status='delivered' THEN 'delivered' WHEN ` + alias + `.ticket_batch_id IS NOT NULL OR (SELECT COUNT(*) FROM project_ticket_items pi WHERE ` + identity + `)<>1 OR (SELECT COUNT(*) FROM project_ticket_items pi JOIN project_tickets pt ON pt.id=pi.ticket_id WHERE ` + matching + `)<>1 THEN 'unlinked' WHEN EXISTS(SELECT 1 FROM project_ticket_items pi JOIN project_tickets pt ON pt.id=pi.ticket_id WHERE ` + matching + ` AND pt.status<>'草稿') THEN 'delivered' ELSE 'pending' END`
}

func readTicketIssueRows(tx *sql.Tx, companyID, search string, page, pageSize int) (int64, []ticketIssueRow, error) {
	base := ticketCandidateSQL(search != "")
	query := base + ` ORDER BY work_date DESC,order_id DESC,item_kind,item_id LIMIT ? OFFSET ?`
	countSQL := `SELECT COUNT(*) FROM (` + base + `) counted_rows`
	args := []any{companyID}
	if search != "" {
		needle := "%" + search + "%"
		args = append(args, needle)
	}
	args = append(args, companyID)
	if search != "" {
		args = append(args, "%"+search+"%")
	}
	var total int64
	if err := tx.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return 0, nil, err
	}
	rowsArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, err := tx.Query(query, rowsArgs...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []ticketIssueRow{}
	for rows.Next() {
		var row ticketIssueRow
		if err = rows.Scan(&row.ItemID, &row.ItemKind, &row.OrderID, &row.WorkDate, &row.OrderNumber, &row.ProjectID, &row.ProjectName, &row.Address, &row.CraftID, &row.SubitemID, &row.ItemName, &row.SubitemName, &row.Location, &row.WorkerName, &row.Quantity, &row.Unit, &row.TotalCents, &row.Status, &row.BatchID, &row.OrderVersion, &row.Category, &row.QtyMicro, &row.PriceCents, &row.Segments, &row.Region, &row.Description, &row.ChargeMode, &row.ChargeNote, &row.IncludedPrice); err != nil {
			return 0, nil, err
		}
		items = append(items, row)
	}
	return total, items, rows.Err()
}

type ticketReminderCraft struct {
	ID       string
	Name     string
	Subitems string
}

type ticketReminderProject struct {
	ID      string
	Name    string
	Address string
}

type ticketWorkCount struct {
	Total     int64
	Delivered int64
}

type ticketReminderWork struct {
	ProjectID   string
	CraftID     string
	CraftName   string
	SubitemID   string
	SubitemName string
	Count       ticketWorkCount
}

func ticketReminderStatus(total, delivered int64) string {
	if total == 0 {
		return "no_work"
	}
	pending := total - delivered
	if pending > 0 && delivered > 0 {
		return "mixed"
	}
	if delivered > 0 {
		return "delivered"
	}
	return "pending"
}

func readTicketReminders(tx *sql.Tx, companyID string) ([]ticketReminder, error) {
	craftRows, err := tx.Query(`SELECT id,name,COALESCE(subitems,JSON_ARRAY()) FROM `+craftConfigurationSource+` WHERE company_id=? AND active=1 AND source_active=1 ORDER BY category,display_order,name,id`, companyID)
	if err != nil {
		return nil, err
	}
	crafts := []ticketReminderCraft{}
	for craftRows.Next() {
		var craft ticketReminderCraft
		if err = craftRows.Scan(&craft.ID, &craft.Name, &craft.Subitems); err != nil {
			craftRows.Close()
			return nil, err
		}
		crafts = append(crafts, craft)
	}
	err = craftRows.Err()
	craftRows.Close()
	if err != nil {
		return nil, err
	}
	projectRows, err := tx.Query("SELECT id,name,address FROM projects WHERE company_id=? ORDER BY id", companyID)
	if err != nil {
		return nil, err
	}
	projects := []ticketReminderProject{}
	for projectRows.Next() {
		var project ticketReminderProject
		if err = projectRows.Scan(&project.ID, &project.Name, &project.Address); err != nil {
			projectRows.Close()
			return nil, err
		}
		projects = append(projects, project)
	}
	err = projectRows.Err()
	projectRows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]ticketReminderWork{}
	rows, err := tx.Query(`SELECT o.project_id,l.craft_id,MAX(l.name),l.subitem_id,MAX(COALESCE(l.subitem_name,'')),COUNT(*),SUM(CASE WHEN tb.status='delivered' OR EXISTS(SELECT 1 FROM project_ticket_items pi JOIN project_tickets pt ON pt.id=pi.ticket_id WHERE pi.item_id=l.id AND pi.item_kind='line' AND pt.status<>'草稿') THEN 1 ELSE 0 END) FROM order_lines l JOIN orders o ON o.id=l.order_id LEFT JOIN ticket_batches tb ON tb.id=l.ticket_batch_id WHERE o.company_id=? AND o.status<>'已取消' AND l.temporary=0 GROUP BY o.project_id,l.craft_id,l.subitem_id`, companyID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var work ticketReminderWork
		if err = rows.Scan(&work.ProjectID, &work.CraftID, &work.CraftName, &work.SubitemID, &work.SubitemName, &work.Count.Total, &work.Count.Delivered); err != nil {
			rows.Close()
			return nil, err
		}
		counts[ticketReminderKey(work.ProjectID, work.CraftID, work.SubitemID)] = work
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return buildTicketReminders(crafts, projects, counts)
}

func buildTicketReminders(crafts []ticketReminderCraft, projects []ticketReminderProject, counts map[string]ticketReminderWork) ([]ticketReminder, error) {
	result := []ticketReminder{}
	matched := map[string]bool{}
	projectByID := make(map[string]ticketReminderProject, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
		for _, craft := range crafts {
			items := []CraftSubitem{}
			if err := json.Unmarshal([]byte(craft.Subitems), &items); err != nil {
				return nil, err
			}
			if len(items) == 0 {
				items = []CraftSubitem{{}}
			}
			for _, subitem := range items {
				key := ticketReminderKey(project.ID, craft.ID, subitem.ID)
				matched[key] = true
				count := counts[key].Count
				result = append(result, ticketReminder{ProjectID: project.ID, ProjectName: project.Name, Address: project.Address, CraftID: craft.ID, CraftName: craft.Name, SubitemID: subitem.ID, SubitemName: subitem.Name, Status: ticketReminderStatus(count.Total, count.Delivered), TotalCount: count.Total, PendingCount: count.Total - count.Delivered, DeliveredCount: count.Delivered})
			}
		}
	}
	// Changed or removed configuration never turns actual work into "no work".
	// Keep those saved identities separate; names are display snapshots only.
	unmapped := map[string]bool{}
	for key := range counts {
		if !matched[key] {
			unmapped[key] = true
		}
	}
	for _, key := range sortedTicketKeys(unmapped) {
		work := counts[key]
		project, ok := projectByID[work.ProjectID]
		if !ok {
			continue
		}
		count := work.Count
		result = append(result, ticketReminder{ProjectID: project.ID, ProjectName: project.Name, Address: project.Address, CraftID: work.CraftID, CraftName: work.CraftName, SubitemID: work.SubitemID, SubitemName: work.SubitemName, Status: ticketReminderStatus(count.Total, count.Delivered), TotalCount: count.Total, PendingCount: count.Total - count.Delivered, DeliveredCount: count.Delivered, Unmapped: true})
	}
	return result, nil
}

func ticketReminderKey(projectID, craftID, subitemID string) string {
	return projectID + "\x00" + craftID + "\x00" + subitemID
}

func readTicketBatchSummaries(tx *sql.Tx, companyID string, page, pageSize int) (int64, []ticketBatchView, error) {
	var total int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM ticket_batches WHERE company_id=?", companyID).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(`SELECT id,company_id,company_name,number,ticket_no,DATE_FORMAT(delivered_at,'%Y-%m-%d'),recipient,status,version,total_cents,void_reason,voided_by_name,created_by_name,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s') FROM ticket_batches WHERE company_id=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, companyID, pageSize, (page-1)*pageSize)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := []ticketBatchView{}
	for rows.Next() {
		var batch ticketBatchView
		if err = rows.Scan(&batch.ID, &batch.CompanyID, &batch.CompanyName, &batch.Number, &batch.TicketNo, &batch.DeliveredAt, &batch.Recipient, &batch.Status, &batch.Version, &batch.TotalCents, &batch.VoidReason, &batch.VoidedByName, &batch.CreatedByName, &batch.CreatedAt); err != nil {
			return 0, nil, err
		}
		items = append(items, batch)
	}
	return total, items, rows.Err()
}

func (a *App) ticketBatchRead(w http.ResponseWriter, r *http.Request, u User) error {
	if err := ticketIssuePermission(u); err != nil {
		return err
	}
	batchID := strings.TrimSpace(r.URL.Query().Get("id"))
	if batchID == "" || len(batchID) > 64 {
		return fail(400, "请选择交票批次")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var snapshot []byte
	err = tx.QueryRow("SELECT snapshot FROM ticket_batch_versions WHERE batch_id=? ORDER BY version DESC LIMIT 1", batchID).Scan(&snapshot)
	if err == sql.ErrNoRows {
		return fail(404, "交票批次不存在")
	}
	if err != nil {
		return err
	}
	var response ticketBatchResponse
	if err = json.Unmarshal(snapshot, &response); err != nil {
		return fail(409, "交票批次历史快照无法读取")
	}
	if response.Batch.ID != batchID {
		return fail(409, "交票批次历史快照编号不一致")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, response)
	return nil
}

func validateTicketSelection(items []ticketSelection) ([]ticketSelection, error) {
	if len(items) == 0 || len(items) > maxTicketBatchItems {
		return nil, fail(400, "请选择1至500条实际收费明细")
	}
	seen := make(map[string]bool, len(items))
	result := make([]ticketSelection, 0, len(items))
	for _, item := range items {
		item.ItemID = strings.TrimSpace(item.ItemID)
		if item.ItemID == "" || len(item.ItemID) > 64 || (item.ItemKind != "line" && item.ItemKind != "sale") || item.OrderVersion < 1 {
			return nil, fail(400, "交票明细编号、类型或预览施工版本无效")
		}
		key := item.ItemKind + "\x00" + item.ItemID
		if seen[key] {
			return nil, fail(400, "同一实际收费明细不能重复选择")
		}
		seen[key] = true
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ItemKind != result[j].ItemKind {
			return result[i].ItemKind < result[j].ItemKind
		}
		return result[i].ItemID < result[j].ItemID
	})
	return result, nil
}

func ticketBatchTotal(items []ticketBatchItemView) (int64, error) {
	var total int64
	for _, item := range items {
		if !ticketIssueAmountValid(item.TotalCents) || total > maxTicketBatchCents-item.TotalCents {
			return 0, fail(400, "交票合计金额超出范围")
		}
		total += item.TotalCents
	}
	return total, nil
}

func (a *App) createTicketBatch(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := ticketIssuePermission(u); err != nil {
		return nil, err
	}
	var input ticketBatchCreateRequest
	if err := decode(b, &input); err != nil {
		return nil, err
	}
	input.CompanyID = strings.TrimSpace(input.CompanyID)
	input.TicketNo = strings.TrimSpace(input.TicketNo)
	input.Recipient = strings.TrimSpace(input.Recipient)
	if input.CompanyID == "" || len(input.CompanyID) > 64 || len(input.TicketNo) > 100 || input.Recipient == "" || len([]rune(input.Recipient)) > 100 {
		return nil, fail(400, "公司、票号或收票人信息无效")
	}
	if _, err := time.Parse("2006-01-02", input.DeliveredAt); err != nil {
		return nil, fail(400, "请填写有效的交票日期")
	}
	selection, err := validateTicketSelection(input.Items)
	if err != nil {
		return nil, err
	}
	var companyName string
	if err = tx.QueryRow("SELECT name FROM companies WHERE id=?", input.CompanyID).Scan(&companyName); err == sql.ErrNoRows {
		return nil, fail(404, "公司不存在")
	} else if err != nil {
		return nil, err
	}
	refs, err := readTicketOrderRefs(tx, selection)
	if err != nil {
		return nil, err
	}
	if len(refs) != len(selection) {
		return nil, fail(400, "部分施工明细不存在或已变化，请刷新后重新选择")
	}
	projects, orders := uniqueTicketRefs(refs)
	for _, projectID := range projects {
		var locked string
		if err = tx.QueryRow("SELECT id FROM projects WHERE id=? AND company_id=? FOR UPDATE", projectID, input.CompanyID).Scan(&locked); err == sql.ErrNoRows {
			return nil, fail(400, "所选明细不属于该公司工地")
		} else if err != nil {
			return nil, err
		}
	}
	orderMeta := make(map[string]ticketOrderMeta, len(orders))
	for _, orderID := range orders {
		var meta ticketOrderMeta
		err = tx.QueryRow(`SELECT id,company_id,project_id,status,number,DATE_FORMAT(work_date,'%Y-%m-%d'),project_name,address,worker_name,ticket_managed,version FROM orders WHERE id=? FOR UPDATE`, orderID).Scan(&meta.ID, &meta.CompanyID, &meta.ProjectID, &meta.Status, &meta.Number, &meta.WorkDate, &meta.ProjectName, &meta.Address, &meta.WorkerName, &meta.TicketManaged, &meta.Version)
		if err == sql.ErrNoRows {
			return nil, fail(400, "所选施工记录不存在")
		}
		if err != nil {
			return nil, err
		}
		if meta.CompanyID != input.CompanyID || meta.Status == "已取消" {
			return nil, fail(409, "施工公司或状态已变化，不能交票")
		}
		if !meta.TicketManaged {
			return nil, fail(409, "旧记录尚未关联工地应收，不能在交票管理中重复归票")
		}
		orderMeta[orderID] = meta
	}
	items := make([]ticketBatchItemView, 0, len(selection))
	for _, selected := range selection {
		ref := refs[selected.ItemKind+"\x00"+selected.ItemID]
		meta := orderMeta[ref.OrderID]
		if meta.ProjectID != ref.ProjectID || ref.CompanyID != input.CompanyID {
			return nil, fail(409, "施工工地归属已变化，请刷新后重新选择")
		}
		if selected.OrderVersion != meta.Version {
			return nil, fail(409, "施工明细在预览后已变化，请刷新核对后重新打印，再确认交票")
		}
		item, activeBatch, sourceStatus, err := lockTicketIssueItem(tx, selected, meta)
		if err != nil {
			return nil, err
		}
		if activeBatch != "" {
			return nil, fail(409, "所选明细已在有效交票批次中，不能重复交票")
		}
		if sourceStatus != "" && sourceStatus != "草稿" {
			return nil, fail(409, "所选明细已有有效工地票据，不能再次交票")
		}
		items = append(items, item)
	}
	items, err = appendTicketPackageSnapshots(tx, items)
	if err != nil {
		return nil, err
	}
	total, err := ticketBatchTotal(items)
	if err != nil {
		return nil, err
	}
	batchID := id()
	number := "TB-" + strings.ReplaceAll(input.DeliveredAt, "-", "") + "-" + batchID[:12]
	batch := ticketBatchView{ID: batchID, CompanyID: input.CompanyID, CompanyName: companyName, Number: number, TicketNo: input.TicketNo, DeliveredAt: input.DeliveredAt, Recipient: input.Recipient, Status: "delivered", Version: 1, TotalCents: total, CreatedByName: u.Name}
	if _, err = tx.Exec(`INSERT INTO ticket_batches(id,company_id,company_name,number,ticket_no,delivered_at,recipient,status,version,total_cents,created_by,created_by_name) VALUES(?,?,?,?,?,?,?,'delivered',1,?,?,?)`, batch.ID, batch.CompanyID, batch.CompanyName, batch.Number, batch.TicketNo, batch.DeliveredAt, batch.Recipient, batch.TotalCents, u.ID, u.Name); err != nil {
		return nil, err
	}
	if err = tx.QueryRow("SELECT DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s') FROM ticket_batches WHERE id=?", batch.ID).Scan(&batch.CreatedAt); err != nil {
		return nil, err
	}
	for _, item := range items {
		switch item.ItemKind {
		case "line", "sale":
			var result sql.Result
			if item.ItemKind == "line" {
				result, err = tx.Exec("UPDATE order_lines SET ticket_batch_id=? WHERE id=? AND ticket_batch_id IS NULL", batchID, item.ItemID)
			} else {
				result, err = tx.Exec("UPDATE order_sales SET ticket_batch_id=? WHERE id=? AND ticket_batch_id IS NULL", batchID, item.ItemID)
			}
			if err != nil {
				return nil, err
			}
			if changed, e := result.RowsAffected(); e != nil {
				return nil, e
			} else if changed != 1 {
				return nil, fail(409, "施工明细已变化，请刷新后重新选择")
			}
		case "package":
			// Package snapshots do not claim an order row; the project package is unique.
		default:
			return nil, fail(409, "交票批次包含无法识别的明细类型")
		}
		if _, err = tx.Exec(`INSERT INTO ticket_batch_items(batch_id,item_kind,item_id,order_id,project_id,source_project_ticket_id,craft_id,subitem_id,total_cents,quantity,unit,item_name,subitem_name,location,worker_name,snapshot) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, batch.ID, item.ItemKind, item.ItemID, item.OrderID, item.ProjectID, item.SourceProjectTicketID, item.CraftID, item.SubitemID, item.TotalCents, item.Quantity, item.Unit, item.ItemName, item.SubitemName, item.Location, item.WorkerName, jsonText(item)); err != nil {
			return nil, err
		}
	}
	response := ticketBatchResponse{Batch: batch, Items: items}
	if err = saveTicketBatchVersion(tx, u, response, "交票确认"); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "公司批次交票", batch.ID, map[string]any{"number": batch.Number, "company_id": batch.CompanyID, "item_count": len(items), "total_cents": total, "request_key": key}); err != nil {
		return nil, err
	}
	return response, nil
}

func appendTicketPackageSnapshots(tx *sql.Tx, items []ticketBatchItemView) ([]ticketBatchItemView, error) {
	pairs := map[string][2]string{}
	for _, item := range items {
		if item.ProjectID == "" || item.SourceProjectTicketID == "" {
			continue
		}
		key := item.ProjectID + "\x00" + item.SourceProjectTicketID
		pairs[key] = [2]string{item.ProjectID, item.SourceProjectTicketID}
	}
	keys := sortedTicketKeysFromMap(pairs)
	if len(keys) == 0 {
		return items, nil
	}
	args := make([]any, 0, len(keys)*2)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		pair := pairs[key]
		values = append(values, "(?,?)")
		args = append(args, pair[0], pair[1])
	}
	query := `SELECT pp.project_id,pp.package_id,pp.name,pp.package_type,pp.room_count,pp.price_cents,pp.charged_ticket_id
		FROM project_packages pp WHERE (pp.project_id,pp.charged_ticket_id) IN (` + strings.Join(values, ",") + `)
		AND NOT EXISTS(SELECT 1 FROM ticket_batch_items prior JOIN ticket_batches b ON b.id=prior.batch_id AND b.status='delivered'
			WHERE prior.project_id=pp.project_id AND prior.source_project_ticket_id=pp.charged_ticket_id
			AND prior.item_kind IN ('line','sale','package')) ORDER BY pp.project_id,pp.package_id`
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	packages := []ticketBatchItemView{}
	for rows.Next() {
		var item ticketBatchItemView
		var packageID, packageType, sourceTicket string
		var price int64
		if err = rows.Scan(&item.ProjectID, &packageID, &item.ItemName, &packageType, &item.PackageRoomCount, &price, &sourceTicket); err != nil {
			rows.Close()
			return nil, err
		}
		item.ItemID = item.ProjectID
		item.ItemKind = "package"
		item.Category = "套餐"
		item.Quantity = 1
		item.Unit = "套"
		item.TotalCents = price
		item.PriceCents = &price
		item.SourceProjectTicketID = sourceTicket
		item.PackageID = packageID
		item.PackageType = packageType
		packages = append(packages, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return append(items, packages...), nil
}

func sortedTicketKeysFromMap[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func readTicketOrderRefs(tx *sql.Tx, items []ticketSelection) (map[string]ticketOrderRef, error) {
	lineIDs, saleIDs := []string{}, []string{}
	for _, item := range items {
		if item.ItemKind == "line" {
			lineIDs = append(lineIDs, item.ItemID)
		} else {
			saleIDs = append(saleIDs, item.ItemID)
		}
	}
	parts, args := []string{}, []any{}
	if len(lineIDs) > 0 {
		parts = append(parts, `SELECT 'line' AS item_kind,l.id AS item_id,o.id AS order_id,o.project_id,o.company_id FROM order_lines l JOIN orders o ON o.id=l.order_id WHERE o.ticket_managed=1 AND l.id IN (`+ticketPlaceholders(len(lineIDs))+`)`)
		for _, v := range lineIDs {
			args = append(args, v)
		}
	}
	if len(saleIDs) > 0 {
		parts = append(parts, `SELECT 'sale' AS item_kind,s.id AS item_id,o.id AS order_id,o.project_id,o.company_id FROM order_sales s JOIN orders o ON o.id=s.order_id WHERE o.ticket_managed=1 AND s.id IN (`+ticketPlaceholders(len(saleIDs))+`)`)
		for _, v := range saleIDs {
			args = append(args, v)
		}
	}
	rows, err := tx.Query(strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]ticketOrderRef, len(items))
	for rows.Next() {
		var ref ticketOrderRef
		if err = rows.Scan(&ref.Kind, &ref.ItemID, &ref.OrderID, &ref.ProjectID, &ref.CompanyID); err != nil {
			return nil, err
		}
		result[ref.Kind+"\x00"+ref.ItemID] = ref
	}
	return result, rows.Err()
}

func ticketPlaceholders(count int) string {
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func uniqueTicketRefs(refs map[string]ticketOrderRef) ([]string, []string) {
	projects, orders := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		projects[ref.ProjectID] = true
		orders[ref.OrderID] = true
	}
	return sortedTicketKeys(projects), sortedTicketKeys(orders)
}

func sortedTicketKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lockTicketIssueItem(tx *sql.Tx, selected ticketSelection, order ticketOrderMeta) (ticketBatchItemView, string, string, error) {
	item := ticketBatchItemView{ItemID: selected.ItemID, ItemKind: selected.ItemKind, OrderID: order.ID, WorkDate: order.WorkDate, OrderNumber: order.Number, ProjectID: order.ProjectID, ProjectName: order.ProjectName, Address: order.Address, WorkerName: order.WorkerName}
	var active sql.NullString
	var sourceStatus string
	if selected.ItemKind == "line" {
		var quantity float64
		var actualLocation string
		err := tx.QueryRow(`SELECT name,craft_id,category,subitem_id,COALESCE(subitem_name,''),COALESCE(actual_location,''),COALESCE(worker_name,''),CAST(qty_micro AS DECIMAL(18,6))/1000000,CAST(qty_micro AS CHAR),mode,price_cents,total_cents,CAST(segments AS CHAR),COALESCE(region,''),COALESCE(description,''),COALESCE(charge_mode,'normal'),COALESCE(charge_note,''),COALESCE(use_included_price,0),ticket_batch_id FROM order_lines WHERE id=? AND order_id=? FOR UPDATE`, selected.ItemID, order.ID).Scan(&item.ItemName, &item.CraftID, &item.Category, &item.SubitemID, &item.SubitemName, &actualLocation, &item.WorkerName, &quantity, &item.QtyMicro, &item.Unit, &item.PriceCents, &item.TotalCents, &item.Segments, &item.Region, &item.Description, &item.ChargeMode, &item.ChargeNote, &item.IncludedPrice, &active)
		if err == sql.ErrNoRows {
			return item, "", "", fail(400, "所选施工明细已不存在")
		}
		if err != nil {
			return item, "", "", err
		}
		item.Quantity = quantity
		item.Location = actualLocation
	} else {
		err := tx.QueryRow(`SELECT product_name,CAST(sale_qty_micro AS DECIMAL(18,6))/1000000,CAST(sale_qty_micro AS CHAR),price_unit,price_cents,total_cents,ticket_batch_id FROM order_sales WHERE id=? AND order_id=? FOR UPDATE`, selected.ItemID, order.ID).Scan(&item.ItemName, &item.Quantity, &item.QtyMicro, &item.Unit, &item.PriceCents, &item.TotalCents, &active)
		if err == sql.ErrNoRows {
			return item, "", "", fail(400, "所选售料明细已不存在")
		}
		if err != nil {
			return item, "", "", err
		}
	}
	if active.Valid {
		return item, active.String, "", nil
	}
	ref := ticketOrderRef{Kind: selected.ItemKind, ItemID: selected.ItemID, OrderID: order.ID, CompanyID: order.CompanyID, ProjectID: order.ProjectID}
	sourceID, sourceStatus, err := readTicketSource(tx, ref)
	item.SourceProjectTicketID = sourceID
	return item, "", sourceStatus, err
}

func readTicketSource(tx *sql.Tx, ref ticketOrderRef) (string, string, error) {
	rows, err := tx.Query(`SELECT COALESCE(t.id,''),COALESCE(t.status,''),i.order_id,COALESCE(t.company_id,''),COALESCE(t.project_id,'') FROM project_ticket_items i LEFT JOIN project_tickets t ON t.id=i.ticket_id WHERE i.item_id=? AND i.item_kind=? ORDER BY i.ticket_id FOR UPDATE`, ref.ItemID, ref.Kind)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	sources := []ticketSourceRef{}
	for rows.Next() {
		var source ticketSourceRef
		if err = rows.Scan(&source.TicketID, &source.Status, &source.OrderID, &source.CompanyID, &source.ProjectID); err != nil {
			return "", "", err
		}
		sources = append(sources, source)
	}
	if err = rows.Err(); err != nil {
		return "", "", err
	}
	return exactTicketSource(ref, sources)
}

type ticketSourceRef struct {
	TicketID  string
	Status    string
	OrderID   string
	CompanyID string
	ProjectID string
}

func exactTicketSource(ref ticketOrderRef, sources []ticketSourceRef) (string, string, error) {
	if len(sources) != 1 {
		return "", "", fail(409, "施工明细须有唯一工地应收关联，关联缺失或重复时不能交票")
	}
	source := sources[0]
	if source.TicketID == "" || source.Status == "" || source.OrderID != ref.OrderID || source.CompanyID != ref.CompanyID || source.ProjectID != ref.ProjectID {
		return "", "", fail(409, "施工明细的工地应收关联与订单、公司或工地不一致，不能交票")
	}
	return source.TicketID, source.Status, nil
}

func saveTicketBatchVersion(tx *sql.Tx, u User, response ticketBatchResponse, event string) error {
	_, err := tx.Exec("INSERT INTO ticket_batch_versions(batch_id,version,event,snapshot,actor_id,actor_name) VALUES(?,?,?,?,?,?)", response.Batch.ID, response.Batch.Version, event, jsonText(response), u.ID, u.Name)
	return err
}

func (a *App) ticketBatchAction(tx *sql.Tx, u User, b []byte, key string) (any, error) {
	if err := ticketIssuePermission(u); err != nil {
		return nil, err
	}
	var input ticketBatchActionRequest
	if err := decode(b, &input); err != nil {
		return nil, err
	}
	input.ID, input.Reason = strings.TrimSpace(input.ID), strings.TrimSpace(input.Reason)
	if input.Action != "void" || input.ID == "" || len(input.ID) > 64 || input.Version < 1 {
		return nil, fail(400, "交票批次操作信息无效")
	}
	if input.Reason == "" || len([]rune(input.Reason)) > 1000 {
		return nil, fail(400, "作废原因须为1至1000字")
	}
	projectIDs, orderIDs, err := readTicketBatchLockRefs(tx, input.ID)
	if err != nil {
		return nil, err
	}
	for _, projectID := range projectIDs {
		var locked string
		if err = tx.QueryRow("SELECT id FROM projects WHERE id=? FOR UPDATE", projectID).Scan(&locked); err == sql.ErrNoRows {
			return nil, fail(409, "交票批次工地不存在，不能安全作废")
		}
		if err != nil {
			return nil, err
		}
	}
	for _, orderID := range orderIDs {
		var locked string
		if err = tx.QueryRow("SELECT id FROM orders WHERE id=? FOR UPDATE", orderID).Scan(&locked); err == sql.ErrNoRows {
			return nil, fail(409, "交票批次施工记录不存在，不能安全作废")
		}
		if err != nil {
			return nil, err
		}
	}
	header, err := readTicketBatchHeader(tx, input.ID, true)
	if err != nil {
		return nil, err
	}
	if header.Batch.Version != input.Version {
		return nil, fail(409, "交票批次已变化，请刷新后重新核对")
	}
	if header.Batch.Status != "delivered" {
		return nil, fail(409, "该交票批次已经作废")
	}
	items, err := readTicketBatchItems(tx, input.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		var current sql.NullString
		var query string
		if item.ItemKind == "line" {
			query = "SELECT ticket_batch_id FROM order_lines WHERE id=? FOR UPDATE"
		} else if item.ItemKind == "sale" {
			query = "SELECT ticket_batch_id FROM order_sales WHERE id=? FOR UPDATE"
		} else if item.ItemKind == "package" {
			continue
		} else {
			return nil, fail(409, "交票批次包含无法识别的明细类型")
		}
		err = tx.QueryRow(query, item.ItemID).Scan(&current)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if current.Valid && current.String != input.ID {
			return nil, fail(409, "交票明细当前归属了其他批次，不能自动作废")
		}
	}
	result, err := tx.Exec(`UPDATE ticket_batches SET status='voided',version=version+1,void_reason=?,voided_by=?,voided_by_name=?,voided_at=UTC_TIMESTAMP() WHERE id=? AND status='delivered' AND version=?`, input.Reason, u.ID, u.Name, input.ID, input.Version)
	if err != nil {
		return nil, err
	}
	if changed, e := result.RowsAffected(); e != nil {
		return nil, e
	} else if changed != 1 {
		return nil, fail(409, "交票批次已变化，请刷新后重新核对")
	}
	for _, item := range items {
		if item.ItemKind == "package" {
			continue
		}
		var result sql.Result
		if item.ItemKind == "line" {
			result, err = tx.Exec("UPDATE order_lines SET ticket_batch_id=NULL WHERE id=? AND ticket_batch_id=?", item.ItemID, input.ID)
		} else if item.ItemKind == "sale" {
			result, err = tx.Exec("UPDATE order_sales SET ticket_batch_id=NULL WHERE id=? AND ticket_batch_id=?", item.ItemID, input.ID)
		} else {
			return nil, fail(409, "交票批次包含无法识别的明细类型")
		}
		if err != nil {
			return nil, err
		}
		if _, err = result.RowsAffected(); err != nil {
			return nil, err
		}
	}
	if err = unlinkTicketBatchCash(tx, u, input.ID, input.Reason); err != nil {
		return nil, err
	}
	header.Batch.Status, header.Batch.Version, header.Batch.VoidReason, header.Batch.VoidedByName = "voided", header.Batch.Version+1, input.Reason, u.Name
	response := ticketBatchResponse{Batch: header.Batch, Items: items}
	if err = saveTicketBatchVersion(tx, u, response, "作废交票批次"); err != nil {
		return nil, err
	}
	if err = audit(tx, u, "作废公司交票批次", input.ID, map[string]any{"version": header.Batch.Version, "reason": input.Reason, "item_count": len(items), "request_key": key}); err != nil {
		return nil, err
	}
	return response, nil
}

func readTicketBatchLockRefs(tx *sql.Tx, batchID string) ([]string, []string, error) {
	rows, err := tx.Query("SELECT DISTINCT project_id,order_id FROM ticket_batch_items WHERE batch_id=? ORDER BY project_id,order_id", batchID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	projectSet, orderSet := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var project, order string
		if err = rows.Scan(&project, &order); err != nil {
			return nil, nil, err
		}
		projectSet[project] = true
		if order != "" {
			orderSet[order] = true
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(projectSet) == 0 {
		return nil, nil, fail(404, "交票批次不存在或没有明细")
	}
	return sortedTicketKeys(projectSet), sortedTicketKeys(orderSet), nil
}

func readTicketBatchHeader(tx *sql.Tx, batchID string, lock bool) (ticketBatchHeader, error) {
	header := ticketBatchHeader{}
	query := `SELECT id,company_id,company_name,number,ticket_no,DATE_FORMAT(delivered_at,'%Y-%m-%d'),recipient,status,version,total_cents,void_reason,voided_by_name,created_by_name,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s'),created_by,voided_by FROM ticket_batches WHERE id=?`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRow(query, batchID).Scan(&header.Batch.ID, &header.Batch.CompanyID, &header.Batch.CompanyName, &header.Batch.Number, &header.Batch.TicketNo, &header.Batch.DeliveredAt, &header.Batch.Recipient, &header.Batch.Status, &header.Batch.Version, &header.Batch.TotalCents, &header.Batch.VoidReason, &header.Batch.VoidedByName, &header.Batch.CreatedByName, &header.Batch.CreatedAt, &header.CreatedBy, &header.VoidedBy)
	if err == sql.ErrNoRows {
		return header, fail(404, "交票批次不存在")
	}
	return header, err
}

func readTicketBatchItems(tx *sql.Tx, batchID string) ([]ticketBatchItemView, error) {
	rows, err := tx.Query("SELECT snapshot FROM ticket_batch_items WHERE batch_id=? ORDER BY project_id,order_id,item_kind,item_id", batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ticketBatchItemView{}
	for rows.Next() {
		var raw []byte
		var item ticketBatchItemView
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, fail(409, "交票明细历史快照无法读取")
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ticketIssueAmountValid is kept small and pure so the accepted monetary range
// and zero-charge behavior can be regression tested without a database.
func ticketIssueAmountValid(amount int64) bool {
	return amount >= 0 && amount <= maxTicketBatchCents
}
