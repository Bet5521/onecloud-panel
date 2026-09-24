package api

import (
	"net/http"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
)

// GET /api/panel/status — 面板版本/路径/运行态。
func (a *API) panelStatus(w http.ResponseWriter, r *http.Request) {
	if a.selfSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "自身管理服务未启用")
		return
	}
	st, err := a.selfSvc.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "状态查询失败")
		return
	}
	writeJSON(w, st)
}

// GET /api/panel/journal?lines=200 — 面板服务日志。
func (a *API) panelJournal(w http.ResponseWriter, r *http.Request) {
	if a.selfSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "自身管理服务未启用")
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lines = n
		}
	}
	if lines < 1 {
		lines = 200
	}
	if lines > 5000 {
		lines = 5000
	}
	out, err := a.selfSvc.Journal(r.Context(), lines)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"journal": filterResetCodeLines(out)})
}

// filterResetCodeLines 过滤服务日志中的密码重置码行（防御纵深）：
// 验证码仅允许通过服务器本地日志（journalctl）获取，不经过面板 API 视图。
func filterResetCodeLines(j string) string {
	if !strings.Contains(j, "重置码：") {
		return j
	}
	lines := strings.Split(j, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, "[密码重置]") && strings.Contains(ln, "重置码：") {
			lines[i] = "[已过滤] 该行为密码重置码日志，请通过服务器本地日志查看"
		}
	}
	return strings.Join(lines, "\n")
}

// POST /api/panel/restart — 异步重启面板。
func (a *API) panelRestart(w http.ResponseWriter, r *http.Request) {
	if a.selfSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "自身管理服务未启用")
		return
	}
	var userID int64
	username := ""
	if id := auth.FromContext(r.Context()); id != nil && id.User != nil {
		userID = id.User.ID
		username = id.User.Username
	}
	taskID, err := a.selfSvc.Restart(r.Context(), userID)
	if err != nil {
		a.audit.Record(r, "settings", "restart", "panel", "self", audit.ResultFailure,
			audit.DetailJSON(map[string]any{"error": err.Error()}))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "settings", "restart", "user", username, audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"task_id": taskID}))
	writeJSON(w, map[string]int64{"task_id": taskID})
}
