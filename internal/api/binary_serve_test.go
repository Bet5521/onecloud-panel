package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
	"onecloud-panel/internal/update"
)

// 架构标识归一：uname -m / GOARCH 风格都应映射到发布产物后缀。
func TestAssetArchMapping(t *testing.T) {
	cases := map[string]string{
		"armv7l": "armv7", "armv6l": "armv7", "arm": "armv7", "armhf": "armv7",
		"aarch64": "arm64", "arm64": "arm64",
		"x86_64": "amd64", "amd64": "amd64",
		"i386": "386", "i686": "386", "386": "386",
		"": "", "mips": "",
	}
	for in, want := range cases {
		if got := assetArchFor(in); got != want {
			t.Errorf("assetArchFor(%q) = %q, want %q", in, got, want)
		}
	}
	if got := releaseAssetName("armv7l"); got != "onecloud-panel-linux-armv7" {
		t.Errorf("releaseAssetName(armv7l) = %q", got)
	}
	if got := releaseAssetName("loongarch64"); got != "" {
		t.Errorf("releaseAssetName(loongarch64) 应为空，实际 %q", got)
	}
}

// fakeRelease 用 httptest 承载 Release 资产内容，避免测试触网。
func fakeRelease(t *testing.T, name, payload string) (*httptest.Server, func(context.Context, string) (*update.Release, error)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	fetch := func(context.Context, string) (*update.Release, error) {
		return &update.Release{
			TagName: "v9.9.9",
			Assets: []update.Asset{{
				Name: name, Size: int64(len(payload)), BrowserDownloadURL: srv.URL + "/asset",
			}},
		}, nil
	}
	return srv, fetch
}

// 本地发布目录缺失时，/dl 应从在线 Release 拉取对应架构产物并转发（跨架构安装）。
func TestDownloadBinaryFromReleaseFallback(t *testing.T) {
	if runtime.GOARCH == "arm" {
		t.Skip("arm 主机上 armv7 会命中自身二进制，跳过回退路径")
	}
	_, _, apiObj := newTestAPI(t)
	payload := "release-armv7-binary"
	_, fetch := fakeRelease(t, "onecloud-panel-linux-armv7", payload)
	apiObj.SetReleaseFetcher(fetch)

	relDir := t.TempDir()
	apiObj.SetReleaseDir(relDir)
	h := apiObj.Handler()

	w := do(t, h, "GET", "/dl/onecloud-panel-linux-armv7", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != payload {
		t.Fatalf("内容不符: %q", w.Body.String())
	}
	if got := w.Header().Get("X-OCP-Binary-Source"); got != "release:v9.9.9" {
		t.Fatalf("来源标记 = %q", got)
	}
	// 回填缓存：本地发布目录出现该产物
	cached := filepath.Join(relDir, "onecloud-panel-linux-armv7")
	b, err := os.ReadFile(cached)
	if err != nil {
		t.Fatalf("应回填缓存: %v", err)
	}
	if string(b) != payload {
		t.Fatalf("缓存内容不符: %q", string(b))
	}
}

// Release 中不存在该产物 → 404（而非静默返回本机二进制）。
func TestDownloadBinaryReleaseMissingAsset(t *testing.T) {
	if runtime.GOARCH == "arm" {
		t.Skip("arm 主机上 armv7 会命中自身二进制")
	}
	_, _, apiObj := newTestAPI(t)
	_, fetch := fakeRelease(t, "onecloud-panel-linux-amd64", "x")
	apiObj.SetReleaseFetcher(fetch)
	apiObj.SetReleaseDir(t.TempDir())
	h := apiObj.Handler()

	if w := do(t, h, "GET", "/dl/onecloud-panel-linux-armv7", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

// agent-binary 必须按节点架构下发，而非面板自身架构（异构节点自升级）。
func TestAgentBinaryMatchesNodeArch(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	const tok = "node-token-armv7"
	now := time.Now().Unix()
	if _, err := s.CreateNode(&store.Node{
		Name: "玩客云-异构", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "192.168.6.145:9000",
		Arch: "armv7l", AgentTokenHash: hashB64(tok),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	payload := "armv7-agent-binary"
	_, fetch := fakeRelease(t, "onecloud-panel-linux-armv7", payload)
	apiObj.SetReleaseFetcher(fetch)
	apiObj.SetReleaseDir(t.TempDir())
	h := apiObj.Handler()

	w := do(t, h, "GET", "/api/agent-binary?t="+tok, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != payload {
		t.Fatalf("应为 armv7 产物，实际 %q", w.Body.String())
	}

	// 未知 Token → 401
	if w := do(t, h, "GET", "/api/agent-binary?t=bogus", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token code = %d, want 401", w.Code)
	}
}

// 节点架构不可识别时必须拒绝，避免推错架构二进制。
func TestAgentBinaryUnknownArch(t *testing.T) {
	_, s, apiObj := newTestAPI(t)
	const tok = "node-token-unknown"
	now := time.Now().Unix()
	if _, err := s.CreateNode(&store.Node{
		Name: "未知架构", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "10.0.0.9:9000",
		Arch: "", AgentTokenHash: hashB64(tok),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	apiObj.SetReleaseFetcher(func(context.Context, string) (*update.Release, error) {
		t.Fatal("未知架构不应触网查询 Release")
		return nil, nil
	})
	h := apiObj.Handler()
	if w := do(t, h, "GET", "/api/agent-binary?t="+tok, nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// system.AssetArch 与 api 层映射保持一致（防两处漂移）。
func TestSystemAssetArchParity(t *testing.T) {
	for _, a := range []string{"armv7l", "aarch64", "x86_64", "i386", "amd64", "", "foo"} {
		if system.AssetArch(a) != assetArchFor(a) {
			t.Fatalf("AssetArch(%q) 与 assetArchFor 不一致", a)
		}
	}
}
