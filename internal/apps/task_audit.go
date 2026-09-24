package apps

import (
	"encoding/json"
	"fmt"
	"strconv"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
)

// taskAudit 后台任务到达终态时，按任务真实结果写审计（替代旧的"入队即成功"）。
func (m *Manager) taskAudit(t *store.BackgroundTask, status string) {
	if m.audit == nil {
		return
	}
	result := audit.ResultSuccess
	if status != store.TaskSuccess {
		result = audit.ResultFailure
	}
	switch t.Type {
	case "app_install", "app_uninstall":
		var p TaskPayload
		if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.AppID == "" {
			return
		}
		action := "install"
		if t.Type == "app_uninstall" {
			action = "uninstall"
		}
		target := p.AppID + "@" + strconv.FormatInt(p.NodeID, 10)
		detail := audit.DetailJSON(map[string]any{
			"method": p.Method, "purge_data": p.PurgeData,
		})
		m.audit.RecordTask(t.CreatedBy, "app", action, "installation",
			target, result, detail)
		// 安装/卸载成功终态 → 应用变动事件通知
		if result == audit.ResultSuccess {
			m.emitAppChange(t.Type, p)
		}
	case "docker_install":
		var p DockerTaskPayload
		if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 {
			return
		}
		m.audit.RecordTask(t.CreatedBy, "app", "docker_install", "node",
			strconv.FormatInt(p.NodeID, 10), result, "")
	case "docker_apply_config":
		var p DockerTaskPayload
		if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 {
			return
		}
		m.audit.RecordTask(t.CreatedBy, "app", "docker_apply_config", "node",
			strconv.FormatInt(p.NodeID, 10), result, "")
	}
}

// emitAppChange 安装/卸载成功后发出应用变动事件（NotifyEvent 未注入时跳过）。
func (m *Manager) emitAppChange(taskType string, p TaskPayload) {
	if m.NotifyEvent == nil {
		return
	}
	appName := p.AppID
	if rp, ok := m.recipes.Get(p.AppID); ok && rp.Name != "" {
		appName = rp.Name
	}
	nodeName := strconv.FormatInt(p.NodeID, 10)
	if n, err := m.store.GetNode(p.NodeID); err == nil && n.Name != "" {
		nodeName = n.Name
	}
	if taskType == "app_install" {
		m.NotifyEvent(notify.EventAppChange, "应用安装成功",
			fmt.Sprintf("应用「%s」已在节点「%s」上安装成功（%s）。", appName, nodeName, p.Method))
	} else {
		m.NotifyEvent(notify.EventAppChange, "应用卸载成功",
			fmt.Sprintf("应用「%s」已从节点「%s」卸载。", appName, nodeName))
	}
}
