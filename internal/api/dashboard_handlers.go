package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"onecloud-panel/internal/store"
)

const onlineWindow = 90 * time.Second

// GET /api/dashboard/summary — 集群概览。
func (a *API) dashboardSummary(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.ListNodes()
	if err != nil {
		writeError(w, 500, "节点查询失败")
		return
	}
	now := time.Now().Unix()

	type nodeApp struct {
		Total   int `json:"total"`
		Running int `json:"running"`
		Error   int `json:"error"`
		Stopped int `json:"stopped"`
	}
	type nodeRes struct {
		MemUsed   int64      `json:"mem_used"`
		MemTotal  int64      `json:"mem_total"`
		DiskUsed  int64      `json:"disk_used"`
		DiskTotal int64      `json:"disk_total"`
		LoadAvg   [3]float64 `json:"load_avg"`
		CPUCores  int        `json:"cpu_cores"`
		Uptime    int64      `json:"uptime_seconds"`
		Live      bool       `json:"live"`
	}
	type nodeBrief struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		Mode        string  `json:"mode"`
		Status      string  `json:"status"`
		NetworkType string  `json:"network_type"`
		Online      bool    `json:"online"`
		Arch        string  `json:"arch"`
		Apps        nodeApp `json:"apps"`
		Resources   nodeRes `json:"resources"`
	}

	stats := map[string]int{
		"nodes_total": len(nodes), "nodes_online": 0, "nodes_pending": 0,
		"lan": 0, "wireguard": 0, "public": 0, "local": 0, "docker": 0,
	}

	// 按节点应用统计
	byNode := a.apps.InstallationSummaryByNode(r.Context())

	briefs := make([]nodeBrief, 0, len(nodes))
	type job struct {
		idx  int
		node *store.Node
	}
	var jobs []job
	for i := range nodes {
		n := &nodes[i]
		online := n.Mode == "local" || (n.LastSeen > 0 && now-n.LastSeen <= int64(onlineWindow.Seconds()))
		if online {
			stats["nodes_online"]++
		}
		if n.Status == "pending" {
			stats["nodes_pending"]++
		}
		if n.DockerVersion != "" {
			stats["docker"]++
		}
		switch n.NetworkType {
		case "lan":
			stats["lan"]++
		case "wireguard":
			stats["wireguard"]++
		case "public":
			stats["public"]++
		case "local":
			stats["local"]++
		default:
			stats["lan"]++
		}
		as := byNode[n.ID]
		briefs = append(briefs, nodeBrief{
			ID: n.ID, Name: n.Name, Mode: n.Mode, Status: n.Status,
			NetworkType: n.NetworkType, Online: online, Arch: n.Arch,
			Apps:      nodeApp{Total: as.Total, Running: as.Running, Error: as.Error, Stopped: as.Stopped},
			Resources: nodeRes{MemTotal: n.MemTotal, CPUCores: n.CPUCores},
		})
		if online {
			jobs = append(jobs, job{idx: len(briefs) - 1, node: n})
		}
	}

	// 并发采集在线节点实时资源（短超时，失败降级到静态字段）
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			h, err := a.apps.InfoFor(ctx, j.node)
			if err != nil || h == nil {
				return
			}
			briefs[j.idx].Resources = nodeRes{
				MemUsed: h.MemUsed, MemTotal: h.MemTotal,
				DiskUsed: h.DiskUsed, DiskTotal: h.DiskTotal,
				LoadAvg: h.LoadAvg, CPUCores: h.CPUCores,
				Uptime: h.Uptime, Live: true,
			}
		}(j)
	}
	wg.Wait()

	// 全集群应用统计
	appStats := a.apps.InstallationSummary(r.Context())

	recentAudit, _, err := a.audit.Query(store.AuditFilter{Page: 1, PageSize: 10})
	if err != nil {
		writeError(w, 500, "审计查询失败")
		return
	}
	recentTasks, _, err := a.store.ListTasks(store.TaskFilter{Limit: 5})
	if err != nil {
		writeError(w, 500, "任务查询失败")
		return
	}

	writeJSON(w, map[string]any{
		"stats":         stats,
		"nodes":         briefs,
		"app_stats":     appStats,
		"recent_audits": recentAudit,
		"recent_tasks":  recentTasks,
		"server_time":   now,
	})
}
