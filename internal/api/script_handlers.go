package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/scriptsvc"
	"onecloud-panel/internal/store"
)

// scriptDTO 脚本视图模型；列表不含内容，详情含。
type scriptDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content,omitempty"`
	OwnerUserID *int64 `json:"owner_user_id"`
	System      bool   `json:"system"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func scriptToDTO(sc *store.ShellScript, withContent bool) scriptDTO {
	d := scriptDTO{
		ID: sc.ID, Name: sc.Name, Description: sc.Description,
		OwnerUserID: sc.OwnerUserID, System: sc.OwnerUserID == nil,
		CreatedAt: sc.CreatedAt, UpdatedAt: sc.UpdatedAt,
	}
	if withContent {
		d.Content = sc.Content
	}
	return d
}

type scriptReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	System      bool   `json:"system"` // 仅管理员：true 表示系统级（owner=NULL）
}

// scriptVisible 可见性（与列表一致）：管理员全可见；普通用户可见本人 + 系统级。
func scriptVisible(r *http.Request, sc *store.ShellScript) bool {
	if callerIsAdmin(r) {
		return true
	}
	uid, ok := callerID(r)
	if !ok {
		return false
	}
	return sc.OwnerUserID == nil || *sc.OwnerUserID == uid
}

// scriptManageable 管理（改/删）权限：管理员或创建者本人（系统级仅管理员）。
func scriptManageable(r *http.Request, sc *store.ShellScript) bool {
	if callerIsAdmin(r) {
		return true
	}
	uid, ok := callerID(r)
	if !ok {
		return false
	}
	return sc.OwnerUserID != nil && *sc.OwnerUserID == uid
}

// GET /api/scripts — 脚本清单。管理员见全部，普通用户见本人创建 + 系统级。
func (a *API) listScripts(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	visibleTo := &uid
	if callerIsAdmin(r) || !ok {
		visibleTo = nil // 管理员/未知：全部
	}
	scripts, err := a.store.ListShellScripts(visibleTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询脚本失败")
		return
	}
	out := make([]scriptDTO, 0, len(scripts))
	for i := range scripts {
		out = append(out, scriptToDTO(&scripts[i], false))
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// GET /api/scripts/{id} — 详情（含内容，供编辑回显）。
func (a *API) getScript(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	writeJSON(w, scriptToDTO(sc, true))
}

// POST /api/scripts — 新建脚本。
func (a *API) createScript(w http.ResponseWriter, r *http.Request) {
	uid, ok := callerID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}
	var req scriptReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "脚本名称必填")
		return
	}
	if err := scriptsvc.ValidateContent(req.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	owner := &uid
	if callerIsAdmin(r) && req.System {
		owner = nil
	}
	created, err := a.store.CreateShellScript(&store.ShellScript{
		Name: req.Name, Description: strings.TrimSpace(req.Description),
		Content: req.Content, OwnerUserID: owner,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建脚本失败")
		return
	}
	a.audit.Record(r, "app", "script_create", "script",
		strconv.FormatInt(created.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": created.Name, "size": len(created.Content)}))
	writeJSON(w, scriptToDTO(created, true))
}

// PUT /api/scripts/{id} — 更新脚本（内容变更后重新部署才会在节点上生效）。
func (a *API) updateScript(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	if !scriptManageable(r, sc) {
		writeError(w, http.StatusForbidden, "仅创建者或管理员可以修改脚本")
		return
	}
	var req scriptReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = sc.Name
	}
	content := req.Content
	if strings.TrimSpace(content) == "" {
		content = sc.Content // 未传内容时不做覆盖
	}
	if err := scriptsvc.ValidateContent(content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.UpdateShellScript(id, name, strings.TrimSpace(req.Description), content); err != nil {
		writeError(w, http.StatusInternalServerError, "更新脚本失败")
		return
	}
	updated, _ := a.store.GetShellScript(id)
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "脚本读取失败")
		return
	}
	a.audit.Record(r, "app", "script_update", "script",
		strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": name, "content_changed": content != sc.Content}))
	writeJSON(w, scriptToDTO(updated, true))
}

// DELETE /api/scripts/{id} — 删除脚本；先尽力清理各节点上的部署。
func (a *API) deleteScript(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	if !scriptManageable(r, sc) {
		writeError(w, http.StatusForbidden, "仅创建者或管理员可以删除脚本")
		return
	}
	// 尽力而为清理各节点（自启单元 + 脚本文件）；失败不阻塞删除
	deps, _ := a.store.ListShellScriptDeployments(id)
	warns := 0
	for _, d := range deps {
		n, err := a.store.GetNode(d.NodeID)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		if err := a.scripts.CleanupNode(ctx, n, id); err != nil {
			warns++
			log.Printf("清理节点 %d 上的脚本 %d 失败: %v", d.NodeID, id, err)
		}
		cancel()
	}
	if err := a.store.DeleteShellScript(id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	a.audit.Record(r, "app", "script_delete", "script",
		strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"name": sc.Name, "deployments": len(deps), "cleanup_warns": warns}))
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /api/scripts/{id}/deployments — 脚本在各节点的部署状态。
func (a *API) listScriptDeployments(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	deps, err := a.store.ListShellScriptDeployments(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询部署状态失败")
		return
	}
	items := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		items = append(items, map[string]any{
			"node_id":      d.NodeID,
			"auto_start":   d.AutoStart,
			"content_hash": d.ContentHash,
		})
	}
	writeJSON(w, map[string]any{"items": items})
}

// POST /api/scripts/{id}/deploy — 同步部署到节点（写入脚本并按需配置自启）。
func (a *API) deployScript(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	var req struct {
		NodeID    int64 `json:"node_id"`
		AutoStart bool  `json:"auto_start"`
	}
	if err := decodeJSON(r, &req); err != nil || req.NodeID == 0 {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n, err := a.store.GetNode(req.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	var buf bytes.Buffer
	if err := a.scripts.Deploy(r.Context(), &buf, n, sc, req.AutoStart); err != nil {
		a.audit.Record(r, "app", "script_deploy", "script",
			strconv.FormatInt(id, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"node_id": req.NodeID, "error": err.Error()}))
		writeError(w, http.StatusInternalServerError, "部署失败: "+err.Error())
		return
	}
	a.audit.Record(r, "app", "script_deploy", "script",
		strconv.FormatInt(id, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"node_id": req.NodeID, "auto_start": req.AutoStart}))
	writeJSON(w, map[string]any{"status": "ok", "output": buf.String()})
}

// POST /api/scripts/{id}/run — 后台任务运行脚本（先按已存部署重新落盘再执行）。
func (a *API) runScript(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID 非法")
		return
	}
	sc, err := a.store.GetShellScript(id)
	if err != nil || !scriptVisible(r, sc) {
		writeError(w, http.StatusNotFound, "脚本不存在")
		return
	}
	var req struct {
		NodeID int64 `json:"node_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.NodeID == 0 {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	n, err := a.store.GetNode(req.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if !assertNodeOwner(w, r, n) {
		return
	}
	payload, _ := json.Marshal(scriptsvc.RunPayload{NodeID: req.NodeID, ScriptID: id})
	uid, _ := callerID(r)
	taskID, err := a.store.CreateTask(&store.BackgroundTask{
		Type: "script_run", NodeID: &req.NodeID, Payload: string(payload), CreatedBy: &uid,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "任务入队失败: "+err.Error())
		return
	}
	a.tasks.Notify()
	// 审计由任务到达真实终态时统一记录（见 scriptsvc.taskAudit）
	writeJSON(w, map[string]any{"task_id": taskID})
}
