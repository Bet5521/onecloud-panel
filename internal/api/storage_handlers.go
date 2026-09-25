package api

import (
	"net/http"
	"strconv"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/store"
)

// GET /api/nodes/{id}/storage/devices — 块设备列表（磁盘/分区，含挂载状态）。
func (a *API) nodeStorageDevices(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	devs, err := a.storage.List(r.Context(), n)
	if err != nil {
		writeError(w, http.StatusBadGateway, "设备列表获取失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": devs})
}

type storageMountReq struct {
	Device     string `json:"device"`
	MountPoint string `json:"mountpoint"`
}

// POST /api/nodes/{id}/storage/mount — 挂载设备（目录不存在自动创建）。
func (a *API) nodeStorageMount(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	var req storageMountReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.storage.Mount(r.Context(), n, req.Device, req.MountPoint); err != nil {
		a.audit.Record(r, "node", "storage_mount", "node", strconv.FormatInt(n.ID, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"device": req.Device, "mountpoint": req.MountPoint, "error": err.Error()}))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "node", "storage_mount", "node", strconv.FormatInt(n.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"device": req.Device, "mountpoint": req.MountPoint}))
	writeJSON(w, map[string]string{"status": "ok"})
}

// POST /api/nodes/{id}/storage/unmount — 卸载挂载点。
func (a *API) nodeStorageUnmount(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	var req storageMountReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.storage.Unmount(r.Context(), n, req.MountPoint); err != nil {
		a.audit.Record(r, "node", "storage_unmount", "node", strconv.FormatInt(n.ID, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"mountpoint": req.MountPoint, "error": err.Error()}))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "node", "storage_unmount", "node", strconv.FormatInt(n.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"mountpoint": req.MountPoint}))
	writeJSON(w, map[string]string{"status": "ok"})
}

type storageAutostartReq struct {
	Device     string `json:"device"`
	MountPoint string `json:"mountpoint"`
	Enabled    bool   `json:"enabled"`
}

// POST /api/nodes/{id}/storage/autostart — 设置/取消开机自动挂载（fstab nofail）。
func (a *API) nodeStorageAutostart(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	var req storageAutostartReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.storage.Autostart(r.Context(), n, req.Device, req.MountPoint, req.Enabled); err != nil {
		a.audit.Record(r, "node", "storage_autostart", "node", strconv.FormatInt(n.ID, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"device": req.Device, "mountpoint": req.MountPoint,
				"enabled": req.Enabled, "error": err.Error()}))
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.Record(r, "node", "storage_autostart", "node", strconv.FormatInt(n.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"device": req.Device, "mountpoint": req.MountPoint, "enabled": req.Enabled}))
	writeJSON(w, map[string]string{"status": "ok"})
}

// scopedNode 公共前置：解析节点 ID、取节点、校验归属（失败已写响应）。
func (a *API) scopedNode(w http.ResponseWriter, r *http.Request) (*store.Node, bool) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	n, err := a.store.GetNode(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "节点不存在")
		return nil, false
	}
	if !assertNodeOwner(w, r, n) {
		return nil, false
	}
	return n, true
}
