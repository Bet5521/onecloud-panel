package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withChain(next http.Handler) http.Handler {
	return limitBody(securityHeaders(csrfGuard(next)))
}

func TestSecurityHeadersPresent(t *testing.T) {
	h := withChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))

	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Content-Security-Policy":    "default-src 'self'",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for k, prefix := range want {
		if v := rec.Header().Get(k); !strings.HasPrefix(v, prefix) {
			t.Fatalf("响应头 %s 期望前缀 %q，实际 %q", k, prefix, v)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("纯 HTTP 不应返回 HSTS")
	}
}

func TestBodyLimitRejectsOversize(t *testing.T) {
	h := withChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(strings.Repeat("a", maxJSONBody+1024)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应被拒绝，实际 %d", rec.Code)
	}
}

func TestCSRFRejectsCrossOriginWithCookie(t *testing.T) {
	h := withChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Cookie", "ocp_session=abc")
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨站写请求应被拒绝，实际 %d", rec.Code)
	}

	// 同站 Origin 应放行
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req2.Header.Set("Cookie", "ocp_session=abc")
	req2.Header.Set("Origin", "http://panel.local:8080")
	req2.Host = "panel.local:8080"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("同站请求应放行，实际 %d", rec2.Code)
	}

	// 无 Origin/Referer 的非浏览器客户端不受影响
	req3 := httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("非浏览器客户端应放行，实际 %d", rec3.Code)
	}
}

func TestCSRFAllowsGetAndNoCookie(t *testing.T) {
	h := withChain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 请求不应受 CSRF 影响，实际 %d", rec.Code)
	}
}
