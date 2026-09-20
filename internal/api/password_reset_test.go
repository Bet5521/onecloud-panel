package api

import (
	"net/http"
	"testing"
	"time"

	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

// 登录页密码重置完整链路：申请 → 错误码拒绝 → 正确码重置 → 旧密码失效/新密码可用 → 重置码不可复用。
func TestPasswordResetFlow(t *testing.T) {
	_, _, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	admin := adminLogin(t, h)

	var gotCode string
	apiObj.SetResetCodeSink(func(username, code string) {
		if username == "rootadmin" {
			gotCode = code
		}
	})

	// 申请重置（存在的用户）
	w := do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "rootadmin"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	okBody := w.Body.String()
	if len(gotCode) != 8 {
		t.Fatalf("应通过 sink 捕获 8 位重置码，得到 %q", gotCode)
	}

	// 防用户枚举：不存在用户的响应与存在用户完全一致
	w = do(t, h, "POST", "/api/auth/password-reset/request",
		map[string]string{"username": "nobodyhere"}, nil)
	if w.Code != http.StatusOK || w.Body.String() != okBody {
		t.Fatalf("防枚举应答不一致: %d %q vs %q", w.Code, w.Body.String(), okBody)
	}

	// 错误重置码 → 400
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username":     "rootadmin",
		"code":         "ZZZZZZZZ",
		"new_password": "BrandNewPass2",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("错误码应答 = %d, want 400", w.Code)
	}

	// 新密码长度不足 → 400
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username":     "rootadmin",
		"code":         gotCode,
		"new_password": "short",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("短密码应答 = %d, want 400", w.Code)
	}

	// 正确重置码（小写输入也应接受）→ 200
	lower := make([]byte, len(gotCode))
	for i := range gotCode {
		c := gotCode[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower[i] = c
	}
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username":     "rootadmin",
		"code":         string(lower),
		"new_password": "BrandNewPass2",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}

	// 旧密码登录失败
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码登录应答 = %d, want 401", w.Code)
	}
	// 新密码可登录
	jar := newJar(t)
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "BrandNewPass2"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("新密码登录应答 = %d %s", w.Code, w.Body.String())
	}

	// 重置成功后旧会话全部失效（强制重登）：原 admin jar 访问 me 应 401
	w = do(t, h, "GET", "/api/auth/me", nil, admin)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("重置后旧会话应答 = %d, want 401", w.Code)
	}

	// 重置码不可复用（记录已清理）
	w = do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username":     "rootadmin",
		"code":         gotCode,
		"new_password": "AnotherPass3",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重置码复用应答 = %d, want 400", w.Code)
	}
}

// 过期重置码应被拒绝。
func TestPasswordResetExpired(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	h := apiObj.Handler()
	_ = adminLogin(t, h)

	roles, _ := s.ListRoles()
	var rid int64
	for _, r := range roles {
		if r.Code == "viewer" {
			rid = r.ID
		}
	}
	hash, _ := auth.HashPassword("ViewerPass1")
	u, err := s.CreateUser("expireduser", hash, rid)
	if err != nil {
		t.Fatal(err)
	}

	// 写入一条已过期记录
	now := time.Now().Unix()
	if err := s.CreatePasswordReset(&store.PasswordReset{
		UserID:    u.ID,
		CodeHash:  hashResetCode("AAAAAAAA"),
		ExpiresAt: now - 60,
		CreatedAt: now - 600,
	}); err != nil {
		t.Fatal(err)
	}

	w := do(t, h, "POST", "/api/auth/password-reset/confirm", map[string]string{
		"username":     "expireduser",
		"code":         "AAAAAAAA",
		"new_password": "BrandNewPass2",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("过期码应答 = %d, want 400", w.Code)
	}
}
