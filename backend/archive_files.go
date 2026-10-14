package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var archiveKinds = []string{"施工明细", "售料明细", "施工验收表", "工资明细", "施工内部留档表", "财务应收", "旧单据应收", "票据应收", "收款流水", "工资付款", "工资调整", "工资扣款", "工资预支", "进货金额", "报销登记", "报销付款", "报账预支", "报账核销", "报账退回"}

func archivePermission(kind string) string {
	switch kind {
	case "施工明细", "售料明细", "施工验收表":
		return "orders"
	case "工资明细", "施工内部留档表", "工资付款", "工资调整", "工资扣款", "工资预支":
		return "wages"
	case "进货金额":
		return "warehouse"
	case "报销登记", "报销付款", "报账预支", "报账核销", "报账退回":
		return "expenses"
	case "财务应收", "旧单据应收", "票据应收", "收款流水":
		return "finance"
	default:
		return ""
	}
}
func canReadArchive(u User, kind string) bool {
	permission := archivePermission(kind)
	if permission == "" || !u.can(permission) {
		return false
	}
	// Independent payroll, expense and procurement ledgers contain no customer
	// charges. The internal construction workbook still needs customer prices.
	if kind != "施工内部留档表" && (permission == "wages" || permission == "expenses" || permission == "warehouse") {
		return true
	}
	return u.can("customer_prices")
}
func archiveReadableKinds(u User) []string {
	kinds := []string{}
	for _, kind := range archiveKinds {
		if canReadArchive(u, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func validArchiveRun(run string) bool {
	if len(run) != 32 {
		return false
	}
	_, err := hex.DecodeString(run)
	return err == nil
}
func archiveFileParts(run, file string) (string, error) {
	parts := strings.Split(file, "/")
	if !validArchiveRun(run) || len(parts) != 2 || parts[0] != run || parts[1] == "" || parts[1] == "." || parts[1] == ".." || filepath.Base(parts[1]) != parts[1] || strings.ContainsAny(file, "\\:\x00\r\n") {
		return "", fail(400, "归档路径无效")
	}
	return parts[1], nil
}

// Open through a bounded root, reject directory/leaf links, then match the
// lstat identity against the opened file. Serve the open handle, never reopen a
// user-controlled path after checking it.
func openArchiveFile(run, file string) (*os.File, error) {
	leaf, err := archiveFileParts(run, file)
	if err != nil {
		return nil, err
	}
	monthly := filepath.Join(env("BACKUP_DIR", "backups"), "monthly")
	for _, path := range []string{monthly, filepath.Join(monthly, run)} {
		info, e := os.Lstat(path)
		if e != nil {
			return nil, fail(404, "归档文件缺失，请按该月份重新生成")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fail(422, "归档目录无效或含链接")
		}
	}
	info, err := os.Lstat(filepath.Join(monthly, run, leaf))
	if err != nil {
		return nil, fail(404, "归档文件缺失，请按该月份重新生成")
	}
	if !info.Mode().IsRegular() {
		return nil, fail(422, "归档文件无效或含链接")
	}
	root, err := os.OpenRoot(monthly)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Join(run, leaf))
	if err != nil {
		return nil, fail(422, "归档文件无法安全读取")
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return nil, fail(422, "归档文件在读取时变化，请重试")
	}
	return f, nil
}

func archiveGroupKey(f ArchiveFile) string {
	if f.CompanyID != "" {
		return "c:" + f.CompanyID
	}
	sum := sha256.Sum256([]byte(f.Company))
	return "l:" + fmt.Sprintf("%x", sum)
}

func readableArchiveFile(run string, f ArchiveFile) ArchiveFile {
	opened, err := openArchiveFile(run, f.File)
	f.Available = err == nil
	if err != nil {
		return f
	}
	defer opened.Close()
	// Older manifests reused the same kinds for sales and ticket receivables.
	// Recognize only the actual bounded CSV header, leaving historical bytes and
	// the manifest untouched. Unrecognized headers retain the original label.
	if strings.EqualFold(filepath.Ext(f.File), ".csv") && (f.Kind == "施工明细" || f.Kind == "财务应收") {
		reader := csv.NewReader(bufio.NewReader(io.LimitReader(opened, 8192)))
		header, e := reader.Read()
		if e == nil {
			for i := range header {
				header[i] = strings.TrimPrefix(header[i], "\ufeff")
			}
			joined := strings.Join(header, "|")
			if f.Kind == "施工明细" && joined == "公司|施工/送货记录号|实际日期|工地|销售材料|销售数量|单位|销售单价元|货款元" {
				f.Kind = "售料明细"
			}
			if f.Kind == "财务应收" && strings.HasPrefix(joined, "公司|工地票号|建票日期|工地|票类|当前状态|归档时应收元|") {
				f.Kind = "票据应收"
			}
			if f.Kind == "财务应收" && strings.HasPrefix(joined, "公司|施工月份|单号|工地|施工日期|施工人|施工金额|") {
				f.Kind = "旧单据应收"
			}
		}
	}
	return f
}
