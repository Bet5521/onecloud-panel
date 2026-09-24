package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
)

// NotifyScheduleLoop 定时状态摘要循环：按 notification_schedule 设置周期性向
// 指定通道推送节点/应用在线状态摘要。分发不走事件订阅过滤（接收通道由设置显式指定）。
func (a *API) NotifyScheduleLoop(ctx context.Context) {
	var nextRun time.Time
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		sc, err := a.store.GetNotificationSchedule()
		if err != nil {
			log.Printf("[定时摘要] 读取设置失败: %v", err)
			continue
		}
		if !sc.Enabled {
			nextRun = time.Time{} // 停用后重新启用时重新起算
			continue
		}
		now := time.Now()
		if nextRun.IsZero() {
			// 启用后首个间隔到期才发送
			nextRun = now.Add(time.Duration(sc.IntervalHours) * time.Hour)
			continue
		}
		if now.Before(nextRun) {
			continue
		}
		a.sendScheduleSummary(sc)
		nextRun = now.Add(time.Duration(sc.IntervalHours) * time.Hour)
	}
}

// sendScheduleSummary 组装并发送状态摘要（纯文本多行）。
func (a *API) sendScheduleSummary(sc *store.NotificationSchedule) {
	var lines []string
	if sc.IncludeNodes {
		nodes, err := a.store.ListNodes()
		if err != nil {
			log.Printf("[定时摘要] 节点查询失败: %v", err)
		} else {
			now := time.Now().Unix()
			online, offline := 0, 0
			lines = append(lines, "── 节点状态 ──")
			for i := range nodes {
				n := &nodes[i]
				isOnline := n.Mode == "local" || (n.LastSeen > 0 && now-n.LastSeen <= int64(onlineWindow.Seconds()))
				state := "离线"
				if isOnline {
					state = "在线"
					online++
				} else {
					offline++
				}
				lines = append(lines, fmt.Sprintf("%s（%s）— %s", n.Name, n.Mode, state))
			}
			lines = append(lines, fmt.Sprintf("共 %d 个节点：在线 %d / 离线 %d", len(nodes), online, offline))
		}
	}
	if sc.IncludeApps {
		appStates := a.apps.AppStatesByNode(context.Background())
		if len(appStates) == 0 {
			lines = append(lines, "── 应用状态 ──", "当前无在线节点或已安装应用。")
		} else {
			lines = append(lines, "── 应用状态 ──")
			for nodeID, states := range appStates {
				nodeName := fmt.Sprintf("节点 #%d", nodeID)
				if n, err := a.store.GetNode(nodeID); err == nil && n.Name != "" {
					nodeName = n.Name
				}
				onlineApps, offlineApps := 0, 0
				var offlineList []string
				for appID, st := range states {
					if st == "running" {
						onlineApps++
						continue
					}
					offlineApps++
					offlineList = append(offlineList, a.appDisplayName(appID))
				}
				line := fmt.Sprintf("%s：%d 个应用在线", nodeName, onlineApps)
				if offlineApps > 0 {
					line += fmt.Sprintf("，离线：%s", joinNames(offlineList))
				}
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		lines = []string{"未选择任何摘要内容（节点/应用）。"}
	}

	title := "OneCloud Panel 状态摘要"
	body := time.Now().Format("2006-01-02 15:04") + "\n" + joinLines(lines)
	log.Printf("[定时摘要] 发送状态摘要至 %d 个指定通道", len(sc.ChannelIDs))
	for _, chID := range sc.ChannelIDs {
		c, err := a.store.NotificationChannelByID(chID)
		if err != nil {
			log.Printf("[定时摘要] 通道 #%d 不存在，跳过", chID)
			continue
		}
		if !c.Enabled {
			log.Printf("[定时摘要] 通道「%s」未启用，跳过", c.Name)
			continue
		}
		sender, berr := notify.Build(c.Type, parseConfig(c.ConfigJSON))
		if berr != nil {
			log.Printf("[定时摘要] 通道「%s」构建发送器失败，跳过: %v", c.Name, berr)
			continue
		}
		if serr := sender.Send(notify.Message{Title: title, Body: body}); serr != nil {
			log.Printf("[定时摘要] 通道「%s」发送失败: %v", c.Name, serr)
		}
	}
}

// joinNames 逗号连接名称列表。
func joinNames(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += "、"
		}
		out += s
	}
	return out
}

// joinLines 换行连接行列表。
func joinLines(list []string) string {
	out := ""
	for _, s := range list {
		out += s + "\n"
	}
	return out
}
