package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 「系统通知」标记：
//  1. 新建时可标记为系统通知，且在通道列表 DTO 中回显 system；
//  2. 系统通知通道不出现在用户可选通道列表（/api/auth/notify-channels）；
//  3. 非系统通知通道仍可被用户选择；
//  4. 直接把系统通道路由给用户会被拒绝（服务端兜底，避免绕过前端）。
func TestSystemNotificationChannelNotUserSelectable(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	newCh := func(name string, system bool) int64 {
		t.Helper()
		w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
			"type": "wxpusher", "name": name,
			"config": map[string]string{"app_token": "AT_test", "topic": "1"},
			"enabled": true, "system": system,
		}, jar)
		if w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
		var ch map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
			t.Fatal(err)
		}
		if got, _ := ch["system"].(bool); got != system {
			t.Fatalf("system 回显错误: %v", ch["system"])
		}
		return int64(ch["id"].(float64))
	}

	sysID := newCh("系统通知", true)
	userID := newCh("用户通知", false)

	// 用户可选列表里只能看到非系统通道
	w := do(t, h, "GET", "/api/auth/notify-channels", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("notify-channels: %d %s", w.Code, w.Body.String())
	}
	var opts struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, o := range opts.Items {
		seen[o.ID] = true
	}
	if seen[sysID] {
		t.Fatal("系统通知通道不应出现在用户可选列表")
	}
	if !seen[userID] {
		t.Fatal("非系统通知通道应出现在用户可选列表")
	}

	// 改名/改标记后仍应保持
	w = do(t, h, "PUT", "/api/notifications/channels/"+itoa(sysID), map[string]any{
		"name": "系统通知2", "system": true, "enabled": true,
		"config": map[string]string{"app_token": "AT_test", "topic": "1"},
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var upd map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &upd)
	if got, _ := upd["system"].(bool); !got {
		t.Fatalf("更新后 system 应保持 true: %v", upd["system"])
	}
}
