package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// 自签模式启用：证书/私钥文件生成，设置项落库；关闭后开关归零。
func TestTLSSettingsSelfSigned(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	dataDir := t.TempDir()
	apiObj.SetDataDir(dataDir)
	h := apiObj.Handler()
	admin := adminLogin(t, h)

	// 启用自签 HTTPS + 强制跳转
	w := do(t, h, "POST", "/api/settings/tls", map[string]any{
		"enabled":     true,
		"mode":        "selfsigned",
		"hosts":       "panel.local, 192.168.1.194",
		"force_https": true,
	}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("enable tls: %d %s", w.Code, w.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["restart_required"] != true {
		t.Fatalf("restart_required 应为 true: %s", w.Body.String())
	}

	// 设置项落库
	cases := map[string]string{
		"tls_enabled":     "1",
		"tls_mode":        "selfsigned",
		"tls_hosts":       "panel.local,192.168.1.194",
		"tls_force_https": "1",
	}
	for k, want := range cases {
		if v, _, _ := s.GetSetting(k); v != want {
			t.Fatalf("%s = %q, want %q", k, v, want)
		}
	}

	// 证书/私钥文件生成且可加载
	certFile, _, _ := s.GetSetting("tls_cert_file")
	keyFile, _, _ := s.GetSetting("tls_key_file")
	for _, f := range []string{certFile, keyFile} {
		if fi, err := os.Stat(f); err != nil || fi.Size() == 0 {
			t.Fatalf("证书文件缺失: %s %v", f, err)
		}
	}
	if filepath.Dir(certFile) != filepath.Join(dataDir, "tls") {
		t.Fatalf("证书应落在数据目录 tls/ 下: %s", certFile)
	}

	// 关闭 HTTPS：开关归零，证书文件保留
	w = do(t, h, "POST", "/api/settings/tls", map[string]any{
		"enabled": false,
	}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("disable tls: %d %s", w.Code, w.Body.String())
	}
	if v, _, _ := s.GetSetting("tls_enabled"); v != "0" {
		t.Fatalf("关闭后 tls_enabled = %q, want 0", v)
	}
	if _, err := os.Stat(certFile); err != nil {
		t.Fatalf("关闭不应删除证书文件: %v", err)
	}
}

// 自定义模式：证书与私钥不匹配应拒绝（400）。
func TestTLSSettingsCustomBadPair(t *testing.T) {
	_, _, apiObj := newTestAPI(t)
	apiObj.SetDataDir(t.TempDir())
	h := apiObj.Handler()
	admin := adminLogin(t, h)

	w := do(t, h, "POST", "/api/settings/tls", map[string]any{
		"enabled": true,
		"mode":    "custom",
		"cert_pem": "-----BEGIN CERTIFICATE-----\n" +
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n-----END CERTIFICATE-----\n",
		"key_pem": "-----BEGIN PRIVATE KEY-----\n" +
			"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n-----END PRIVATE KEY-----\n",
	}, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("坏证书应答 = %d %s, want 400", w.Code, w.Body.String())
	}

	// 非法 mode
	w = do(t, h, "POST", "/api/settings/tls", map[string]any{
		"enabled": true,
		"mode":    "letsencrypt",
	}, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应答 = %d, want 400", w.Code)
	}
}
