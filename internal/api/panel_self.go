package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// POST /api/panel/restore — 上传备份文件恢复数据库，随后异步重启面板以加载新库。
func (a *API) panelRestore(w http.ResponseWriter, r *http.Request) {
	if a.dataDir == "" {
		writeError(w, http.StatusInternalServerError, "数据目录未配置")
		return
	}
	if a.selfSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "自身管理服务未启用")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少上传文件")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取上传失败")
		return
	}
	if len(data) < 16 || string(data[:15]) != "SQLite format 3" {
		writeError(w, http.StatusBadRequest, "文件不是有效的 SQLite 数据库")
		return
	}
	dbPath := filepath.Join(a.dataDir, "panel.db")
	if err := os.WriteFile(dbPath, data, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "写入数据库失败: "+err.Error())
		return
	}
	// 清掉可能残留的 WAL/SHM，避免旧事务干扰
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suf)
	}
	var userID int64
	username := ""
	if id := auth.FromContext(r.Context()); id != nil && id.User != nil {
		userID = id.User.ID
		username = id.User.Username
	}
	taskID, err := a.selfSvc.Restart(r.Context(), userID)
	if err != nil {
		a.audit.Record(r, "settings", "restore", "panel", "self", audit.ResultFailure,
			audit.DetailJSON(map[string]any{"error": err.Error()}))
		writeError(w, http.StatusBadRequest, "恢复已写入但重启失败: "+err.Error())
		return
	}
	a.audit.Record(r, "settings", "restore", "user", username, audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"task_id": taskID}))
	writeJSON(w, map[string]any{"task_id": taskID, "status": "restored"})
}
