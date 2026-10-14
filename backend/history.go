package main

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) listHistory(w http.ResponseWriter, r *http.Request, u User, stock bool) error {
	permission, table, order := "users", "audit", "id DESC"
	if stock {
		permission, table, order = "warehouse", "stock_documents", "created_at DESC,id DESC"
	}
	if !u.can(permission) {
		return fail(403, "无查看权限")
	}
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	if stock {
		if q.Get("warehouse") != "" {
			where = append(where, "(source_id=? OR destination_id=?)")
			args = append(args, q.Get("warehouse"), q.Get("warehouse"))
		}
		if q.Get("kind") != "" {
			where = append(where, "kind=?")
			args = append(args, q.Get("kind"))
		}
		if q.Get("state") != "all" {
			where = append(where, "voided=?")
			args = append(args, q.Get("state") == "void")
		}
	} else {
		if q.Get("actor") != "" {
			where = append(where, "actor_name LIKE ?")
			args = append(args, "%"+q.Get("actor")+"%")
		}
		if q.Get("action") != "" {
			where = append(where, "action LIKE ?")
			args = append(args, "%"+q.Get("action")+"%")
		}
	}
	if q.Get("q") != "" {
		if stock {
			where = append(where, "(id LIKE ? OR note LIKE ?)")
		} else {
			where = append(where, "(target_id LIKE ? OR detail LIKE ?)")
		}
		args = append(args, "%"+q.Get("q")+"%", "%"+q.Get("q")+"%")
	}
	date := "created_at"
	if stock {
		date = "work_date"
	}
	for _, f := range []string{"from", "to"} {
		if q.Get(f) != "" {
			d, e := time.Parse("2006-01-02", q.Get(f))
			if e != nil {
				return fail(400, "日期无效")
			}
			if f == "from" {
				where = append(where, date+">=?")
				args = append(args, d.Format("2006-01-02"))
			} else {
				where = append(where, date+"<?")
				args = append(args, d.AddDate(0, 0, 1).Format("2006-01-02"))
			}
		}
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		return fail(400, "页码无效")
	}
	clause := " FROM " + table + " WHERE " + strings.Join(where, " AND ")
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var total int64
	if e = tx.QueryRow("SELECT COUNT(*)"+clause, args...).Scan(&total); e != nil {
		return e
	}
	if stock {
		stockClause := strings.Replace(clause, " FROM stock_documents", " FROM stock_documents d", 1)
		list, queryErr := scanStockHistory(tx, "SELECT "+stockHistoryColumns+stockClause+" ORDER BY d.created_at DESC,d.id DESC LIMIT 100 OFFSET ?", append(args, (page-1)*100))
		if queryErr != nil {
			return queryErr
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		write(w, 200, queryPageResult[stockHistoryRecord]{Rows: list, Total: total, Page: page})
		return nil
	}
	list, e := scanRows[auditHistoryRecord](tx, "SELECT id,actor_id,actor_name,action,target_id,detail,created_at"+clause+" ORDER BY "+order+" LIMIT 100 OFFSET ?", append(args, (page-1)*100), func(r *sql.Rows, item *auditHistoryRecord) error {
		return r.Scan(&item.ID, &item.ActorID, &item.ActorName, &item.Action, &item.TargetID, &item.Detail, &item.CreatedAt)
	})
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	write(w, 200, queryPageResult[auditHistoryRecord]{Rows: list, Total: total, Page: page})
	return nil
}

func (a *App) financeEdit(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("finance") {
		return nil, fail(403, "无财务权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return nil, err
	}
	var p struct {
		Company string `json:"company_id"`
		Orders  []struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
			Address string `json:"address"`
			Contact string `json:"contact"`
			Date    string `json:"work_date"`
			Note    string `json:"note"`
		} `json:"orders"`
	}
	if e := decode(b, &p); e != nil {
		return nil, e
	}
	if p.Company == "" || len(p.Orders) < 1 || len(p.Orders) > 100 {
		return nil, fail(400, "请选择同一公司的1至100张单据")
	}
	sort.Slice(p.Orders, func(i, j int) bool { return p.Orders[i].ID < p.Orders[j].ID })
	seen := map[string]bool{}
	for _, o := range p.Orders {
		if seen[o.ID] {
			return nil, fail(400, "不能重复选择单据")
		}
		seen[o.ID] = true
		if strings.TrimSpace(o.Address) == "" || len([]rune(o.Address)) > 300 || len([]rune(o.Contact)) > 100 || len([]rune(o.Note)) > 10000 {
			return nil, fail(400, "地址、项目经理或备注无效")
		}
		if _, e := time.Parse("2006-01-02", o.Date); e != nil {
			return nil, fail(400, "日期无效")
		}
		var companyID, status, oldAddress, oldContact, oldDate, oldNote string
		var version int64
		var ticketManaged bool
		e := tx.QueryRow("SELECT company_id,version,status,address,contact,work_date,note,ticket_managed FROM orders WHERE id=? FOR UPDATE", o.ID).Scan(&companyID, &version, &status, &oldAddress, &oldContact, &oldDate, &oldNote, &ticketManaged)
		if e == sql.ErrNoRows {
			return nil, fail(404, "单据不存在")
		}
		if e != nil {
			return nil, e
		}
		if companyID != p.Company {
			return nil, fail(400, "单据不属于所选公司")
		}
		if version != o.Version {
			return nil, fail(409, "单据已被修改，请刷新重选")
		}
		if status == "已取消" {
			return nil, fail(400, "已取消单据不可修改")
		}
		if ticketManaged {
			return nil, fail(400, "请在工地施工记录中修改，已交票内容需保留历史")
		}
		if _, e = tx.Exec("UPDATE orders SET address=?,contact=?,work_date=?,note=?,version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", o.Address, o.Contact, o.Date, o.Note, o.ID); e != nil {
			return nil, e
		}
		before := map[string]any{"address": oldAddress, "contact": oldContact, "work_date": oldDate, "note": oldNote}
		if e = audit(tx, u, "财务批量修改", o.ID, map[string]any{"before": before, "after": o}); e != nil {
			return nil, e
		}
	}
	return map[string]any{"ok": true, "count": len(p.Orders)}, nil
}
