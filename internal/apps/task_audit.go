package apps

import (
	"encoding/json"
	"strconv"

	"onecloud-panel/internal/audit"
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
