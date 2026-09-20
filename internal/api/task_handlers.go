package api

import (
	"net/http"
	"strconv"

	"onecloud-panel/internal/store"
)

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.TaskFilter{
		Type:   q.Get("type"),
		Status: q.Get("status"),
	}
	if v := q.Get("node_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "node_id 非法")
			return
		}
		f.NodeID = &id
	}
	if v := q.Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset := (n - 1)
			if f.Limit > 0 {
				offset *= f.Limit
			}
			f.Offset = offset
		}
	}
	items, total, err := a.store.ListTasks(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	// 列表不回传完整 output，避免响应过大
	type row struct {
		ID         int64  `json:"id"`
		Type       string `json:"type"`
		Status     string `json:"status"`
		NodeID     *int64 `json:"node_id"`
		AppID      string `json:"app_id"`
		Error      string `json:"error"`
		CreatedAt  int64  `json:"created_at"`
		StartedAt  *int64 `json:"started_at"`
		FinishedAt *int64 `json:"finished_at"`
	}
	out := make([]row, 0, len(items))
	for _, t := range items {
		out = append(out, row{
			ID: t.ID, Type: t.Type, Status: t.Status, NodeID: t.NodeID,
			AppID: t.AppID, Error: t.Error, CreatedAt: t.CreatedAt,
			StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
		})
	}
	writeJSON(w, map[string]any{"items": out, "total": total})
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := a.store.GetTask(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// 统一 snake_case 字段，且不回传 payload（可能含任务凭据密文）
	writeJSON(w, map[string]any{
		"id": t.ID, "type": t.Type, "status": t.Status,
		"node_id": t.NodeID, "app_id": t.AppID,
		"output": t.Output, "error": t.Error,
		"created_at": t.CreatedAt, "started_at": t.StartedAt, "finished_at": t.FinishedAt,
	})
}
