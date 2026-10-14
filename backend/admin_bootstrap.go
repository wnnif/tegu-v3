package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const initialAdminPasswordMaxBytes = 72

type adminBootstrapResult uint8

const (
	adminBootstrapUnchanged adminBootstrapResult = iota
	adminBootstrapCreated
)

// Explicit configuration only: never fall back to a shared/default password.
// Files may end with one LF/CRLF. Spaces are part of the password, not trimmed.
func readInitialAdminPassword() ([]byte, error) {
	path := os.Getenv("ADMIN_INITIAL_PASSWORD_FILE")
	inline := os.Getenv("ADMIN_INITIAL_PASSWORD")
	if path != "" && inline != "" {
		return nil, errors.New("请仅配置 ADMIN_INITIAL_PASSWORD_FILE 或 ADMIN_INITIAL_PASSWORD 其中一项，不能同时配置")
	}
	if path == "" {
		if inline == "" {
			return nil, errors.New("空用户库尚未初始化管理员；请显式配置 ADMIN_INITIAL_PASSWORD_FILE（推荐）或 ADMIN_INITIAL_PASSWORD，再运行 tegu init-admin；不会生成默认密码")
		}
		return validateInitialAdminPassword([]byte(inline))
	}

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("无法读取初始管理员密码文件；请检查 ADMIN_INITIAL_PASSWORD_FILE 指向可读取的普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法打开初始管理员密码文件；请检查文件是否存在和访问权限")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("初始管理员密码文件必须是可读取的普通文件")
	}
	// Bound the read even if the file changes after Stat; allow a final CRLF.
	data, err := io.ReadAll(io.LimitReader(file, initialAdminPasswordMaxBytes+3))
	if err != nil {
		clear(data)
		return nil, errors.New("读取初始管理员密码文件失败；密码未输出")
	}
	if len(data) > initialAdminPasswordMaxBytes+2 {
		clear(data)
		return nil, initialAdminPasswordFormatError()
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	return validateInitialAdminPassword(data)
}

func initialAdminPasswordFormatError() error {
	return errors.New("初始管理员密码必须是非空、有效 UTF-8 的单行文本，最多72字节，不能含换行或空字节；不要求至少8位")
}

func validateInitialAdminPassword(password []byte) ([]byte, error) {
	if len(password) == 0 || len(password) > initialAdminPasswordMaxBytes || !utf8.Valid(password) || strings.ContainsAny(string(password), "\r\n\x00") {
		clear(password)
		return nil, initialAdminPasswordFormatError()
	}
	return password, nil
}

// Kept for existing callers. Existing users are never reset or supplemented.
func (a *App) bootstrap() error {
	_, err := a.initializeAdmin(context.Background(), readInitialAdminPassword)
	return err
}

// A pinned connection owns both the named lock and transaction. The lock name
// uses the actual selected database, not MYSQL_DATABASE or a caller-provided DSN.
func (a *App) initializeAdmin(parent context.Context, loadPassword func() ([]byte, error)) (adminBootstrapResult, error) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return adminBootstrapUnchanged, errors.New("管理员初始化无法连接数据库；请检查数据库配置与服务状态")
	}
	defer conn.Close()
	var database string
	if err = conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil || database == "" {
		return adminBootstrapUnchanged, errors.New("管理员初始化未连接到明确的数据库；请检查 MYSQL_DATABASE")
	}
	// Lowercase conservatively serializes schemas whose names differ only in case
	// on case-insensitive MySQL installations. The name is below MySQL's 64 bytes.
	lockName := "tegu:init-admin:" + hash([]byte(strings.ToLower(database)))[:32]
	var locked sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,30)", lockName).Scan(&locked); err != nil {
		discardAdminBootstrapConnection(conn)
		return adminBootstrapUnchanged, errors.New("管理员初始化锁获取失败；未创建或重置账号，请核对数据库后重试")
	}
	if !locked.Valid || locked.Int64 != 1 {
		return adminBootstrapUnchanged, errors.New("管理员初始化锁不可用或等待超时；请稍后重试，不会重置已有账号")
	}
	defer releaseAdminBootstrapLock(conn, lockName)

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return adminBootstrapUnchanged, errors.New("无法开始管理员初始化事务；未创建或重置账号")
	}
	defer tx.Rollback()
	var users, availableAdmins int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(role='admin' AND active=1 AND deleted=0),0) FROM users").Scan(&users, &availableAdmins); err != nil {
		return adminBootstrapUnchanged, errors.New("无法检查已有账号；请先完成 tegu init-db，已有账号不会被覆盖")
	}
	if users > 0 {
		if availableAdmins == 0 {
			return adminBootstrapUnchanged, errors.New("数据库已有账号但没有可用系统管理员；为保护原账号，拒绝自动创建或重置，请核对账号恢复方案")
		}
		return adminBootstrapUnchanged, nil
	}
	password, err := loadPassword()
	if err != nil {
		return adminBootstrapUnchanged, err
	}
	defer clear(password)
	passwordHash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return adminBootstrapUnchanged, errors.New("无法生成初始管理员密码哈希；未创建账号，密码未输出")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES(?,?,?,?,?,?,?)", id(), "admin", "系统管理员", string(passwordHash), "admin", jsonText(allPermissions), jsonText([]string{}))
	if err != nil {
		return adminBootstrapUnchanged, errors.New("创建初始管理员失败；初始化事务已回滚，不会覆盖已有账号")
	}
	if err = tx.Commit(); err != nil {
		return adminBootstrapUnchanged, errors.New("管理员初始化提交未确认；请核对数据库后重试，重试不会重置已有账号")
	}
	return adminBootstrapCreated, nil
}

func discardAdminBootstrapConnection(conn *sql.Conn) {
	// A failed lock operation must not return a potentially locked connection to
	// the pool. ErrBadConn tells database/sql to close its underlying connection.
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

func releaseAdminBootstrapLock(conn *sql.Conn, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
		discardAdminBootstrapConnection(conn)
	}
}
