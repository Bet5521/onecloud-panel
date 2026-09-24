package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/store"
)

// GET /api/nodes/{id}/docker/status：实时探测 Engine。
func (a *API) dockerStatus(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := a.nodes.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ok, ver, _ := a.apps.DockerStatus(ctx, n)
	resp := map[string]any{
		"available":       ok,
		"version":         ver,
		"stored_version":  n.DockerVersion,
		"install_command": "apt 官方仓库（containerd + docker-ce + docker-ce-cli，不含 compose）",
	}
	if !ok && n.DockerVersion != "" {
		resp["hint"] = "心跳曾报告 Docker，当前无法连接 Engine"
	}
	writeJSON(w, resp)
}

// POST /api/nodes/{id}/docker/install：入队按需安装任务。
func (a *API) installDocker(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, gerr := a.nodes.Get(id)
	if gerr != nil {
		writeError(w, http.StatusNotFound, gerr.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	payload, _ := json.Marshal(apps.DockerTaskPayload{NodeID: id})
	taskID, err := a.store.CreateTask(&store.BackgroundTask{
		Type: "docker_install", NodeID: &id, Payload: string(payload),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.tasks.Notify()
	// 审计由任务到达真实终态时统一记录（见 apps.taskAudit）
	writeJSON(w, map[string]any{"task_id": taskID})
}

// PUT /api/nodes/{id}/docker-config：保存节点级 Docker 镜像加速与第三方仓库配置。
func (a *API) updateNodeDockerConfig(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, gerr := a.nodes.Get(id)
	if gerr != nil {
		writeError(w, http.StatusNotFound, gerr.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	var req struct {
		Mirrors            string `json:"mirrors"`
		InsecureRegistries string `json:"insecure_registries"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.store.UpdateNodeDockerConfig(id, req.Mirrors, req.InsecureRegistries); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "node", "docker_config", "node",
		strconvID(id), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// POST /api/nodes/{id}/docker/apply-config：为已装 Docker 的节点应用镜像加速/仓库配置。
func (a *API) applyNodeDockerConfig(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := a.nodes.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	if n.DockerVersion == "" {
		writeError(w, http.StatusBadRequest, "节点未安装 Docker，请先安装 Docker")
		return
	}
	payload, _ := json.Marshal(apps.DockerTaskPayload{NodeID: id})
	taskID, err := a.store.CreateTask(&store.BackgroundTask{
		Type: "docker_apply_config", NodeID: &id, Payload: string(payload),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.tasks.Notify()
	writeJSON(w, map[string]any{"task_id": taskID})
}

func strconvID(id int64) string {
	return strconv.FormatInt(id, 10)
}
