package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 每用户通知接收标识（notify_target）端到端契约：
//  1. 需要标识的通道（wxpusher/sms）在用户可选列表里带 needs_target 与标签；
//  2. 绑定该类通道但不填标识 → 400（前端据此做必填校验，不能出现"选了就存不下去"的死路）；
//  3. 填了标识 → 保存成功；
//  4. GET /api/auth/me 必须回显 notify_target，否则「个人资料」无法回填已保存的标识。
func TestNotifyTargetRoundTrip(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 管理员建一个 wxpusher 通道（按用户维度需要 UID）
	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "wxpusher", "name": "推送",
		"config": map[string]string{"app_token": "AT_test", "topic": "1"}, "enabled": true,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create wxpusher: %d %s", w.Code, w.Body.String())
	}
	var ch map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	chID := int64(ch["id"].(float64))

	// 用户可选通道应带 needs_target 与标签（前端据此渲染"接收标识"输入框）
	w = do(t, h, "GET", "/api/auth/notify-channels", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("notify-channels: %d", w.Code)
	}
	var opts struct {
		Items []struct {
			ID          int64  `json:"id"`
			NeedsTarget bool   `json:"needs_target"`
			TargetLabel string `json:"target_label"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range opts.Items {
		if o.ID == chID {
			found = true
			if !o.NeedsTarget || o.TargetLabel == "" {
				t.Fatalf("wxpusher 通道应要求接收标识: %+v", o)
			}
		}
	}
	if !found {
		t.Fatal("启用通道未出现在用户可选列表")
	}

	// 绑定通道但缺标识 → 400
	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"notify_method": "channel", "notify_channel_id": chID,
	}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺接收标识应 400, got %d %s", w.Code, w.Body.String())
	}

	// 带标识 → 成功
	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"notify_method": "channel", "notify_channel_id": chID, "notify_target": "UID_ABC,UID_DEF",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("绑定失败: %d %s", w.Code, w.Body.String())
	}

	// me 必须回显 notify_target（前端回填依赖）
	w = do(t, h, "GET", "/api/auth/me", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d", w.Code)
	}
	var me map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if _, ok := me["notify_target"]; !ok {
		t.Fatal("me 响应缺少 notify_target 字段")
	}
	if me["notify_target"] != "UID_ABC,UID_DEF" {
		t.Fatalf("me.notify_target = %v, want UID_ABC,UID_DEF", me["notify_target"])
	}

	// 清空标识：显式传空串应被接受（saved as empty）
	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"notify_method": "log", "notify_target": "",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("清空标识失败: %d %s", w.Code, w.Body.String())
	}
}
