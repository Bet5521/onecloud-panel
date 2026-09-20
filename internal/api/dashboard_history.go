package api

import (
	"net/http"
	"strconv"
	"time"
)

// GET /api/dashboard/history?node=<id>&hours=24 — 节点历史指标采样。
func (a *API) dashboardHistory(w http.ResponseWriter, r *http.Request) {
	nodeID, _ := strconv.ParseInt(r.URL.Query().Get("node"), 10, 64)
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	if nodeID <= 0 {
		writeJSON(w, map[string]any{"items": []any{}})
		return
	}
	since := time.Now().Unix() - int64(hours)*3600
	samples, err := a.store.ListMetricSamples(nodeID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	items := make([]map[string]any, 0, len(samples))
	for _, m := range samples {
		items = append(items, map[string]any{
			"ts":       m.Ts,
			"cpu_pct":  m.CPUPct,
			"mem_pct":  m.MemPct,
			"disk_pct": m.DiskPct,
			"load1":    m.Load1,
		})
	}
	writeJSON(w, map[string]any{"items": items})
}
