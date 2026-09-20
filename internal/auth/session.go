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
	store *store.Store
	ttl   time.Duration
}

// NewManager 创建会话管理器。
func NewManager(s *store.Store, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{store: s, ttl: ttl}
}

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
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
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
