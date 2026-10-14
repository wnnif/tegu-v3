package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestV3BackupTableCoverage(t *testing.T) {
	for _, table := range []string{"users", "projects", "catalog_seed_imports", "catalog_seed_provenance", "catalog_seed_packages", "catalog_seed_package_coverage", "project_packages", "payroll_bonus_worker_locks", "orders", "order_lines", "order_materials", "order_sales", "order_line_wage_adjustments", "order_worker_wages", "ticket_batches", "ticket_batch_items", "ticket_batch_versions", "company_receipts", "company_receipt_versions", "project_ticket_receipts", "customer_discounts", "customer_discount_versions", "stock_balances", "stock_movements", "product_purchase_costs", "payroll_adjustments", "payroll_deductions", "payroll_advances", "payroll_settlements", "payroll_settlement_sources", "payroll_payments", "payroll_history", "payroll_bonus_entries", "payroll_bonus_settlements", "payroll_bonus_payments", "expense_claims", "expense_advances", "expense_advance_allocations", "expense_payments", "expense_advance_returns", "expense_history", "attachment_files", "archive_runs", "settings"} {
		if !contains(backupTables, table) {
			t.Fatal("V3 business/history missing from backup whitelist", table)
		}
	}
}

func snapshotV3Rows(t *testing.T, file string) map[string][]string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.UseNumber()
	rows := map[string][]string{}
	for {
		var record snapshotRecord
		if err = decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if record.Table != "" {
			body, err := json.Marshal(record.Row)
			if err != nil {
				t.Fatal(err)
			}
			rows[record.Table] = append(rows[record.Table], string(body))
		}
	}
	for table := range rows {
		sort.Strings(rows[table])
	}
	return rows
}

func databaseV3Rows(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	if !contains(backupTables, table) {
		t.Fatal("unsafe table comparison")
	}
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for rows.Next() {
		values, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		row := map[string]any{}
		for i, column := range columns {
			if value, ok := values[i].([]byte); ok {
				row[column] = string(value)
			} else {
				row[column] = values[i]
			}
		}
		body, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, string(body))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(result)
	return result
}

func TestV3BackupRestoreCriticalRecordsMySQL(t *testing.T) {
	a, cfg := maintenanceFaultDB(t)
	name := os.Getenv("GO_MYSQL_V3_RESTORE_TARGET")
	if !regexp.MustCompile(`^tegu_restore_[a-z0-9_]{1,40}$`).MatchString(name) || name == cfg.DBName {
		t.Fatal("V3 restore requires one explicitly authorized tegu_restore_ target")
	}
	// No wildcard or automatic restore target. The runner has permission only
	// for this one new test database, verified by the task's environment controller.
	if _, err := a.db.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal("authorized fresh restore target must not already exist", err)
	}
	t.Cleanup(func() {
		if _, err := a.db.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error("exact restore test target cleanup failed", err)
		}
	})
	seedV3ArchiveFacts(t, a)
	for _, query := range []string{
		`INSERT INTO users(id,username,name,password_hash,role,permissions,crafts) VALUES('v3-account','v3-fixture-user','合成权限账号','synthetic-not-a-login-secret','employee',JSON_ARRAY('wages','expenses'),JSON_ARRAY('v3-craft'))`,
		`INSERT INTO workers(id,name) VALUES('v3-worker','合成工人')`,
		`INSERT INTO warehouses(id,name) VALUES('v3-warehouse','合成总仓')`,
		`INSERT INTO products(id,name,unit) VALUES('v3-product','合成商品','桶')`,
		`INSERT INTO stock_balances(warehouse_id,product_id,qty_micro) VALUES('v3-warehouse','v3-product',2000000)`,
		`INSERT INTO product_purchase_costs(product_id,purchased_quantum,purchase_cents) VALUES('v3-product',2000000,20000)`,
		`INSERT INTO stock_movements(operation_id,warehouse_id,product_id,delta_micro,delta_quantum,source_type,source_id,actor_id) VALUES('v3-stock-op','v3-warehouse','v3-product',2000000,2000000,'进货','v3-archive-purchase','fixture')`,
		`INSERT INTO order_materials(order_id,line_id,product_id,warehouse_id,qty_micro) VALUES('v3-archive-order','v3-archive-line','v3-product','v3-warehouse',1000000)`,
		`INSERT INTO order_sales(id,order_id,product_id,warehouse_id,qty_micro,sale_qty_micro,price_cents,total_cents,product_name,unit,price_unit,input_unit,input_qty,sort_order) VALUES('v3-sale','v3-archive-order','v3-product','v3-warehouse',1000000,1000000,1000,1000,'合成商品','桶','桶','base','1',1)`,
		`UPDATE orders SET total_cents=101000 WHERE id='v3-archive-order'`,
		`INSERT INTO order_line_wage_adjustments(id,order_id,line_id,before_cents,after_cents,standard_cents,worker_id,worker_name,reason,actor_id,actor_name,operation_id) VALUES('v3-line-wage-history','v3-archive-order','v3-archive-line',99000,100000,99000,'v3-worker','合成工人','合成工资调整历史','fixture','合成操作人','v3-line-wage-history')`,
		`INSERT INTO catalog_seed_imports(seed_id,manifest_digest,sources,entry_counts) VALUES('v3-fixture-seed',REPEAT('0',64),JSON_ARRAY(),JSON_OBJECT('packages',1))`,
		`INSERT INTO catalog_seed_provenance(seed_id,target_kind,target_id,component,classification,source_id,source_file,source_sheet,source_row,test_only,detail) VALUES('v3-fixture-seed','company','v3-archive-co','identity','confirmed','fixture','fixture.json','公司',1,0,JSON_OBJECT('name','合成V3公司'))`,
		`INSERT INTO catalog_seed_packages(seed_id,package_id,company_id,name,room_count,price_cents,classification,source_id,source_sheet,source_row,note) VALUES('v3-fixture-seed','v3-fixture-package','v3-archive-co','合成房型套餐',3,300000,'package','fixture','套餐',1,'合成备份测试')`,
		`INSERT INTO catalog_seed_package_coverage(seed_id,package_id,item_name,craft_id,subitem_id,mapping,source_id,source_sheet,source_row,note) VALUES('v3-fixture-seed','v3-fixture-package','墙地固·墙','craft-fixture','wall','mapped','fixture','套餐覆盖',2,'合成备份测试')`,
		`INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,total_cents) VALUES('v3-ticket','v3-archive-site','v3-archive-co','合成V3公司','合成工地','合成地址','V3-TICKET','normal',101000)`,
		`INSERT INTO project_packages(project_id,seed_id,package_id,company_id,name,room_count,price_cents,source_id,source_sheet,source_row,coverage,charged_ticket_id,created_by) VALUES('v3-archive-site','v3-fixture-seed','v3-fixture-package','v3-archive-co','合成房型套餐',3,300000,'fixture','套餐',1,JSON_ARRAY(JSON_OBJECT('item_name','墙地固·墙','craft_id','craft-fixture','source_craft_id','source-fixture','subitem_id','wall','mapping','mapped')),'v3-ticket','fixture')`,
		`INSERT INTO project_ticket_items(item_id,ticket_id,order_id,item_kind) VALUES('v3-archive-line','v3-ticket','v3-archive-order','line'),('v3-sale','v3-ticket','v3-archive-order','sale')`,
		`INSERT INTO project_ticket_versions(ticket_id,version,event,snapshot,created_by) VALUES('v3-ticket',1,'合成票据历史',JSON_OBJECT('total_cents',101000),'fixture')`,
		`INSERT INTO ticket_batches(id,company_id,company_name,number,delivered_at,recipient,total_cents,created_by) VALUES('v3-batch','v3-archive-co','合成V3公司','V3-BATCH','2026-10-01','合成收票人',101000,'fixture')`,
		`INSERT INTO ticket_batch_items(batch_id,item_kind,item_id,order_id,project_id,source_project_ticket_id,total_cents,quantity,snapshot) VALUES('v3-batch','line','v3-archive-line','v3-archive-order','v3-archive-site','v3-ticket',100000,1,JSON_OBJECT('subitem_name','合成部位'))`,
		`INSERT INTO ticket_batch_versions(batch_id,version,event,snapshot,actor_id) VALUES('v3-batch',1,'合成交票历史',JSON_OBJECT('total_cents',101000),'fixture')`,
		`UPDATE order_lines SET ticket_batch_id='v3-batch' WHERE id='v3-archive-line'`,
		`INSERT INTO company_receipts(id,company_id,company_name,amount_cents,received_at,created_by) VALUES('v3-cash','v3-archive-co','合成V3公司',40000,'2026-10-03','fixture')`,
		`INSERT INTO company_receipt_versions(receipt_id,version,event,snapshot,actor_id) VALUES('v3-cash',1,'合成到账历史',JSON_OBJECT('amount_cents',40000),'fixture')`,
		`INSERT INTO project_ticket_receipts(id,ticket_id,receipt_id,batch_id,amount_cents,received_at,created_by) VALUES('v3-allocation','v3-ticket','v3-cash','v3-batch',30000,'2026-10-03','fixture')`,
		`INSERT INTO customer_discounts(id,company_id,ticket_id,batch_id,amount_cents,reason,created_by) VALUES('v3-discount','v3-archive-co','v3-ticket','v3-batch',5000,'合成减免','fixture')`,
		`INSERT INTO customer_discount_versions(discount_id,version,event,snapshot,actor_id) VALUES('v3-discount',1,'合成减免历史',JSON_OBJECT('amount_cents',5000),'fixture')`,
		`INSERT INTO payroll_history(id,entity_type,entity_id,event,after_snapshot,actor_id,operation_id) VALUES('v3-payroll-history','settlement','v3-archive-settle','合成工资历史',JSON_OBJECT('paid_cents',50000),'fixture','v3-payroll-history')`,
		`INSERT INTO payroll_bonus_entries(id,source_key,entry_kind,worker_id,worker_name,work_date,bonus_year,amount_cents,reason,actor_id,actor_name,operation_id) VALUES('v3-bonus-entry','fixture-manual-bonus','manual','v3-worker','合成工人','2026-10-01',2026,10000,'合成奖金登记','fixture','合成操作人','v3-bonus-entry')`,
		`INSERT INTO payroll_bonus_settlements(id,worker_id,worker_name,bonus_year,settlement_date,amount_cents,status,actor_id,actor_name,operation_id) VALUES('v3-bonus-settle','v3-worker','合成工人',2026,'2026-10-05',10000,'confirmed','fixture','合成操作人','v3-bonus-settle')`,
		`INSERT INTO payroll_bonus_payments(id,settlement_id,amount_cents,paid_date,status,actor_id,actor_name,operation_id) VALUES('v3-bonus-pay','v3-bonus-settle',4000,'2026-10-06','active','fixture','合成操作人','v3-bonus-pay')`,
		`INSERT INTO payroll_history(id,entity_type,entity_id,event,after_snapshot,actor_id,operation_id) VALUES('v3-bonus-history','bonus','v3-bonus-entry','合成奖金历史',JSON_OBJECT('amount_cents',10000),'fixture','v3-bonus-history')`,
		`INSERT INTO expense_history(id,entity_type,entity_id,event,after_snapshot,actor_id,operation_id) VALUES('v3-expense-history','claim','v3-archive-expense','合成报销历史',JSON_OBJECT('paid_cents',10000),'fixture','v3-expense-history')`,
		`INSERT INTO attachment_files(id,order_id,kind,file_name,content_type,size_bytes,content_hash,content_data,created_by) VALUES('11111111111111111111111111111111','v3-archive-order','ticket','fixture.txt','text/plain',7,REPEAT('0',64),'Zml4dHVyZQ==','fixture')`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	backupDir, dataDir := t.TempDir(), t.TempDir()
	t.Setenv("BACKUP_DIR", backupDir)
	t.Setenv("DATA_DIR", dataDir)
	secret := "synthetic-cloud-credential-for-restore-test"
	encrypted, err := encryptSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "backup.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("INSERT INTO settings(setting_key,value) VALUES('backup',?)", jsonText(BackupSettings{Enabled: false, Secret: encrypted, Retain: 168})); err != nil {
		t.Fatal(err)
	}
	archives, err := a.archive("2026-10", "", false, User{Role: "admin"})
	if err != nil || len(archives) == 0 {
		t.Fatal("V3 archive fixture", err)
	}
	file, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(backupDir, file)
	counts, err := inspectSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	want := snapshotV3Rows(t, path)
	for _, table := range []string{"users", "projects", "catalog_seed_imports", "catalog_seed_provenance", "catalog_seed_packages", "catalog_seed_package_coverage", "project_packages", "orders", "order_lines", "order_materials", "order_sales", "order_line_wage_adjustments", "order_worker_wages", "ticket_batches", "ticket_batch_items", "ticket_batch_versions", "company_receipts", "company_receipt_versions", "project_ticket_receipts", "customer_discounts", "customer_discount_versions", "stock_balances", "stock_movements", "product_purchase_costs", "payroll_adjustments", "payroll_deductions", "payroll_advances", "payroll_settlements", "payroll_settlement_sources", "payroll_payments", "payroll_history", "payroll_bonus_entries", "payroll_bonus_settlements", "payroll_bonus_payments", "expense_claims", "expense_advances", "expense_advance_allocations", "expense_payments", "expense_advance_returns", "expense_history", "attachment_files", "archive_runs", "settings"} {
		if counts[table] < 1 {
			t.Fatal("restore fixture failed to cover critical V3 table", table)
		}
	}
	if _, err = a.restoreToDatabase(restoreRequest{File: file, Target: name, Confirm: name}, User{ID: "fixture", Name: "合成操作人", Role: "admin"}, nil); err != nil {
		t.Fatal(err)
	}
	restoredCfg := cfg
	restoredCfg.DBName = name
	restored, err := sql.Open("mysql", restoredCfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, table := range backupTables {
		got := databaseV3Rows(t, restored, table)
		if !reflect.DeepEqual(got, want[table]) {
			t.Fatalf("V3 restored rows differ for %s: restored=%d snapshot=%d", table, len(got), len(want[table]))
		}
		if table != "backup_runs" && table != "audit" {
			if !reflect.DeepEqual(databaseV3Rows(t, a.db, table), want[table]) {
				t.Fatalf("restore changed current source table %s", table)
			}
		}
	}
	// Historical CSV/XLSX files are external to the database snapshot. Copy
	// only these fixture artifacts and compare each byte hash in the new root.
	newBackupDir := t.TempDir()
	for _, archive := range archives {
		rel := filepath.FromSlash(archive.File)
		original := filepath.Join(backupDir, "monthly", rel)
		contents, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(newBackupDir, "monthly", rel)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, contents, 0600); err != nil {
			t.Fatal(err)
		}
		sum, err := fileHash(target)
		if err != nil || sum != hash(contents) {
			t.Fatal("external monthly archive copy hash mismatch", err)
		}
	}
	t.Setenv("BACKUP_DIR", newBackupDir)
	for _, archive := range archives {
		parts := strings.Split(archive.File, "/")
		f, err := openArchiveFile(parts[0], archive.File)
		if err != nil {
			t.Fatal("restored monthly file cannot be opened", err)
		}
		f.Close()
	}
	settings, err := (&App{db: restored}).backupSettings()
	if err != nil || settings.Secret != encrypted {
		t.Fatal("encrypted cloud settings were not preserved", err)
	}
	newDataDir := t.TempDir()
	t.Setenv("DATA_DIR", newDataDir)
	if err = os.WriteFile(filepath.Join(newDataDir, "backup.key"), make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = decryptSecret(settings.Secret); err == nil {
		t.Fatal("unrelated key unexpectedly decrypted restored credentials")
	}
	if err = os.WriteFile(filepath.Join(newDataDir, "backup.key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	if clear, err := decryptSecret(settings.Secret); err != nil || clear != secret {
		t.Fatal("preserved external key cannot decrypt restored synthetic credentials")
	}
	t.Log("all backup tables matched restored row/field contents; V3 work, tickets, cash allocations, stock, payroll, expense, permissions and history populated; source unchanged except explicit backup/restore records; external monthly bytes verified; copied backup.key decrypts restored synthetic settings; exact restore target cleaned")
}
