package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"
)

const maxAttachmentBytes = 2 * 1024 * 1024

type AttachmentUpload struct {
	Name    string `json:"name"`
	Content string `json:"content_base64"`
}

type validatedAttachment struct {
	name, contentType, content, checksum string
	size                                 int
}

func validateAttachment(upload *AttachmentUpload) (*validatedAttachment, error) {
	if upload == nil {
		return nil, nil
	}
	name := strings.TrimSpace(upload.Name)
	if name == "" || len([]rune(name)) > 200 || strings.ContainsAny(name, "\\/\r\n\x00") {
		return nil, fail(400, "附件文件名无效")
	}
	if len(upload.Content) == 0 || len(upload.Content) > base64.StdEncoding.EncodedLen(maxAttachmentBytes) {
		return nil, fail(413, "附件不能超过2 MB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(upload.Content)
	if err != nil || len(data) == 0 || len(data) > maxAttachmentBytes {
		return nil, fail(400, "附件内容无效或超过2 MB")
	}
	contentType := http.DetectContentType(data)
	allowed := map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "application/pdf": true}
	if !allowed[contentType] {
		return nil, fail(400, "附件仅支持PNG、JPG、WEBP图片或PDF")
	}
	sum := sha256.Sum256(data)
	return &validatedAttachment{name: name, contentType: contentType, content: upload.Content, checksum: hex.EncodeToString(sum[:]), size: len(data)}, nil
}

func storeAttachment(tx *sql.Tx, orderID, kind string, upload *validatedAttachment, actor string) (string, error) {
	if upload == nil {
		return "", nil
	}
	fileID := id()
	_, err := tx.Exec("INSERT INTO attachment_files(id,order_id,kind,file_name,content_type,size_bytes,content_hash,content_data,created_by) VALUES(?,?,?,?,?,?,?,?,?)", fileID, orderID, kind, upload.name, upload.contentType, upload.size, upload.checksum, upload.content, actor)
	if err != nil {
		return "", err
	}
	return "file:" + fileID, nil
}

var attachmentID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (a *App) attachmentFile(w http.ResponseWriter, r *http.Request, u User) error {
	if !u.can("finance") {
		return fail(403, "无财务附件下载权限")
	}
	if err := requireCustomerPrices(u); err != nil {
		return err
	}
	fileID := r.URL.Query().Get("id")
	if !attachmentID.MatchString(fileID) {
		return fail(400, "附件编号无效")
	}
	var name, contentType, content, checksum string
	var size int
	err := a.db.QueryRow("SELECT file_name,content_type,content_data,content_hash,size_bytes FROM attachment_files WHERE id=?", fileID).Scan(&name, &contentType, &content, &checksum, &size)
	if err == sql.ErrNoRows {
		return fail(404, "附件不存在")
	}
	if err != nil {
		return err
	}
	data, err := base64.StdEncoding.Strict().DecodeString(content)
	if err != nil || len(data) != size {
		return fmt.Errorf("attachment %s content invalid", fileID)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != checksum {
		return fmt.Errorf("attachment %s checksum invalid", fileID)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}
