package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strings"
)

type archiveCompanyRecord struct {
	CashReceived *int64        `json:"cash_received_cents,omitempty"`
	FinanceScope string        `json:"finance_scope,omitempty"`
	Key          string        `json:"key"`
	RunID        string        `json:"run_id"`
	CompanyID    string        `json:"company_id"`
	Company      string        `json:"company"`
	Month        string        `json:"month"`
	CreatedAt    string        `json:"created_at"`
	Automatic    bool          `json:"automatic"`
	Legacy       bool          `json:"legacy"`
	Files        []ArchiveFile `json:"files"`
	OrderCount   *int64        `json:"order_count,omitempty"`
	Construction *int64        `json:"construction_cents,omitempty"`
	Receivable   *int64        `json:"receivable_cents,omitempty"`
	Received     *int64        `json:"received_cents,omitempty"`
	Outstanding  *int64        `json:"outstanding_cents,omitempty"`
	Wage         *int64        `json:"wage_cents,omitempty"`
}

type archiveCompanyResponse struct {
	Rows        []archiveCompanyRecord `json:"rows"`
	Total       int64                  `json:"total"`
	Page        int                    `json:"page"`
	PageSize    int                    `json:"page_size"`
	LegacyTotal int64                  `json:"legacy_total"`
	Latest      *archiveStatusRecord   `json:"latest"`
	CanOrders   bool                   `json:"can_orders"`
	CanFinance  bool                   `json:"can_finance"`
	CanWages    bool                   `json:"can_wages"`
}

// Permission and stable identity grouping precede pagination. A single run may
// contain arbitrarily many companies, and a company may have many versions.
func archiveCompanyCTE(u User, month, company string, history bool) (string, []any) {
	allowed := archiveReadableKinds(u)
	args := make([]any, 0, len(allowed)+len(archiveKinds)+2)
	for _, kind := range allowed {
		args = append(args, kind)
	}
	readable := "j.kind IN (?" + strings.Repeat(",?", len(allowed)-1) + ")"
	for _, kind := range archiveKinds {
		args = append(args, kind)
	}
	where := "j.kind IN (?" + strings.Repeat(",?", len(archiveKinds)-1) + ")"
	if month != "" {
		where += " AND a.month=?"
		args = append(args, month)
	}
	if company != "" {
		where += " AND j.company_id=?"
		args = append(args, company)
	}
	eligible := "readable=1"
	if !history {
		eligible += " AND company_id<>'' AND version_rank=1"
	}
	return `WITH file_rows AS (
	 SELECT a.id run_id,a.month,a.automatic,a.created_at,
	 COALESCE(NULLIF(j.generated_at,''),DATE_FORMAT(a.created_at,'%Y-%m-%dT%H:%i:%s.000000000Z')) rank_at,
	 COALESCE(j.company_id,'') COLLATE utf8mb4_bin company_id,j.company COLLATE utf8mb4_bin company,
	 CASE WHEN COALESCE(j.company_id,'')='' THEN CONCAT('l:',SHA2(j.company,256)) ELSE CONCAT('c:',j.company_id) END COLLATE utf8mb4_bin group_key,j.entry,(` + readable + `) readable
	 FROM archive_runs a JOIN JSON_TABLE(a.manifest,'$[*]' COLUMNS (
	 entry JSON PATH '$',company_id VARCHAR(64) PATH '$.company_id' NULL ON EMPTY,
	 company VARCHAR(200) PATH '$.company',kind VARCHAR(40) PATH '$.kind',generated_at VARCHAR(40) PATH '$.generated_at' NULL ON EMPTY
	 )) j WHERE ` + where + `
	), company_groups AS (
	 SELECT run_id,month,automatic,created_at,MIN(rank_at) rank_at,company_id,group_key,MIN(company) company,MAX(readable) readable,JSON_ARRAYAGG(entry) manifest
	 FROM file_rows GROUP BY run_id,month,automatic,created_at,company_id,group_key
	), ranked AS (
	 SELECT company_groups.*,ROW_NUMBER() OVER (PARTITION BY month,company_id ORDER BY rank_at DESC,run_id DESC) version_rank FROM company_groups
	), eligible AS (SELECT * FROM ranked WHERE ` + eligible + `) `, args
}

func readArchiveStatus(q interface{ QueryRow(string, ...any) *sql.Row }) (*archiveStatusRecord, error) {
	var raw string
	err := q.QueryRow("SELECT value FROM settings WHERE setting_key='archive_status'").Scan(&raw)
	if err == sql.ErrNoRows || (err == nil && raw == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var status *archiveStatusRecord
	err = json.Unmarshal([]byte(raw), &status)
	return status, err
}

func (a *App) listArchiveCompanies(w http.ResponseWriter, r *http.Request, u User) error {
	if len(archiveReadableKinds(u)) == 0 {
		return fail(403, "没有导出权限")
	}
	q := r.URL.Query()
	page, err := queryPage(q)
	if err != nil {
		return err
	}
	month, company := q.Get("month"), q.Get("company_id")
	if month != "" {
		if _, _, err = monthRange(month); err != nil {
			return err
		}
	}
	if len(company) > 64 {
		return fail(400, "公司编号无效")
	}
	history := q.Get("history") == "1"
	if v := q.Get("history"); v != "" && v != "0" && v != "1" {
		return fail(400, "历史版本模式无效")
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cte, args := archiveCompanyCTE(u, month, company, history)
	response := archiveCompanyResponse{Rows: []archiveCompanyRecord{}, Page: page, PageSize: 100, CanOrders: u.can("orders") && u.can("customer_prices"), CanFinance: u.can("finance") && u.can("customer_prices"), CanWages: u.can("wages")}
	if err = tx.QueryRowContext(r.Context(), cte+"SELECT (SELECT COUNT(*) FROM eligible),(SELECT COUNT(*) FROM company_groups WHERE company_id='' AND readable=1)", args...).Scan(&response.Total, &response.LegacyTotal); err != nil {
		return err
	}
	rows, err := tx.QueryContext(r.Context(), cte+"SELECT group_key,run_id,company_id,company,month,created_at,automatic,manifest FROM eligible ORDER BY month DESC,rank_at DESC,run_id DESC,group_key LIMIT 100 OFFSET ?", append(append([]any{}, args...), (page-1)*100)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item archiveCompanyRecord
		var manifest string
		if err = rows.Scan(&item.Key, &item.RunID, &item.CompanyID, &item.Company, &item.Month, &item.CreatedAt, &item.Automatic, &manifest); err != nil {
			break
		}
		item.Legacy = item.CompanyID == ""
		if err = json.Unmarshal([]byte(manifest), &item.Files); err != nil {
			break
		}
		visible := make([]ArchiveFile, 0, len(item.Files))
		for _, file := range item.Files {
			if canReadArchive(u, file.Kind) {
				visible = append(visible, file)
			}
		}
		item.Files = visible
		if err = summarizeArchiveCompany(&item, u); err != nil {
			break
		}
		for i := range item.Files {
			item.Files[i] = readableArchiveFile(item.RunID, item.Files[i])
		}
		response.Rows = append(response.Rows, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if response.Latest, err = readArchiveStatus(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	write(w, 200, response)
	return nil
}

func addArchiveCents(total *int64, amount int64) error {
	if amount < 0 || *total > math.MaxInt64-amount {
		return fail(422, "归档汇总金额无效或超出范围")
	}
	*total += amount
	return nil
}

func summarizeArchiveCompany(item *archiveCompanyRecord, u User) error {
	var cash int64
	cashFound := false
	var receivable, received, outstanding, wage int64
	financeFound, financeComplete, wageFound := false, true, false
	for _, f := range item.Files {
		if !canReadArchive(u, f.Kind) {
			return fail(422, "归档权限分类无效")
		}
		if f.FinanceScope == "allocation" {
			item.FinanceScope = "allocation"
		}
		if f.CashReceived != nil {
			cashFound = true
			if err := addArchiveCents(&cash, *f.CashReceived); err != nil {
				return err
			}
		}
		if f.Kind == "施工验收表" && u.can("orders") && item.OrderCount == nil {
			if f.Count < 0 || f.Amount < 0 {
				return fail(422, "归档施工汇总无效")
			}
			count, amount := int64(f.Count), f.Amount
			item.OrderCount, item.Construction = &count, &amount
		}
		if f.Kind == "工资明细" && u.can("wages") {
			wageFound = true
			if err := addArchiveCents(&wage, f.Amount); err != nil {
				return err
			}
		}
		if f.Kind == "票据应收" || f.Kind == "旧单据应收" || f.Kind == "财务应收" {
			financeFound = true
			if err := addArchiveCents(&receivable, f.Amount); err != nil {
				return err
			}
			if f.Received == nil || f.Outstanding == nil {
				financeComplete = false
				continue
			}
			if err := addArchiveCents(&received, *f.Received); err != nil {
				return err
			}
			if err := addArchiveCents(&outstanding, *f.Outstanding); err != nil {
				return err
			}
		}
	}
	if cashFound {
		item.CashReceived = &cash
	}
	if financeFound {
		item.Receivable = &receivable
		if financeComplete {
			item.Received, item.Outstanding = &received, &outstanding
		}
	}
	if wageFound {
		item.Wage = &wage
	}
	// New manifests describe a complete company/month generation. A skipped
	// zero-row report is an actual zero, while legacy missing metadata stays
	// unknown. Never derive old balances from today's mutable business rows.
	if !item.Legacy && item.CompanyID != archiveSystemCompanyID {
		if u.can("orders") && u.can("customer_prices") && item.OrderCount == nil {
			count, amount := int64(0), int64(0)
			item.OrderCount, item.Construction = &count, &amount
		}
		if u.can("finance") && u.can("customer_prices") && !financeFound {
			item.Receivable, item.Received, item.Outstanding = &receivable, &received, &outstanding
		}
		if u.can("wages") && !wageFound {
			item.Wage = &wage
		}
	}
	return nil
}
