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
	name          string
	path          string
	oldSeedPath   string
	newSeedPath   string
	backfillApply bool
	confirm       string
}

type catalogSeedBackfillSummary struct {
	Added                   int
	Updated                 int
	Skipped                 int
	Conflicts               int
	PlaceholdersDeactivated int
	ConfirmationToken       string
	ConflictReasons         []string `json:"-"`
}

type teguCommandActions struct {
	initDB            func() error
	initAdmin         func() (adminBootstrapResult, error)
	serve             func() error
	restore           func(string) error
	importCrafts      func(string) error
	importProducts    func(string) error
	importSeed        func(string) error
	backfillSeedPlan  func(string, string) (catalogSeedBackfillSummary, error)
	backfillSeedApply func(string, string, string) (catalogSeedBackfillSummary, error)
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
	case "backfill-catalog-seed":
		if len(args) < 3 || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" {
			return teguCommand{}, errors.New("此命令必须指定旧版和新版 seed 文件；请运行 tegu help 查看用法")
		}
		command.oldSeedPath, command.newSeedPath = args[1], args[2]
		modeSeen, confirmSeen := false, false
		for i := 3; i < len(args); i++ {
			switch args[i] {
			case "--dry-run":
				if modeSeen {
					return teguCommand{}, errors.New("dry-run 与 apply 只能指定一种模式；请运行 tegu help 查看用法")
				}
				modeSeen = true
			case "--apply":
				if modeSeen {
					return teguCommand{}, errors.New("dry-run 与 apply 只能指定一种模式；请运行 tegu help 查看用法")
				}
				modeSeen = true
				command.backfillApply = true
			case "--confirm":
				if confirmSeen || i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
					return teguCommand{}, errors.New("--confirm 必须指定一个确认码；请运行 tegu help 查看用法")
				}
				confirmSeen = true
				command.confirm = args[i+1]
				i++
			default:
				return teguCommand{}, errors.New("增量回填参数无效；请运行 tegu help 查看用法")
			}
		}
		if command.backfillApply != confirmSeen {
			return teguCommand{}, errors.New("apply 必须同时提供 --confirm 确认码；请运行 tegu help 查看用法")
		}
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
		_, err = fmt.Fprintln(out, "用法：tegu [serve | init-db | init-admin | version | help | restore <文件> | import-crafts <文件> | import-products <文件> | import-catalog-seed <文件> | backfill-catalog-seed <旧seed文件> <新seed文件> [--dry-run | --apply --confirm <确认码>]]\ninit-db 仅执行现有版本化迁移，不创建管理员。\ninit-admin 仅在空用户库创建 admin；请先配置 ADMIN_INITIAL_PASSWORD_FILE（推荐）或 ADMIN_INITIAL_PASSWORD。\nimport-crafts、import-products 和 import-catalog-seed 仅导入基础目录，不创建管理员。完整 seed 导入仅接受空业务库和随包校验的来源清单；同一摘要重复执行无副作用，不同摘要或非空目标拒绝覆盖。\nbackfill-catalog-seed 需要旧版和当前 seed 文件，默认只读 dry-run；需将 dry-run 返回的确认码与 --apply 一起提供才会执行；该命令不会自动运行 init-db 或迁移。\n无命令或 serve 启动服务；已有账号和密码不会被初始化覆盖。")
		return err
	}
	actions, closeDB, err := openActions()
	if err != nil {
		return err
	}
	defer closeDB()
	if command.name == "backfill-catalog-seed" {
		if command.backfillApply {
			if actions.backfillSeedApply == nil {
				return errors.New("增量报价回填命令不可用；未写入数据库")
			}
			result, applyErr := actions.backfillSeedApply(command.oldSeedPath, command.newSeedPath, command.confirm)
			if applyErr != nil {
				return safeCatalogBackfillError("增量报价回填", applyErr)
			}
			if result.Conflicts > 0 {
				return fmt.Errorf("增量报价回填检测到 %d 项冲突；执行已拒绝", result.Conflicts)
			}
			return writeCatalogBackfillSummary(out, "增量报价回填完成", result, false)
		}
		if actions.backfillSeedPlan == nil {
			return errors.New("增量报价回填 dry-run 命令不可用；未写入数据库")
		}
		plan, planErr := actions.backfillSeedPlan(command.oldSeedPath, command.newSeedPath)
		if planErr != nil {
			return safeCatalogBackfillError("增量报价回填 dry-run", planErr)
		}
		return writeCatalogBackfillSummary(out, "增量报价回填计划（只读 dry-run）", plan, plan.Conflicts == 0)
	}
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

func writeCatalogBackfillSummary(out io.Writer, heading string, summary catalogSeedBackfillSummary, showToken bool) error {
	if _, err := fmt.Fprintf(out, "%s\n新增：%d\n更新：%d\n跳过：%d\n冲突：%d\n1元测试占位拟软停用：%d\n", heading, summary.Added, summary.Updated, summary.Skipped, summary.Conflicts, summary.PlaceholdersDeactivated); err != nil {
		return err
	}
	for _, reason := range summary.ConflictReasons {
		if _, err := fmt.Fprintf(out, "冲突原因：%s\n", reason); err != nil {
			return err
		}
	}
	if showToken && summary.Conflicts == 0 {
		if strings.TrimSpace(summary.ConfirmationToken) == "" {
			return errors.New("dry-run 未生成确认码；未写入数据库")
		}
		_, err := fmt.Fprintf(out, "确认码：%s\n", summary.ConfirmationToken)
		return err
	}
	if !showToken && summary.Conflicts > 0 {
		_, err := fmt.Fprintln(out, "检测到冲突，计划不可执行；请先核对冲突并重新 dry-run。")
		return err
	}
	return nil
}

func safeCatalogBackfillError(operation string, err error) error {
	var appFailure failure
	if errors.As(err, &appFailure) {
		return fmt.Errorf("%s失败：%s", operation, appFailure.Message)
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) {
		return fmt.Errorf("%s失败（MySQL错误码%d）；连接信息未输出", operation, mysqlError.Number)
	}
	return fmt.Errorf("%s失败；请检查 seed 文件、数据库权限与状态，连接信息未输出", operation)
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
		backfillSeedPlan: func(oldFile, newFile string) (catalogSeedBackfillSummary, error) {
			return a.planCatalogSeedBackfill(context.Background(), oldFile, newFile)
		},
		backfillSeedApply: func(oldFile, newFile, confirmationToken string) (catalogSeedBackfillSummary, error) {
			return a.applyCatalogSeedBackfill(context.Background(), oldFile, newFile, confirmationToken)
		},
	}, func() { db.Close() }, nil
}
