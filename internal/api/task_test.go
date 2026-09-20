package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"onecloud-panel/internal/store"
)

func TestTasksAPI(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	nodeID := int64(2)
	_, err := s.CreateTask(&store.BackgroundTask{
		Type: "install_app", Status: store.TaskSuccess, NodeID: &nodeID,
		AppID: "gitea", Payload: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 列表（不回传完整 output）
	w := do(t, h, "GET", "/api/tasks?type=install_app", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var list struct {
		Total int `json:"total"`
		Items []struct {
			ID     int64  `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total != 1 || list.Items[0].Type != "install_app" {
		t.Fatalf("列表异常: %s", w.Body.String())
	}

	// 详情：snake_case 字段，且不回传 payload（可能含任务凭据密文）
	w = do(t, h, "GET", "/api/tasks/"+str(list.Items[0].ID), nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}
	var detail struct {
		ID     int64  `json:"id"`
		AppID  string `json:"app_id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.AppID != "gitea" || detail.Status != store.TaskSuccess {
		t.Fatalf("详情异常: %+v", detail)
	}
	if strings.Contains(w.Body.String(), "payload") {
		t.Fatalf("详情不应回传 payload: %s", w.Body.String())
	}

	// 不存在 → 404
	w = do(t, h, "GET", "/api/tasks/9999", nil, jar)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing code = %d, want 404", w.Code)
	}

	// 未登录 → 401
	w = do(t, h, "GET", "/api/tasks", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anon code = %d, want 401", w.Code)
	}
}

func str(i int64) string { return strconv.FormatInt(i, 10) }
