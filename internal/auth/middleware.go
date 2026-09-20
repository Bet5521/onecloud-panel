package auth

import (
	"context"
	"encoding/json"
	"net/http"
)

// Middleware 提供会话解析与权限校验。
type Middleware struct {
	sessions *Manager
}

// NewMiddleware 创建鉴权中间件。
func NewMiddleware(m *Manager) *Middleware { return &Middleware{sessions: m} }

// RequireAuth 要求有效会话，并将身份注入上下文。
func (mw *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, err := mw.sessions.Resolve(r)
		if err != nil || id == nil {
			writeAuthError(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		ctx := id.WithContext(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission 要求指定权限点（须在 RequireAuth 之后使用）。
func RequirePermission(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := FromContext(r.Context())
		if id == nil {
			writeAuthError(w, http.StatusUnauthorized, "未登录")
			return
		}
		if !id.Has(perm) {
			writeAuthError(w, http.StatusForbidden, "无操作权限")
			return
		}
		next(w, r)
	}
}

// IdentityFrom 供处理器读取身份。
func IdentityFrom(ctx context.Context) *Identity { return FromContext(ctx) }

func writeAuthError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
