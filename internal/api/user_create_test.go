package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"onecloud-panel/internal/store"
)

// roleIDByCode 取内置角色 ID。
func roleIDByCode(t *testing.T, s *store.Store, code string) int64 {
	t.Helper()
	roles, err := s.ListRoles()
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	for _, r := range roles {
		if r.Code == code {
			return r.ID
		}
	}
	t.Fatalf("缺少角色 %s", code)
	return 0
}

// 新建用户（含姓名/手机号）保存成功并落库；超级验证码仅本次返回。
func TestCreateUserWithProfile(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)
	roleID := roleIDByCode(t, s, "operator")

	w := do(t, h, "POST", "/api/users", map[string]any{
		"username": "alice", "password": "AlicePass1", "role_id": roleID,
		"real_name": " 爱丽丝 ", "phone": " 13800138000 ",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	var dto userDTO
	_ = json.Unmarshal(w.Body.Bytes(), &dto)
	if dto.RealName != "爱丽丝" || dto.Phone != "13800138000" {
		t.Fatalf("响应资料不符: %+v", dto)
	}
	if dto.NotifyMethod != "log" {
		t.Fatalf("默认通知方式 = %q, want log", dto.NotifyMethod)
	}
	if len(dto.SuperCode) != 10 {
		t.Fatalf("超级验证码未返回: %+v", dto)
	}

	u, err := s.UserByUsername("alice")
	if err != nil {
		t.Fatalf("落库用户不存在: %v", err)
	}
	if u.RealName != "爱丽丝" || u.Phone != "13800138000" {
		t.Fatalf("落库资料不符: real_name=%q phone=%q", u.RealName, u.Phone)
	}

	// 列表接口回显资料
	w = do(t, h, "GET", "/api/users", nil, jar)
	if !containsJSON(w.Body.Bytes(), `"username":"alice"`) ||
		!containsJSON(w.Body.Bytes(), `"real_name":"爱丽丝"`) {
		t.Fatalf("用户列表未回显资料: %s", w.Body.String())
	}
}

// 用户名重复 → 409；非法角色 / 非法通知方式 → 400。
func TestCreateUserValidation(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)
	roleID := roleIDByCode(t, s, "operator")

	ok := map[string]any{"username": "bob", "password": "BobPass123", "role_id": roleID}
	if w := do(t, h, "POST", "/api/users", ok, jar); w.Code != http.StatusOK {
		t.Fatalf("首次创建失败: %d %s", w.Code, w.Body.String())
	}

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"重名", map[string]any{"username": "bob", "password": "BobPass123", "role_id": roleID}, "已存在"},
		{"角色不存在", map[string]any{"username": "carl", "password": "CarlPass1", "role_id": 9999}, "角色不存在"},
		{"通知方式非法", map[string]any{"username": "dave", "password": "DavePass1",
			"role_id": roleID, "notify_method": "telepathy"}, "通知方式"},
		{"姓名过长", map[string]any{"username": "erin", "password": "ErinPass1",
			"role_id": roleID, "real_name": strings.Repeat("a", 33)}, "姓名长度"},
		{"通道未指定", map[string]any{"username": "fred", "password": "FredPass1",
			"role_id": roleID, "notify_method": "channel"}, "必须选择通知通道"},
		{"通道不存在", map[string]any{"username": "gina", "password": "GinaPass1",
			"role_id": roleID, "notify_method": "channel", "notify_channel_id": 8888}, "通知通道不存在"},
	}
	for _, c := range cases {
		w := do(t, h, "POST", "/api/users", c.body, jar)
		if c.name == "重名" {
			if w.Code != http.StatusConflict {
				t.Fatalf("%s: code = %d, want 409 (%s)", c.name, w.Code, w.Body.String())
			}
			continue
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (%s)", c.name, w.Code, w.Body.String())
		}
		if !containsJSON(w.Body.Bytes(), c.want) {
			t.Fatalf("%s: 错误信息 %s 未包含 %q", c.name, w.Body.String(), c.want)
		}
	}
}

// DisallowUnknownFields 契约：未登记字段必须 400，防止静默丢字段（需求 4 的根因）。
func TestCreateUserRejectsUnknownField(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)
	roleID := roleIDByCode(t, s, "operator")

	w := do(t, h, "POST", "/api/users", map[string]any{
		"username": "hank", "password": "HankPass1", "role_id": roleID,
		"nickname": "未登记字段",
	}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知字段 code = %d, want 400", w.Code)
	}
}

// 通知方式为通道时需选择已启用通道；绑定成功后回显通道 ID。
func TestCreateUserWithNotifyChannel(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)
	roleID := roleIDByCode(t, s, "operator")

	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": "本地钩子",
		"config": map[string]string{"url": "http://127.0.0.1:1/hook"}, "enabled": true,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var ch map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ch)
	chID := int64(ch["id"].(float64))

	w = do(t, h, "POST", "/api/users", map[string]any{
		"username": "ivy", "password": "IvyPass123", "role_id": roleID,
		"notify_method": "channel", "notify_channel_id": chID,
		"notify_email": "ivy@example.com", "notify_sms_phone": "13900139000",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	var dto userDTO
	_ = json.Unmarshal(w.Body.Bytes(), &dto)
	if dto.NotifyMethod != "channel" || dto.NotifyChannelID != chID {
		t.Fatalf("通知方式未落库: %+v", dto)
	}
	if dto.NotifyEmail != "ivy@example.com" || dto.NotifySMSPhone != "13900139000" {
		t.Fatalf("通知联系方式未落库: %+v", dto)
	}
	u, err := s.UserByUsername("ivy")
	if err != nil {
		t.Fatal(err)
	}
	if u.NotifyChannelID == nil || *u.NotifyChannelID != chID {
		t.Fatalf("库中通知通道 = %v, want %d", u.NotifyChannelID, chID)
	}
}

// 个人资料维护（右上角用户信息菜单）：姓名/手机号/通知方式。
func TestUpdateMyProfileNotify(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": "我的钩子",
		"config": map[string]string{"url": "http://127.0.0.1:1/hook"}, "enabled": true,
	}, jar)
	var ch map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ch)
	chID := int64(ch["id"].(float64))

	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"real_name": "管理员", "phone": "13700137000",
		"notify_method": "channel", "notify_channel_id": chID,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update profile: %d %s", w.Code, w.Body.String())
	}
	u, err := s.UserByUsername("rootadmin")
	if err != nil {
		t.Fatal(err)
	}
	if u.RealName != "管理员" || u.Phone != "13700137000" {
		t.Fatalf("资料未落库: %+v", u)
	}
	if u.NotifyMethod != "channel" || u.NotifyChannelID == nil || *u.NotifyChannelID != chID {
		t.Fatalf("通知方式未落库: %+v", u)
	}

	// 未启用通道应被拒
	w = do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": "停用钩子",
		"config": map[string]string{"url": "http://127.0.0.1:1/off"}, "enabled": false,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("创建停用通道失败: %d %s", w.Code, w.Body.String())
	}
	var off map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &off)
	offID := int64(off["id"].(float64))

	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"real_name": "管理员", "phone": "13700137000",
		"notify_method": "channel", "notify_channel_id": offID,
	}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("禁用通道应被拒, code = %d (%s)", w.Code, w.Body.String())
	}

	// 可选通道列表不含密钥
	w = do(t, h, "GET", "/api/auth/notify-channels", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("notify-channels: %d %s", w.Code, w.Body.String())
	}
	if containsJSON(w.Body.Bytes(), "config") {
		t.Fatalf("可选通道列表泄露配置: %s", w.Body.String())
	}
}

// containsJSON 子串判断（读起来比 strings.Contains(body...) 更清晰）。
func containsJSON(body []byte, sub string) bool {
	return strings.Contains(string(body), sub)
}
