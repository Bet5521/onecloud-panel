package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /install.sh 公开可下载，内容为安装脚本。
func TestInstallScriptRoute(t *testing.T) {
	_, _, apiObj := newTestAPI(t)
	h := apiObj.Handler()

	w := do(t, h, "GET", "/install.sh", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "x-shellscript") {
		t.Fatalf("content-type = %q", ct)
	}
	body := w.Body.String()
	for _, marker := range []string{"onecloud-panel", "systemctl", "detect_arch", "uninstall"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("脚本缺少 %q", marker)
		}
	}
}

// /dl/{name} 白名单下载各架构二进制。
func TestDownloadBinaryRoute(t *testing.T) {
	_, _, apiObj := newTestAPI(t)

	relDir := t.TempDir()
	payload := "fake-binary-armv7"
	if err := os.WriteFile(filepath.Join(relDir, "onecloud-panel-linux-armv7"),
		[]byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	apiObj.SetReleaseDir(relDir)
	h := apiObj.Handler()

	// 正常下载（无需登录）
	w := do(t, h, "GET", "/dl/onecloud-panel-linux-armv7", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dl code = %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != payload {
		t.Fatalf("内容不符: %q", w.Body.String())
	}

	// 白名单外文件名 → 400
	for _, bad := range []string{
		"/dl/onecloud-panel-linux-armv8",
		"/dl/..%2f..%2fpanel.db",
		"/dl/install.sh",
	} {
		if w = do(t, h, "GET", bad, nil, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s code = %d, want 400", bad, w.Code)
		}
	}

	// 未配置发布目录 → 404
	apiObj.SetReleaseDir("")
	if w = do(t, h, "GET", "/dl/onecloud-panel-linux-amd64", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("no release dir code = %d, want 404", w.Code)
	}

	// 请求目录中不存在的架构 → http.ServeFile 404
	apiObj.SetReleaseDir(relDir)
	if w = do(t, h, "GET", "/dl/onecloud-panel-linux-amd64", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing arch code = %d, want 404", w.Code)
	}
}
