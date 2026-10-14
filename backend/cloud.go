package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupSettings struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	Secret    string `json:"secret"`
	HasSecret bool   `json:"has_secret"`
	Retain    int    `json:"retain"`
	Interval  int    `json:"interval"`
}

func (a *App) backupSettings() (BackupSettings, error) {
	s := BackupSettings{Region: "us-east-1", Prefix: "tegu", Retain: 168, Interval: 60}
	var raw string
	e := a.db.QueryRow("SELECT value FROM settings WHERE setting_key='backup'").Scan(&raw)
	if e != nil {
		if e == sql.ErrNoRows {
			return s, nil
		}
		return s, e
	}
	e = json.Unmarshal([]byte(raw), &s)
	return s, e
}
func backupKey() ([]byte, error) {
	dir := env("DATA_DIR", "data")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	file := filepath.Join(dir, "backup.key")
	if b, e := os.ReadFile(file); e == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("备份密钥长度错误")
		}
		return b, nil
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		return os.ReadFile(file)
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	return b, nil
}
func encryptSecret(s string) (string, error) {
	k, e := backupKey()
	if e != nil {
		return "", e
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(g.Seal(n, n, []byte(s), []byte("tegu-cloud-v1"))), nil
}
func decryptSecret(s string) (string, error) {
	k, e := backupKey()
	if e != nil {
		return "", e
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	raw, e := base64.StdEncoding.DecodeString(s)
	if e != nil || len(raw) < g.NonceSize() {
		return "", fmt.Errorf("云存储凭据无效")
	}
	plain, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("tegu-cloud-v1"))
	return string(plain), e
}
func (a *App) saveBackupSettings(u User, b []byte) error {
	if !u.can("backups") {
		return fail(403, "仅系统管理员可设置备份")
	}
	var s BackupSettings
	if e := decode(b, &s); e != nil {
		return e
	}
	previous, e := a.backupSettings()
	if e != nil {
		return e
	}
	if s.Retain < 24 || s.Retain > 8760 {
		return fail(400, "本地保留份数应为24至8760")
	}
	if s.Interval == 0 {
		s.Interval = 60
	}
	if !contains([]string{"15", "30", "60", "360", "1440"}, fmt.Sprint(s.Interval)) {
		return fail(400, "备份周期无效")
	}
	if s.Secret == "" {
		s.Secret = previous.Secret
	} else {
		s.Secret, e = encryptSecret(s.Secret)
		if e != nil {
			return e
		}
	}
	if s.Enabled {
		endpoint, e := url.Parse(s.Endpoint)
		if e != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.Trim(endpoint.Path, "/") != "" {
			return fail(400, "请输入HTTPS云存储服务地址，不含桶名或路径")
		}
		if s.Bucket == "" || strings.ContainsAny(s.Bucket, "/\\ ?#") || s.AccessKey == "" || s.Secret == "" || s.Region == "" {
			return fail(400, "请填写桶名、区域及云存储密钥")
		}
	}
	s.HasSecret = false
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO settings(setting_key,value) VALUES('backup',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", jsonText(s)); e != nil {
		return e
	}
	if e = audit(tx, u, "更新备份设置", "backup", map[string]any{"enabled": s.Enabled, "endpoint": s.Endpoint, "bucket": s.Bucket, "retain": s.Retain, "interval": s.Interval}); e != nil {
		return e
	}
	return tx.Commit()
}
func mac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func s3Request(s BackupSettings, secret, key string, body io.Reader, length int64, payloadHash string, now time.Time) (*http.Request, error) {
	target, e := url.Parse(strings.TrimRight(s.Endpoint, "/") + "/" + s.Bucket + "/" + strings.Trim(s.Prefix, "/") + "/" + key)
	if e != nil {
		return nil, e
	}
	// Set Path separately so reserved characters in object names are always escaped.
	target.Path = "/" + s.Bucket + "/" + strings.Trim(strings.Trim(s.Prefix, "/")+"/"+key, "/")
	target.RawQuery = ""
	target.Fragment = ""
	req, e := http.NewRequest("PUT", target.String(), body)
	if e != nil {
		return nil, e
	}
	req.ContentLength = length
	stamp, day := now.UTC().Format("20060102T150405Z"), now.UTC().Format("20060102")
	headers := "host:" + target.Host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + stamp + "\n"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := "PUT\n" + target.EscapedPath() + "\n\n" + headers + "\n" + signed + "\n" + payloadHash
	scope := day + "/" + s.Region + "/s3/aws4_request"
	keyDate := mac([]byte("AWS4"+secret), day)
	keyRegion := mac(keyDate, s.Region)
	keyService := mac(keyRegion, "s3")
	keySign := mac(keyService, "aws4_request")
	signature := hex.EncodeToString(mac(keySign, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hash([]byte(canonical))))
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	return req, nil
}
func cloudPut(s BackupSettings, key string, body io.Reader, size int64, sum string) error {
	secret, e := decryptSecret(s.Secret)
	if e != nil {
		return fmt.Errorf("无法解密云存储凭据，请重新填写密钥: %w", e)
	}
	req, e := s3Request(s, secret, key, body, size, sum, time.Now())
	if e != nil {
		return e
	}
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("云存储重定向被拒绝，请核对Endpoint及区域")
	}}
	res, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("云备份连接失败")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("云备份上传失败，HTTP %d，请核对权限与区域", res.StatusCode)
	}
	return nil
}
func uploadBackup(s BackupSettings, file string) error {
	sum, e := fileHash(file)
	if e != nil {
		return e
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	name := filepath.Base(file)
	if e = cloudPut(s, name, f, info.Size(), sum); e != nil {
		return e
	}
	return cloudPut(s, name+".sha256", bytes.NewReader([]byte(sum)), int64(len(sum)), hash([]byte(sum)))
}
func pruneBackups(dir string, retain int) error {
	if retain < 24 {
		return nil
	}
	files, e := filepath.Glob(filepath.Join(dir, "tegu-go-*.ndjson"))
	if e != nil {
		return e
	}
	sort.Strings(files)
	if len(files) <= retain {
		return nil
	}
	keep := map[string]bool{}
	days, months := map[string]bool{}, map[string]bool{}
	for i := len(files) - 1; i >= 0; i-- {
		name := strings.TrimPrefix(filepath.Base(files[i]), "tegu-go-")
		if len(name) < 8 {
			continue
		}
		day, month := name[:8], name[:6]
		if !days[day] && len(days) < 35 {
			keep[files[i]] = true
			days[day] = true
		}
		if !months[month] && len(months) < 12 {
			keep[files[i]] = true
			months[month] = true
		}
	}
	for _, f := range files[:len(files)-retain] {
		if keep[f] {
			continue
		}
		// Delete only completed, checksummed snapshots created by this application.
		if _, e = os.Stat(f + ".sha256"); e != nil {
			continue
		}
		if e = os.Remove(f); e != nil {
			return e
		}
		if e = os.Remove(f + ".sha256"); e != nil {
			return e
		}
	}
	return nil
}
