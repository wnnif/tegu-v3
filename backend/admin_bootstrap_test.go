package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func clearInitialAdminPasswordEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", "")
	t.Setenv("ADMIN_INITIAL_PASSWORD", "")
}

func TestInitialAdminPasswordExplicitConfiguration(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	if password, err := readInitialAdminPassword(); err == nil || password != nil {
		t.Fatal("missing configuration must not produce a default password")
	}
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"one_byte", "a", true},
		{"spaces_preserved", " a ", true},
		{"ascii_72", strings.Repeat("a", 72), true},
		{"ascii_73", strings.Repeat("a", 73), false},
		{"unicode_72_bytes", strings.Repeat("密", 24), true},
		{"unicode_over_72_bytes", strings.Repeat("密", 25), false},
		{"newline", "a\nb", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADMIN_INITIAL_PASSWORD", tc.value)
			password, err := readInitialAdminPassword()
			defer clear(password)
			if tc.valid {
				if err != nil || string(password) != tc.value {
					t.Fatal("valid explicitly configured password changed or rejected")
				}
			} else if err == nil || password != nil {
				t.Fatal("invalid password accepted")
			}
		})
	}
}

// NUL cannot be stored in an OS environment; Windows converts invalid UTF-8
// environment input to UTF-16. Exercise the byte validation and file path
// directly so these negative cases are portable rather than OS-dependent.
func TestInitialAdminPasswordRejectsInvalidBytes(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, input := range [][]byte{[]byte("a\x00b"), {0xff}} {
		if password, err := validateInitialAdminPassword(input); err == nil || password != nil {
			t.Fatal("invalid password bytes accepted")
		}
		for _, b := range input {
			if b != 0 {
				t.Fatal("invalid password buffer was not cleared")
			}
		}
	}
}

func TestInitialAdminPasswordFile(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, tc := range []struct {
		name, content, want string
		valid               bool
	}{
		{"plain", "a", "a", true},
		{"lf", "a\n", "a", true},
		{"crlf", "a\r\n", "a", true},
		{"spaces", " a \n", " a ", true},
		{"boundary_crlf", strings.Repeat("a", 72) + "\r\n", strings.Repeat("a", 72), true},
		{"unicode_lf", strings.Repeat("密", 24) + "\n", strings.Repeat("密", 24), true},
		{"empty", "", "", false},
		{"only_newline", "\n", "", false},
		{"two_newlines", "a\n\n", "", false},
		{"embedded_newline", "a\nb", "", false},
		{"null_byte", "a\x00b", "", false},
		{"invalid_utf8", string([]byte{0xff}), "", false},
		{"too_large", strings.Repeat("a", 1024), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "initial-password")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal("cannot write synthetic password fixture")
			}
			t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", path)
			password, err := readInitialAdminPassword()
			defer clear(password)
			if tc.valid {
				if err != nil || string(password) != tc.want {
					t.Fatal("password file content changed or rejected")
				}
			} else if err == nil || password != nil || strings.Contains(err.Error(), path) {
				t.Fatal("invalid password file accepted or its path leaked")
			}
		})
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing-private-file")} {
		t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", path)
		if _, err := readInitialAdminPassword(); err == nil || strings.Contains(err.Error(), path) {
			t.Fatal("non-file/missing file accepted or path leaked")
		}
	}
	t.Setenv("ADMIN_INITIAL_PASSWORD", "synthetic-inline-only")
	if _, err := readInitialAdminPassword(); err == nil || strings.Contains(err.Error(), "synthetic-inline-only") {
		t.Fatal("ambiguous password sources accepted or password leaked")
	}
}

// All SQL in these tests is an in-memory driver, with no TCP, MySQL or schema
// writes. It requires the lock, transaction and insert to share one connection.
type adminBootstrapTestState struct {
	mu                          sync.Mutex
	lock                        chan struct{}
	database, failureStage      string
	users, admins               int64
	passwordHash, userID        string
	inserts, commits, rollbacks int
	releases, closes            int
	lockNames                   []string
	lockResult                  *driver.Value
}

type adminBootstrapTestConnector struct{ state *adminBootstrapTestState }
type adminBootstrapTestDriver struct{ state *adminBootstrapTestState }
type adminBootstrapTestConn struct {
	state     *adminBootstrapTestState
	locked    bool
	inTx      bool
	pendingID string
	pendingPW string
}
type adminBootstrapTestTx struct{ conn *adminBootstrapTestConn }
type adminBootstrapTestRows struct {
	values []driver.Value
	done   bool
}

func (c adminBootstrapTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &adminBootstrapTestConn{state: c.state}, nil
}
func (c adminBootstrapTestConnector) Driver() driver.Driver {
	return adminBootstrapTestDriver{state: c.state}
}
func (d adminBootstrapTestDriver) Open(string) (driver.Conn, error) {
	return &adminBootstrapTestConn{state: d.state}, nil
}
func (c *adminBootstrapTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare in memory-only bootstrap test")
}
func (c *adminBootstrapTestConn) Close() error {
	if c.locked {
		c.locked = false
		c.state.lock <- struct{}{}
	}
	c.state.mu.Lock()
	c.state.closes++
	c.state.mu.Unlock()
	return nil
}
func (c *adminBootstrapTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected context-aware transaction")
}
func (c *adminBootstrapTestConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !c.locked || opts.Isolation != driver.IsolationLevel(sql.LevelReadCommitted) || c.state.failureStage == "begin" {
		return nil, errors.New("synthetic-private-driver-error")
	}
	c.inTx = true
	return &adminBootstrapTestTx{conn: c}, nil
}
func (c *adminBootstrapTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	row := func(values ...driver.Value) (driver.Rows, error) {
		return &adminBootstrapTestRows{values: values}, nil
	}
	switch query {
	case "SELECT DATABASE()":
		if c.state.failureStage == "database" {
			return nil, errors.New("synthetic-private-driver-error")
		}
		return row(c.state.database)
	case "SELECT GET_LOCK(?,30)":
		if c.state.failureStage == "lock" {
			return nil, errors.New("synthetic-private-driver-error")
		}
		if c.state.lockResult != nil {
			return row(*c.state.lockResult)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.state.lock:
			c.locked = true
		}
		c.state.mu.Lock()
		c.state.lockNames = append(c.state.lockNames, args[0].Value.(string))
		c.state.mu.Unlock()
		return row(int64(1))
	case "SELECT COUNT(*),COALESCE(SUM(role='admin' AND active=1 AND deleted=0),0) FROM users":
		if !c.locked || !c.inTx || c.state.failureStage == "count" {
			return nil, errors.New("synthetic-private-driver-error")
		}
		c.state.mu.Lock()
		defer c.state.mu.Unlock()
		return row(c.state.users, c.state.admins)
	case "SELECT RELEASE_LOCK(?)":
		if !c.locked || c.state.failureStage == "release" {
			return nil, errors.New("synthetic-private-driver-error")
		}
		c.locked = false
		c.state.lock <- struct{}{}
		c.state.mu.Lock()
		c.state.releases++
		c.state.mu.Unlock()
		return row(int64(1))
	default:
		return nil, errors.New("unexpected SQL in memory-only test")
	}
}
func (c *adminBootstrapTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if query != "INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES(?,?,?,?,?,?,?)" || !c.locked || !c.inTx || c.state.failureStage == "insert" {
		return nil, errors.New("synthetic-private-driver-error")
	}
	if len(args) != 7 || args[1].Value != "admin" || args[2].Value != "系统管理员" || args[4].Value != "admin" || args[5].Value != jsonText(allPermissions) || args[6].Value != jsonText([]string{}) {
		return nil, errors.New("wrong initial account fields")
	}
	c.pendingID = args[0].Value.(string)
	c.pendingPW = args[3].Value.(string)
	c.state.mu.Lock()
	c.state.inserts++
	c.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}
func (tx *adminBootstrapTestTx) Commit() error {
	c := tx.conn
	c.inTx = false
	if c.state.failureStage == "commit" {
		c.pendingID, c.pendingPW = "", ""
		return errors.New("synthetic-private-driver-error")
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.commits++
	if c.pendingID != "" {
		c.state.users, c.state.admins = 1, 1
		c.state.userID, c.state.passwordHash = c.pendingID, c.pendingPW
	}
	c.pendingID, c.pendingPW = "", ""
	return nil
}
func (tx *adminBootstrapTestTx) Rollback() error {
	c := tx.conn
	c.inTx = false
	c.pendingID, c.pendingPW = "", ""
	c.state.mu.Lock()
	c.state.rollbacks++
	c.state.mu.Unlock()
	return nil
}
func (r *adminBootstrapTestRows) Columns() []string {
	columns := make([]string, len(r.values))
	for i := range columns {
		columns[i] = "synthetic-column"
	}
	return columns
}
func (r *adminBootstrapTestRows) Close() error { return nil }
func (r *adminBootstrapTestRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.values)
	return nil
}

func adminBootstrapMemoryDB(t *testing.T, users, admins int64) (*App, *adminBootstrapTestState) {
	t.Helper()
	state := &adminBootstrapTestState{lock: make(chan struct{}, 1), database: "actual_schema_" + strings.Repeat("x", 50), users: users, admins: admins}
	state.lock <- struct{}{}
	db := sql.OpenDB(adminBootstrapTestConnector{state: state})
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { db.Close() })
	return &App{db: db}, state
}

func TestAdminBootstrapEmptyRequiresExplicitPassword(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	a, state := adminBootstrapMemoryDB(t, 0, 0)
	if err := a.bootstrap(); err == nil || !strings.Contains(err.Error(), "ADMIN_INITIAL_PASSWORD_FILE") {
		t.Fatal("empty database without explicit password must fail clearly")
	}
	if state.users != 0 || state.inserts != 0 || state.commits != 0 || state.rollbacks != 1 || state.releases != 1 {
		t.Fatal("missing password changed users or failed to roll back/release lock")
	}
}

func TestAdminBootstrapExistingAccountsNeverReset(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	t.Setenv("ADMIN_INITIAL_PASSWORD_FILE", "not-a-readable-password-file")
	t.Setenv("ADMIN_INITIAL_PASSWORD", strings.Repeat("x", 73))
	for _, tc := range []struct {
		name          string
		users, admins int64
		wantError     bool
	}{
		{"existing_admin", 2, 1, false},
		{"only_employees", 1, 0, true},
		{"inactive_or_deleted_admin", 3, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, state := adminBootstrapMemoryDB(t, tc.users, tc.admins)
			result, err := a.initializeAdmin(context.Background(), func() ([]byte, error) {
				t.Fatal("existing accounts must not read initial password configuration")
				return nil, nil
			})
			if (err != nil) != tc.wantError || result != adminBootstrapUnchanged || state.users != tc.users || state.inserts != 0 || state.commits != 0 || state.releases != 1 {
				t.Fatal("existing accounts changed or initialization did not fail closed")
			}
		})
	}
	a, _ := adminBootstrapMemoryDB(t, 1, 1)
	if err := a.bootstrap(); err != nil {
		t.Fatal("existing usable admin must start despite missing/invalid bootstrap secrets")
	}
}

func TestAdminBootstrapCreatesOnceAndPreservesPassword(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	a, state := adminBootstrapMemoryDB(t, 0, 0)
	loads := 0
	load := func() ([]byte, error) { loads++; return []byte("a"), nil }
	result, err := a.initializeAdmin(context.Background(), load)
	if err != nil || result != adminBootstrapCreated || state.users != 1 || state.admins != 1 || state.commits != 1 || state.inserts != 1 || bcrypt.CompareHashAndPassword([]byte(state.passwordHash), []byte("a")) != nil {
		t.Fatal("single-character explicit password did not create exactly one valid administrator")
	}
	initialID, initialHash := state.userID, state.passwordHash
	result, err = a.initializeAdmin(context.Background(), load)
	if err != nil || result != adminBootstrapUnchanged || loads != 1 || state.inserts != 1 || state.userID != initialID || state.passwordHash != initialHash {
		t.Fatal("repeat initialization read a new password or reset an existing account")
	}
	if state.releases != 2 || len(state.lockNames) != 2 || state.lockNames[0] != state.lockNames[1] || len(state.lockNames[0]) > 64 || strings.Contains(state.lockNames[0], state.database) {
		t.Fatal("database-scoped lock was not stable, bounded or released")
	}
}

func TestAdminBootstrapConcurrentInitializationMemoryOnly(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	a, state := adminBootstrapMemoryDB(t, 0, 0)
	var loads, created atomic.Int32
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := a.initializeAdmin(context.Background(), func() ([]byte, error) {
				loads.Add(1)
				return []byte("a"), nil
			})
			if result == adminBootstrapCreated {
				created.Add(1)
			}
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal("in-memory concurrent initialization failed")
		}
	}
	if created.Load() != 1 || loads.Load() != 1 || state.users != 1 || state.inserts != 1 || state.commits != 1 || state.releases != 8 {
		t.Fatal("concurrent initialization did not serialize to a single account/password")
	}
}

func TestAdminBootstrapDatabaseFailuresAreSafe(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, stage := range []string{"database", "lock", "begin", "count", "insert", "commit"} {
		t.Run(stage, func(t *testing.T) {
			a, state := adminBootstrapMemoryDB(t, 0, 0)
			state.failureStage = stage
			result, err := a.initializeAdmin(context.Background(), func() ([]byte, error) { return []byte("a"), nil })
			if err == nil || result != adminBootstrapUnchanged || strings.Contains(err.Error(), "synthetic-private-driver-error") || state.users != 0 || state.passwordHash != "" {
				t.Fatal("database failure leaked raw error or changed stored users")
			}
			if stage == "lock" && state.closes != 1 {
				t.Fatal("failed lock connection must be discarded, not returned to pool")
			}
			if stage == "insert" && state.rollbacks != 1 {
				t.Fatal("failed insert must roll back its transaction")
			}
		})
	}
	for _, value := range []driver.Value{nil, int64(0)} {
		a, state := adminBootstrapMemoryDB(t, 0, 0)
		state.lockResult = &value
		if _, err := a.initializeAdmin(context.Background(), func() ([]byte, error) {
			t.Fatal("password must not be loaded without exclusive lock")
			return nil, nil
		}); err == nil || state.inserts != 0 {
			t.Fatal("unavailable/null lock was accepted")
		}
	}
}

func TestAdminBootstrapFailedReleaseDiscardsConnection(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	a, state := adminBootstrapMemoryDB(t, 0, 0)
	state.failureStage = "release"
	result, err := a.initializeAdmin(context.Background(), func() ([]byte, error) { return []byte("a"), nil })
	if err != nil || result != adminBootstrapCreated || state.closes != 1 || len(state.lock) != 1 {
		t.Fatal("failed release left a named lock on a pooled connection")
	}
}
