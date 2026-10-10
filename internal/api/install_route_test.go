package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"onecloud-panel/internal/system"
	"onecloud-panel/internal/update"
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

// /dl/{name} 白名单下载各架构二进制：
// 本机架构下发面板自身；其他架构取本地发布目录，缺失时从在线 Release 转发。
func TestDownloadBinaryRoute(t *testing.T) {
	_, _, apiObj := newTestAPI(t)

	relDir := t.TempDir()
	payload := "fake-binary-armv7"
	if err := os.WriteFile(filepath.Join(relDir, "onecloud-panel-linux-armv7"),
		[]byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	apiObj.SetReleaseDir(relDir)
	// 避免测试触网：本地已有的 armv7 命中发布目录，本机架构命中自身二进制。
	apiObj.SetReleaseFetcher(func(context.Context, string) (*update.Release, error) {
		return nil, errors.New("测试不应触网")
	})
	h := apiObj.Handler()

	// 非本机架构且本地存在 → 直接下发
	if runtime.GOARCH != "arm" {
		w := do(t, h, "GET", "/dl/onecloud-panel-linux-armv7", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("dl code = %d body=%s", w.Code, w.Body.String())
		}
		if w.Body.String() != payload {
			t.Fatalf("内容不符: %q", w.Body.String())
		}
	}

	// 白名单外文件名 → 400
	for _, bad := range []string{
		"/dl/onecloud-panel-linux-armv8",
		"/dl/..%2f..%2fpanel.db",
		"/dl/install.sh",
	} {
		if w := do(t, h, "GET", bad, nil, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s code = %d, want 400", bad, w.Code)
		}
	}

	// 本机架构 → 下发面板自身可执行文件（无需发布目录）
	own := "onecloud-panel-linux-" + ownAssetArch()
	apiObj.SetReleaseDir("")
	if w := do(t, h, "GET", "/dl/"+own, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("own arch code = %d, want 200", w.Code)
	} else if w.Body.Len() == 0 {
		t.Fatal("own arch 返回空内容")
	} else if got := w.Header().Get("X-OCP-Binary-Source"); got != "self" {
		t.Fatalf("own arch source = %q, want self", got)
	}
}

// ownAssetArch 测试辅助：当前测试进程对应的发布产物后缀。
func ownAssetArch() string {
	a := system.AssetArch(runtime.GOARCH)
	if a == "" {
		a = runtime.GOARCH
	}
	return a
}
