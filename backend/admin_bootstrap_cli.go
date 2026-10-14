package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type teguCommand struct {
	name string
	path string
}

type teguCommandActions struct {
	initDB         func() error
	initAdmin      func() (adminBootstrapResult, error)
	serve          func() error
	restore        func(string) error
	importCrafts   func(string) error
	importProducts func(string) error
	importSeed     func(string) error
}

func parseTeguCommand(args []string) (teguCommand, error) {
	if len(args) == 0 {
		return teguCommand{name: "serve"}, nil
	}
	command := teguCommand{name: args[0]}
	switch command.name {
	case "serve", "init-db", "init-admin", "version", "--version", "help", "--help", "-h":
		if len(args) != 1 {
			return teguCommand{}, errors.New("此命令不接受额外参数；请运行 tegu help 查看用法")
		}
	case "restore", "import-crafts", "import-products", "import-catalog-seed":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return teguCommand{}, errors.New("此命令必须指定且仅指定一个文件；请运行 tegu help 查看用法")
		}
		command.path = args[1]
	default:
		// Do not echo unknown arguments: a mistyped argument may be a secret.
		return teguCommand{}, errors.New("未知命令；未连接数据库或启动服务，请运行 tegu help 查看用法")
	}
	return command, nil
}

// Parse and handle read-only commands before even constructing a DB connection.
// The factory makes the command side-effect boundary independently testable.
func runTegu(args []string, out io.Writer, openActions func() (teguCommandActions, func(), error)) error {
	command, err := parseTeguCommand(args)
	if err != nil {
		return err
	}
	switch command.name {
	case "version", "--version":
		_, err = fmt.Fprintf(out, "tegu %s\nBuildCommit: %s\n", Version, BuildCommit)
		return err
	case "help", "--help", "-h":
		_, err = fmt.Fprintln(out, "用法：tegu [serve | init-db | init-admin | version | help | restore <文件> | import-crafts <文件> | import-products <文件> | import-catalog-seed <文件>]\ninit-db 仅执行现有版本化迁移，不创建管理员。\ninit-admin 仅在空用户库创建 admin；请先配置 ADMIN_INITIAL_PASSWORD_FILE（推荐）或 ADMIN_INITIAL_PASSWORD。\nimport-crafts、import-products 和 import-catalog-seed 仅导入基础目录，不创建管理员。完整 seed 导入仅接受空业务库和随包校验的来源清单；同一摘要重复执行无副作用，不同摘要或非空目标拒绝覆盖。\n无命令或 serve 启动服务；已有账号和密码不会被初始化覆盖。")
		return err
	}
	actions, closeDB, err := openActions()
	if err != nil {
		return err
	}
	defer closeDB()
	if command.name == "init-admin" {
		result, err := actions.initAdmin()
		if err != nil {
			return err
		}
		if result == adminBootstrapCreated {
			_, err = fmt.Fprintln(out, "初始系统管理员 admin 已创建；密码未输出。")
		} else {
			_, err = fmt.Fprintln(out, "已有可用系统管理员；未新增账号，也未更改任何密码。")
		}
		return err
	}
	if err = actions.initDB(); err != nil {
		return err
	}
	switch command.name {
	case "init-db":
		_, err = fmt.Fprintln(out, "数据库结构初始化/迁移完成；未创建或重置管理员。")
		return err
	case "restore":
		if err = actions.restore(command.path); err != nil {
			return safeStartupError("数据库恢复失败", err)
		}
		_, err = fmt.Fprintln(out, "恢复成功")
		return err
	case "import-products":
		if err = actions.importProducts(command.path); err != nil {
			return safeStartupError("商品目录导入失败", err)
		}
		_, err = fmt.Fprintln(out, "商品目录导入完成")
		return err
	case "import-crafts":
		if err = actions.importCrafts(command.path); err != nil {
			return safeStartupError("工艺导入失败", err)
		}
		_, err = fmt.Fprintln(out, "工艺导入完成")
		return err
	case "import-catalog-seed":
		if actions.importSeed == nil {
			return errors.New("基础资料导入命令不可用；未写入数据库")
		}
		if err = actions.importSeed(command.path); err != nil {
			return safeStartupError("基础资料导入失败", err)
		}
		_, err = fmt.Fprintln(out, "来源清单基础资料导入完成")
		return err
	case "serve":
		if _, err = actions.initAdmin(); err != nil {
			return err
		}
		return actions.serve()
	}
	return errors.New("命令处理失败；未启动服务")
}

// Never print a driver error or DSN: their text can contain connection secrets.
func safeStartupError(operation string, err error) error {
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) {
		return fmt.Errorf("%s（MySQL错误码%d）；请检查配置、权限与服务状态，连接信息未输出", operation, mysqlError.Number)
	}
	return fmt.Errorf("%s；请检查配置、权限与服务状态，连接信息未输出", operation)
}

func openTeguCommandActions() (teguCommandActions, func(), error) {
	cfg := mysql.Config{User: env("MYSQL_USER", "tegu"), Passwd: os.Getenv("MYSQL_PASSWORD"), Net: "tcp", Addr: env("MYSQL_HOST", "127.0.0.1") + ":" + env("MYSQL_PORT", "3306"), DBName: env("MYSQL_DATABASE", "tegu_v3"), AllowNativePasswords: true, Timeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, Params: map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"}}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return teguCommandActions{}, nil, safeStartupError("数据库连接配置无效", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return teguCommandActions{}, nil, safeStartupError("数据库连接失败", err)
	}
	a := &App{db: db, attempts: map[string][]time.Time{}}
	return teguCommandActions{
		initDB: func() error {
			if err := initSchema(db); err != nil {
				return safeStartupError("数据库结构初始化/迁移失败", err)
			}
			return nil
		},
		initAdmin: func() (adminBootstrapResult, error) {
			return a.initializeAdmin(context.Background(), readInitialAdminPassword)
		},
		serve: a.serve, restore: a.restore, importCrafts: a.importCrafts, importProducts: a.importProducts, importSeed: a.importCatalogSeed,
	}, func() { db.Close() }, nil
}
