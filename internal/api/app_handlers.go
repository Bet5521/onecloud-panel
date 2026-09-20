package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"

	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/store"
)

// nodeAppFromPath 读取 {id}（节点）与 {app}（配方 ID）。
func nodeAppFromPath(r *http.Request) (int64, string, error) {
	id, err := idFromPath(r)
	if err != nil {
		return 0, "", err
	}
	app := r.PathValue("app")
	if app == "" {
		return 0, "", http.ErrMissingBoundary
	}
	return id, app, nil
}

type installReq struct {
	Method string            `json:"method"`
	Vars   map[string]string `json:"vars"`
}

// POST /api/nodes/{id}/apps/{app}/install
func (a *API) installApp(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req installReq
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式错误")
			return
		}
	}
	if req.Method == "" {
		req.Method = "native"
	}
	if req.Method != "native" && req.Method != "docker" {
		writeError(w, http.StatusBadRequest, "不支持的安装方式")
		return
	}
	// 前置校验：配方/节点/架构兼容性（任务失败前直接给出错误）
	n, err := a.store.GetNode(nodeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "节点不存在")
		return
	}
	rp, ok := a.apps.Registry().Get(appID)
	if !ok {
		writeError(w, http.StatusBadRequest, "应用配方不存在")
		return
	}
	if supports, reason := rp.Supports(req.Method, n.Arch); !supports {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
	// 变量类型/字符集前置校验（防注入）
	if err := a.apps.ValidateInstallVars(appID, req.Vars); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload := apps.TaskPayload{
		NodeID: nodeID, AppID: appID, Method: req.Method, Vars: req.Vars,
	}
	data, _ := json.Marshal(payload)
	id, err := a.store.CreateTask(&store.BackgroundTask{
		Type: "app_install", NodeID: &nodeID, AppID: appID, Payload: string(data),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.tasks.Notify()
	// 审计由任务到达真实终态时统一记录（见 apps.taskAudit）
	writeJSON(w, map[string]any{"task_id": id})
}

type uninstallReq struct {
	PurgeData bool `json:"purge_data"`
}

// POST .../uninstall
func (a *API) uninstallApp(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req uninstallReq
	if r.ContentLength > 0 {
		_ = decodeJSON(r, &req)
	}
	// 卸载方式以安装记录为准（记录丢失则默认 native，由任务实际处理）。
	method := "native"
	if in, err := a.store.GetInstallation(nodeID, appID); err == nil && in != nil && in.Method != "" {
		method = in.Method
	}
	payload := apps.TaskPayload{
		NodeID: nodeID, AppID: appID, Method: method, PurgeData: req.PurgeData,
	}
	data, _ := json.Marshal(payload)
	id, err := a.store.CreateTask(&store.BackgroundTask{
		Type: "app_uninstall", NodeID: &nodeID, AppID: appID, Payload: string(data),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.tasks.Notify()
	// 审计由任务到达真实终态时统一记录（见 apps.taskAudit）
	writeJSON(w, map[string]any{"task_id": id})
}

// POST .../{action}: start|stop|restart
func (a *API) appServiceAction(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := r.PathValue("action")
	unit, err := a.apps.ServiceAction(r.Context(), nodeID, appID, action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit.Record(r, "app", action, "installation",
		appID+"@"+strconv.FormatInt(nodeID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"unit": unit}))
	writeJSON(w, map[string]any{"status": "ok", "unit": unit})
}

// GET 节点已安装应用清单。
func (a *API) listInstallations(w http.ResponseWriter, r *http.Request) {
	nodeID, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := a.store.ListInstallations(nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

// GET 应用状态。
func (a *API) appStatus(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := a.apps.Status(r.Context(), nodeID, appID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, res)
}

// GET 应用日志。
func (a *API) appJournal(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			lines = n
		}
	}
	out, err := a.apps.Journal(r.Context(), nodeID, appID, lines)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// GET/PUT 白名单配置编辑；返回是否建议重启。
func (a *API) readAppConfig(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	allowed, reason := a.configAllowed(appID, path)
	if !allowed {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	n, _ := a.nodes.Get(nodeID)
	ex, err := a.apps.ExecutorFor(n)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := ex.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": path, "content": string(b)})
}

func (a *API) writeAppConfig(w http.ResponseWriter, r *http.Request) {
	nodeID, appID, err := nodeAppFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	allowed, reason := a.configAllowed(appID, req.Path)
	if !allowed {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	n, _ := a.nodes.Get(nodeID)
	ex, err := a.apps.ExecutorFor(n)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ex.WriteFile(req.Path, []byte(req.Content)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "app", "config_write", "installation",
		appID+"@"+strconv.FormatInt(nodeID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"path": req.Path}))
	writeJSON(w, map[string]any{
		"status":           "ok",
		"restart_required": true,
		"hint":             "配置已保存，重启服务后生效",
	})
}

// configAllowed 按配方白名单校验配置路径（先做绝对路径归一化，杜绝等价形式）。
func (a *API) configAllowed(appID, path string) (bool, string) {
	rp, ok := a.recipes.Get(appID)
	if !ok {
		return false, "配方不存在"
	}
	path = filepath.Clean(path)
	for _, c := range rp.ConfigFiles {
		if filepath.Clean(c.Path) == path {
			return true, ""
		}
	}
	return false, "路径不在该应用的配置白名单内"
}
