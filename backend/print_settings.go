package main

import (
	"database/sql"
	"encoding/json"
	"strings"
)

type PrintSettings struct {
	Title   string `json:"title"`
	Company string `json:"company"`
}

func (a *App) printSettings() (PrintSettings, error) {
	s := PrintSettings{Title: "施工验收单", Company: "特固施工"}
	var raw string
	e := a.db.QueryRow("SELECT value FROM settings WHERE setting_key='print'").Scan(&raw)
	if e == sql.ErrNoRows {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	e = json.Unmarshal([]byte(raw), &s)
	return s, e
}
func (a *App) savePrintSettings(tx *sql.Tx, u User, b []byte) (any, error) {
	if !u.can("rates") {
		return nil, fail(403, "无打印设置权限")
	}
	var s PrintSettings
	if e := decode(b, &s); e != nil {
		return nil, e
	}
	s.Title = strings.TrimSpace(s.Title)
	s.Company = strings.TrimSpace(s.Company)
	if s.Title == "" || s.Company == "" || len([]rune(s.Title)) > 100 || len([]rune(s.Company)) > 100 {
		return nil, fail(400, "请填写100字以内的标题和施工单位")
	}
	if _, e := tx.Exec("INSERT INTO settings(setting_key,value) VALUES('print',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", jsonText(s)); e != nil {
		return nil, e
	}
	if e := audit(tx, u, "修改打印设置", "print", s); e != nil {
		return nil, e
	}
	return s, nil
}
