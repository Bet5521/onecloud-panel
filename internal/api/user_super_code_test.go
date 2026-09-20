package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"onecloud-panel/internal/auth"
)

// 从 setup/create 响应中解析用户 DTO。
func decodeUserDTO(t *testing.T, body []byte) userDTO {
	t.Helper()
	var dto userDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decode user dto: %v body=%s", err, body)
	}
	return dto
}

func TestSetupReturnsSuperCode(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := newJar(t)

	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "firstadmin", "password": "FirstPass1"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode setup resp: %v", err)
	}
	code := resp["super_code"]
	if len(code) != 10 {
		t.Fatalf("super_code 长度 = %d, want 10: %q", len(code), code)
	}
	u, err := s.UserByUsername("firstadmin")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if !auth.VerifySuperCode(code, u.SuperCode) {
		t.Fatal("超级验证码哈希与明文不匹配")
	}
}

func TestUserSuperCodeLifecycle(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := newJar(t)

	// 初始化（setup 自动登录为管理员）
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	// 创建用户 → 返回一次明文验证码
	w = do(t, h, "POST", "/api/users",
		map[string]any{"username": "alice", "password": "AlicePass1", "role_id": 2}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	dto := decodeUserDTO(t, w.Body.Bytes())
	firstCode := dto.SuperCode
	if len(firstCode) != 10 {
		t.Fatalf("create 响应缺少 super_code: %s", w.Body.String())
	}
	if dto.RealName != "" || dto.Phone != "" {
		t.Fatal("新用户资料应为空")
	}
	au, err := s.UserByUsername("alice")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if !auth.VerifySuperCode(firstCode, au.SuperCode) {
		t.Fatal("创建用户超级验证码哈希不匹配")
	}

	// 管理员更新姓名/手机号
	w = do(t, h, "PUT", "/api/users/"+itoa(dto.ID),
		map[string]any{"real_name": "张三", "phone": "13800138000"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update profile: %d %s", w.Code, w.Body.String())
	}
	dto = decodeUserDTO(t, w.Body.Bytes())
	if dto.RealName != "张三" || dto.Phone != "13800138000" {
		t.Fatalf("资料未生效: %+v", dto)
	}
	if dto.SuperCode != "" {
		t.Fatal("常规响应不应包含超级验证码")
	}

	// 过长姓名拒绝
	w = do(t, h, "PUT", "/api/users/"+itoa(dto.ID),
		map[string]any{"real_name": strings.Repeat("长", 33)}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("过长姓名 code = %d, want 400", w.Code)
	}

	// 本人更新资料
	aliceJar := newJar(t)
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "alice", "password": "AlicePass1"}, aliceJar)
	if w.Code != http.StatusOK {
		t.Fatalf("alice login: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "PUT", "/api/auth/profile",
		map[string]string{"real_name": "李四", "phone": "13900139000"}, aliceJar)
	if w.Code != http.StatusOK {
		t.Fatalf("self profile: %d %s", w.Code, w.Body.String())
	}
	au, _ = s.UserByUsername("alice")
	if au.RealName != "李四" || au.Phone != "13900139000" {
		t.Fatalf("本人资料未生效: %s %s", au.RealName, au.Phone)
	}

	// 重新生成超级验证码：旧码失效，新码可用
	w = do(t, h, "POST", "/api/users/"+itoa(dto.ID)+"/super-code", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rotate resp: %v", err)
	}
	newCode := resp["super_code"]
	if len(newCode) != 10 || newCode == firstCode {
		t.Fatalf("新验证码异常: %q", newCode)
	}
	au, _ = s.UserByUsername("alice")
	if auth.VerifySuperCode(firstCode, au.SuperCode) {
		t.Fatal("旧验证码应已失效")
	}
	if !auth.VerifySuperCode(newCode, au.SuperCode) {
		t.Fatal("新验证码哈希不匹配")
	}

	// 管理员重置密码 → 轮换验证码并注销目标会话
	w = do(t, h, "POST", "/api/users/"+itoa(dto.ID)+"/password",
		map[string]string{"password": "NewPass123"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode reset resp: %v", err)
	}
	resetCode := resp["super_code"]
	if len(resetCode) != 10 {
		t.Fatal("重置密码响应缺少新验证码")
	}
	au, _ = s.UserByUsername("alice")
	if !auth.VerifySuperCode(resetCode, au.SuperCode) {
		t.Fatal("重置密码后验证码未轮换")
	}
	// 旧会话已注销
	w = do(t, h, "GET", "/api/auth/me", nil, aliceJar)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("重置密码后旧会话应失效, got %d", w.Code)
	}
	// 新密码可登录
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "alice", "password": "NewPass123"}, newJar(t))
	if w.Code != http.StatusOK {
		t.Fatalf("new password login: %d", w.Code)
	}
}
