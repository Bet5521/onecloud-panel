package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"

	"onecloud-panel/internal/store"
)

// CookieName 会话 Cookie 名。
const CookieName = "ocp_session"

// DefaultTTL 会话有效期。
const DefaultTTL = 12 * time.Hour

// Manager 会话管理。
type Manager struct {
	store  *store.Store
	ttl    time.Duration
	secure bool // 启用 HTTPS 时置位，Cookie 带 Secure 属性
}

// NewManager 创建会话管理器。
func NewManager(s *store.Store, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{store: s, ttl: ttl}
}

// SetSecure 设置会话 Cookie 的 Secure 属性（面板启用 HTTPS 后应置位）。
func (m *Manager) SetSecure(v bool) { m.secure = v }

// Issue 生成令牌、落库并写入 Cookie。
func (m *Manager) Issue(w http.ResponseWriter, userID int64, ip, ua string) error {
	token, hash, err := newToken()
	if err != nil {
		return err
	}
	if err := m.store.CreateSession(hash, userID, ip, ua, m.ttl); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(m.ttl),
	})
	return nil
}

// Resolve 从请求解析有效身份；无效返回 nil。
func (m *Manager) Resolve(r *http.Request) (*Identity, string, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, "", nil
	}
	hash := hashToken(c.Value)
	uid, err := m.store.SessionUser(hash)
	if err != nil {
		return nil, "", nil
	}
	if uid < 0 {
		return nil, "", nil
	}
	u, err := m.store.UserByID(uid)
	if err != nil || u.Status != "active" {
		return nil, "", nil
	}
	role, err := m.store.RoleByID(u.RoleID)
	if err != nil {
		return nil, "", nil
	}
	perms, err := m.store.RolePermissions(u.RoleID)
	if err != nil {
		return nil, "", nil
	}
	_ = m.store.TouchSession(hash)
	return &Identity{User: u, RoleCode: role.Code, Perms: perms}, hash, nil
}

// Revoke 吊销请求所携带的会话。
func (m *Manager) Revoke(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	return m.store.DeleteSession(hashToken(c.Value))
}

// ClearCookie 清除浏览器会话 Cookie。
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// SIDOf 由会话哈希导出对外可见的会话标识（哈希前缀）。
// 只暴露 SHA-256 前缀，无法反推令牌，可用于列表展示与定向注销。
func SIDOf(tokenHash string) string {
	if len(tokenHash) <= 16 {
		return tokenHash
	}
	return tokenHash[:16]
}

// CurrentSID 返回请求所携带会话的对外标识；无有效会话返回空串。
func (m *Manager) CurrentSID(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return SIDOf(hashToken(c.Value))
}

// ListSessions 列出用户全部有效会话。
func (m *Manager) ListSessions(userID int64) ([]store.SessionInfo, error) {
	return m.store.ListUserSessions(userID)
}

// RevokeSession 定向注销用户自己的某个会话，返回是否命中。
func (m *Manager) RevokeSession(userID int64, sid string) (bool, error) {
	n, err := m.store.DeleteUserSessionByID(userID, sid)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RevokeOtherSessions 注销该用户除 keepSID 外的全部会话，返回注销数量。
func (m *Manager) RevokeOtherSessions(userID int64, keepSID string) (int, error) {
	sessions, err := m.store.ListUserSessions(userID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, s := range sessions {
		if s.ID == keepSID {
			continue
		}
		if n, err := m.store.DeleteUserSessionByID(userID, s.ID); err == nil && n > 0 {
			removed += int(n)
		}
	}
	return removed, nil
}

func newToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
