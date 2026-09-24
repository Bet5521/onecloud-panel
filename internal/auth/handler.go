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
	captcha  *CaptchaStore
	onLogin  AuditHook
}

// NewHandler 创建认证处理器。
func NewHandler(s *store.Store, m *Manager, l *LoginLimiter, hook AuditHook) *Handler {
	return &Handler{store: s, sessions: m, limiter: l, captcha: NewCaptchaStore(), onLogin: hook}
}

type loginReq struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	CaptchaID   string `json:"captcha_id"`
	CaptchaCode string `json:"captcha_code"`
}

// captchaThreshold 窗口内失败达到该次数后要求验证码。
const captchaThreshold = 2

// Captcha GET /api/auth/captcha — 生成新验证码（公开路由）。
func (h *Handler) Captcha(w http.ResponseWriter, r *http.Request) {
	id, image := h.captcha.Generate()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"captcha_id": id, "image": image})
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

	// 短时间内多次登录失败：要求图形验证码（验证码错误同样计入失败，防绕过）
	if h.limiter.FailCount(req.Username) >= captchaThreshold || h.limiter.FailCount(ip) >= captchaThreshold {
		if !h.captcha.Verify(req.CaptchaID, req.CaptchaCode) {
			h.limiter.Fail(req.Username)
			h.limiter.Fail(ip)
			h.fire(LoginEvent{false, req.Username, ip, "验证码错误或缺失"})
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "请输入验证码后重试", "code": "captcha_required"})
			return
		}
	}

	u, err := h.store.UserByUsername(req.Username)
	if err != nil {
		// 等时处理：对不存在的用户同样跑一次 argon2id，避免用响应耗时枚举账号
		DummyVerify(req.Password)
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
	h.sessions.ClearCookie(w)
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
	// 本人订阅的通知事件列表
	notifyEvents := []string{}
	if evs, err := h.store.ListUserEvents(u.ID); err == nil && len(evs) > 0 {
		notifyEvents = evs
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
		"notify_events":     notifyEvents,
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

// ClientIP 从请求中提取客户端 IP。
//
// X-Forwarded-For 只有在直连对端为回环/内网地址（即请求穿过了可信反向代理）时才采信；
// 否则任何人都能伪造该头绕过登录限流并污染审计日志。采信时取最左侧第一个合法 IP。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !IsTrustedProxyPeer(host) {
		return host
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		part := xff
		if idx := strings.Index(xff, ","); idx > 0 {
			part = xff[:idx]
		}
		if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
			return ip.String()
		}
		// 非法 IP（伪造/异常头）：忽略该头，回退到直连地址
	}
	return host
}

// IsTrustedProxyPeer 判断 IP 是否为回环/内网地址（反向代理通常部署在本机或内网）。
func IsTrustedProxyPeer(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
