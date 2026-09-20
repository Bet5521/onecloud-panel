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

// countingHook 启动计数 webhook 接收端。
func countingHook(t *testing.T, hits *int, lastBody *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*hits++
		if lastBody != nil {
			*lastBody = string(b)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createWebhookChannel(t *testing.T, h http.Handler, jar http.CookieJar,
	name, url string, enabled bool) int64 {
	t.Helper()
	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "webhook", "name": name,
		"config": map[string]string{"url": url}, "enabled": enabled,
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("创建通道 %s: %d %s", name, w.Code, w.Body.String())
	}
	var ch map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ch)
	return int64(ch["id"].(float64))
}

func createUserWithNotify(t *testing.T, h http.Handler, jar http.CookieJar,
	s *store.Store, username, method string, chID int64, email, smsPhone string) {
	t.Helper()
	body := map[string]any{
		"username": username, "password": "UserPass123",
		"role_id": roleIDByCode(t, s, "viewer"), "notify_method": method,
	}
	if chID > 0 {
		body["notify_channel_id"] = chID
	}
	if email != "" {
		body["notify_email"] = email
	}
	if smsPhone != "" {
		body["notify_sms_phone"] = smsPhone
	}
	if w := do(t, h, "POST", "/api/users", body, jar); w.Code != http.StatusOK {
		t.Fatalf("创建用户 %s: %d %s", username, w.Code, w.Body.String())
	}
}

// 定向下发：通知方式=通道时只投递到该用户绑定的那一条通道，不广播。
func TestResetCodeTargetedToBoundChannel(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	var hitsA, hitsB int
	var bodyA string
	hookA := countingHook(t, &hitsA, &bodyA)
	hookB := countingHook(t, &hitsB, nil)
	chA := createWebhookChannel(t, h, jar, "通道A", hookA.URL, true)
	createWebhookChannel(t, h, jar, "通道B", hookB.URL, true)

	createUserWithNotify(t, h, jar, s, "bob", "channel", chA, "", "")

	code := ""
	apiObj.SetResetCodeSink(func(_, c string) { code = c })
	w := do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "bob", "method": "notification"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	if code == "" {
		t.Fatal("codeSink 未捕获重置码")
	}
	if hitsA != 1 {
		t.Fatalf("绑定通道收到 %d 次，want 1", hitsA)
	}
	if hitsB != 0 {
		t.Fatalf("未绑定通道不应收到通知，实际 %d 次", hitsB)
	}
	if !strings.Contains(bodyA, code) {
		t.Fatalf("绑定通道未收到重置码: body=%s code=%s", bodyA, code)
	}

	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "bob", "code": code, "new_password": "BobNewPass1",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "bob", "password": "BobNewPass1"}, newJar(t))
	if w.Code != http.StatusOK {
		t.Fatalf("新密码登录: %d %s", w.Code, w.Body.String())
	}
}

// 短信平台未配置（含预留但未实现的华为云/百度云）时，验证码不能经短信下发。
func TestResetCodeSMSRequiresPlatform(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	var webhookHits int
	hook := countingHook(t, &webhookHits, nil)
	createWebhookChannel(t, h, jar, "兜底钩子", hook.URL, true)

	// 华为云：类型与配置项已预留，但平台未实现 → 不可用
	w := do(t, h, "POST", "/api/notifications/channels", map[string]any{
		"type": "sms", "name": "华为云短信", "enabled": true,
		"config": map[string]string{
			"provider": "huawei", "sign_name": "面板", "template_code": "SMS_1",
		},
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("创建短信通道: %d %s", w.Code, w.Body.String())
	}

	createUserWithNotify(t, h, jar, s, "carol", "sms", 0, "", "13900139000")

	u, err := s.UserByUsername("carol")
	if err != nil {
		t.Fatal(err)
	}
	// 直接断言定向下发被拒
	if _, err := apiObj.deliverToUserNotify(u, "ABCD1234"); err == nil ||
		!strings.Contains(err.Error(), "短信平台未配置") {
		t.Fatalf("短信平台未配置时应拒绝下发, got %v", err)
	}

	code := ""
	apiObj.SetResetCodeSink(func(_, c string) { code = c })
	w = do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "carol", "method": "notification"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	if code == "" {
		t.Fatal("短信不可用时仍应生成重置码（仅落服务日志兜底）")
	}
	if webhookHits != 0 {
		t.Fatalf("短信方式失败不应回退广播到其它通道，实际 %d 次", webhookHits)
	}

	// 兜底路径下，服务日志里的码仍可完成重置
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username": "carol", "code": code, "new_password": "CarolNewPass1",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
}

// 通知方式=log 时不产生任何外部投递。
func TestResetCodeLogMethodNoExternalDelivery(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	var hits int
	hook := countingHook(t, &hits, nil)
	createWebhookChannel(t, h, jar, "钩子", hook.URL, true)
	createUserWithNotify(t, h, jar, s, "dave", "log", 0, "", "")

	u, err := s.UserByUsername("dave")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := apiObj.deliverToUserNotify(u, "ABCD1234")
	if err != nil {
		t.Fatalf("log 方式不应报错: %v", err)
	}
	if delivery != "" {
		t.Fatalf("log 方式下发描述应为空, got %q", delivery)
	}

	w := do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "dave", "method": "notification"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	if hits != 0 {
		t.Fatalf("log 方式不应产生外部投递，实际 %d 次", hits)
	}
}

// 邮箱方式：用户未绑定邮箱且 SMTP 未配置时明确拒绝。
func TestResetCodeEmailWithoutSMTP(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	createUserWithNotify(t, h, jar, s, "erin", "email", 0, "erin@example.com", "")

	u, err := s.UserByUsername("erin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apiObj.deliverToUserNotify(u, "ABCD1234"); err == nil ||
		!strings.Contains(err.Error(), "SMTP") {
		t.Fatalf("SMTP 未配置时应拒绝下发, got %v", err)
	}
}
