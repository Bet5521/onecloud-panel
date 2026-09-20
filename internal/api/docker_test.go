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

	// 状态（Windows 开发机引擎不可用 → available=false）
	w := do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/docker/status", nil, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status code=%d %s", w.Code, w.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st["available"] != false {
		t.Fatalf("status 异常: %s", w.Body.String())
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

	// viewer：状态可读，安装 403
	viewer := makeViewer(t, h, s, "vdocker")
	w = do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/docker/status", nil, viewer)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer status code=%d", w.Code)
	}
	w = do(t, h, "POST", "/api/nodes/"+itoa(localID)+"/docker/install", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer install code=%d, want 403", w.Code)
	}
}
