package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"onecloud-panel/internal/store"
)

// Docker 状态与按需安装入队；viewer 无 node:write 被拒。
func TestDockerAPI(t *testing.T) {
	localID, s, h := appTestSetup(t)
	admin := adminLogin(t, h)

	// 状态：字段齐全、available 为布尔、install_command 非空。
	// 本机是否装有可用的 Docker 引擎随运行环境而定（GitHub Actions 的 ubuntu runner
	// 自带并运行 Docker），因此不硬编码 available 的值，只校验其类型与相关不变量。
	w := do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/docker/status", nil, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status code=%d %s", w.Code, w.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("status 解析失败: %v (%s)", err, w.Body.String())
	}
	avail, ok := st["available"].(bool)
	if !ok {
		t.Fatalf("available 应为布尔: %s", w.Body.String())
	}
	if cmd, _ := st["install_command"].(string); cmd == "" {
		t.Fatalf("install_command 不应为空: %s", w.Body.String())
	}
	// 引擎不可用时不返回版本号（DockerStatus 在 Ping 失败时返回空版本）
	if !avail {
		if v, _ := st["version"].(string); v != "" {
			t.Fatalf("引擎不可用时 version 应为空: %s", w.Body.String())
		}
	}

	// 触发安装入队
	w = do(t, h, "POST", "/api/nodes/"+itoa(localID)+"/docker/install", nil, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("install code=%d %s", w.Code, w.Body.String())
	}
	tasks, _, _ := s.ListTasks(store.TaskFilter{Type: "docker_install", Limit: 10})
	if len(tasks) != 1 || tasks[0].Payload != `{"node_id":1}` {
		t.Fatalf("docker_install 任务异常: %+v", tasks)
	}

	// viewer：非属主访问他人节点 → 404 掩蔽；安装无 node:write → 403
	viewer := makeViewer(t, h, s, "vdocker")
	w = do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/docker/status", nil, viewer)
	if w.Code != http.StatusNotFound {
		t.Fatalf("viewer status code=%d, want 404 (非属主掩蔽)", w.Code)
	}
	w = do(t, h, "POST", "/api/nodes/"+itoa(localID)+"/docker/install", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer install code=%d, want 403", w.Code)
	}
}
