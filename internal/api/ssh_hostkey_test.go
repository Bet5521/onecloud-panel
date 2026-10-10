package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"onecloud-panel/internal/auth"
)

// startHostKeyProbeServer 启动一个最小 SSH 服务端，仅用于主机密钥探测测试。
func startHostKeyProbeServer(t *testing.T) (host string, port int, fingerprint string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("构造签名者失败: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				_ = conn
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(ssh.UnknownChannelType, "test server")
				}
			}()
		}
	}()
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("拆分地址失败: %v", err)
	}
	pn, _ := strconv.Atoi(p)
	return h, pn, ssh.FingerprintSHA256(signer.PublicKey())
}

// 端到端契约：探测端点返回的 JSON 字段必须与前端约定一致
// （fingerprint / key_type / known / changed），首次为 known=false。
func TestSSHHostKeyProbeContract(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	host, port, want := startHostKeyProbeServer(t)
	w := do(t, h, "POST", "/api/nodes/ssh-hostkey",
		map[string]any{"host": host, "port": port}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("探测失败: code = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Fingerprint string `json:"fingerprint"`
		KeyType     string `json:"key_type"`
		Known       bool   `json:"known"`
		Changed     bool   `json:"changed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, w.Body.String())
	}
	if got.Fingerprint != want {
		t.Fatalf("指纹不符: got %s want %s", got.Fingerprint, want)
	}
	if got.KeyType == "" {
		t.Fatal("key_type 不应为空")
	}
	if got.Known || got.Changed {
		t.Fatalf("首次探测应为 known=false changed=false，实际 %+v", got)
	}
}

// 主机指纹探测入参校验：空主机名与越界端口都应 400。
func TestSSHHostKeyProbeValidation(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"缺主机", map[string]any{"port": 22}, "必填"},
		{"空白主机", map[string]any{"host": "   "}, "必填"},
		{"端口越界", map[string]any{"host": "127.0.0.1", "port": 70000}, "端口非法"},
	}
	for _, c := range cases {
		w := do(t, h, "POST", "/api/nodes/ssh-hostkey", c.body, jar)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (%s)", c.name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), c.want) {
			t.Fatalf("%s: 错误信息 %s 未包含 %q", c.name, w.Body.String(), c.want)
		}
	}

	// 请求体非法 JSON 亦应 400 而非 500。
	if w := do(t, h, "POST", "/api/nodes/ssh-hostkey", "not-json", jar); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON: code = %d, want 400", w.Code)
	}
}

// 目标不可达时应返回 502 且带可读原因，而不是 500。
func TestSSHHostKeyProbeUnreachable(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	w := do(t, h, "POST", "/api/nodes/ssh-hostkey",
		map[string]any{"host": "127.0.0.1", "port": 1}, jar)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("不可达主机: code = %d, want 502 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "探测失败") {
		t.Fatalf("错误信息应说明探测失败: %s", w.Body.String())
	}
}

// viewer 无 node:write：主机指纹探测应 403。
func TestSSHHostKeyProbeRequiresNodeWrite(t *testing.T) {
	_, s, h := newTestServer(t)
	_ = adminLogin(t, h)

	hash, _ := auth.HashPassword("ViewerPass1")
	if _, err := s.CreateUser("viewer2", hash, roleIDByCode(t, s, "viewer")); err != nil {
		t.Fatal(err)
	}
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "viewer2", "password": "ViewerPass1"}, jar); w.Code != http.StatusOK {
		t.Fatalf("viewer login: %d", w.Code)
	}

	w := do(t, h, "POST", "/api/nodes/ssh-hostkey",
		map[string]any{"host": "127.0.0.1", "port": 22}, jar)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer ssh-hostkey code = %d, want 403", w.Code)
	}
}
