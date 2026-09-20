package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onecloud-panel/internal/store"
)

// 通知通道 CRUD + 脱敏回显 + 测试发送（webhook 本地服务）。
func TestNotificationChannels(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := newJar(t)

	// 初始化（setup 自动登录管理员）
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 本地 webhook 接收端
	var gotBody []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	// 创建 webhook 通道
	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": "本地钩子",
		"config": map[string]string{"url": hook.URL}, "enabled": true,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create webhook: %d %s", w.Code, w.Body.String())
	}
	var ch map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	id := int64(ch["id"].(float64))
	if ch["type_name"] != "通用 Webhook" {
		t.Fatalf("type_name = %v", ch["type_name"])
	}

	// 创建 serverchan 通道 → sendkey 回显必须脱敏
	w = do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "serverchan", "name": "Server酱",
		"config": map[string]string{"sendkey": "SCT9999SECRET"},
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create serverchan: %d %s", w.Code, w.Body.String())
	}
	var sc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &sc)
	scID := int64(sc["id"].(float64))
	cfgMap := sc["config"].(map[string]any)
	if cfgMap["sendkey"] != "******" {
		t.Fatalf("sendkey 未脱敏: %v", cfgMap["sendkey"])
	}

	// GET 列表同样脱敏
	w = do(t, h, "GET", "/api/notifications/channels", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"sendkey":"******"`) {
		t.Fatalf("列表回显未脱敏: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SCT9999SECRET") {
		t.Fatal("列表回显泄露明文密钥")
	}

	// PUT serverchan：提交掩码 → 保留原值；再测试无效配置被拒
	w = do(t, h, "PUT", "/api/notifications/channels/"+itoa(scID), map[string]any{
		"name": "Server酱改名", "config": map[string]string{"sendkey": "******"}, "enabled": false,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update serverchan: %d %s", w.Code, w.Body.String())
	}
	chRow, err := s.NotificationChannelByID(scID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chRow.ConfigJSON, "SCT9999SECRET") {
		t.Fatalf("掩码提交未保留原密钥: %s", chRow.ConfigJSON)
	}
	if chRow.Name != "Server酱改名" || chRow.Enabled {
		t.Fatalf("更新未生效: %+v", chRow)
	}
	w = do(t, h, "PUT", "/api/notifications/channels/"+itoa(scID), map[string]any{
		"config": map[string]string{"sendkey": ""}, "enabled": true,
	}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("空密钥应被拒绝, got %d", w.Code)
	}

	// 无效类型拒绝
	w = do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "sms", "name": "x", "config": map[string]string{},
	}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("无效类型应 400, got %d", w.Code)
	}

	// 测试发送 → webhook 收到 JSON，tested_at 更新
	w = do(t, h, "POST", "/api/notifications/channels/"+itoa(id)+"/test", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("test send: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(gotBody), `"title"`) {
		t.Fatalf("webhook 未收到预期负载: %s", gotBody)
	}
	updated, _ := s.NotificationChannelByID(id)
	if updated.TestedAt == 0 {
		t.Fatal("测试成功后 tested_at 未更新")
	}

	// 删除
	w = do(t, h, "DELETE", "/api/notifications/channels/"+itoa(id), nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := s.NotificationChannelByID(id); err == nil {
		t.Fatal("删除后仍能查到通道")
	}
}

// notifyAll 仅发送到启用通道：此处校验 store 层启用过滤逻辑。
func TestEnabledNotificationChannelsFilter(t *testing.T) {
	_, s, _ := newTestAPI(t)
	mk := func(name string, enabled bool) *store.NotificationChannel {
		return &store.NotificationChannel{
			Type: "webhook", Name: name, ConfigJSON: `{"url":"http://127.0.0.1:1/x"}`, Enabled: enabled,
		}
	}
	if _, err := s.CreateNotificationChannel(mk("a", true)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNotificationChannel(mk("b", false)); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.EnabledNotificationChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].Name != "a" {
		t.Fatalf("启用过滤不符: %+v", enabled)
	}
}
