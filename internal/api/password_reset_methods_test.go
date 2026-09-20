package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 超级验证码重置：验证成功 → 设新密码 → 返回并轮换新超级验证码。
func TestResetBySuperCode(t *testing.T) {
	_, _, h := newTestServer(t)

	// setup 获取初始管理员超级验证码
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	var setupResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &setupResp)
	firstCode := setupResp["super_code"]
	if firstCode == "" {
		t.Fatal("setup 未返回超级验证码")
	}

	// 超级验证码直接确认重置
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "rootadmin", "code": firstCode,
		"new_password": "NewRootPass1", "method": "super_code",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm super_code: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	rotated := resp["super_code"]
	if len(rotated) != 10 {
		t.Fatalf("重置响应缺少新超级验证码: %s", w.Body.String())
	}
	if rotated == firstCode {
		t.Fatal("超级验证码未轮换")
	}

	// 新密码可登录
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "NewRootPass1"}, newJar(t))
	if w.Code != http.StatusOK {
		t.Fatalf("new password login: %d", w.Code)
	}

	// 旧超级验证码失效
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "rootadmin", "code": firstCode,
		"new_password": "AnotherPass1", "method": "super_code",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("旧超级验证码应被拒绝, got %d", w.Code)
	}

	// 新超级验证码可用
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "rootadmin", "code": rotated,
		"new_password": "ThirdPass123", "method": "super_code",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("新超级验证码重置: %d %s", w.Code, w.Body.String())
	}
}

// 通知渠道重置：request(method=notification) → 用户绑定的通道收到码 → confirm 成功。
func TestResetByNotification(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d", w.Code)
	}

	// 本地 webhook 接收端捕获通知正文
	var gotBody []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	// 创建启用中的 webhook 通道
	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": "本地钩子",
		"config": map[string]string{"url": hook.URL}, "enabled": true,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 需求 3：通知方式为定向下发，先把管理员绑定到该通道
	w = do(t, h, "PUT", "/api/auth/profile", map[string]any{
		"notify_method": "channel", "notify_channel_id": created.ID,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("绑定通知通道: %d %s", w.Code, w.Body.String())
	}

	// 以通知方式申请重置码
	code := ""
	apiObj.SetResetCodeSink(func(username, c string) { code = c })
	w = do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "rootadmin", "method": "notification"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	if code == "" {
		t.Fatal("codeSink 未捕获重置码")
	}
	if !strings.Contains(string(gotBody), code) {
		t.Fatalf("通知通道未收到重置码: body=%s code=%s", gotBody, code)
	}

	// 用收到的码确认重置
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "rootadmin", "code": code, "new_password": "NotifyPass123",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["super_code"]) != 10 {
		t.Fatalf("确认响应缺少新超级验证码: %s", w.Body.String())
	}

	// 新密码可登录
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "NotifyPass123"}, newJar(t))
	if w.Code != http.StatusOK {
		t.Fatalf("new password login: %d", w.Code)
	}

	// 落库确认超级验证码哈希已轮换
	u, err := s.UserByUsername("rootadmin")
	if err != nil {
		t.Fatal(err)
	}
	if u.SuperCode == "" {
		t.Fatal("super_code 哈希为空")
	}
}

// super_code 方式的 request 无需下发短期码。
func TestResetRequestSuperCodeNoop(t *testing.T) {
	_, _, h := newTestServer(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, nil); w.Code != http.StatusOK {
		t.Fatalf("setup: %d", w.Code)
	}
	w := do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "rootadmin", "method": "super_code"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["method"] != "super_code" {
		t.Fatalf("method 回显不符: %v", resp)
	}
}
