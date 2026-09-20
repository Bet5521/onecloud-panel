package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
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
