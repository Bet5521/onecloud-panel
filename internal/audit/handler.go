package audit

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"onecloud-panel/internal/store"
)

// Handler 审计日志查询处理器。
type Handler struct{ svc *Service }

// NewHandler 创建审计处理器。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// filterFromQuery 解析查询条件（列表与导出共用）。
func filterFromQuery(q map[string][]string) store.AuditFilter {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return ""
	}
	f := store.AuditFilter{
		Username: get("username"),
		Module:   get("module"),
		Action:   get("action"),
		Result:   get("result"),
	}
	if v := get("start"); v != "" {
		f.Start, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := get("end"); v != "" {
		f.End, _ = strconv.ParseInt(v, 10, 64)
	}
	f.Page, _ = strconv.Atoi(get("page"))
	f.PageSize, _ = strconv.Atoi(get("page_size"))
	return f
}

// List GET /api/audit-logs
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	f := filterFromQuery(r.URL.Query())
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

// exportMaxRows 导出行数上限（分页流式写出，超出部分截断并写入文件尾说明）。
const exportMaxRows = 100000

// exportPageSize 导出分页大小（避免一次性载入全部日志）。
const exportPageSize = 500

// ExportCSV GET /api/audit-logs/export — 按当前筛选条件导出 CSV。
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	f := filterFromQuery(r.URL.Query())
	f.Page = 1
	f.PageSize = exportPageSize

	name := "onecloud-panel-audit-" + time.Now().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"id", "time", "username", "ip", "module", "action",
		"target_type", "target_id", "result", "request_id", "detail",
	})

	written := 0
	truncated := false
	for {
		logs, _, err := h.svc.Query(f)
		if err != nil {
			// 头已发出，只能在流内标注错误
			_, _ = fmt.Fprintf(w, "# 查询失败: %v\n", err)
			return
		}
		for i := range logs {
			if written >= exportMaxRows {
				truncated = true
				break
			}
			l := logs[i]
			_ = cw.Write([]string{
				strconv.FormatInt(l.ID, 10),
				time.Unix(l.Ts, 0).Format("2006-01-02 15:04:05"),
				l.Username, l.IP, l.Module, l.Action,
				l.TargetType, l.TargetID, l.Result, l.RequestID, l.Detail,
			})
			written++
		}
		cw.Flush()
		if truncated || len(logs) < exportPageSize {
			break
		}
		f.Page++
	}
	if truncated {
		_, _ = fmt.Fprintf(w, "# 已达到导出上限 %d 行，请缩小时间范围后继续导出\n", exportMaxRows)
	}
	cw.Flush()

	h.svc.Record(r, "audit", "export_csv", "audit_log", "",
		ResultSuccess, DetailJSON(map[string]any{"rows": written}))
}
