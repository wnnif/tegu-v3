package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type restoreRequest struct {
	File    string `json:"file"`
	Target  string `json:"target"`
	Confirm string `json:"confirm"`
}

func backupPath(name string) (string, error) {
	if filepath.Base(name) != name || !strings.HasPrefix(name, "tegu-go-") || !strings.HasSuffix(name, ".ndjson") {
		return "", fail(400, "备份文件名无效")
	}
	path := filepath.Join(env("BACKUP_DIR", "backups"), name)
	info, e := os.Lstat(path)
	if e != nil {
		return "", fail(404, "备份已按保留策略清理或不存在")
	}
	if !info.Mode().IsRegular() {
		return "", fail(400, "备份不是普通文件")
	}
	return path, nil
}
func inspectSnapshot(file string) (map[string]int, error) {
	expected, e := os.ReadFile(file + ".sha256")
	if e != nil {
		return nil, fail(400, "缺少校验文件")
	}
	sum, e := fileHash(file)
	if e != nil {
		return nil, e
	}
	if sum != strings.TrimSpace(string(expected)) {
		return nil, fail(400, "备份校验失败")
	}
	f, e := os.Open(file)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var head snapshotRecord
	if e = dec.Decode(&head); e != nil || head.Format != "tegu-go-v1" {
		return nil, fail(400, "备份格式无效")
	}
	counts := map[string]int{}
	ended := false
	for {
		var record snapshotRecord
		e = dec.Decode(&record)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if ended {
			return nil, fail(400, "备份尾部无效")
		}
		if record.End {
			for _, table := range backupTables {
				if record.Counts[table] != counts[table] {
					return nil, fail(400, "备份行数不匹配")
				}
			}
			ended = true
			continue
		}
		if !contains(backupTables, record.Table) {
			return nil, fail(400, "备份含未知表")
		}
		counts[record.Table]++
	}
	if !ended {
		return nil, fail(400, "备份缺少结束记录")
	}
	return counts, nil
}
func (a *App) backupFile(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("backups") {
		return fail(403, "仅系统管理员可下载备份")
	}
	file, e := backupPath(r.URL.Query().Get("file"))
	if e != nil {
		return e
	}
	if r.URL.Query().Get("checksum") == "1" {
		file += ".sha256"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(file))
	http.ServeFile(w, r, file)
	return nil
}
func (a *App) backupConsole(w http.ResponseWriter, r *http.Request, u User, b []byte) error {
	if !u.can("backups") {
		return fail(403, "仅系统管理员可维护备份")
	}
	var p restoreRequest
	if e := decode(b, &p); e != nil {
		return e
	}
	file, e := backupPath(p.File)
	if e != nil {
		return e
	}
	counts, e := inspectSnapshot(file)
	if e != nil {
		return e
	}
	if r.URL.Path == "/api/maintenance/verify" {
		write(w, 200, map[string]any{"counts": counts, "checksum": "通过", "format": "tegu-go-v1"})
		return nil
	}
	result, e := a.restoreToDatabase(p, u, nil)
	if e == nil {
		write(w, 200, result)
	}
	return e
}

func (a *App) restoreToDatabase(p restoreRequest, u User, progress func(string, int, int)) (map[string]any, error) {
	file, e := backupPath(p.File)
	if e != nil {
		return nil, e
	}
	if progress != nil {
		progress("校验快照", 0, 3)
	}
	counts, e := inspectSnapshot(file)
	if e != nil {
		return nil, e
	}
	if !regexp.MustCompile(`^tegu_restore_[a-z0-9_]{1,40}$`).MatchString(p.Target) || p.Target == env("MYSQL_DATABASE", "tegu_v3") || p.Confirm != p.Target {
		return nil, fail(400, "恢复目标必须为独立的 tegu_restore_ 开头空库，并输入相同库名确认")
	}
	if !a.backupMu.TryLock() {
		return nil, fail(409, "备份或恢复正在执行")
	}
	defer a.backupMu.Unlock()
	cfg := mysql.Config{User: env("MYSQL_USER", "tegu"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: env("MYSQL_HOST", "127.0.0.1") + ":" + env("MYSQL_PORT", "3306"), DBName: p.Target, AllowNativePasswords: true, Timeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, Params: map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"}}
	target, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return nil, e
	}
	defer target.Close()
	if e = target.PingContext(context.Background()); e != nil {
		return nil, fail(400, "无法连接恢复目标：请先创建独立空库并为应用账号授权该库；当前业务库未修改")
	}
	lock, e := target.Conn(context.Background())
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	var locked int
	if e = lock.QueryRowContext(context.Background(), "SELECT GET_LOCK(CONCAT(DATABASE(),':restore'),0)").Scan(&locked); e != nil {
		return nil, e
	}
	if locked != 1 {
		return nil, fail(409, "该目标库已有恢复任务执行中")
	}
	defer lock.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':restore'))")
	var n int
	if e = target.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&n); e != nil {
		return nil, e
	}
	if n != 0 {
		return nil, fail(409, "恢复目标必须没有任何表，请使用新的空库")
	}
	if progress != nil {
		progress("恢复到独立空库", 1, 3)
	}
	if e = initSchema(target); e != nil {
		return nil, e
	}
	restored := &App{db: target}
	if e = restored.restore(file); e != nil {
		log.Printf("restore target %s failed: %v", p.Target, e)
		return nil, fail(400, "恢复未完成，目标库不可投入使用；请检查服务日志并重新准备空库")
	}
	if progress != nil {
		progress("核对并记录结果", 2, 3)
	}
	tx, e := a.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = audit(tx, u, "恢复到独立数据库", p.Target, map[string]any{"file": p.File, "counts": counts}); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return map[string]any{"target": p.Target, "counts": counts, "message": "数据已恢复到独立数据库，当前服务未切换。月度表格请在恢复环境重新生成；云存储须迁移backup.key或重新填写凭据。"}, nil
}
func (a *App) backupList(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("backups") {
		return fail(403, "仅系统管理员可维护备份")
	}
	p, e := queryPage(r.URL.Query())
	if e != nil {
		return e
	}
	var count int64
	if e = a.db.QueryRow("SELECT COUNT(*) FROM backup_runs").Scan(&count); e != nil {
		return e
	}
	v, e := scanBackupRuns(a.db, p)
	if e != nil {
		return e
	}
	for i := range v {
		file, err := backupPath(v[i].File)
		v[i].Available = err == nil
		if err == nil {
			info, err := os.Stat(file)
			if err == nil {
				v[i].Bytes = info.Size()
			}
		}
	}
	var used int64
	e = filepath.WalkDir(env("BACKUP_DIR", "backups"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			used += info.Size()
		}
		return nil
	})
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	result := backupListResponse{Rows: v, Total: count, Page: p, UsedBytes: used}
	if free, err := freeDisk(env("BACKUP_DIR", "backups")); err == nil {
		result.FreeBytes = &free
		result.LowSpace = free < 5*1024*1024*1024
	}
	write(w, 200, result)
	return nil
}
