package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCraftWorkflowMonthlyArchiveMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	backupDir := t.TempDir()
	t.Setenv("BACKUP_DIR", backupDir)
	maintenanceFaultExec(t, a.db, `INSERT INTO companies(id,name) VALUES('workflow-archive-company','合成历史公司')`)
	maintenanceFaultExec(t, a.db, `INSERT INTO projects(id,number,company_id,name,address,created_by,ticket_mode_snapshot) VALUES('workflow-archive-project','ARP','workflow-archive-company','合成历史工地','合成历史地址','test','single')`)
	for _, fixture := range []struct {
		id, date, status   string
		delivery, quantity int64
	}{
		{"start", "2026-10-01", "进行中", 2000, 1},
		{"end", "2026-10-31", "进行中", 3000, 2},
		{"before", "2026-09-30", "进行中", 5000, 1},
		{"after", "2026-11-01", "进行中", 5000, 1},
		{"cancelled", "2026-10-15", "已取消", 5000, 1},
	} {
		orderID := "workflow-archive-" + fixture.id
		if _, err := a.db.Exec(`INSERT INTO orders(id,number,company_id,company_name,project_id,address,work_date,note,status,total_cents,wage_cents,delivery_wage_cents,created_by,worker_name,ticket_managed) VALUES(?,?,'workflow-archive-company','合成历史公司','workflow-archive-project','合成历史地址',?,'',?,?,?,?,'test','合成施工人',1)`, orderID, orderID, fixture.date, fixture.status, 20000+200*fixture.quantity, 6000+fixture.delivery, fixture.delivery); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,wage_cents,total_cents,labor_cents,temporary) VALUES(?,?,'开工','合成按个项目','个','[{"mode":"个","count":2}]',2000000,10000,3000,20000,6000,1)`, orderID+"-line", orderID); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO order_sales(id,order_id,product_id,warehouse_id,qty_micro,sale_qty_micro,price_cents,total_cents,product_name,unit,price_unit,input_unit,input_qty,sort_order) VALUES(?,?,'synthetic-product','synthetic-warehouse',?,?,200,?,'合成售料','桶','桶','base',?,1)`, orderID+"-sale", orderID, fixture.quantity*1000000, fixture.quantity*1000000, fixture.quantity*200, scaledDecimal(fixture.quantity*1000000, 6)); err != nil {
			t.Fatal(err)
		}
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO project_tickets(id,project_id,company_id,company_name,project_name,address,number,kind,slot,status,total_cents,created_at,updated_at) VALUES('workflow-archive-ticket','workflow-archive-project','workflow-archive-company','合成历史公司','合成历史工地','合成历史地址','ART','normal',1,'部分收款',40600,'2026-10-01','2026-10-31')`)
	for _, suffix := range []string{"start", "end"} {
		orderID := "workflow-archive-" + suffix
		for _, kind := range []string{"line", "sale"} {
			if _, err := a.db.Exec(`INSERT INTO project_ticket_items(item_id,ticket_id,order_id,item_kind) VALUES(?,'workflow-archive-ticket',?,?)`, orderID+"-"+kind, orderID, kind); err != nil {
				t.Fatal(err)
			}
		}
	}
	maintenanceFaultExec(t, a.db, `INSERT INTO project_ticket_receipts(id,ticket_id,amount_cents,received_at,created_by,voided) VALUES('workflow-receipt-valid','workflow-archive-ticket',10000,'2026-10-31','test',0),('workflow-receipt-void','workflow-archive-ticket',1000,'2026-10-31','test',1),('workflow-receipt-next','workflow-archive-ticket',5000,'2026-11-01','test',0)`)
	maintenanceFaultExec(t, a.db, `UPDATE companies SET name='更名公司' WHERE id='workflow-archive-company'`)
	maintenanceFaultExec(t, a.db, `UPDATE projects SET name='更名工地',address='新主地址' WHERE id='workflow-archive-project'`)
	files, err := a.archive("2026-10", "workflow-archive-company", false, User{Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var sales, wages, tickets, receipts int64
	var salesRows, wageRows, ticketRows int
	for _, file := range files {
		if filepath.Ext(file.File) != ".csv" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(backupDir, "monthly", file.File))
		if readErr != nil {
			t.Fatal(readErr)
		}
		records, readErr := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff"))).ReadAll()
		if readErr != nil || len(records) < 2 {
			t.Fatal("invalid archive CSV", file.Kind, readErr)
		}
		if file.Kind == "工资明细" {
			wages += file.Amount
			wageRows += file.Count
			if records[len(records)-1][6] != "170.00" {
				t.Fatal("flexible delivery wage missing from wage archive", records)
			}
		}
		if file.Kind == "售料明细" && len(records[0]) == 9 && records[0][4] == "销售材料" {
			sales += file.Amount
			salesRows += file.Count
			if len(records) != 4 || records[1][5] != "1.000000" || records[2][5] != "2.000000" || records[1][6] != "桶" || records[1][7] != "2.00" || records[3][8] != "6.00" {
				t.Fatal("sale quantities, units or totals incorrect", records)
			}
		}
		if file.Kind == "票据应收" {
			tickets += file.Amount
			ticketRows += file.Count
			if file.Count > 0 && (records[1][0] != "合成历史公司" || records[1][3] != "合成历史地址" || records[1][7] != "150.00" || records[1][8] != "256.00") {
				t.Fatal("ticket financial snapshot/name changed", records)
			}
		}
		if file.Kind == "收款流水" {
			receipts += file.Amount
			if file.Count != 2 || records[len(records)-1][8] != "100.00" {
				t.Fatal("receipt month or voided total incorrect", records)
			}
		}
	}
	if sales != 600 || salesRows != 2 || wages != 17000 || wageRows != 2 || tickets != 40600 || ticketRows != 1 || receipts != 10000 {
		t.Fatalf("archive duplicated managed amounts or included cancelled/boundary records: sales=%d/%d wages=%d/%d tickets=%d/%d receipts=%d", sales, salesRows, wages, wageRows, tickets, ticketRows, receipts)
	}
}
