package apps

import (
	"context"
	"net/url"
	"strings"
	"time"

	"onecloud-panel/internal/store"
)

// InstallSummary 全集群应用运行状态聚合（仪表盘 AC-15）。
type InstallSummary struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Error   int `json:"error"`
	Stopped int `json:"stopped"`
}

const summaryWindow = 90 * time.Second

func summaryOnline(n *store.Node, now int64) bool {
	if n.Mode == "local" {
		return true
	}
	return n.LastSeen > 0 && now-n.LastSeen <= int64(summaryWindow.Seconds())
}

// InstallationSummary 聚合全部已安装应用的运行/异常/停止计数。
// 离线节点的应用只计入 total（避免误报状态）。
func (m *Manager) InstallationSummary(ctx context.Context) InstallSummary {
	byNode := m.InstallationSummaryByNode(ctx)
	sum := InstallSummary{}
	for _, s := range byNode {
		sum.Total += s.Total
		sum.Running += s.Running
		sum.Error += s.Error
		sum.Stopped += s.Stopped
	}
	return sum
}

// InstallationSummaryByNode 返回每个节点的应用运行统计（含离线节点的 total）。
func (m *Manager) InstallationSummaryByNode(ctx context.Context) map[int64]InstallSummary {
	all, err := m.store.ListAllInstallations()
	if err != nil || len(all) == 0 {
		return map[int64]InstallSummary{}
	}
	now := time.Now().Unix()
	groups := map[int64][]store.AppInstallation{}
	for _, in := range all {
		groups[in.NodeID] = append(groups[in.NodeID], in)
	}
	out := make(map[int64]InstallSummary, len(groups))
	for nodeID, list := range groups {
		s := InstallSummary{Total: len(list)}
		n, err := m.store.GetNode(nodeID)
		if err != nil || !summaryOnline(n, now) {
			out[nodeID] = s
			continue
		}
		m.summarizeNode(ctx, n, list, &s)
		out[nodeID] = s
	}
	return out
}

func (m *Manager) summarizeNode(ctx context.Context, n *store.Node,
	list []store.AppInstallation, sum *InstallSummary) {
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// ---- native：一次 systemctl is-active 批量探测 ----
	var units []string
	for _, in := range list {
		if in.Method == "docker" {
			continue
		}
		unit := in.ServiceName
		if unit == "" {
			if rp, ok := m.recipes.Get(in.AppID); ok {
				unit = rp.Native.UnitName
			}
		}
		units = append(units, unit)
	}
	if len(units) > 0 {
		if ex, err := m.ExecutorFor(n); err == nil {
			r, err := ex.Exec(rctx, "systemctl",
				append([]string{"is-active"}, units...)...)
			if err == nil {
				lines := strings.Split(strings.Trim(r.Output, "\n"), "\n")
				for i := 0; i < len(units); i++ {
					state := ""
					if i < len(lines) {
						state = strings.TrimSpace(lines[i])
					}
					switch state {
					case "active":
						sum.Running++
					case "failed":
						sum.Error++
					default:
						sum.Stopped++
					}
				}
			}
		}
	}

	// ---- docker：一次列出全部容器取 State ----
	var dlist []store.AppInstallation
	for _, in := range list {
		if in.Method == "docker" {
			dlist = append(dlist, in)
		}
	}
	if len(dlist) == 0 {
		return
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return
	}
	resp, err := eng.Do(rctx, "GET", "/containers/json",
		url.Values{"all": []string{"1"}}, nil, "")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var items []containerListItem
	if resp.StatusCode == 200 && decodeJSONReader(resp, &items) == nil {
		states := map[string]string{}
		for _, it := range items {
			for _, nm := range it.Names {
				states[nm] = it.State
			}
		}
		for _, in := range dlist {
			cname := in.ContainerName
			if cname == "" {
				cname = containerName(in.AppID)
			}
			switch states["/"+cname] {
			case "running":
				sum.Running++
			case "exited", "dead", "":
				sum.Error++
			default:
				sum.Stopped++
			}
		}
	}
}
