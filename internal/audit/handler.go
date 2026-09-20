package audit

import (
	"encoding/json"
	"net/http"
	"strconv"

	"onecloud-panel/internal/store"
)

// Handler 审计日志查询处理器。
type Handler struct{ svc *Service }

// NewHandler 创建审计处理器。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List GET /api/audit-logs
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{
		Username: q.Get("username"),
		Module:   q.Get("module"),
		Result:   q.Get("result"),
	}
	if v := q.Get("start"); v != "" {
		f.Start, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("end"); v != "" {
		f.End, _ = strconv.ParseInt(v, 10, 64)
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.PageSize, _ = strconv.Atoi(q.Get("page_size"))

	logs, total, err := h.svc.Query(f)
	if err != nil {
		http.Error(w, `{"error":"查询失败"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": logs,
		"total": total,
	})
}
