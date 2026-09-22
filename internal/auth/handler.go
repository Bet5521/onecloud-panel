package auth

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"onecloud-panel/internal/store"
)

// LoginEvent 登录结果事件（供审计模块订阅）。
type LoginEvent struct {
	Success  bool
	Username string
	IP       string
	Detail   string
}

// AuditHook 登录审计回调。
type AuditHook func(LoginEvent)

// Handler 认证相关 HTTP 处理器。
type Handler struct {
	store    *store.Store
	sessions *Manager
	limiter  *LoginLimiter
	onLogin  AuditHook
}

// NewHandler 创建认证处理器。
func NewHandler(s *store.Store, m *Manager, l *LoginLimiter, hook AuditHook) *Handler {
	return &Handler{store: s, sessions: m, limiter: l, onLogin: hook}
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login POST /api/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	ip := ClientIP(r)

	if !h.limiter.Allow(req.Username) || !h.limiter.Allow(ip) {
		h.fire(LoginEvent{false, req.Username, ip, "登录限流"})
		writeAuthError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}

	u, err := h.store.UserByUsername(req.Username)
	if err != nil {
		h.limiter.Fail(req.Username)
		h.limiter.Fail(ip)
		h.fire(LoginEvent{false, req.Username, ip, "用户不存在"})
		writeAuthError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	ok, err := VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok || u.Status != "active" {
		h.limiter.Fail(req.Username)
		h.limiter.Fail(ip)
		reason := "密码错误"
		if u.Status != "active" {
			reason = "账号已禁用"
		}
		h.fire(LoginEvent{false, req.Username, ip, reason})
		writeAuthError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	if err := h.sessions.Issue(w, u.ID, ip, r.UserAgent()); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "会话创建失败")
		return
	}
	h.limiter.Reset(req.Username)
	h.limiter.Reset(ip)
	h.fire(LoginEvent{true, req.Username, ip, ""})
	h.writeMe(w, u)
}

// Logout POST /api/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	_ = h.sessions.Revoke(r)
	ClearCookie(w)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Me GET /api/auth/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	id := FromContext(r.Context())
	if id == nil {
		writeAuthError(w, http.StatusUnauthorized, "未登录")
		return
	}
	h.writeMe(w, id.User)
}

func (h *Handler) writeMe(w http.ResponseWriter, u *store.User) {
	role, err := h.store.RoleByID(u.RoleID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "角色查询失败")
		return
	}
	perms, err := h.store.RolePermissions(u.RoleID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "权限查询失败")
		return
	}
	list := make([]string, 0, len(perms))
	for p := range perms {
		list = append(list, p)
	}
	panelName := "OneCloud Panel"
	if v, ok, err := h.store.GetSetting("panel_name"); err == nil && ok && v != "" {
		panelName = v
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	channelID := int64(0)
	if u.NotifyChannelID != nil {
		channelID = *u.NotifyChannelID
	}
	// 每用户个性化接收标识（WxPusher UID / 接收手机号 / Webhook 用户标识等），
	// 必须回显，否则「个人资料 → 通知方式」无法回填已保存的标识。
	notifyTarget := ""
	if u.NotifyTarget != nil {
		notifyTarget = *u.NotifyTarget
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                u.ID,
		"username":          u.Username,
		"real_name":         u.RealName,
		"phone":             u.Phone,
		"notify_method":     u.NotifyMethod,
		"notify_email":      u.NotifyEmail,
		"notify_sms_phone":  u.NotifySMSPhone,
		"notify_channel_id": channelID,
		"notify_target":     notifyTarget,
		"role":              role.Code,
		"role_name":         role.Name,
		"permissions":       list,
		"panel_name":        panelName,
	})
}

func (h *Handler) fire(e LoginEvent) {
	if h.onLogin != nil {
		h.onLogin(e)
	}
}

// ClientIP 从请求中提取客户端 IP（兼容反向代理 XFF）。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
