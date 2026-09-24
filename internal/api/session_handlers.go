package api

import (
	"net/http"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
)

// sessionDTO 登录设备/会话视图。
type sessionDTO struct {
	ID        string `json:"id"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	ExpiresAt int64  `json:"expires_at"`
	Current   bool   `json:"current"`
}

// GET /api/auth/sessions — 本人全部有效登录会话（登录设备管理）。
func (a *API) listMySessions(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	if !ok || a.sessions == nil {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}
	items, err := a.sessions.ListSessions(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "会话查询失败")
		return
	}
	cur := a.sessions.CurrentSID(r)
	out := make([]sessionDTO, 0, len(items))
	for _, s := range items {
		out = append(out, sessionDTO{
			ID: s.ID, IP: s.IP, UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt, LastSeen: s.LastSeen, ExpiresAt: s.ExpiresAt,
			Current: s.ID == cur,
		})
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// DELETE /api/auth/sessions/{id} — 注销本人指定会话。
func (a *API) revokeMySession(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	if !ok || a.sessions == nil {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}
	sid := strings.TrimSpace(r.PathValue("id"))
	if sid == "" || len(sid) > 32 {
		writeError(w, http.StatusBadRequest, "会话标识非法")
		return
	}
	hit, err := a.sessions.RevokeSession(uid, sid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "注销失败")
		return
	}
	if !hit {
		writeError(w, http.StatusNotFound, "会话不存在或已过期")
		return
	}
	// 注销的是当前会话时同步清除 Cookie，避免浏览器继续持有失效令牌
	if sid == a.sessions.CurrentSID(r) {
		a.sessions.ClearCookie(w)
	}
	a.audit.Record(r, "auth", "session_revoke", "session", sid, audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// POST /api/auth/sessions/revoke-others — 注销本人除当前外的全部会话。
func (a *API) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	if !ok || a.sessions == nil {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}
	n, err := a.sessions.RevokeOtherSessions(uid, a.sessions.CurrentSID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "注销失败")
		return
	}
	a.audit.Record(r, "auth", "session_revoke_others", "user",
		strconv.FormatInt(uid, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]any{"status": "ok", "revoked": n})
}
