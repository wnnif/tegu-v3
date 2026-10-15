package main

import (
	"database/sql"
	"fmt"
	"time"
)

// Migration 446 adds the payroll ledger and immutable settlement project
// snapshots. The tables are recreated idempotently after restoring older snapshots.
func migratePayroll(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS payroll_adjustments(
		 id VARCHAR(64) PRIMARY KEY,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,
		 amount_cents BIGINT NOT NULL,effective_date DATE NOT NULL,project_id VARCHAR(64) NOT NULL DEFAULT '',
		 project_name VARCHAR(200) NOT NULL DEFAULT '',address VARCHAR(300) NOT NULL DEFAULT '',reason VARCHAR(1000) NOT NULL,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',reversal_of VARCHAR(64) NULL,
		 version BIGINT NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_payroll_adjustment_reversal(reversal_of),INDEX(worker_id,effective_date,id),INDEX(project_id,worker_id,effective_date,id),
		 CHECK(amount_cents<>0 AND amount_cents>=-100000000000000 AND amount_cents<=100000000000000))`,
		`CREATE TABLE IF NOT EXISTS payroll_deductions(
		 id VARCHAR(64) PRIMARY KEY,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,
		 amount_cents BIGINT NOT NULL,deduction_date DATE NOT NULL,project_id VARCHAR(64) NOT NULL DEFAULT '',
		 project_name VARCHAR(200) NOT NULL DEFAULT '',address VARCHAR(300) NOT NULL DEFAULT '',reason VARCHAR(1000) NOT NULL,
		 legal_basis VARCHAR(1000) NOT NULL DEFAULT '',status VARCHAR(20) NOT NULL DEFAULT 'pending',version BIGINT NOT NULL DEFAULT 1,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(worker_id,status,deduction_date,id),INDEX(project_id,worker_id,deduction_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('pending','confirmed','voided')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_advances(
		 id VARCHAR(64) PRIMARY KEY,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,
		 amount_cents BIGINT NOT NULL,advance_date DATE NOT NULL,project_id VARCHAR(64) NOT NULL DEFAULT '',
		 project_name VARCHAR(200) NOT NULL DEFAULT '',address VARCHAR(300) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',
		 status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,actor_id VARCHAR(64) NOT NULL,
		 actor_name VARCHAR(100) NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(worker_id,status,advance_date,id),INDEX(project_id,worker_id,advance_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','voided')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_settlements(
		 id VARCHAR(64) PRIMARY KEY,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,
		 settlement_date DATE NULL,gross_cents BIGINT NOT NULL,adjustment_cents BIGINT NOT NULL,
		 deduction_cents BIGINT NOT NULL,advance_cents BIGINT NOT NULL,payable_cents BIGINT NOT NULL,
		 status VARCHAR(20) NOT NULL DEFAULT 'confirmed',version BIGINT NOT NULL DEFAULT 1,
		 note VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',
		 operation_id VARCHAR(100) NOT NULL DEFAULT '',legacy BOOLEAN NOT NULL DEFAULT 0,legacy_paid BOOLEAN NOT NULL DEFAULT 0,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_payroll_settlement_operation(actor_id,operation_id),INDEX(worker_id,status,settlement_date,id),
		 CHECK(gross_cents>=0 AND deduction_cents>=0 AND advance_cents>=0 AND payable_cents>=0),CHECK(status IN ('confirmed','voided')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_settlement_sources(
		 settlement_id VARCHAR(64) NOT NULL,source_type VARCHAR(20) NOT NULL,source_id VARCHAR(64) NOT NULL,
		 worker_id VARCHAR(64) NOT NULL,project_id VARCHAR(64) NULL,amount_cents BIGINT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 PRIMARY KEY(settlement_id,source_type,source_id),INDEX(source_type,source_id,settlement_id),INDEX(worker_id,settlement_id),INDEX idx_payroll_source_project(project_id,settlement_id),
		 CHECK(source_type IN ('wage','adjustment','deduction','advance')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_payments(
		 id VARCHAR(64) PRIMARY KEY,settlement_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,
		 paid_date DATE NULL,method VARCHAR(40) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',
		 status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,reversal_reason VARCHAR(1000) NOT NULL DEFAULT '',
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_payroll_payment_operation(actor_id,operation_id),INDEX(settlement_id,status,paid_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','reversed')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_history(
		 id VARCHAR(64) PRIMARY KEY,entity_type VARCHAR(20) NOT NULL,entity_id VARCHAR(64) NOT NULL,
		 event VARCHAR(100) NOT NULL,reason VARCHAR(1000) NOT NULL DEFAULT '',before_snapshot JSON NULL,after_snapshot JSON NOT NULL,
		 actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX(entity_type,entity_id,id),INDEX(actor_id,id),INDEX(operation_id),INDEX(created_at,id))`,
		`CREATE TABLE IF NOT EXISTS payroll_bonus_entries(
		 id VARCHAR(64) PRIMARY KEY,source_key VARCHAR(255) NOT NULL,entry_kind VARCHAR(24) NOT NULL,rule_key VARCHAR(64) NOT NULL DEFAULT '',
		 worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,order_id VARCHAR(64) NOT NULL DEFAULT '',line_id VARCHAR(64) NOT NULL DEFAULT '',order_number VARCHAR(80) NOT NULL DEFAULT '',
		 project_id VARCHAR(64) NOT NULL DEFAULT '',project_name VARCHAR(200) NOT NULL DEFAULT '',address VARCHAR(300) NOT NULL DEFAULT '',work_date DATE NOT NULL,bonus_year SMALLINT NOT NULL,
		 category VARCHAR(30) NOT NULL DEFAULT '',line_name VARCHAR(200) NOT NULL DEFAULT '',subitem_name VARCHAR(200) NOT NULL DEFAULT '',unit VARCHAR(30) NOT NULL DEFAULT '',qty_micro BIGINT NOT NULL DEFAULT 0,rate_cents BIGINT NOT NULL DEFAULT 0,
		 amount_cents BIGINT NOT NULL,reason VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 reversal_of VARCHAR(64) NULL,version BIGINT NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_bonus_entry_operation(actor_id,operation_id,source_key),UNIQUE KEY idx_bonus_entry_reversal(reversal_of),
		 INDEX(worker_id,bonus_year,work_date,id),INDEX(order_id,line_id,rule_key,bonus_year,worker_id,id),INDEX(project_id,worker_id,bonus_year,id),
		 CHECK(entry_kind IN ('automatic','manual','reversal')),CHECK(amount_cents<>0 AND amount_cents>=-100000000000000 AND amount_cents<=100000000000000),
		 CHECK(bonus_year>=1000 AND bonus_year<=9999),CHECK(qty_micro>=0 AND rate_cents>=0))`,
		`CREATE TABLE IF NOT EXISTS payroll_bonus_settlements(
		 id VARCHAR(64) PRIMARY KEY,worker_id VARCHAR(64) NOT NULL,worker_name VARCHAR(100) NOT NULL,bonus_year SMALLINT NOT NULL,settlement_date DATE NOT NULL,amount_cents BIGINT NOT NULL,
		 status VARCHAR(20) NOT NULL DEFAULT 'confirmed',version BIGINT NOT NULL DEFAULT 1,note VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_bonus_settlement_operation(actor_id,operation_id),INDEX(worker_id,bonus_year,status,settlement_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(bonus_year>=1000 AND bonus_year<=9999),CHECK(status IN ('confirmed','voided')))` ,
		`CREATE TABLE IF NOT EXISTS payroll_bonus_payments(
		 id VARCHAR(64) PRIMARY KEY,settlement_id VARCHAR(64) NOT NULL,amount_cents BIGINT NOT NULL,paid_date DATE NOT NULL,method VARCHAR(40) NOT NULL DEFAULT '',note VARCHAR(1000) NOT NULL DEFAULT '',
		 status VARCHAR(20) NOT NULL DEFAULT 'active',version BIGINT NOT NULL DEFAULT 1,reversal_reason VARCHAR(1000) NOT NULL DEFAULT '',actor_id VARCHAR(64) NOT NULL,actor_name VARCHAR(100) NOT NULL DEFAULT '',operation_id VARCHAR(100) NOT NULL,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		 UNIQUE KEY idx_bonus_payment_operation(actor_id,operation_id),INDEX(settlement_id,status,paid_date,id),
		 CHECK(amount_cents>0 AND amount_cents<=100000000000000),CHECK(status IN ('active','reversed')))` ,
	} {
		if _, err := db.Exec(ddl + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"); err != nil {
			return err
		}
	}
	if err := ensurePayrollSettlementProjectSnapshot(db); err != nil {
		return err
	}
	if err := backfillLegacyPayroll(db); err != nil {
		return err
	}
	if err := backfillPayrollSettlementProjectSnapshots(db); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(446)")
	return err
}

func migrateBonusPayrollMarker(db *sql.DB) error {
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=448").Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	actor := User{ID: "system-bonus-migration", Name: "奖金规则迁移", Role: "admin"}
	lastOrderID, lastLineID := "", ""
	for {
		rows, queryErr := tx.Query(`SELECT o.id,o.number,o.project_id,o.project_name,o.address,DATE_FORMAT(o.work_date,'%Y-%m-%d'),
			l.id,l.craft_id,l.category,l.name,COALESCE(l.subitem_id,''),COALESCE(l.subitem_name,''),l.mode,l.temporary,l.qty_micro,l.worker_id,l.worker_name
			FROM orders o JOIN order_lines l ON l.order_id=o.id
			WHERE o.status<>'已取消' AND l.mode='平方' AND l.temporary=0 AND l.qty_micro>0 AND l.worker_id<>''
			AND ((l.category='防水涂料' AND (l.craft_id='craft-v3-waterproof-standard-two-coats' OR l.name='正常两遍防水')) OR
				(l.category='开工' AND (l.craft_id='craft-v3-fine-wall-ground' OR l.name='精开工·墙地固')))
			AND (o.id>? OR (o.id=? AND l.id>?)) ORDER BY o.id,l.id LIMIT 500`, lastOrderID, lastOrderID, lastLineID)
		if queryErr != nil {
			return queryErr
		}
		type legacyBonusCandidate struct {
			entry             bonusEntry
			craftID, subitemID string
		}
		batch := make([]legacyBonusCandidate, 0, 500)
		for rows.Next() {
			var candidate legacyBonusCandidate
			var temporary bool
			entry := &candidate.entry
			if err = rows.Scan(&entry.OrderID, &entry.OrderNumber, &entry.ProjectID, &entry.ProjectName, &entry.Address, &entry.WorkDate,
				&entry.LineID, &candidate.craftID, &entry.Category, &entry.LineName, &candidate.subitemID, &entry.SubitemName, &entry.Unit, &temporary, &entry.QtyMicro, &entry.WorkerID, &entry.WorkerName); err != nil {
				rows.Close()
				return err
			}
			lastOrderID, lastLineID = entry.OrderID, entry.LineID
			batch = append(batch, candidate)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, candidate := range batch {
			entry := candidate.entry
			_, ruleMatch := automaticBonusRule(entry.Category, candidate.craftID, entry.LineName, candidate.subitemID, entry.SubitemName, entry.Unit, false)
			if !ruleMatch {
				continue
			}
			parsed, parseErr := time.Parse("2006-01-02", entry.WorkDate)
			if parseErr != nil {
				return parseErr
			}
			rule, _ := automaticBonusRule(entry.Category, candidate.craftID, entry.LineName, candidate.subitemID, entry.SubitemName, entry.Unit, false)
			entry.BonusYear = parsed.Year()
			entry.EntryKind, entry.RuleKey = "automatic", rule.Key
			entry.SourceKey = bonusSourceKey(entry.OrderID, entry.LineID, entry.WorkerID, entry.BonusYear, rule.Key)
			entry.RateCents, entry.Reason, entry.ActorName = rule.RateCents, rule.Reason, actor.Name
			entry.AmountCents, err = bonusAmountCents(entry.QtyMicro, entry.RateCents)
			if err != nil {
				return fmt.Errorf("bonus migration order %s line %s: %w", entry.OrderID, entry.LineID, err)
			}
			entry.ID = entry.SourceKey
			operationID := "bonus-migration-448:" + entry.SourceKey
			result, insertErr := tx.Exec(`INSERT IGNORE INTO payroll_bonus_entries(id,source_key,entry_kind,rule_key,worker_id,worker_name,order_id,line_id,order_number,
				project_id,project_name,address,work_date,bonus_year,category,line_name,subitem_name,unit,qty_micro,rate_cents,amount_cents,reason,actor_id,actor_name,operation_id)
				VALUES(?,?,'automatic',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, entry.ID, entry.SourceKey, entry.RuleKey, entry.WorkerID, entry.WorkerName, entry.OrderID, entry.LineID, entry.OrderNumber,
				entry.ProjectID, entry.ProjectName, entry.Address, entry.WorkDate, entry.BonusYear, entry.Category, entry.LineName, entry.SubitemName, entry.Unit, entry.QtyMicro, entry.RateCents, entry.AmountCents, entry.Reason, actor.ID, actor.Name, operationID)
			if insertErr != nil {
				return insertErr
			}
			affected, affectedErr := result.RowsAffected()
			if affectedErr != nil {
				return affectedErr
			}
			if affected == 0 {
				continue
			}
			if err = payrollHistory(tx, actor, operationID, "bonus", entry.ID, "automatic-bonus-backfill", "迁移回填已确认奖金规则", nil, entry); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(448)"); err != nil {
		return err
	}
	return tx.Commit()
}

func ensurePayrollSettlementProjectSnapshot(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='payroll_settlement_sources' AND COLUMN_NAME='project_id'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE payroll_settlement_sources ADD COLUMN project_id VARCHAR(64) NULL AFTER worker_id"); err != nil {
			return err
		}
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='payroll_settlement_sources' AND INDEX_NAME='idx_payroll_source_project'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec("CREATE INDEX idx_payroll_source_project ON payroll_settlement_sources(project_id,settlement_id)"); err != nil {
			return err
		}
	}
	return nil
}

func backfillPayrollSettlementProjectSnapshots(db *sql.DB) error {
	_, err := db.Exec(`UPDATE payroll_settlement_sources src
		LEFT JOIN order_worker_wages ww ON src.source_type='wage' AND ww.id=src.source_id
		LEFT JOIN orders o ON o.id=ww.order_id
		LEFT JOIN payroll_adjustments adj ON src.source_type='adjustment' AND adj.id=src.source_id
		LEFT JOIN payroll_deductions ded ON src.source_type='deduction' AND ded.id=src.source_id
		LEFT JOIN payroll_advances adv ON src.source_type='advance' AND adv.id=src.source_id
		SET src.project_id=CASE src.source_type
			WHEN 'wage' THEN COALESCE(o.project_id,'')
			WHEN 'adjustment' THEN COALESCE(adj.project_id,'')
			WHEN 'deduction' THEN COALESCE(ded.project_id,'')
			WHEN 'advance' THEN COALESCE(adv.project_id,'')
			ELSE ''
		END
		WHERE src.project_id IS NULL`)
	return err
}

// Preserve old fully-paid wage flags as an explicit confirmed settlement.
// Only an old pay event creates a dated payment row; the bare flag is marked
// as legacy-paid without inventing a payment date or payment event.
func backfillLegacyPayroll(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT IGNORE INTO payroll_settlements(
		id,worker_id,worker_name,settlement_date,gross_cents,adjustment_cents,deduction_cents,advance_cents,payable_cents,
		status,version,note,actor_id,actor_name,operation_id,legacy,legacy_paid,created_at,updated_at)
		SELECT MD5(CONCAT('payroll-legacy-settlement:',ww.id)),ww.worker_id,ww.worker_name,
		DATE(latest.created_at),ww.wage_cents,0,0,0,ww.wage_cents,'confirmed',1,
		'迁移回填：保留历史已发工资',COALESCE(NULLIF(latest.actor_id,''),'legacy-migration'),COALESCE(NULLIF(u.name,''),'历史工资迁移'),
		CONCAT('legacy-payroll-',ww.id),1,(latest.id IS NULL),COALESCE(latest.created_at,o.updated_at),COALESCE(latest.created_at,o.updated_at)
		FROM order_worker_wages ww JOIN orders o ON o.id=ww.order_id
		LEFT JOIN wage_settlements latest ON latest.id=(SELECT ws.id FROM wage_settlements ws WHERE ws.wage_id=ww.id AND ws.kind='pay' ORDER BY ws.created_at DESC,ws.id DESC LIMIT 1)
		LEFT JOIN users u ON u.id=latest.actor_id
		WHERE ww.wage_paid=1 AND ww.worker_id<>''`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT IGNORE INTO payroll_settlement_sources(settlement_id,source_type,source_id,worker_id,project_id,amount_cents)
		SELECT MD5(CONCAT('payroll-legacy-settlement:',ww.id)),'wage',ww.id,ww.worker_id,COALESCE(o.project_id,''),ww.wage_cents
		FROM order_worker_wages ww LEFT JOIN orders o ON o.id=ww.order_id WHERE ww.wage_paid=1 AND ww.worker_id<>''`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT IGNORE INTO payroll_payments(
		id,settlement_id,amount_cents,paid_date,method,note,status,version,actor_id,actor_name,operation_id,created_at,updated_at)
		SELECT MD5(CONCAT('payroll-legacy-payment:',ww.id)),MD5(CONCAT('payroll-legacy-settlement:',ww.id)),latest.amount_cents,
		DATE(latest.created_at),'','历史付款流水回填','active',1,latest.actor_id,COALESCE(NULLIF(u.name,''),'历史工资迁移'),
		CONCAT('legacy-payroll-',ww.id),latest.created_at,latest.created_at
		FROM order_worker_wages ww
		JOIN wage_settlements latest ON latest.id=(SELECT ws.id FROM wage_settlements ws WHERE ws.wage_id=ww.id AND ws.kind='pay' ORDER BY ws.created_at DESC,ws.id DESC LIMIT 1)
		LEFT JOIN users u ON u.id=latest.actor_id
		WHERE ww.wage_paid=1 AND ww.worker_id<>'' AND latest.amount_cents>0`); err != nil {
		return err
	}
	return tx.Commit()
}
