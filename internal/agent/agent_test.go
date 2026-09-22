package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"onecloud-panel/internal/config"
)

// mockPanel 模拟面板的注册/心跳端点。
func mockPanel(t *testing.T, checkToken string, nodeToken string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(pathRegister, func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.RegisterToken != checkToken {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(&RegisterResponse{NodeID: 7, Token: nodeToken})
	})
	mux.HandleFunc(pathHeartbeat, func(w http.ResponseWriter, r *http.Request) {
		var req HeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Token != nodeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(&HeartbeatResponse{OK: true})
	})
	return httptest.NewServer(mux)
}

func TestRegisterAndHeartbeat(t *testing.T) {
	srv := mockPanel(t, "reg-123", "node-long-token")
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := Register(ctx, srv.URL, "reg-123", 9000, false)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.NodeID != 7 || resp.Token != "node-long-token" {
		t.Fatalf("register resp = %+v", resp)
	}

	// 错误注册令牌
	_, err = Register(ctx, srv.URL, "wrong", 9000, false)
	if err == nil {
		t.Fatal("错误注册令牌不应成功")
	}
}

func startAgentServer() (*tokenHolder, http.Handler) {
	h := &tokenHolder{}
	h.set("good-token")
	return h, newServer(h).mux()
}

func agentDo(h http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	if token != "" {
		r.Header.Set(authHeader, bearer+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAgentTokenAuth(t *testing.T) {
	_, h := startAgentServer()

	// healthz 无需 token
	w := agentDo(h, "GET", "/healthz", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("healthz = %d", w.Code)
	}

	// 无 token
	w = agentDo(h, "POST", "/v1/exec", ExecReq{Command: "go"}, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
	// 错误 token
	w = agentDo(h, "POST", "/v1/exec", ExecReq{Command: "go"}, "bad")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d, want 401", w.Code)
	}
	// 正确 token → 执行 go version
	w = agentDo(h, "POST", "/v1/exec",
		ExecReq{Command: "go", Args: []string{"version"}, TimeoutSeconds: 30}, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("exec = %d body=%s", w.Code, w.Body.String())
	}
	var er ExecResp
	_ = json.Unmarshal(w.Body.Bytes(), &er)
	if er.ExitCode != 0 || !bytes.Contains([]byte(er.Output), []byte("go version")) {
		t.Fatalf("exec resp = %+v", er)
	}

	// systemctl 非法动作
	w = agentDo(h, "POST", "/v1/systemctl",
		SystemctlReq{Action: "evil", Unit: "x"}, "good-token")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad systemctl = %d, want 400", w.Code)
	}
}

func TestAgentFileAndRotate(t *testing.T) {
	holder, h := startAgentServer()

	// PUT 文件（写到临时目录）
	p := filepath.Join(t.TempDir(), "conf.txt")
	w := agentDo(h, "PUT", "/v1/file",
		FileReq{Path: p, Content: "hello config"}, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("put file = %d body=%s", w.Code, w.Body.String())
	}
	// GET 文件
	w = agentDo(h, "GET", "/v1/file?path="+p, nil, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("get file = %d", w.Code)
	}
	var fr FileResp
	_ = json.Unmarshal(w.Body.Bytes(), &fr)
	if fr.Content != "hello config" {
		t.Fatalf("file content = %q", fr.Content)
	}

	// Token 轮换
	w = agentDo(h, "POST", "/v1/token/rotate",
		map[string]string{"token": "new-long-token-1234"}, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("rotate = %d", w.Code)
	}
	if holder.get() != "new-long-token-1234" {
		t.Fatalf("holder token = %s", holder.get())
	}
	// 旧 Token 失效
	w = agentDo(h, "POST", "/v1/exec", ExecReq{Command: "go"}, "good-token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("旧 token 应失效, got %d", w.Code)
	}
	// 过短的新 Token 拒绝
	w = agentDo(h, "POST", "/v1/token/rotate",
		map[string]string{"token": "short"}, "new-long-token-1234")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("短 token rotate = %d, want 400", w.Code)
	}
}

// TestAgentBinaryFileRoundTrip 回归测试：二进制文件必须经 base64 无损传输。
//
// 历史 bug：二进制被塞进 JSON 字符串字段（content），Go 的 encoding/json 会把
// 非法 UTF-8 字节替换成 U+FFFD（EF BF BD），导致文件体积膨胀且内容损坏。
// 真机表现为自定义应用 systemd 单元启动即 "Exec format error"（status=203/EXEC）。
func TestAgentBinaryFileRoundTrip(t *testing.T) {
	_, h := startAgentServer()
	p := filepath.Join(t.TempDir(), "bin")

	// 构造含 ELF 魔数 + 大量非法 UTF-8 字节的载荷
	payload := make([]byte, 0, 1<<18)
	payload = append(payload, 0x7f, 'E', 'L', 'F', 0x01, 0x01, 0x01, 0x00)
	for i := 0; i < 1<<16; i++ {
		payload = append(payload, byte(i%251), 0xff, 0xfe, 0x80)
	}

	w := agentDo(h, "PUT", "/v1/file",
		FileReq{Path: p, ContentB64: base64.StdEncoding.EncodeToString(payload)}, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("put binary = %d body=%s", w.Code, w.Body.String())
	}

	w = agentDo(h, "GET", "/v1/file?path="+p, nil, "good-token")
	if w.Code != http.StatusOK {
		t.Fatalf("get binary = %d", w.Code)
	}
	var fr FileResp
	if err := json.Unmarshal(w.Body.Bytes(), &fr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fr.ContentB64 == "" {
		t.Fatal("二进制响应缺少 content_b64")
	}
	got, err := base64.StdEncoding.DecodeString(fr.ContentB64)
	if err != nil {
		t.Fatalf("decode content_b64: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("二进制往返不一致: got %d bytes, want %d bytes", len(got), len(payload))
	}
	if fr.Content != "" {
		t.Fatalf("非法 UTF-8 内容不应填充 content 字段, got %d chars", len(fr.Content))
	}
}

// applyConfigToken：本地无身份才用传入 Token 兜底，已有身份不被 env 覆盖。
func TestApplyConfigToken(t *testing.T) {
	// 本地无身份（agent.json 丢失）→ 用 --token/env 兜底
	s := &State{}
	applyConfigToken(s, "env-token")
	if s.Token != "env-token" {
		t.Fatalf("无身份时应采用传入 Token, got %q", s.Token)
	}

	// 已有本地身份 → 保持不动，避免回退心跳轮换后的新 Token
	rotated := &State{Token: "rotated-token"}
	applyConfigToken(rotated, "stale-env-token")
	if rotated.Token != "rotated-token" {
		t.Fatalf("已有身份不应被 env 覆盖, got %q", rotated.Token)
	}

	// 两者皆空 → 仍为空（走注册流程）
	empty := &State{}
	applyConfigToken(empty, "")
	if empty.Token != "" {
		t.Fatalf("空 Token 不应写入, got %q", empty.Token)
	}
}

// 有 --token 时 Agent 应正常启动（install.sh 的已注册重装路径），
// 仅在既无本地身份又无任何 Token 时才报错退出。
func TestRunAcceptsConfigTokenWithoutState(t *testing.T) {
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(&config.Agent{
			Server:  "http://127.0.0.1:1",
			Token:   "seeded-token",
			DataDir: t.TempDir(),
			Listen:  "127.0.0.1:0",
		})
	}()
	select {
	case err := <-errCh:
		t.Fatalf("提供 --token 时不应因缺少注册令牌退出: %v", err)
	case <-time.After(700 * time.Millisecond):
	}

	if err := Run(&config.Agent{
		Server:  "http://127.0.0.1:1",
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
	}); err == nil {
		t.Fatal("既无本地身份又无 Token 时应报错")
	}
}
