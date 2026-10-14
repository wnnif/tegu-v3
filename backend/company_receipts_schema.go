package main

import "database/sql"

// The monetary source remains project_tickets. Receipt heads describe money
// actually received; project_ticket_receipts now describes its allocations.
func migrateCompanyReceipts(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS company_receipts(id VARCHAR(64) PRIMARY KEY,company_id VARCHAR(64) NOT NULL,company_name VARCHAR(200) NOT NULL,amount_cents BIGINT NOT NULL,received_at DATE NOT NULL,method VARCHAR(40) NOT NULL DEFAULT '',voucher_no VARCHAR(100) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,created_by VARCHAR(64) NOT NULL,created_by_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,void_reason VARCHAR(1000) NOT NULL DEFAULT '',INDEX(company_id,status,received_at,id),CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','voided')))`,
		`CREATE TABLE IF NOT EXISTS company_receipt_versions(receipt_id VARCHAR(64) NOT NULL,version BIGINT NOT NULL,event VARCHAR(100) NOT NULL,reason VARCHAR(1000) NOT NULL DEFAULT '',snapshot JSON NOT NULL,actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(receipt_id,version))`,
		`CREATE TABLE IF NOT EXISTS customer_discounts(id VARCHAR(64) PRIMARY KEY,company_id VARCHAR(64) NOT NULL,ticket_id VARCHAR(64) NOT NULL,batch_id VARCHAR(64) NOT NULL DEFAULT '',amount_cents BIGINT NOT NULL,reason VARCHAR(1000) NOT NULL,version BIGINT NOT NULL DEFAULT 1,voided BOOLEAN NOT NULL DEFAULT 0,created_by VARCHAR(64) NOT NULL,created_by_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,void_reason VARCHAR(1000) NOT NULL DEFAULT '',INDEX(company_id,voided,created_at,id),INDEX(ticket_id,voided,id),INDEX(batch_id,voided,id),CHECK(amount_cents>0 AND amount_cents<=100000000000000))`,
		`CREATE TABLE IF NOT EXISTS customer_discount_versions(discount_id VARCHAR(64) NOT NULL,version BIGINT NOT NULL,event VARCHAR(100) NOT NULL,reason VARCHAR(1000) NOT NULL DEFAULT '',snapshot JSON NOT NULL,actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(discount_id,version))`,
	} {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
	}
	for _, name := range []string{"receipt_id", "batch_id"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='project_ticket_receipts' AND COLUMN_NAME=?", name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec("ALTER TABLE project_ticket_receipts ADD COLUMN " + name + " VARCHAR(64) NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	for _, index := range []struct{ table, name, columns string }{{"project_ticket_receipts", "idx_ticket_receipt_head", "receipt_id,voided,id"}, {"project_ticket_receipts", "idx_ticket_receipt_batch", "batch_id,ticket_id,voided"}, {"company_receipts", "idx_company_receipt_date", "received_at,id"}, {"ticket_batch_items", "idx_ticket_batch_source", "source_project_ticket_id,batch_id"}} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?", index.table, index.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec("CREATE INDEX " + index.name + " ON " + index.table + "(" + index.columns + ")"); err != nil {
				return err
			}
		}
	}
	if err := backfillCompanyReceipts(db); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(445)")
	return err
}

// Called after restoring an older snapshot too. Stable old IDs preserve each
// actual payment independently; equal dates/amounts never imply one payment.
func backfillCompanyReceipts(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = backfillCompanyReceiptsTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillCompanyReceiptsTx(tx *sql.Tx) error {
	_, err := tx.Exec(`INSERT IGNORE INTO company_receipts(id,company_id,company_name,amount_cents,received_at,method,voucher_no,note,status,created_by,created_by_name,created_at,updated_at)
		SELECT r.id,t.company_id,t.company_name,r.amount_cents,r.received_at,r.method,r.voucher_no,r.note,IF(r.voided,'voided','active'),r.created_by,COALESCE(u.name,''),r.created_at,r.created_at
		FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id LEFT JOIN users u ON u.id=r.created_by WHERE r.receipt_id=''`)
	if err != nil {
		return err
	}
	var conflicts int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id LEFT JOIN company_receipts c ON c.id=r.id WHERE r.receipt_id='' AND (c.id IS NULL OR c.company_id<>t.company_id OR c.amount_cents<>r.amount_cents OR c.received_at<>r.received_at OR c.status<>IF(r.voided,'voided','active'))`).Scan(&conflicts); err != nil {
		return err
	}
	if conflicts != 0 {
		return fail(409, "历史收款关联不一致，不能自动转换真实到账")
	}
	_, err = tx.Exec(`INSERT IGNORE INTO company_receipt_versions(receipt_id,version,event,reason,snapshot,actor_id,actor_name,created_at)
		SELECT c.id,1,'保留历史实际收款','历史收款逐笔转换',JSON_OBJECT('receipt',JSON_OBJECT('id',c.id,'company_id',c.company_id,'amount_cents',c.amount_cents,'received_at',DATE_FORMAT(c.received_at,'%Y-%m-%d'),'status',c.status,'version',1),'allocations',JSON_ARRAY(JSON_OBJECT('id',r.id,'ticket_id',r.ticket_id,'amount_cents',r.amount_cents,'batch_id',''))),c.created_by,c.created_by_name,c.created_at
		FROM company_receipts c JOIN project_ticket_receipts r ON r.id=c.id WHERE r.receipt_id=''`)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE project_ticket_receipts SET receipt_id=id WHERE receipt_id=''"); err != nil {
		return err
	}
	return nil
}
