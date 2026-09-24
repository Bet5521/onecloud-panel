package api

import (
	"net/http"
	"strings"
	"sync"

	"onecloud-panel/internal/auth"
)

// GET /api/system/status — 初始化状态（公开）
func (a *API) systemStatus(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.CountUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "状态查询失败")
		return
	}
	writeJSON(w, map[string]bool{"initialized": n > 0})
}

type setupReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setupMu 串行化首次初始化：未加锁时两个并发请求都能通过「尚无用户」检查，
// 同时创建两个管理员账号（面板为单进程，进程内互斥即足够）。
var setupMu sync.Mutex

// POST /api/setup — 首次初始化，创建管理员（公开，仅一次）
func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	setupMu.Lock()
	defer setupMu.Unlock()
	n, err := a.store.CountUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "状态查询失败")
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "面板已初始化")
		return
	}
	var req setupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 32 {
		writeError(w, http.StatusBadRequest, "用户名长度需为 3-32")
		return
	}
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		writeError(w, http.StatusBadRequest, "密码长度至少 8 位")
		return
	}

	var adminRoleID int64
	if err := a.store.DB.QueryRow(`SELECT id FROM roles WHERE code='admin'`).Scan(&adminRoleID); err != nil {
		writeError(w, http.StatusInternalServerError, "角色查询失败")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	u, err := a.store.CreateUser(req.Username, hash, adminRoleID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "用户名已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "创建用户失败")
		return
	}
	// 初始管理员同样生成超级验证码，明文仅本次响应返回
	code, err := a.newSuperCode(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "超级验证码生成失败")
		return
	}
	// 初始化成功后直接建立会话，前端无缝进入面板
	if a.sessions != nil {
		if err := a.sessions.Issue(w, u.ID, auth.ClientIP(r), r.UserAgent()); err != nil {
			writeError(w, http.StatusInternalServerError, "会话创建失败")
			return
		}
	}
	a.audit.Record(r, "system", "setup", "user", req.Username, "success", "")
	writeJSON(w, map[string]string{"status": "ok", "super_code": code})
}
