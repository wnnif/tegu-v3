package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var categories = []string{"开工", "包管", "安全防护架", "回填", "防水涂料", "室外防水", "丙纶", "维修"}
var allPermissions = []string{"orders", "orders_temporary", "order_delete", "customer_prices", "combo_pricing", "customers", "rates", "warehouse", "workers", "wages", "finance", "expenses", "users", "backups"}

type User struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Crafts      []string `json:"crafts"`
	Active      bool     `json:"active"`
	Version     int64    `json:"version"`

	// Resolved only from the current database relationship, never request JSON.
	companyCraftSources map[string]string
}
type App struct {
	db       *sql.DB
	backupMu sync.Mutex
	exportMu sync.Mutex
	loginMu  sync.Mutex
	attempts map[string][]time.Time
}
type failure struct {
	Code    int
	Message string
}

func (e failure) Error() string       { return e.Message }
func fail(code int, msg string) error { return failure{code, msg} }
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(b []byte) string  { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
func (u User) can(p string) bool {
	if u.Role == "admin" {
		return true
	}
	if u.Role == "operator" {
		return p != "backups" && (p != combinationPermission || contains(u.Permissions, p))
	}
	return u.Role == "employee" && p != "backups" && p != "users" && contains(u.Permissions, p)
}

// Crafts contains public craft IDs. One public grant applies to every company's
// configuration of that craft; module, price and wage permissions stay separate.
func (u User) canFillCraft(publicCraftID string) bool {
	return u.can("orders") && publicCraftID != "" && !contains(categories, publicCraftID) && (u.Role == "admin" || u.Role == "operator" || contains(u.Crafts, publicCraftID))
}

func (u User) canFillCompanyCraft(craftID string) bool {
	if u.Role == "admin" || u.Role == "operator" {
		return u.canFillCraft(craftID)
	}
	return u.canFillCraft(u.companyCraftSources[craftID])
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func respondError(w http.ResponseWriter, e error) {
	var f failure
	var me *mysql.MySQLError
	if errors.As(e, &f) {
		write(w, f.Code, map[string]any{"error": f.Message})
		return
	}
	if errors.As(e, &me) && (me.Number == 1062 || me.Number == 1213 || me.Number == 1205) {
		write(w, 409, map[string]any{"error": "资料已更新或重复，请刷新后核对重试"})
		return
	}
	log.Printf("request failed: %v", e)
	write(w, 500, map[string]any{"error": "服务暂时无法完成请求，请稍后重试"})
}
func decode(data []byte, v any) error {
	if e := json.Unmarshal(data, v); e != nil {
		return fail(400, "表单格式不正确")
	}
	return nil
}
func body(r *http.Request) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024+1))
	if e != nil || len(b) > 4*1024*1024 {
		return nil, fail(413, "提交内容过大")
	}
	return b, nil
}
func audit(tx *sql.Tx, u User, action, target string, detail any) error {
	_, e := tx.Exec("INSERT INTO audit(actor_id,actor_name,action,target_id,detail) VALUES(?,?,?,?,?)", u.ID, u.Name, action, target, jsonText(detail))
	return e
}
func (a *App) mutate(r *http.Request, u User, data []byte, fn func(*sql.Tx, string) (any, error)) (any, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 100 {
		return nil, fail(400, "缺少有效的提交编号")
	}
	fingerprint := hash(append([]byte(r.Method+" "+r.URL.Path+"\n"), data...))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for attempt := 0; ; attempt++ {
		result, err := a.mutateAttempt(ctx, u, key, fingerprint, r.URL.Path, data, fn)
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1213 || attempt >= 4 {
			return result, err
		}
		// MySQL rolls back the whole deadlocked transaction; retry with the same operation key.
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (a *App) mutateAttempt(ctx context.Context, u User, key, fingerprint, path string, data []byte, fn func(*sql.Tx, string) (any, error)) (any, error) {
	tx, e := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	// A replay must obey today's public grants before reading cached success.
	// This read uses the same transaction and never trusts submitted source IDs.
	if e = publicCraftMutationAllowed(tx, path, u, data); e != nil {
		return nil, e
	}
	_, e = tx.Exec("INSERT INTO requests(user_id,request_key,body_hash) VALUES(?,?,?) ON DUPLICATE KEY UPDATE request_key=request_key", u.ID, key, fingerprint)
	if e != nil {
		return nil, e
	}
	var before string
	var response sql.NullString
	e = tx.QueryRow("SELECT body_hash,response FROM requests WHERE user_id=? AND request_key=? FOR UPDATE", u.ID, key).Scan(&before, &response)
	if e != nil {
		return nil, e
	}
	if before != fingerprint {
		return nil, fail(409, "同一提交编号不能用于不同内容")
	}
	if response.Valid {
		var result any
		e = json.Unmarshal([]byte(response.String), &result)
		return result, e
	}
	result, e := fn(tx, key)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec("UPDATE requests SET response=? WHERE user_id=? AND request_key=?", jsonText(result), u.ID, key)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return result, nil
}
func (a *App) user(r *http.Request) (User, error) {
	var u User
	cookie, e := r.Cookie("tegu_session")
	if e != nil {
		return u, fail(401, "请先登录")
	}
	var permissions, crafts string
	e = a.db.QueryRow("SELECT u.id,u.username,u.name,u.role,u.permissions,u.crafts,u.active,u.version FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token=? AND s.expires_at>UTC_TIMESTAMP() AND u.active=1", hash([]byte(cookie.Value))).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &permissions, &crafts, &u.Active, &u.Version)
	if e != nil {
		return u, fail(401, "登录已失效，请重新登录")
	}
	json.Unmarshal([]byte(permissions), &u.Permissions)
	json.Unmarshal([]byte(crafts), &u.Crafts)
	return u, nil
}
func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	var input struct{ Username, Password string }
	b, e := body(r)
	if e != nil {
		return e
	}
	if e = decode(b, &input); e != nil {
		return e
	}
	username, e := normalizeUsername(input.Username)
	if e != nil {
		return e
	}
	if len(input.Password) > 72 {
		return fail(400, "账号或密码长度无效")
	}
	a.loginMu.Lock()
	for k, times := range a.attempts {
		if len(times) == 0 || time.Since(times[len(times)-1]) >= 10*time.Minute {
			delete(a.attempts, k)
		}
	}
	if len(a.attempts) >= 10000 {
		a.loginMu.Unlock()
		return fail(429, "登录请求较多，请稍后再试")
	}
	recent := []time.Time{}
	for _, t := range a.attempts[username] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		a.loginMu.Unlock()
		return fail(429, "登录尝试过多，请十分钟后再试")
	}
	a.attempts[username] = append(recent, time.Now())
	a.loginMu.Unlock()
	var uid, pw string
	e = a.db.QueryRow("SELECT id,password_hash FROM users WHERE username=? AND active=1", username).Scan(&uid, &pw)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(pw), []byte(input.Password)) != nil {
		return fail(401, "账号或密码错误")
	}
	token := id() + id()
	_, e = a.db.Exec("INSERT INTO sessions(token,user_id,expires_at) VALUES(?,?,DATE_ADD(UTC_TIMESTAMP(),INTERVAL 12 HOUR))", hash([]byte(token)), uid)
	if e != nil {
		return e
	}
	a.loginMu.Lock()
	delete(a.attempts, username)
	a.loginMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "tegu_session", Value: token, Path: "/", HttpOnly: true, Secure: env("COOKIE_SECURE", "false") == "true", SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	write(w, 200, map[string]any{"ok": true})
	return nil
}
func (a *App) route(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	var e error
	defer func() {
		if p := recover(); p != nil {
			log.Println(p)
			respondError(w, errors.New("unexpected failure"))
		} else if e != nil {
			respondError(w, e)
		}
	}()
	if r.Method != "GET" {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != env("PUBLIC_ORIGIN", "http://localhost:5173") {
			e = fail(403, "请求来源不正确")
			return
		}
	}
	if r.URL.Path == "/api/health" {
		e = a.db.PingContext(r.Context())
		if e == nil {
			write(w, 200, map[string]any{"ok": true, "database": "MySQL"})
		}
		return
	}
	if r.URL.Path == "/api/login" && r.Method == "POST" {
		e = a.login(w, r)
		return
	}
	var u User
	u, e = a.user(r)
	if e != nil {
		return
	}
	if r.URL.Path == "/api/me" {
		write(w, 200, map[string]any{"user": u, "categories": categories})
		return
	}
	if r.URL.Path == "/api/logout" && r.Method == "POST" {
		cookie, _ := r.Cookie("tegu_session")
		_, e = a.db.Exec("DELETE FROM sessions WHERE token=?", hash([]byte(cookie.Value)))
		http.SetCookie(w, &http.Cookie{Name: "tegu_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		write(w, 200, map[string]any{"ok": true})
		return
	}
	if r.URL.Path == "/api/attachment-file" && r.Method == "GET" {
		e = a.attachmentFile(w, r, u)
		return
	}
	if r.URL.Path == "/api/attachment-capability" && r.Method == "GET" {
		write(w, 200, map[string]any{"enabled": u.can("finance") && u.can(customerPricePermission)})
		return
	}
	if r.Method == "GET" {
		e = a.get(w, r, u)
		return
	}
	if r.Method != "POST" {
		e = fail(405, "不支持此操作")
		return
	}
	var b []byte
	b, e = body(r)
	if e != nil {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/maintenance/") {
		e = a.maintenance(w, r, u, b)
		return
	}
	// Check sensitive mutations before an idempotent replay can return cached money.
	if e = customerPriceMutationAllowed(r.URL.Path, u, b); e != nil {
		return
	}
	var result any
	result, e = a.mutate(r, u, b, func(tx *sql.Tx, key string) (any, error) { return a.post(tx, r.URL.Path, u, b, key) })
	if e == nil && (r.URL.Path == "/api/order" || r.URL.Path == "/api/project-combination") {
		// Cached idempotent responses are re-filtered under current permissions.
		result, e = orderSaveResultForUser(result, u)
	}
	if e == nil {
		write(w, 200, result)
	}
}
func main() {
	if err := runTegu(os.Args[1:], os.Stdout, openTeguCommandActions); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func (a *App) serve() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	schedulerDone := make(chan struct{})
	go func() { defer close(schedulerDone); a.scheduler(ctx) }()
	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: http.HandlerFunc(a.route), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		c, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		srv.Shutdown(c)
	}()
	log.Println("特固施工 Go API", srv.Addr)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		cancel()
		<-schedulerDone
		return errors.New("HTTP 服务启动失败；请检查监听地址与端口")
	}
	cancel()
	<-schedulerDone
	return nil
}
