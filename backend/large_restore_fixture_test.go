package main

import "testing"

func TestLargeRestoreWageFixtureMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	for _, query := range []string{
		`INSERT INTO companies(id,name) VALUES('fixture-co','合成当前快照公司')`,
		`INSERT INTO orders(id,number,company_id,address,work_date,worker_id,worker_name,note,created_by,wage_cents,wage_paid,version) VALUES('fixture-current','FIXTURE-CURRENT','fixture-co','当前多人','2026-10-01','a','甲','','fixture',300,1,4),('fixture-old','FIXTURE-OLD','fixture-co','历史单人','2026-10-01','old-person','历史快照姓名','','fixture',987,1,7)`,
		`INSERT INTO order_lines(id,order_id,category,name,mode,segments,qty_micro,price_cents,total_cents,wage_cents,labor_cents,temporary,worker_id,worker_name) VALUES('fixture-line-a','fixture-current','开工','甲施工','金额',JSON_ARRAY(),1000000,0,0,100,100,1,'a','甲'),('fixture-line-b','fixture-current','开工','乙施工','金额',JSON_ARRAY(),1000000,0,0,200,200,1,'b','乙'),('fixture-line-old','fixture-old','开工','历史施工','金额',JSON_ARRAY(),1000000,0,0,111,111,1,'old-person','历史快照姓名')`,
		`INSERT INTO order_worker_wages(id,order_id,worker_id,worker_name,wage_cents,wage_paid,version) VALUES('fixture-account-a','fixture-current','a','甲',100,1,3),('fixture-account-b','fixture-current','b','乙',200,0,2)`,
	} {
		maintenanceFaultExec(t, a.db, query)
	}
	completeLargeRestoreWageFixture(t, a.db)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", 3)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE id='fixture-account-a' AND wage_cents=100 AND wage_paid=1 AND version=3 AND legacy=0", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE id='fixture-account-b' AND wage_cents=200 AND wage_paid=0 AND version=2 AND legacy=0", 1)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages WHERE id=MD5('fixture-legacy:fixture-old') AND order_id='fixture-old' AND worker_id='old-person' AND worker_name='历史快照姓名' AND wage_cents=987 AND wage_paid=1 AND version=7 AND legacy=1", 1)
	completeLargeRestoreWageFixture(t, a.db)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_worker_wages", 3)
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM order_lines WHERE id='fixture-line-old' AND labor_cents=111", 1)
	t.Log("snapshot fixture preparation independently completes only old accounts, preserves all current account fields and legacy snapshots, and does not rewrite historical work")
}
