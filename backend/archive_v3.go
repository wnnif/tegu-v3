package main

import (
	"database/sql"
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const archiveSystemCompanyID = "system:business"
const archiveSystemCompanyName = "全公司（工资付款、进货及报销）"

func archiveV3Location(subitem, actual, region string) string {
	values := []string{}
	for _, value := range []string{subitem, actual, region} {
		if value = strings.TrimSpace(value); value != "" && !contains(values, value) {
			values = append(values, value)
		}
	}
	return strings.Join(values, " / ")
}

func archiveV3ChargeLabel(mode string, included bool) string {
	switch mode {
	case "free":
		return "免费施工"
	case "redo":
		return "补做不收费"
	}
	if included {
		return "组合含价"
	}
	return "正常收费"
}

type v3ArchiveReport struct {
	kind, query string
	headers     []string
	amountIndex int
}

// V3 cash ledgers may settle work from several companies. Expense records do
// not have a company at all. Keep their actual recorded dates in a separate
// system group rather than inventing a company or splitting a cash payment.
func appendV3Archives(tx *sql.Tx, from, to, company, run, dir string, files *[]ArchiveFile) error {
	if company != "" {
		return nil
	}
	for _, spec := range v3ArchiveReports() {
		rows, err := tx.Query(spec.query, from, to)
		if err != nil {
			return err
		}
		entry, err := writeV3ArchiveCSV(rows, spec, run, dir)
		if err != nil {
			return err
		}
		if entry.Count > 0 {
			*files = append(*files, entry)
		}
	}
	return nil
}

func v3ArchiveReports() []v3ArchiveReport {
	return []v3ArchiveReport{
		{"工资付款", `SELECT p.id,s.worker_name,p.paid_date,p.settlement_id,p.method,IF(p.status='active' AND s.status='confirmed','有效','已撤销或结算作废'),CAST(p.amount_cents/100 AS DECIMAL(24,2)),p.note,IF(p.status='active' AND s.status='confirmed',p.amount_cents,0) FROM payroll_payments p JOIN payroll_settlements s ON s.id=p.settlement_id WHERE p.paid_date>=? AND p.paid_date<? ORDER BY p.paid_date,p.id`, []string{"付款编号", "工人", "实际付款日期", "结算编号", "付款方式", "状态", "实际付款元", "备注"}, 6},
		{"工资调整", `SELECT id,worker_name,effective_date,project_name,address,CAST(amount_cents/100 AS DECIMAL(24,2)),reason,COALESCE(reversal_of,''),amount_cents FROM payroll_adjustments WHERE effective_date>=? AND effective_date<? ORDER BY effective_date,id`, []string{"调整编号", "工人", "工资发生日期", "工地名称快照", "地址快照", "调整元（可正可负）", "原因", "反向记录对应编号"}, 5},
		{"工资扣款", `SELECT id,worker_name,deduction_date,project_name,address,CASE status WHEN 'confirmed' THEN '已确认' WHEN 'pending' THEN '待确认' ELSE '已作废' END,CAST(amount_cents/100 AS DECIMAL(24,2)),reason,IF(status='confirmed',amount_cents,0) FROM payroll_deductions WHERE deduction_date>=? AND deduction_date<? ORDER BY deduction_date,id`, []string{"扣款编号", "工人", "扣款发生日期", "工地名称快照", "地址快照", "状态", "扣款元", "原因"}, 6},
		{"工资预支", `SELECT id,worker_name,advance_date,project_name,address,IF(status='active','有效','已作废'),CAST(amount_cents/100 AS DECIMAL(24,2)),note,IF(status='active',amount_cents,0) FROM payroll_advances WHERE advance_date>=? AND advance_date<? ORDER BY advance_date,id`, []string{"预支编号", "工人", "实际预支日期", "工地名称快照", "地址快照", "状态", "已付预支元", "备注"}, 6},
		{"进货金额", `SELECT id,work_date,handler,IF(voided=0,'有效','已作废'),CAST(total_cents/100 AS DECIMAL(24,2)),note,IF(voided=0,total_cents,0) FROM stock_documents WHERE kind='进货' AND work_date>=? AND work_date<? ORDER BY work_date,id`, []string{"进货编号", "进货发生日期", "经办人", "状态", "进货金额元", "备注"}, 4},
		{"报销登记", `SELECT id,person_name,expense_date,category,CASE status WHEN 'confirmed' THEN '已确认' WHEN 'pending' THEN '待确认' ELSE '已作废' END,CAST(amount_cents/100 AS DECIMAL(24,2)),COALESCE(purchase_document_id,''),purchase_number,note,IF(status<>'voided',amount_cents,0) FROM expense_claims WHERE expense_date>=? AND expense_date<? ORDER BY expense_date,id`, []string{"报销编号", "报销人", "费用发生日期", "类别", "状态", "登记报销元", "关联进货编号（不再次计采购）", "进货单号快照", "备注"}, 5},
		{"报销付款", `SELECT p.id,c.person_name,p.paid_date,p.claim_id,IF(p.status='active' AND c.status<>'voided','有效','已撤销或报销作废'),CAST(p.amount_cents/100 AS DECIMAL(24,2)),p.note,IF(p.status='active' AND c.status<>'voided',p.amount_cents,0) FROM expense_payments p JOIN expense_claims c ON c.id=p.claim_id WHERE p.paid_date>=? AND p.paid_date<? ORDER BY p.paid_date,p.id`, []string{"付款编号", "报销人", "实际付款日期", "报销编号", "状态", "实际补付款元（不含预支核销）", "备注"}, 5},
		{"报账预支", `SELECT id,person_name,advance_date,purpose,IF(status='active','有效','已作废'),CAST(amount_cents/100 AS DECIMAL(24,2)),note,IF(status='active',amount_cents,0) FROM expense_advances WHERE advance_date>=? AND advance_date<? ORDER BY advance_date,id`, []string{"预支编号", "报账人", "实际预支日期", "用途", "状态", "已付预支元", "备注"}, 5},
		{"报账核销", `SELECT x.id,c.person_name,x.applied_date,x.claim_id,x.advance_id,IF(x.status='active' AND c.status<>'voided' AND a.status='active','有效','已撤销或来源作废'),CAST(x.amount_cents/100 AS DECIMAL(24,2)),x.note,IF(x.status='active' AND c.status<>'voided' AND a.status='active',x.amount_cents,0) FROM expense_advance_allocations x JOIN expense_claims c ON c.id=x.claim_id JOIN expense_advances a ON a.id=x.advance_id WHERE x.applied_date>=? AND x.applied_date<? ORDER BY x.applied_date,x.id`, []string{"核销编号", "报账人", "核销日期", "报销编号", "预支编号", "状态", "预支核销元（不产生新付款）", "备注"}, 6},
		{"报账退回", `SELECT r.id,a.person_name,r.returned_date,r.advance_id,IF(r.status='active' AND a.status='active','有效','已撤销或预支作废'),CAST(r.amount_cents/100 AS DECIMAL(24,2)),r.note,IF(r.status='active' AND a.status='active',r.amount_cents,0) FROM expense_advance_returns r JOIN expense_advances a ON a.id=r.advance_id WHERE r.returned_date>=? AND r.returned_date<? ORDER BY r.returned_date,r.id`, []string{"退回编号", "报账人", "实际退回日期", "预支编号", "状态", "退回元（收回预支）", "备注"}, 5},
	}
}

func writeV3ArchiveCSV(rows *sql.Rows, spec v3ArchiveReport, run, dir string) (entry ArchiveFile, resultErr error) {
	defer rows.Close()
	name := id() + ".csv"
	var file *os.File
	var writer *csv.Writer
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	var total int64
	for rows.Next() {
		values := make([]sql.NullString, len(spec.headers))
		dest := make([]any, 0, len(values)+1)
		for i := range values {
			dest = append(dest, &values[i])
		}
		var cents int64
		dest = append(dest, &cents)
		if err := rows.Scan(dest...); err != nil {
			return entry, err
		}
		if cents > 0 && total > math.MaxInt64-cents || cents < 0 && total < math.MinInt64-cents {
			return entry, fail(422, "归档金额超出范围")
		}
		if file == nil {
			var err error
			file, err = os.Create(filepath.Join(dir, name+".partial"))
			if err != nil {
				return entry, err
			}
			if _, err = file.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
				return entry, err
			}
			writer = csv.NewWriter(file)
			writer.UseCRLF = true
			if err = writer.Write(spec.headers); err != nil {
				return entry, err
			}
		}
		line := make([]string, len(values))
		for i, value := range values {
			line[i] = csvSafe(value.String)
		}
		if err := writer.Write(line); err != nil {
			return entry, err
		}
		total += cents
		entry.Count++
	}
	if err := rows.Err(); err != nil {
		return entry, err
	}
	if file == nil {
		return entry, nil
	}
	summary := make([]string, len(spec.headers))
	summary[0], summary[spec.amountIndex] = "合计（有效金额元）", scaledDecimal(total, 2)
	if err := writer.Write(summary); err != nil {
		return entry, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return entry, err
	}
	if err := file.Sync(); err != nil {
		return entry, err
	}
	if err := file.Close(); err != nil {
		return entry, err
	}
	file = nil
	if err := os.Rename(filepath.Join(dir, name+".partial"), filepath.Join(dir, name)); err != nil {
		return entry, err
	}
	entry.File, entry.CompanyID, entry.Company = run+"/"+name, archiveSystemCompanyID, archiveSystemCompanyName
	entry.Kind, entry.Amount, entry.Available = spec.kind, total, true
	return entry, nil
}
