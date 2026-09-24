package api

import (
	"net/http"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/update"
	"onecloud-panel/internal/version"
)

// GET /api/update/check — 查询 GitHub 最新 Release 并与当前版本对比（settings:read）。
func (a *API) updateCheck(w http.ResponseWriter, r *http.Request) {
	proxy, _, _ := a.store.GetSetting("github_proxy")
	rel, err := update.FetchLatest(r.Context(), proxy)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, update.BuildCheckResult(version.Version, rel))
}

// POST /api/update/apply — 下载最新版本、校验并替换自身二进制，随后经 systemd 自动重启（settings:write）。
func (a *API) updateApply(w http.ResponseWriter, r *http.Request) {
	if a.selfSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "自身管理服务未启用")
		return
	}
	var userID int64
	if id := auth.FromContext(r.Context()); id != nil && id.User != nil {
		userID = id.User.ID
	}

	// 预检 systemd 可重启性，避免「替换成功却无法自动重启」的坏状态。
	if err := a.selfSvc.EnsureRestartable(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proxy, _, _ := a.store.GetSetting("github_proxy")
	tag, err := update.Apply(r.Context(), proxy)
	if err != nil {
		a.audit.Record(r, "settings", "update", "panel", tag, audit.ResultFailure,
			audit.DetailJSON(map[string]any{"error": err.Error()}))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 复用面板重启链路：systemd 延迟 1 秒重启加载新版本。
	taskID, rerr := a.selfSvc.Restart(r.Context(), userID)
	if rerr != nil {
		a.audit.Record(r, "settings", "update", "panel", tag, audit.ResultFailure,
			audit.DetailJSON(map[string]any{
				"error": rerr.Error(), "binary_replaced": true,
				"detail": "二进制已更新但触发重启失败，请手动重启面板服务",
			}))
		writeError(w, http.StatusInternalServerError,
			"二进制已更新，但触发自动重启失败："+rerr.Error()+"（请手动重启面板服务）")
		return
	}
	a.audit.Record(r, "settings", "update", "panel", tag, audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"task_id": taskID, "from": version.Version, "to": tag}))
	writeJSON(w, map[string]any{"task_id": taskID, "version": tag})
}
