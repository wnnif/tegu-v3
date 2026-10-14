package main

import (
	"database/sql"
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
)

// Stream complete result sets to each company's archive. The caller owns the
// repeatable-read transaction and removes partial artifacts on any failure.
func appendWorkflowArchives(tx *sql.Tx, from, to, company, run, dir string, files *[]ArchiveFile) error {
	type report struct {
		kind, query string
		headers     []string
		amountIndex int
		utcActivity bool
	}
	utcFrom, utcTo, err := businessDateRangeUTC(from, to)
	if err != nil {
		return err
	}
	companyClause := ""
	args := []any{from, to}
	if company != "" {
		companyClause = " AND o.company_id=?"
		args = append(args, company)
	}
	reports := []report{
		{"售料明细", `SELECT o.company_id,COALESCE(NULLIF(o.company_name,''),c.name),o.number,o.work_date,o.address,s.product_name,CAST(s.sale_qty_micro/1000000 AS DECIMAL(24,6)),s.price_unit,CAST(s.price_cents/100 AS DECIMAL(24,2)),CAST(s.total_cents/100 AS DECIMAL(24,2)),s.total_cents FROM order_sales s JOIN orders o ON o.id=s.order_id JOIN companies c ON c.id=o.company_id WHERE o.status<>'已取消' AND o.work_date>=? AND o.work_date<?` + companyClause + ` ORDER BY o.company_id,o.work_date,o.id,s.sort_order,s.id`, []string{"公司", "施工/送货记录号", "实际日期", "工地", "销售材料", "销售数量", "单位", "销售单价元", "货款元"}, 8, false},
		{"票据应收", `SELECT o.company_id,o.company_name,o.number,DATE_FORMAT(o.created_at,'%Y-%m-%d'),o.address,o.kind,o.status,CAST(o.total_cents/100 AS DECIMAL(24,2)),CAST(COALESCE(r.received,0)/100 AS DECIMAL(24,2)),CAST((o.total_cents-COALESCE(r.received,0)-COALESCE(d.discount,0))/100 AS DECIMAL(24,2)),COALESCE(DATE_FORMAT(o.delivered_at,'%Y-%m-%d'),''),o.recipient,CAST(COALESCE(d.discount,0)/100 AS DECIMAL(24,2)),o.total_cents,COALESCE(r.received,0),(o.total_cents-COALESCE(r.received,0)-COALESCE(d.discount,0)),COALESCE(d.discount,0) FROM project_tickets o LEFT JOIN(SELECT ticket_id,SUM(amount_cents) received FROM project_ticket_receipts WHERE voided=0 GROUP BY ticket_id) r ON r.ticket_id=o.id LEFT JOIN (SELECT ticket_id,SUM(amount_cents) discount FROM customer_discounts WHERE voided=0 GROUP BY ticket_id) d ON d.ticket_id=o.id WHERE o.updated_at>=? AND o.updated_at<?` + companyClause + ` ORDER BY o.company_id,o.created_at,o.id`, []string{"公司", "工地票号", "建票日期", "工地", "票类", "当前状态", "归档时累计收费元", "归档时已分配元", "归档时尚未核销元", "原交票日期", "原收票人", "客户减免元"}, 6, true},
		{"收款流水", `SELECT o.company_id,o.company_name,o.id,o.received_at,'公司收款',o.method,o.voucher_no,o.note,IF(o.status='voided','已作废','有效'),CAST(o.amount_cents/100 AS DECIMAL(24,2)),IF(o.status='voided',0,o.amount_cents) FROM company_receipts o WHERE o.received_at>=? AND o.received_at<?` + companyClause + ` ORDER BY o.company_id,o.received_at,o.id`, []string{"公司", "收款编号", "实际到账日期", "收款范围", "收款方式", "凭证号", "备注", "状态", "实际到账金额元"}, 8, false},
	}
	for _, spec := range reports {
		reportArgs := args
		if spec.utcActivity {
			reportArgs = append([]any{utcFrom, utcTo}, args[2:]...)
		}
		rows, err := tx.Query(spec.query, reportArgs...)
		if err != nil {
			return err
		}
		var file *os.File
		var writer *csv.Writer
		var current, companyName, name string
		var total int64
		var receivedTotal, outstandingTotal, discountTotal int64
		count := 0
		finish := func() error {
			if file == nil {
				return nil
			}
			summary := make([]string, len(spec.headers))
			summary[0], summary[spec.amountIndex] = "合计（有效金额元）", scaledDecimal(total, 2)
			if spec.kind == "票据应收" {
				summary[7], summary[8], summary[11] = scaledDecimal(receivedTotal, 2), scaledDecimal(outstandingTotal, 2), scaledDecimal(discountTotal, 2)
			}
			if e := writer.Write(summary); e != nil {
				file.Close()
				return e
			}
			writer.Flush()
			if e := writer.Error(); e != nil {
				file.Close()
				return e
			}
			if e := file.Sync(); e != nil {
				file.Close()
				return e
			}
			if e := file.Close(); e != nil {
				return e
			}
			if e := os.Rename(filepath.Join(dir, name+".partial"), filepath.Join(dir, name)); e != nil {
				return e
			}
			entry := ArchiveFile{File: run + "/" + name, CompanyID: current, Company: companyName, Kind: spec.kind, Count: count, Amount: total, Available: true}
			if spec.kind == "票据应收" {
				received, outstanding := receivedTotal, outstandingTotal
				entry.Received, entry.Outstanding = &received, &outstanding
				entry.FinanceScope = "allocation"
			}
			if spec.kind == "收款流水" {
				cash := total
				entry.CashReceived = &cash
				entry.FinanceScope = "cash"
			}
			*files = append(*files, entry)
			file = nil
			return nil
		}
		for rows.Next() {
			var companyID string
			var cents int64
			var received, outstanding, discount int64
			values := make([]sql.NullString, len(spec.headers))
			dest := []any{&companyID}
			for i := range values {
				dest = append(dest, &values[i])
			}
			dest = append(dest, &cents)
			if spec.kind == "票据应收" {
				dest = append(dest, &received, &outstanding, &discount)
			}
			if err = rows.Scan(dest...); err != nil {
				break
			}
			if companyID != current {
				if err = finish(); err != nil {
					break
				}
				current = companyID
				companyName = values[0].String
				name = id() + ".csv"
				file, err = os.Create(filepath.Join(dir, name+".partial"))
				if err != nil {
					break
				}
				if _, err = file.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
					break
				}
				writer = csv.NewWriter(file)
				writer.UseCRLF = true
				if err = writer.Write(spec.headers); err != nil {
					break
				}
				total = 0
				receivedTotal, outstandingTotal, discountTotal = 0, 0, 0
				count = 0
			}
			if cents < 0 || total > math.MaxInt64-cents {
				err = fail(422, "归档金额超出范围")
				break
			}
			total += cents
			if spec.kind == "票据应收" {
				if e := addArchiveCents(&discountTotal, discount); e != nil {
					err = e
					break
				}
				if received < 0 || outstanding < 0 || receivedTotal > math.MaxInt64-received || outstandingTotal > math.MaxInt64-outstanding {
					err = fail(422, "归档收款余额无效或超出可计算范围")
					break
				}
				receivedTotal += received
				outstandingTotal += outstanding
			}
			count++
			line := make([]string, len(values))
			for i, v := range values {
				line[i] = csvSafe(v.String)
			}
			if err = writer.Write(line); err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			if file != nil {
				file.Close()
			}
			return err
		}
		if err = finish(); err != nil {
			return err
		}
	}
	return nil
}
