package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/store"
)

func newTestHandler(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	mgr := NewManager(s, time.Hour)
	limiter := NewLoginLimiter(5, time.Minute)
	h := NewHandler(s, mgr, limiter, nil)
	mw := NewMiddleware(mgr)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", h.Login)
	mux.Handle("/api/auth/logout", mw.RequireAuth(http.HandlerFunc(h.Logout)))
	mux.Handle("/api/auth/me", mw.RequireAuth(http.HandlerFunc(h.Me)))
	mux.Handle("/api/protected",
		mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	mux.Handle("/api/perm-app-write",
		mw.RequireAuth(RequirePermission(store.PermAppWrite, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	mux.Handle("/api/perm-user-write",
		mw.RequireAuth(RequirePermission(store.PermUserWrite, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	return s, mux
}

func createUserWithRole(t *testing.T, s *store.Store, username, roleCode string) {
	t.Helper()
	var roleID int64
	if err := s.DB.QueryRow(`SELECT id FROM roles WHERE code = ?`, roleCode).Scan(&roleID); err != nil {
		t.Fatalf("role: %v", err)
	}
	hash, err := HashPassword("Passw0rd!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := s.CreateUser(username, hash, roleID); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any, cookies ...*http.Cookie) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	// 提取 Set-Cookie
	out := map[string]string{}
	for _, sc := range w.Result().Cookies() {
		out[sc.Name] = sc.Value
	}
	return w, out
}

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash prefix: %s", hash)
	}
	ok, err := VerifyPassword("secret", hash)
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	ok, _ = VerifyPassword("wrong", hash)
	if ok {
		t.Fatal("wrong password verified")
	}
}

func TestSessionAuthFlow(t *testing.T) {
	s, h := newTestHandler(t)
	createUserWithRole(t, s, "alice", "admin")

	// 未登录访问受保护资源
	w, _ := doJSON(t, h, "GET", "/api/protected", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session code = %d, want 401", w.Code)
	}

	// 错误密码
	w, _ = doJSON(t, h, "POST", "/api/auth/login", map[string]string{"username": "alice", "password": "bad"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad password code = %d, want 401", w.Code)
	}

	// 正确登录
	w, cookies := doJSON(t, h, "POST", "/api/auth/login", map[string]string{"username": "alice", "password": "Passw0rd!"})
	if w.Code != http.StatusOK {
		t.Fatalf("login code = %d, want 200", w.Code)
	}
	token, ok := cookies[CookieName]
	if !ok || token == "" {
		t.Fatal("登录未下发会话 Cookie")
	}

	// 携带会话访问
	c := &http.Cookie{Name: CookieName, Value: token}
	w, _ = doJSON(t, h, "GET", "/api/protected", nil, c)
	if w.Code != http.StatusOK {
		t.Fatalf("authed code = %d, want 200", w.Code)
	}

	// 登出后会话失效
	w, _ = doJSON(t, h, "POST", "/api/auth/logout", nil, c)
	if w.Code != http.StatusOK {
		t.Fatalf("logout code = %d", w.Code)
	}
	w, _ = doJSON(t, h, "GET", "/api/protected", nil, c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout code = %d, want 401", w.Code)
	}
}

func TestRolePermissionEnforcement(t *testing.T) {
	s, h := newTestHandler(t)
	createUserWithRole(t, s, "v", "viewer")
	createUserWithRole(t, s, "o", "operator")
	createUserWithRole(t, s, "a", "admin")

	login := func(username string) *http.Cookie {
		w, cookies := doJSON(t, h, "POST", "/api/auth/login",
			map[string]string{"username": username, "password": "Passw0rd!"})
		if w.Code != http.StatusOK {
			t.Fatalf("login %s code %d", username, w.Code)
		}
		return &http.Cookie{Name: CookieName, Value: cookies[CookieName]}
	}

	check := func(c *http.Cookie, path string) int {
		w, _ := doJSON(t, h, "GET", path, nil, c)
		return w.Code
	}

	// viewer：有 app:read 无 app:write / user:write
	if code := check(login("v"), "/api/perm-app-write"); code != http.StatusForbidden {
		t.Fatalf("viewer app-write = %d, want 403", code)
	}
	if code := check(login("v"), "/api/perm-user-write"); code != http.StatusForbidden {
		t.Fatalf("viewer user-write = %d, want 403", code)
	}
	// operator：有 app:write 无 user:write
	if code := check(login("o"), "/api/perm-app-write"); code != http.StatusOK {
		t.Fatalf("operator app-write = %d, want 200", code)
	}
	if code := check(login("o"), "/api/perm-user-write"); code != http.StatusForbidden {
		t.Fatalf("operator user-write = %d, want 403", code)
	}
	// admin：全部放行
	if code := check(login("a"), "/api/perm-user-write"); code != http.StatusOK {
		t.Fatalf("admin user-write = %d, want 200", code)
	}
}
