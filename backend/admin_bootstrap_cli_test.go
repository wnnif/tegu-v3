package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestTeguReadOnlyCommandsNeverOpenDatabase(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, args := range [][]string{{"version"}, {"--version"}, {"help"}, {"--help"}, {"-h"}} {
		var out bytes.Buffer
		if err := runTegu(args, &out, func() (teguCommandActions, func(), error) {
			t.Fatal("version/help must not open a database or load any secrets")
			return teguCommandActions{}, nil, nil
		}); err != nil || out.Len() == 0 {
			t.Fatal("read-only command failed to produce output")
		}
	}
}

func TestTeguInvalidCommandsFailClosed(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, args := range [][]string{
		{"synthetic-private-argument"}, {"init-db", "synthetic-private-argument"},
		{"init-admin", "extra"}, {"version", "extra"}, {"serve", "extra"},
		{"restore"}, {"import-crafts"}, {"import-products"}, {"restore", " "},
		{"import-crafts", "file", "extra"}, {"import-products", "file", "extra"}, {"import-products", " "},
	} {
		var out bytes.Buffer
		err := runTegu(args, &out, func() (teguCommandActions, func(), error) {
			t.Fatal("invalid commands must fail before database/HTTP/scheduler side effects")
			return teguCommandActions{}, nil, nil
		})
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "synthetic-private-argument") {
			t.Fatal("invalid command accepted or untrusted arguments echoed")
		}
	}
}

func TestTeguCommandSideEffectBoundaries(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"init_db", []string{"init-db"}, []string{"open", "migrate", "close"}},
		{"init_admin", []string{"init-admin"}, []string{"open", "admin", "close"}},
		{"default_start", nil, []string{"open", "migrate", "admin", "serve", "close"}},
		{"explicit_start", []string{"serve"}, []string{"open", "migrate", "admin", "serve", "close"}},
		{"restore", []string{"restore", "synthetic-file"}, []string{"open", "migrate", "restore:synthetic-file", "close"}},
		{"import", []string{"import-crafts", "synthetic-file"}, []string{"open", "migrate", "import:synthetic-file", "close"}},
		{"import_products", []string{"import-products", "synthetic-file"}, []string{"open", "migrate", "products:synthetic-file", "close"}},
		{"import_full_seed", []string{"import-catalog-seed", "synthetic-file"}, []string{"open", "migrate", "seed:synthetic-file", "close"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			var out bytes.Buffer
			factory := func() (teguCommandActions, func(), error) {
				calls = append(calls, "open")
				return teguCommandActions{
					initDB: func() error { calls = append(calls, "migrate"); return nil },
					initAdmin: func() (adminBootstrapResult, error) {
						calls = append(calls, "admin")
						return adminBootstrapCreated, nil
					},
					serve:        func() error { calls = append(calls, "serve"); return nil },
					restore:      func(path string) error { calls = append(calls, "restore:"+path); return nil },
					importCrafts: func(path string) error { calls = append(calls, "import:"+path); return nil },
					importProducts: func(path string) error { calls = append(calls, "products:"+path); return nil },
					importSeed: func(path string) error { calls = append(calls, "seed:"+path); return nil },
				}, func() { calls = append(calls, "close") }, nil
			}
			if err := runTegu(tc.args, &out, factory); err != nil || !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("incorrect command boundary: calls=%v want=%v", calls, tc.want)
			}
		})
	}
}

func TestTeguFailedInitializationNeverServes(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	for _, stage := range []string{"open", "migrate", "admin"} {
		t.Run(stage, func(t *testing.T) {
			var out bytes.Buffer
			served, closed, adminCalled := false, false, false
			factory := func() (teguCommandActions, func(), error) {
				if stage == "open" {
					return teguCommandActions{}, nil, errors.New("safe open failure")
				}
				return teguCommandActions{
					initDB: func() error {
						if stage == "migrate" {
							return errors.New("safe migration failure")
						}
						return nil
					},
					initAdmin: func() (adminBootstrapResult, error) {
						adminCalled = true
						return adminBootstrapUnchanged, errors.New("explicit password missing")
					},
					serve: func() error { served = true; return nil },
				}, func() { closed = true }, nil
			}
			if err := runTegu(nil, &out, factory); err == nil || served || out.Len() != 0 || closed != (stage != "open") || adminCalled != (stage == "admin") {
				t.Fatal("failed initialization started HTTP/scheduler or skipped cleanup")
			}
		})
	}
}

func TestTeguInitAdminRepeatOutputDoesNotPromiseReset(t *testing.T) {
	clearInitialAdminPasswordEnv(t)
	var out bytes.Buffer
	err := runTegu([]string{"init-admin"}, &out, func() (teguCommandActions, func(), error) {
		return teguCommandActions{initAdmin: func() (adminBootstrapResult, error) { return adminBootstrapUnchanged, nil }}, func() {}, nil
	})
	if err != nil || !strings.Contains(out.String(), "未更改任何密码") || strings.Contains(out.String(), "已创建") {
		t.Fatal("repeat initialization output falsely reports a new/reset administrator")
	}
}

func TestStartupErrorsNeverContainRawDriverText(t *testing.T) {
	const sensitive = "synthetic-private-dsn-with-password"
	for _, raw := range []error{errors.New(sensitive), &mysql.MySQLError{Number: 1045, Message: sensitive}} {
		message := safeStartupError("数据库连接失败", raw).Error()
		if strings.Contains(message, sensitive) || !strings.Contains(message, "数据库连接失败") {
			t.Fatal("startup error exposed a raw password/DSN")
		}
	}
}
