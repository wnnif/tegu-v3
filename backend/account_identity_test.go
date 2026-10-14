package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeUsername(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		invalid     bool
	}{
		{" \tAdMiN\u3000", "admin", false},
		{strings.Repeat("账", 34), strings.Repeat("账", 34), false},
		{strings.Repeat("账", 100), strings.Repeat("账", 100), false},
		{strings.Repeat("账", 101), "", true},
		{" \t\r\n\u3000", "", true},
		{"", "", true},
		{string([]byte{0xff}), "", true},
	} {
		got, err := normalizeUsername(tc.input)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Fatalf("normalize %q: got %q, error %v", tc.input, got, err)
		}
	}
}

func accountIdentityRequest(a *App, method, path string, input any, cookie *http.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if input != nil {
		_ = json.NewEncoder(&body).Encode(input)
	}
	r := httptest.NewRequest(method, path, &body)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if method == http.MethodPost {
		r.Header.Set("Idempotency-Key", id())
	}
	w := httptest.NewRecorder()
	a.route(w, r)
	return w
}

func TestAdminUsernameNormalizationHTTPMySQL(t *testing.T) {
	const password = "synthetic-identity-only"
	t.Setenv("ADMIN_INITIAL_PASSWORD", password)
	t.Setenv("PUBLIC_ORIGIN", "http://localhost:5173")
	for _, tc := range []struct {
		name, input, stored string
		invalid             bool
	}{
		{"trim_case", " \tAdMiN\u3000", "admin", false},
		{"multibyte", strings.Repeat("账", 34), strings.Repeat("账", 34), false},
		{"unicode_limit", strings.Repeat("账", 100), strings.Repeat("账", 100), false},
		{"too_long", strings.Repeat("账", 101), "admin", true},
		{"blank", " \t\r\n\u3000", "admin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := maintenanceFaultDB(t)
			a.attempts = map[string][]time.Time{}
			if err := a.bootstrap(); err != nil {
				t.Fatal(err)
			}
			login := func(username string) *httptest.ResponseRecorder {
				return accountIdentityRequest(a, "POST", "/api/login", map[string]string{"username": username, "password": password}, nil)
			}
			w := login("admin")
			if w.Code != 200 || len(w.Result().Cookies()) != 1 {
				t.Fatal("initial login", w.Code, w.Body.String())
			}
			cookie := w.Result().Cookies()[0]
			w = accountIdentityRequest(a, "GET", "/api/me", nil, cookie)
			var me struct {
				User User `json:"user"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil {
				t.Fatal("initial account", w.Body.String())
			}
			me.User.Username, me.User.Crafts = tc.input, []string{}
			w = accountIdentityRequest(a, "POST", "/api/user", me.User, cookie)
			if tc.invalid {
				if w.Code != 400 {
					t.Fatal("invalid identity accepted", w.Code, w.Body.String())
				}
				if w = accountIdentityRequest(a, "GET", "/api/me", nil, cookie); w.Code != 200 {
					t.Fatal("rejected edit revoked the valid session", w.Code)
				}
				maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM audit", 0)
				maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM requests", 0)
				if w = login(tc.input); w.Code != 400 {
					t.Fatal("save/login validation differs", w.Code)
				}
			} else {
				if w.Code != 200 {
					t.Fatal("valid identity save", w.Code, w.Body.String())
				}
				if w = accountIdentityRequest(a, "GET", "/api/me", nil, cookie); w.Code != 401 {
					t.Fatal("old session survived account edit", w.Code)
				}
				maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM sessions", 0)
				w = login(tc.input)
				if w.Code != 200 {
					t.Fatal("edited sole admin cannot log in", w.Code, w.Body.String())
				}
				newCookie := w.Result().Cookies()[0]
				if w = accountIdentityRequest(a, "GET", "/api/backup-settings", nil, newCookie); w.Code != 200 {
					t.Fatal("sole admin lost maintenance access", w.Code, w.Body.String())
				}
			}
			var stored string
			if err := a.db.QueryRow("SELECT username FROM users WHERE role='admin' AND active=1").Scan(&stored); err != nil || stored != tc.stored {
				t.Fatal("stored identity differs", stored, err)
			}
			maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM users WHERE role='admin' AND active=1", 1)
			t.Log("sole administrator retains a valid login; rejected edits retain session and roll back audit/request writes")
		})
	}
}

func TestNewUsernameNormalizationHTTPMySQL(t *testing.T) {
	a, _ := maintenanceFaultDB(t)
	a.attempts = map[string][]time.Time{}
	t.Setenv("ADMIN_INITIAL_PASSWORD", "synthetic-identity-only")
	if err := a.bootstrap(); err != nil {
		t.Fatal(err)
	}
	login := func(username, password string) *httptest.ResponseRecorder {
		return accountIdentityRequest(a, "POST", "/api/login", map[string]string{"username": username, "password": password}, nil)
	}
	w := login("admin", "synthetic-identity-only")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	adminCookie := w.Result().Cookies()[0]
	input := map[string]any{"username": " \tWoRKeR\u3000", "name": "合成员工", "password": "a", "role": "employee", "permissions": []string{}, "crafts": []string{}, "active": true}
	w = accountIdentityRequest(a, "POST", "/api/user", input, adminCookie)
	if w.Code != 200 {
		t.Fatal("new account", w.Code, w.Body.String())
	}
	if w = login(" \tWORKER\u3000", "a"); w.Code != 200 {
		t.Fatal("new normalized login", w.Code, w.Body.String())
	}
	maintenanceFaultCount(t, a.db, "SELECT COUNT(*) FROM users WHERE username='worker'", 1)
	input["username"] = " AdMiN "
	if w = accountIdentityRequest(a, "POST", "/api/user", input, adminCookie); w.Code != 409 {
		t.Fatal("normalized duplicate identity accepted", w.Code, w.Body.String())
	}
	if w = accountIdentityRequest(a, "GET", "/api/me", nil, adminCookie); w.Code != 200 {
		t.Fatal("duplicate rejected but admin session revoked", w.Code)
	}
}
