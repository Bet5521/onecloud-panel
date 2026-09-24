package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"onecloud-panel/internal/notify"
)

// notifyProbeInterval 状态探测周期（与 localNodeLoop 同间隔）。
const notifyProbeInterval = 30 * time.Second

// NotifyProbeLoop 状态探测循环：周期性对比节点/应用在线状态快照，
// 状态变化（上线/下线）时通过 EmitEvent 触发事件通知。
// 首轮仅建立快照不发送，避免面板重启产生"全量上线"通知风暴。
func (a *API) NotifyProbeLoop(ctx context.Context) {
	nodeSnap := map[int64]bool{} // 节点ID → 在线
	appSnap := map[string]bool{} // "应用ID@节点ID" → 在线
	first := true
	ticker := time.NewTicker(notifyProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		a.probeOnce(nodeSnap, appSnap, first)
		first = false
	}
}

// probeOnce 执行一轮探测：刷新快照并对状态变化触发事件。
func (a *API) probeOnce(nodeSnap map[int64]bool, appSnap map[string]bool, first bool) {
	nodes, err := a.store.ListNodes()
	if err != nil {
		log.Printf("[状态探测] 节点列表查询失败: %v", err)
		return
	}
	now := time.Now().Unix()
	nodeNames := map[int64]string{}
	currentNodes := map[int64]bool{}
	for i := range nodes {
		n := &nodes[i]
		online := n.Mode == "local" || (n.LastSeen > 0 && now-n.LastSeen <= int64(onlineWindow.Seconds()))
		nodeNames[n.ID] = n.Name
		currentNodes[n.ID] = true
		prev, existed := nodeSnap[n.ID]
		nodeSnap[n.ID] = online
		if !first && existed && prev != online {
			if online {
				a.EmitEvent(notify.EventNodeOnline, "节点已上线",
					fmt.Sprintf("节点「%s」已上线。", n.Name))
			} else {
				a.EmitEvent(notify.EventNodeOffline, "节点已下线",
					fmt.Sprintf("节点「%s」已下线（超过 %d 秒未心跳）。", n.Name, int(onlineWindow.Seconds())))
			}
		}
	}
	// 清理已删除节点的快照
	for id := range nodeSnap {
		if !currentNodes[id] {
			delete(nodeSnap, id)
		}
	}

	// 应用状态探测（仅在线节点）
	appStates := a.apps.AppStatesByNode(context.Background())
	currentApps := map[string]bool{}
	for nodeID, states := range appStates {
		nodeName := nodeNames[nodeID]
		for appID, st := range states {
			online := st == "running"
			key := fmt.Sprintf("%s@%d", appID, nodeID)
			currentApps[key] = true
			prev, existed := appSnap[key]
			appSnap[key] = online
			if !first && existed && prev != online {
				name := a.appDisplayName(appID)
				if online {
					a.EmitEvent(notify.EventAppOnline, "应用已上线",
						fmt.Sprintf("应用「%s」（节点「%s」）已恢复运行。", name, nodeName))
				} else {
					a.EmitEvent(notify.EventAppOffline, "应用已下线",
						fmt.Sprintf("应用「%s」（节点「%s」）已停止运行。", name, nodeName))
				}
			}
		}
	}
	// 清理已卸载应用的快照
	for key := range appSnap {
		if !currentApps[key] {
			delete(appSnap, key)
		}
	}
}

// appDisplayName 应用显示名：优先配方名，回退应用 ID。
func (a *API) appDisplayName(appID string) string {
	if rp, ok := a.recipes.Get(appID); ok && rp.Name != "" {
		return rp.Name
	}
	return appID
}
