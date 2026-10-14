package main

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func safeArchiveDownloadName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r < 32 || strings.ContainsRune("/\\:*?\"<>|", r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), " .")
	if s == "" {
		s = "公司"
	}
	r := []rune(s)
	if len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

func (a *App) downloadArchiveCompany(w http.ResponseWriter, r *http.Request, u User) error {
	if len(archiveReadableKinds(u)) == 0 {
		return fail(403, "没有导出权限")
	}
	run, key := r.URL.Query().Get("run_id"), r.URL.Query().Get("key")
	if !validArchiveRun(run) || key == "" || len(key) > 130 {
		return fail(400, "归档批次或公司标识无效")
	}
	var raw, month string
	err := a.db.QueryRowContext(r.Context(), "SELECT manifest,month FROM archive_runs WHERE id=?", run).Scan(&raw, &month)
	if err == sql.ErrNoRows {
		return fail(404, "归档不存在")
	}
	if err != nil {
		return err
	}
	var manifest []ArchiveFile
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	files := []ArchiveFile{}
	company := ""
	for _, f := range manifest {
		if archiveGroupKey(f) == key && canReadArchive(u, f.Kind) {
			files = append(files, f)
			if company == "" {
				company = f.Company
			}
		}
	}
	if len(files) == 0 {
		return fail(404, "该公司归档不存在或无权限")
	}
	// Spool the entire authorized set before sending headers. A missing file or
	// write failure cannot turn into a successful partial download.
	spool, err := os.CreateTemp("", "tegu-monthly-*.zip")
	if err != nil {
		return err
	}
	defer func() { spool.Close(); os.Remove(spool.Name()) }()
	zw := zip.NewWriter(spool)
	used := map[string]int{}
	for _, f := range files {
		if err = r.Context().Err(); err != nil {
			zw.Close()
			return err
		}
		opened, e := openArchiveFile(run, f.File)
		if e != nil {
			zw.Close()
			return e
		}
		display := readableArchiveFile(run, f)
		ext := strings.ToLower(filepath.Ext(f.File))
		if ext != ".csv" && ext != ".xlsx" {
			opened.Close()
			zw.Close()
			return fail(422, "归档文件格式无效")
		}
		name := safeArchiveDownloadName(display.Kind) + ext
		used[name]++
		if used[name] > 1 {
			name = fmt.Sprintf("%s-%d%s", safeArchiveDownloadName(display.Kind), used[name], ext)
		}
		entry, e := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if e == nil {
			_, e = io.Copy(entry, opened)
		}
		closeErr := opened.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			zw.Close()
			return e
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = spool.Sync(); err != nil {
		return err
	}
	info, err := spool.Stat()
	if err != nil {
		return err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	filename := safeArchiveDownloadName(company) + "-" + safeArchiveDownloadName(month) + "-整月资料.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filename, info.ModTime(), spool)
	return nil
}
