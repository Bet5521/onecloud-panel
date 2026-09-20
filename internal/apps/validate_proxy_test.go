package apps

import (
	"testing"

	"onecloud-panel/internal/recipes"
)

func TestWithProxy(t *testing.T) {
	const px = "https://gh-proxy.com"
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"github", "https://github.com/a/b/releases/v1/x.gz",
			"https://gh-proxy.com/https://github.com/a/b/releases/v1/x.gz"},
		{"raw", "https://raw.githubusercontent.com/a/b/main/x.yaml",
			"https://gh-proxy.com/https://raw.githubusercontent.com/a/b/main/x.yaml"},
		{"codeload", "https://codeload.github.com/a/b/tar.gz/refs/tags/v1",
			"https://gh-proxy.com/https://codeload.github.com/a/b/tar.gz/refs/tags/v1"},
		{"objects", "https://objects.githubusercontent.com/foo",
			"https://gh-proxy.com/https://objects.githubusercontent.com/foo"},
		{"other", "https://example.com/x", "https://example.com/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withProxy(px, c.url); got != c.want {
				t.Errorf("withProxy = %s, want %s", got, c.want)
			}
		})
	}
	if got := withProxy("", "https://github.com/x"); got != "https://github.com/x" {
		t.Errorf("empty proxy should passthrough, got %s", got)
	}
}

func TestValidateVariablesBuiltin(t *testing.T) {
	reg, err := recipes.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	m := &Manager{recipes: reg}

	// cloudflared：含换行 → 拒绝（systemd 指令注入）
	err = m.ValidateInstallVars("cloudflared", map[string]string{
		"tunnel_token": "abc\nExecStartPre=/bin/touch /tmp/pwn",
	})
	if err == nil {
		t.Fatal("含换行的 token 必须被拒绝")
	}

	// cloudflared：合法 token → 通过
	if err := m.ValidateInstallVars("cloudflared", map[string]string{
		"tunnel_token": "eyJhbGciOiJSUzI1NiIs.4j_6Q-tCvX9z.abc",
	}); err != nil {
		t.Fatalf("合法 token 被拒: %v", err)
	}

	// cloudflared：含 % → 拒绝（systemd specifier）
	if err := m.ValidateInstallVars("cloudflared", map[string]string{
		"tunnel_token": "abc%n",
	}); err == nil {
		t.Fatal("含 % 的 token 必须被拒绝")
	}

	// mihomo：端口必须为整数
	if err := m.ValidateInstallVars("mihomo", map[string]string{
		"proxy_port": "abc",
		"api_port":   "9090",
	}); err == nil {
		t.Fatal("非整数端口必须被拒绝")
	}
	if err := m.ValidateInstallVars("mihomo", map[string]string{
		"proxy_port": "7890",
		"api_port":   "9090",
	}); err != nil {
		t.Fatalf("合法端口被拒: %v", err)
	}
}
