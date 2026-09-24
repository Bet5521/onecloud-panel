package api

import (
	"net/http"
	"net/url"
	"strings"
)

// 请求体大小上限：普通 JSON 2 MiB（配置/描述类字段远小于此），
// 表单上传 80 MiB（自定义应用二进制上限 64 MiB，留余量给 multipart 开销）。
const (
	maxJSONBody   = 2 << 20
	maxUploadBody = 80 << 20
)

// contentSecurityPolicy 前端资源策略：仅同源脚本/样式/连接。
// 构建产物 index.html 无内联脚本，因此可保持 script-src 'self' 严格模式。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders 统一安全响应头（全站生效，含静态资源）。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if r.TLS != nil {
			// 仅在 TLS 连接上声明 HSTS，避免纯 HTTP 部署被浏览器误记强制跳转
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody 限制请求体大小，防超大请求体拖垮内存/磁盘。
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			limit := int64(maxJSONBody)
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				limit = maxUploadBody
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityChain 全站安全中间件链：请求体限额 → 安全响应头 → CSRF 同站校验。
// 面板根 handler（静态资源 / healthz / install.sh）与 API handler 共用，
// 确保安全响应头覆盖全站而非仅 /api/*。
func SecurityChain(next http.Handler) http.Handler {
	return limitBody(securityHeaders(csrfGuard(next)))
}

// mutatingMethod 判断是否为改变状态的请求方法。
func mutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// csrfGuard CSRF 防线（与 SameSite=Strict Cookie 互补）：
// 携带会话 Cookie 的跨站写请求会被 Origin/Referer 同站校验拦截。
// 非浏览器客户端（curl / Agent，不带 Origin 与 Referer）不受影响。
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutatingMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") && hasSessionCookie(r) {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "跨站请求被拒绝（CSRF 校验未通过）")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hasSessionCookie 请求是否携带会话 Cookie。
func hasSessionCookie(r *http.Request) bool {
	c, err := r.Cookie("ocp_session")
	return err == nil && c.Value != ""
}

// sameOrigin 校验 Origin（缺失时回退 Referer）与请求 Host 是否同站。
// Origin / Referer 均缺失时视为非浏览器客户端，放行（登录态仍由会话令牌保护）。
func sameOrigin(r *http.Request) bool {
	host := requestHost(r)
	if host == "" {
		return true
	}
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		return true
	}
	if src == "null" {
		return false
	}
	u, err := url.Parse(src)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// requestHost 请求的对外主机名（含端口）；反代场景下优先取 X-Forwarded-Host。
func requestHost(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
		// 多级代理取第一个
		if i := strings.Index(h, ","); i > 0 {
			h = strings.TrimSpace(h[:i])
		}
		return h
	}
	return r.Host
}
