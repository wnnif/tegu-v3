package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type snapshotRecord struct {
	Format string         `json:"format,omitempty"`
	Table  string         `json:"table,omitempty"`
	Row    map[string]any `json:"row,omitempty"`
	Counts map[string]int `json:"counts,omitempty"`
	End    bool           `json:"end,omitempty"`
}

func scaledDecimal(value int64, places int) string {
	digits := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if len(digits) <= places {
		digits = strings.Repeat("0", places+1-len(digits)) + digits
	}
	split := len(digits) - places
	return sign + digits[:split] + "." + digits[split:]
}

func fileHash(file string) (string, error) {
	f, e := os.Open(file)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (a *App) backup(reporters ...func(string, int, int)) (resultName string, resultErr error) {
	if !a.backupMu.TryLock() {
		return "", fail(409, "备份正在进行")
	}
	defer a.backupMu.Unlock()
	release, e := a.operationLock("backup")
	if e != nil {
		return "", e
	}
	defer release()
	report := func(stage string, progress int) {
		if len(reporters) > 0 && reporters[0] != nil {
			reporters[0](stage, progress, len(backupTables)+2)
		}
	}
	report("读取数据库快照", 0)
	dir := env("BACKUP_DIR", "backups")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	name := "tegu-go-" + time.Now().UTC().Format("20060102T150405") + "-" + id() + ".ndjson"
	target := filepath.Join(dir, name)
	f, e := os.OpenFile(target+".partial", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	defer func() {
		f.Close()
		if err := os.Remove(target + ".partial"); err != nil && !os.IsNotExist(err) {
			resultErr = errors.Join(resultErr, fmt.Errorf("清理本次未完成备份: %w", err))
		}
	}()
	tx, e := a.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	enc := json.NewEncoder(f)
	if e = enc.Encode(snapshotRecord{Format: "tegu-go-v1"}); e != nil {
		return "", e
	}
	counts := map[string]int{}
	for index, t := range backupTables {
		query := "SELECT * FROM " + t
		if t == "maintenance_jobs" {
			query += " WHERE status<>'运行中'"
		}
		r, e := tx.Query(query)
		if e != nil {
			return "", e
		}
		cols, _ := r.Columns()
		counts[t] = 0
		for r.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if e = r.Scan(p...); e != nil {
				r.Close()
				return "", e
			}
			row := map[string]any{}
			for i, col := range cols {
				if b, ok := v[i].([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = v[i]
				}
			}
			if e = enc.Encode(snapshotRecord{Table: t, Row: row}); e != nil {
				r.Close()
				return "", e
			}
			counts[t]++
		}
		if e = r.Err(); e != nil {
			r.Close()
			return "", e
		}
		r.Close()
		report("写入快照："+t, index+1)
	}
	if e = enc.Encode(snapshotRecord{End: true, Counts: counts}); e != nil {
		return "", e
	}
	if e = tx.Commit(); e != nil {
		return "", e
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	sum, e := fileHash(target + ".partial")
	if e != nil {
		return "", e
	}
	if e = os.Rename(target+".partial", target); e != nil {
		return "", e
	}
	if e = os.WriteFile(target+".sha256", []byte(sum), 0600); e != nil {
		return "", e
	}
	report("保存校验与备份记录", len(backupTables)+1)
	_, e = a.db.Exec("INSERT INTO backup_runs(id,file,status,message) VALUES(?,?,'成功','一致性快照已生成并校验')", id(), name)
	if e != nil {
		return name, e
	}
	settings, e := a.backupSettings()
	if e != nil {
		return name, e
	}
	var cloudErr error
	if settings.Enabled {
		if cloudErr = uploadBackup(settings, target); cloudErr != nil {
			_, recordErr := a.db.Exec("UPDATE backup_runs SET status='云上传失败',message=? WHERE file=?", cloudErr.Error(), name)
			cloudErr = errors.Join(cloudErr, recordErr)
		} else {
			_, cloudErr = a.db.Exec("UPDATE backup_runs SET message='本地快照及云端文件、校验文件均已保存' WHERE file=?", name)
		}
	}
	// Local retention follows a completed, checksummed local snapshot even when
	// the remote service is unavailable, otherwise an outage grows the disk forever.
	return name, errors.Join(cloudErr, pruneBackups(dir, settings.Retain))
}
func (a *App) restore(file string) error {
	expected, e := os.ReadFile(file + ".sha256")
	if e != nil {
		return e
	}
	sum, e := fileHash(file)
	if e != nil {
		return e
	}
	if sum != strings.TrimSpace(string(expected)) {
		return fail(400, "备份校验失败")
	}
	for _, t := range backupTables {
		var n int
		if e = a.db.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fail(409, "恢复仅允许全新空数据库")
		}
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var head snapshotRecord
	if e = dec.Decode(&head); e != nil || head.Format != "tegu-go-v1" {
		return fail(400, "备份格式不支持")
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	counts := map[string]int{}
	legacyLineWorkers := map[string]bool{}
	legacyProjectNames := map[string]bool{}
	legacyProjectNumbers := map[string]bool{}
	ended := false
	for {
		var r snapshotRecord
		e = dec.Decode(&r)
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if ended {
			return fail(400, "备份尾部无效")
		}
		if r.End {
			for _, t := range backupTables {
				if counts[t] != r.Counts[t] {
					return fail(400, "备份数量核对失败")
				}
			}
			ended = true
			continue
		}
		if !contains(backupTables, r.Table) {
			return fail(400, "备份表无效")
		}
		cols := []string{}
		args := []any{}
		for k, v := range r.Row {
			for _, ch := range k {
				if (ch < 'a' || ch > 'z') && ch != '_' {
					return fail(400, "备份字段无效")
				}
			}
			cols = append(cols, "`"+k+"`")
			if n, ok := v.(json.Number); ok {
				v = n.String()
			}
			args = append(args, v)
		}
		if len(cols) == 0 {
			return fail(400, "空备份行")
		}
		if r.Table == "order_lines" {
			if _, ok := r.Row["worker_id"]; !ok {
				legacyLineWorkers[fmt.Sprint(r.Row["id"])] = true
			}
		}
		if r.Table == "orders" {
			orderID, hasID := r.Row["id"]
			if hasID {
				id := fmt.Sprint(orderID)
				if _, ok := r.Row["project_name"]; !ok {
					legacyProjectNames[id] = true
				}
				if _, ok := r.Row["project_number"]; !ok {
					legacyProjectNumbers[id] = true
				}
			}
		}
		_, e = tx.Exec("INSERT INTO "+r.Table+"("+strings.Join(cols, ",")+") VALUES(?"+strings.Repeat(",?", len(cols)-1)+")", args...)
		if e != nil {
			return e
		}
		counts[r.Table]++
	}
	if !ended {
		return fail(400, "备份不完整")
	}
	// Snapshots made before migration 300 do not contain project snapshot
	// columns. Fill only those missing fields; an explicitly saved empty value
	// in a current snapshot must remain unchanged.
	for orderID := range legacyProjectNames {
		if _, e = tx.Exec("UPDATE orders o JOIN projects p ON p.id=o.project_id SET o.project_name=p.name WHERE o.id=? AND o.project_name='' AND o.project_id<>''", orderID); e != nil {
			return e
		}
	}
	for orderID := range legacyProjectNumbers {
		if _, e = tx.Exec("UPDATE orders o JOIN projects p ON p.id=o.project_id SET o.project_number=p.number WHERE o.id=? AND o.project_number='' AND o.project_id<>''", orderID); e != nil {
			return e
		}
	}
	for lineID := range legacyLineWorkers {
		if _, e = tx.Exec("UPDATE order_lines l JOIN orders o ON o.id=l.order_id SET l.worker_id=o.worker_id,l.worker_name=o.worker_name WHERE l.id=?", lineID); e != nil {
			return e
		}
	}
	if e = validateWorkerAccounts(tx); e != nil {
		return e
	}
	if e = backfillWorkerWages(tx); e != nil {
		return e
	}
	if e = backfillCompanyReceiptsTx(tx); e != nil {
		return e
	}
	return tx.Commit()
}

type ArchiveFile struct {
	File         string `json:"file"`
	Company      string `json:"company"`
	Kind         string `json:"kind"`
	Count        int    `json:"count"`
	Amount       int64  `json:"amount_cents"`
	Available    bool   `json:"available"`
	CompanyID    string `json:"company_id,omitempty"`
	Received     *int64 `json:"received_cents,omitempty"`
	Outstanding  *int64 `json:"outstanding_cents,omitempty"`
	GeneratedAt  string `json:"generated_at,omitempty"`
	FinanceScope string `json:"finance_scope,omitempty"`
	CashReceived *int64 `json:"cash_received_cents,omitempty"`
}

func (a *App) archive(month, company string, automatic bool, actors ...User) (result []ArchiveFile, resultErr error) {
	return a.archiveWithProgress(month, company, automatic, nil, actors...)
}

func (a *App) archiveWithProgress(month, company string, automatic bool, report func(string, int, int), actors ...User) (result []ArchiveFile, resultErr error) {
	if !a.exportMu.TryLock() {
		return nil, fail(409, "表格正在生成")
	}
	defer a.exportMu.Unlock()
	release, lockErr := a.operationLock("archive")
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if report != nil {
		report("读取整月数据", 0, 3)
	}
	defer func() {
		message := ""
		if resultErr != nil {
			message = "归档生成失败，请检查服务日志并重试"
			log.Printf("archive %s failed: %v", month, resultErr)
		}
		if _, err := a.db.Exec("INSERT INTO settings(setting_key,value) VALUES('archive_status',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", jsonText(map[string]any{"month": month, "error": message, "time": time.Now().UTC().Format(time.RFC3339)})); err != nil {
			log.Printf("archive status write failed: %v", err)
		}
	}()
	from, to, e := monthRange(month)
	if e != nil {
		return nil, e
	}
	tx, e := a.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	args := []any{from, to}
	where := "o.work_date>=? AND o.work_date<? AND o.status<>'已取消'"
	if company != "" {
		where += " AND o.company_id=?"
		args = append(args, company)
	}
	orders, e := archiveCompanies(tx, false, where, args)
	if e != nil {
		return nil, e
	}
	groups := map[string]archiveCompany{}
	for _, o := range orders {
		groups[o.ID] = o
	}
	if report != nil {
		report("生成整月表格", 1, 3)
	}
	run := id()
	monthlyDir := filepath.Join(env("BACKUP_DIR", "backups"), "monthly")
	if e = os.MkdirAll(monthlyDir, 0700); e != nil {
		return nil, e
	}
	dir := filepath.Join(monthlyDir, run)
	if e = os.Mkdir(dir, 0700); e != nil {
		return nil, e
	}
	preserveDir := false
	defer func() {
		if !preserveDir {
			resultErr = errors.Join(resultErr, os.RemoveAll(dir))
		}
	}()
	files := []ArchiveFile{}
	for companyID := range groups {
		var amount int64
		count := 0
		name := id() + ".csv"
		f, e := os.Create(filepath.Join(dir, name+".partial"))
		if e != nil {
			return nil, e
		}
		if _, e = f.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
			f.Close()
			return nil, e
		}
		cw := csv.NewWriter(f)
		cw.UseCRLF = true
		cw.Write([]string{"公司", "单号", "工地", "日期", "施工大类", "工艺分项", "规格", "施工部位", "说明", "计费方式", "尺寸明细", "工程量", "单价", "金额", "收费模式", "收费备注"})
		// Bound detail memory while keeping the month in one consistent snapshot.
		cursor := ""
		for {
			batchArgs := append(append([]any{}, args...), companyID, cursor)
			items, queryErr := archiveLines(tx, where, batchArgs)
			if queryErr != nil {
				f.Close()
				return nil, queryErr
			}
			if len(items) == 0 {
				break
			}
			for _, l := range items {
				cursor = l.ID
				lineAmount := l.TotalCents
				if lineAmount < 0 || amount > math.MaxInt64-lineAmount {
					f.Close()
					return nil, fail(422, "归档金额超出可计算范围")
				}
				amount += lineAmount
				count++
				location := archiveV3Location(l.SubitemName, l.ActualLocation, l.Region)
				values := []string{l.Company, l.Number, l.Address, l.WorkDate, l.Category, l.Name, l.Spec, location, l.Description, "按各段计费（整项默认：" + l.Mode + "）", l.Segments, segmentQuantityLabel(l.Mode, l.Segments, l.QtyMicro), scaledDecimal(l.PriceCents, 2), scaledDecimal(l.TotalCents, 2), archiveV3ChargeLabel(l.ChargeMode, l.IncludedPrice), l.ChargeNote}
				for i, v := range values {
					trim := strings.TrimSpace(v)
					if len(trim) > 0 && strings.ContainsAny(trim[:1], "=+-@") {
						values[i] = "'" + v
					}
				}
				cw.Write(values)
			}
			if cw.Error() != nil {
				f.Close()
				return nil, cw.Error()
			}
		}
		summary := make([]string, 16)
		summary[0], summary[13] = "合计", scaledDecimal(amount, 2)
		cw.Write(summary)
		cw.Flush()
		if e = cw.Error(); e != nil {
			f.Close()
			return nil, e
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
		if e = os.Rename(filepath.Join(dir, name+".partial"), filepath.Join(dir, name)); e != nil {
			return nil, e
		}
		if count > 0 {
			files = append(files, ArchiveFile{File: run + "/" + name, CompanyID: companyID, Company: groups[companyID].Name, Kind: "施工明细", Count: count, Amount: amount, Available: true})
		} else if e = os.Remove(filepath.Join(dir, name)); e != nil {
			return nil, e
		}
		acceptance, acceptanceErr := writeAcceptanceArchive(tx, where, args, companyID, groups[companyID].Name, run, dir, false)
		if acceptanceErr != nil {
			return nil, acceptanceErr
		}
		files = append(files, acceptance)
		internal, internalErr := writeAcceptanceArchive(tx, where, args, companyID, groups[companyID].Name, run, dir, true)
		if internalErr != nil {
			return nil, internalErr
		}
		files = append(files, internal)
	}
	for companyID, companyEntry := range groups {
		for _, kind := range []string{"旧单据应收", "工资明细"} {
			name := id() + ".csv"
			file := filepath.Join(dir, name)
			f, e := os.Create(file + ".partial")
			if e != nil {
				return nil, e
			}
			if _, e = f.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
				f.Close()
				return nil, e
			}
			writer := csv.NewWriter(f)
			writer.UseCRLF = true
			headers := []string{"公司", "月份", "单号", "工地", "日期", "施工人", "金额", "状态"}
			if kind == "旧单据应收" {
				headers = []string{"公司", "施工月份", "单号", "工地", "施工日期", "施工人", "施工金额", "已收金额", "剩余应收", "状态", "票据号", "交票日期", "收票人", "交票附件", "退回说明"}
			}
			writer.Write(headers)
			total := int64(0)
			receivedTotal, outstandingTotal := int64(0), int64(0)
			count := 0
			cursor := ""
			for {
				batchArgs := append(append([]any{}, args...), companyID, cursor)
				orderWhere := where
				if kind == "旧单据应收" {
					orderWhere += " AND o.ticket_managed=0"
				}
				var batch []archiveOrder
				var queryErr error
				if kind == "工资明细" {
					batch, queryErr = archiveWorkerWages(tx, orderWhere, batchArgs)
				} else {
					batch, queryErr = archiveOrders(tx, orderWhere, batchArgs)
				}
				if queryErr != nil {
					f.Close()
					return nil, queryErr
				}
				if len(batch) == 0 {
					break
				}
				for _, o := range batch {
					cursor = o.ID
					count++
					amount := o.TotalCents
					status := o.Status
					if kind == "工资明细" {
						amount = o.WageCents
						status = "未记录付款（实际金额见工资管理）"
						if o.WagePaid {
							status = "已有付款记录（实际金额见工资管理）"
						}
					}
					if amount < 0 || total > math.MaxInt64-amount {
						f.Close()
						return nil, fail(422, "归档金额超出可计算范围")
					}
					total += amount
					values := []string{o.Company, month, o.Number, o.Address, o.WorkDate, o.Worker, scaledDecimal(amount, 2), status}
					if kind == "旧单据应收" {
						received, outstanding := o.ReceivedCents, o.OutstandingCents
						if received < 0 || outstanding < 0 || receivedTotal > math.MaxInt64-received || outstandingTotal > math.MaxInt64-outstanding {
							f.Close()
							return nil, fail(422, "归档收款余额无效或超出可计算范围")
						}
						receivedTotal += received
						outstandingTotal += outstanding
						values = []string{o.Company, month, o.Number, o.Address, o.WorkDate, o.Worker, scaledDecimal(amount, 2), scaledDecimal(received, 2), scaledDecimal(outstanding, 2), status, o.TicketNo, o.DeliveredAt, o.Recipient, o.TicketAttachment, o.ReturnNote}
					}
					for i, v := range values {
						if strings.ContainsAny(strings.TrimLeft(v, " \t\r\n")[:min(1, len(strings.TrimLeft(v, " \t\r\n")))], "=+-@") {
							values[i] = "'" + v
						}
					}
					writer.Write(values)
				}
				if writer.Error() != nil {
					f.Close()
					return nil, writer.Error()
				}
			}
			summary := make([]string, len(headers))
			summary[0], summary[6] = "合计", scaledDecimal(total, 2)
			if kind == "旧单据应收" {
				summary[7], summary[8], summary[9] = scaledDecimal(receivedTotal, 2), scaledDecimal(outstandingTotal, 2), fmt.Sprint(count)+"张"
			} else {
				summary[7] = fmt.Sprint(count) + "笔"
			}
			writer.Write(summary)
			writer.Flush()
			if e = writer.Error(); e != nil {
				f.Close()
				return nil, e
			}
			if e = f.Sync(); e != nil {
				f.Close()
				return nil, e
			}
			if e = f.Close(); e != nil {
				return nil, e
			}
			if e = os.Rename(file+".partial", file); e != nil {
				return nil, e
			}
			if count == 0 {
				if e = os.Remove(file); e != nil {
					return nil, e
				}
				continue
			}
			entry := ArchiveFile{File: run + "/" + name, CompanyID: companyID, Company: companyEntry.Name, Kind: kind, Count: count, Amount: total, Available: true}
			if kind == "旧单据应收" {
				entry.Received, entry.Outstanding = &receivedTotal, &outstandingTotal
			}
			files = append(files, entry)
		}
	}
	// Receipts are archived by their arrival month so payments for an older
	// construction month are still included in the current month's bank reconciliation.
	if e = appendReceiptArchives(tx, month, from, to, company, run, dir, &files); e != nil {
		return nil, e
	}
	if e = appendWorkflowArchives(tx, from, to, company, run, dir, &files); e != nil {
		return nil, e
	}
	if e = appendV3Archives(tx, from, to, company, run, dir, &files); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if report != nil {
		report("保存归档记录", 2, 3)
	}
	// The table's timestamp has second precision. Preserve the generation
	// instant in the new manifest so immediate repeats still select the new run.
	generatedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	for i := range files {
		files[i].GeneratedAt = generatedAt
	}
	writeTx, e := a.db.Begin()
	if e != nil {
		return nil, e
	}
	defer writeTx.Rollback()
	if _, e = writeTx.Exec("INSERT INTO archive_runs(id,month,automatic,manifest) VALUES(?,?,?,?)", run, month, automatic, jsonText(files)); e != nil {
		return nil, e
	}
	actor := User{ID: "system", Name: "自动归档"}
	if len(actors) > 0 {
		actor = actors[0]
	}
	if e = audit(writeTx, actor, "生成月度归档", run, map[string]any{"month": month, "company_id": company, "files": len(files), "automatic": automatic}); e != nil {
		return nil, e
	}
	// A commit error may mean the database accepted the manifest. Keep the files
	// until that outcome is checked rather than deleting a possibly listed run.
	preserveDir = true
	return files, writeTx.Commit()
}

func appendReceiptArchives(tx *sql.Tx, month, from, to, company, run, dir string, files *[]ArchiveFile) error {
	where := "r.received_at>=? AND r.received_at<?"
	args := []any{from, to}
	if company != "" {
		where += " AND o.company_id=?"
		args = append(args, company)
	}
	companies, e := archiveCompanies(tx, true, where, args)
	if e != nil {
		return e
	}
	for _, c := range companies {
		entry, e := writeReceiptArchive(tx, month, where, args, c.ID, c.Name, run, dir)
		if e != nil {
			return e
		}
		*files = append(*files, entry)
	}
	return nil
}

func writeReceiptArchive(tx *sql.Tx, month, where string, args []any, companyID, companyName, run, dir string) (ArchiveFile, error) {
	name := id() + ".csv"
	path := filepath.Join(dir, name)
	f, e := os.Create(path + ".partial")
	if e != nil {
		return ArchiveFile{}, e
	}
	defer f.Close()
	if _, e = f.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
		return ArchiveFile{}, e
	}
	writer := csv.NewWriter(f)
	writer.UseCRLF = true
	headers := []string{"公司", "到账月份", "施工单号", "施工日期", "工地", "收款编号", "到账日期", "本笔金额", "方式", "凭证号", "附件", "备注", "记录状态"}
	if e = writer.Write(headers); e != nil {
		return ArchiveFile{}, e
	}
	cursor, count, total := "", 0, int64(0)
	for {
		batchArgs := append(append([]any{}, args...), companyID, cursor)
		batch, e := archiveReceipts(tx, where, batchArgs)
		if e != nil {
			return ArchiveFile{}, e
		}
		if len(batch) == 0 {
			break
		}
		for _, receipt := range batch {
			cursor = receipt.ID
			count++
			amount := receipt.AmountCents
			state := "有效"
			if receipt.Voided {
				state = "已撤销"
			} else if receipt.OrderStatus == "已取消" {
				state = "已取消单据"
			} else {
				if amount < 0 || total > math.MaxInt64-amount {
					return ArchiveFile{}, fail(422, "收款流水金额无效或超出可计算范围")
				}
				total += amount
			}
			values := []string{receipt.Company, month, receipt.Number, receipt.WorkDate, receipt.Address, cursor, receipt.ReceivedAt, scaledDecimal(amount, 2), receipt.Method, receipt.VoucherNo, receipt.Attachment, receipt.Note, state}
			for i, v := range values {
				trimmed := strings.TrimSpace(v)
				if trimmed != "" && strings.ContainsAny(trimmed[:1], "=+-@") {
					values[i] = "'" + v
				}
			}
			if e = writer.Write(values); e != nil {
				return ArchiveFile{}, e
			}
		}
	}
	summary := make([]string, len(headers))
	summary[0], summary[7], summary[12] = "有效收款合计", scaledDecimal(total, 2), "撤销记录保留、不计入合计"
	if e = writer.Write(summary); e != nil {
		return ArchiveFile{}, e
	}
	writer.Flush()
	if e = writer.Error(); e != nil {
		return ArchiveFile{}, e
	}
	if e = f.Sync(); e != nil {
		return ArchiveFile{}, e
	}
	if e = f.Close(); e != nil {
		return ArchiveFile{}, e
	}
	if e = os.Rename(path+".partial", path); e != nil {
		return ArchiveFile{}, e
	}
	return ArchiveFile{File: run + "/" + name, CompanyID: companyID, Company: companyName, Kind: "收款流水", Count: count, Amount: total, Available: true}, nil
}

func (a *App) listArchives(w http.ResponseWriter, r *http.Request, u User) error {
	if len(archiveReadableKinds(u)) == 0 {
		return fail(403, "没有导出权限")
	}
	q := r.URL.Query()
	page, e := queryPage(q)
	if e != nil {
		return e
	}
	wanted := q.Get("file")
	where := " WHERE 1=1"
	args := []any{}
	if wanted != "" {
		parts := strings.Split(wanted, "/")
		if len(parts) != 2 {
			return fail(400, "归档路径无效")
		}
		if _, e = archiveFileParts(parts[0], wanted); e != nil {
			return e
		}
		where += " AND id=?"
		args = append(args, parts[0])
	} else if q.Get("month") != "" {
		if _, _, e = monthRange(q.Get("month")); e != nil {
			return e
		}
		where += " AND month=?"
		args = append(args, q.Get("month"))
	}
	var total int64
	if e = a.db.QueryRow("SELECT COUNT(*) FROM archive_runs"+where, args...).Scan(&total); e != nil {
		return e
	}
	v, e := scanArchiveRuns(a.db, where, args, page)
	if e != nil {
		return e
	}
	for i := range v {
		var files []ArchiveFile
		if e = json.Unmarshal([]byte(v[i].manifest), &files); e != nil {
			return e
		}
		visible := []ArchiveFile{}
		for _, f := range files {
			if !canReadArchive(u, f.Kind) {
				if wanted != "" && wanted == f.File {
					return fail(403, "没有下载此归档文件的权限")
				}
				continue
			}
			if wanted != "" && wanted == f.File {
				opened, err := openArchiveFile(v[i].ID, f.File)
				if err != nil {
					return err
				}
				defer opened.Close()
				info, err := opened.Stat()
				if err != nil {
					return err
				}
				if strings.EqualFold(filepath.Ext(f.File), ".xlsx") {
					filename := "monthly-acceptance.xlsx"
					if f.Kind == "施工内部留档表" {
						filename = "monthly-internal.xlsx"
					}
					w.Header().Set("Content-Disposition", "attachment; filename="+filename)
					w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
				} else {
					w.Header().Set("Content-Disposition", "attachment; filename=monthly.csv")
					w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				}
				http.ServeContent(w, r, filepath.Base(f.File), info.ModTime(), opened)
				return nil
			}
			f = readableArchiveFile(v[i].ID, f)
			visible = append(visible, f)
		}
		v[i].Files = visible
	}
	if wanted != "" {
		return fail(404, "归档文件不存在或无权限")
	}
	var statusRaw string
	var status *archiveStatusRecord
	if e = a.db.QueryRow("SELECT value FROM settings WHERE setting_key='archive_status'").Scan(&statusRaw); e != nil && e != sql.ErrNoRows {
		return e
	}
	if statusRaw != "" {
		if e = json.Unmarshal([]byte(statusRaw), &status); e != nil {
			return e
		}
	}
	write(w, 200, archiveListResponse{Rows: v, Total: total, Page: page, Latest: status})
	return nil
}
func (a *App) maintenance(w http.ResponseWriter, r *http.Request, u User, b []byte) error {
	switch r.URL.Path {
	case "/api/maintenance/cloud-check":
		if !u.can("backups") {
			return fail(403, "仅系统管理员可检查云存储")
		}
		s, e := a.backupSettings()
		if e != nil {
			return e
		}
		if !s.Enabled {
			return fail(400, "请先保存并启用云存储设置")
		}
		payload := []byte("tegu storage connection check")
		if e = cloudPut(s, "connection-check.txt", bytes.NewReader(payload), int64(len(payload)), hash(payload)); e != nil {
			return e
		}
		write(w, 200, map[string]any{"message": "云存储上传成功，检查文件为 connection-check.txt"})
		return nil
	case "/api/maintenance/verify":
		return a.backupConsole(w, r, u, b)
	case "/api/maintenance/restore":
		if !u.can("backups") {
			return fail(403, "仅系统管理员可恢复备份")
		}
		var p restoreRequest
		if e := decode(b, &p); e != nil {
			return e
		}
		if _, e := backupPath(p.File); e != nil {
			return e
		}
		return a.startMaintenanceJob(w, u, "restore", p, func(jobID string) (any, error) {
			return a.restoreToDatabase(p, u, func(stage string, progress, total int) { a.updateMaintenanceJob(jobID, stage, progress, total) })
		})
	case "/api/maintenance/settings":
		if e := a.saveBackupSettings(u, b); e != nil {
			return e
		}
		write(w, 200, map[string]any{"ok": true})
		return nil
	case "/api/maintenance/backup":
		if !u.can("backups") {
			return fail(403, "仅系统管理员可备份")
		}
		return a.startMaintenanceJob(w, u, "backup", struct{}{}, func(jobID string) (any, error) {
			file, e := a.backup(func(stage string, progress, total int) { a.updateMaintenanceJob(jobID, stage, progress, total) })
			if e != nil {
				logBackupError(a, e)
			}
			return map[string]any{"file": file}, e
		})
	case "/api/maintenance/archive":
		if len(archiveReadableKinds(u)) == 0 {
			return fail(403, "无导出权限")
		}
		var p struct {
			Month   string `json:"month"`
			Company string `json:"company"`
		}
		if e := decode(b, &p); e != nil {
			return e
		}
		if _, _, e := monthRange(p.Month); e != nil {
			return e
		}
		if len(p.Company) > 64 {
			return fail(400, "公司编号无效")
		}
		return a.startMaintenanceJob(w, u, "archive", p, func(jobID string) (any, error) {
			files, e := a.archiveWithProgress(p.Month, p.Company, false, func(stage string, progress, total int) { a.updateMaintenanceJob(jobID, stage, progress, total) }, u)
			if e != nil {
				return nil, e
			}
			count := 0
			for _, f := range files {
				if canReadArchive(u, f.Kind) {
					count++
				}
			}
			return map[string]any{"count": count, "month": p.Month}, nil
		})
	}
	return fail(404, "维护接口不存在")
}
func (a *App) scheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var lastBackup, lastArchive time.Time
	run := func() {
		settings, e := a.backupSettings()
		if e != nil {
			logBackupError(a, e)
			return
		}
		interval := settings.Interval
		if interval < 15 {
			interval = 60
		}
		if time.Since(lastBackup) >= time.Duration(interval)*time.Minute {
			lastBackup = time.Now()
			if _, e := a.backup(); e != nil {
				logBackupError(a, e)
			}
		}
		if time.Since(lastArchive) < time.Hour {
			return
		}
		lastArchive = time.Now()
		months, e := a.pendingArchiveMonths(time.Now())
		if e == nil {
			for _, m := range months {
				if _, e = a.archive(m, "", true); e != nil {
					logBackupError(a, e)
				}
			}
		}
		a.db.Exec("DELETE FROM sessions WHERE expires_at<UTC_TIMESTAMP()")
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
func logBackupError(a *App, e error) {
	a.db.Exec("INSERT INTO backup_runs(id,file,status,message) VALUES(?,'','失败',?)", id(), e.Error())
}
