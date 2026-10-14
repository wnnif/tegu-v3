package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func TestValidateAttachment(t *testing.T) {
	upload := &AttachmentUpload{Name: "票据.png", Content: base64.StdEncoding.EncodeToString(tinyPNG)}
	file, err := validateAttachment(upload)
	if err != nil || file.contentType != "image/png" || file.size != len(tinyPNG) {
		t.Fatal("valid image rejected", file, err)
	}
	for _, invalid := range []*AttachmentUpload{
		{Name: "../ticket.png", Content: upload.Content},
		{Name: "ticket.txt", Content: base64.StdEncoding.EncodeToString([]byte("not an image"))},
		{Name: "ticket.png", Content: "%%%"},
		{Name: "ticket.png", Content: base64.StdEncoding.EncodeToString(make([]byte, maxAttachmentBytes+1))},
	} {
		if _, err := validateAttachment(invalid); err == nil {
			t.Fatal("invalid attachment accepted", invalid.Name)
		}
	}
	request := SettlementRequest{Action: "deliver", Company: "c", TicketNo: "T", Recipient: "R", DeliveredAt: "2026-09-28", TicketFile: upload, Orders: []SettlementOrder{{ID: "o", Version: 1}}}
	if err := request.validate(); err != nil {
		t.Fatal(err)
	}
	request.Orders = append(request.Orders, SettlementOrder{ID: "p", Version: 1})
	if err := request.validate(); err == nil {
		t.Fatal("bulk file upload accepted")
	}
	request.Orders = request.Orders[:1]
	request.TicketFile = nil
	request.TicketAttachment = "file:" + id()
	if err := request.validate(); err == nil {
		t.Fatal("manually supplied internal file reference accepted")
	}
}

func TestAttachmentSettlementMySQL(t *testing.T) {
	// Use a disposable schema so fixed idempotency keys cannot pollute the
	// persistent manual-acceptance database or collide across reruns.
	a, cfg := maintenanceFaultDB(t)
	db := a.db
	companyID, orderID := id(), id()
	if _, err := db.Exec("INSERT INTO companies(id,name) VALUES(?,?)", companyID, "附件测试公司"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO orders(id,number,company_id,address,work_date,note,status,total_cents,created_by) VALUES(?,?,?,'测试工地','2026-09-28','','待交单',10000,'test')", orderID, "FILE-"+orderID, companyID); err != nil {
		t.Fatal(err)
	}
	u := User{ID: "attachment-test", Name: "附件测试", Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	upload := &AttachmentUpload{Name: "票据.png", Content: base64.StdEncoding.EncodeToString(tinyPNG)}
	settle := func(key string, request SettlementRequest) error {
		body := []byte(jsonText(request))
		r := httptest.NewRequest("POST", "/api/settle", bytes.NewReader(body))
		r.Header.Set("Idempotency-Key", key)
		_, err := a.mutate(r, u, body, func(tx *sql.Tx, operation string) (any, error) { return a.settle(tx, u, body, operation) })
		return err
	}
	deliver := SettlementRequest{Action: "deliver", Company: companyID, TicketNo: "T", Recipient: "R", DeliveredAt: "2026-09-28", TicketFile: upload, Orders: []SettlementOrder{{ID: orderID, Version: 1}}}
	if err := settle("attachment-deliver-01", deliver); err != nil {
		t.Fatal(err)
	}
	if err := settle("attachment-deliver-01", deliver); err != nil {
		t.Fatal("idempotent retry failed", err)
	}
	var reference string
	if err := db.QueryRow("SELECT ticket_attachment FROM orders WHERE id=?", orderID).Scan(&reference); err != nil || !strings.HasPrefix(reference, "file:") {
		t.Fatal("missing saved ticket", reference, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM attachment_files WHERE order_id=?", orderID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate ticket file", count, err)
	}
	fileID := strings.TrimPrefix(reference, "file:")
	request := httptest.NewRequest("GET", "/api/attachment-file?id="+fileID, nil)
	if err := a.attachmentFile(httptest.NewRecorder(), request, User{Role: "employee", Permissions: []string{"orders"}}); err == nil {
		t.Fatal("unauthorized attachment download allowed")
	}
	response := httptest.NewRecorder()
	if err := a.attachmentFile(response, request, u); err != nil || !bytes.Equal(response.Body.Bytes(), tinyPNG) {
		t.Fatal("saved attachment download mismatch", err)
	}
	broken := deliver
	broken.Orders = []SettlementOrder{{ID: orderID, Version: 1}}
	if err := settle("attachment-deliver-stale", broken); err == nil {
		t.Fatal("stale version accepted")
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM attachment_files WHERE order_id=?", orderID).Scan(&count); err != nil || count != 1 {
		t.Fatal("stale upload left a file", count, err)
	}
	receive := SettlementRequest{Action: "receive_full", Company: companyID, ReceivedAt: "2026-09-28", ReceiptFile: upload, Orders: []SettlementOrder{{ID: orderID, Version: 2}}}
	if err := settle("attachment-receive-01", receive); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM attachment_files WHERE order_id=?", orderID).Scan(&count); err != nil || count != 2 {
		t.Fatal("receipt file missing", count, err)
	}
	if !contains(backupTables, "attachment_files") {
		t.Fatal("attachment table omitted from database snapshot")
	}
	t.Setenv("BACKUP_DIR", t.TempDir())
	name, err := a.backup()
	if err != nil {
		t.Fatal("backup with attachments failed", err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("BACKUP_DIR"), name))
	if err != nil || !bytes.Contains(data, []byte(`"table":"attachment_files"`)) {
		t.Fatal("snapshot omitted attachment content", err)
	}
	adminConfig := cfg
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	restoreName := "tegu_go_test_af_" + id()
	if _, err := admin.Exec("CREATE DATABASE `" + restoreName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE `" + restoreName + "`")
	restoreConfig := cfg
	restoreConfig.DBName = restoreName
	target, err := sql.Open("mysql", restoreConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := initSchema(target); err != nil {
		t.Fatal(err)
	}
	restored := &App{db: target}
	if err := restored.restore(filepath.Join(os.Getenv("BACKUP_DIR"), name)); err != nil {
		t.Fatal("restore with attachments failed", err)
	}
	restoredResponse := httptest.NewRecorder()
	if err := restored.attachmentFile(restoredResponse, request, u); err != nil || !bytes.Equal(restoredResponse.Body.Bytes(), tinyPNG) {
		t.Fatal("restored attachment mismatch", err)
	}
}
