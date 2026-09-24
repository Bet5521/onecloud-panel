package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// pointAPITo 将 githubAPIURL 指向 srv（直连形态），测试结束后还原。
func pointAPITo(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := githubAPIURL
	t.Cleanup(func() { githubAPIURL = orig })
	githubAPIURL = srv.URL + "/repos/Bet5521/onecloud-panel/releases/latest"
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"1.2.3", "v1.2.3", 0},
		{"v1.99.9", "v2.0.0", -1},
		{"v10.0.0", "v9.99.99", 1},
		{"v1.10.0", "v1.9.0", 1},
	}
	for _, c := range cases {
		got, err := CompareVersion(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersion(%q,%q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareVersion(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "v1.x.0", "v1"} {
		if _, err := CompareVersion(bad, "v1.0.0"); err == nil {
			t.Errorf("CompareVersion(%q) 应报错", bad)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "arm", "onecloud-panel-linux-armv7"},
		{"linux", "arm64", "onecloud-panel-linux-arm64"},
		{"linux", "amd64", "onecloud-panel-linux-amd64"},
		{"linux", "386", "onecloud-panel-linux-386"},
		{"linux", "riscv64", ""},
		{"windows", "amd64", "onecloud-panel-windows-amd64.exe"},
		{"windows", "386", ""},
		{"darwin", "arm64", "onecloud-panel-darwin-arm64"},
		{"freebsd", "amd64", ""},
	}
	for _, c := range cases {
		if got := AssetName(c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(%s,%s)=%q want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestWrapProxy(t *testing.T) {
	p := "https://gh-proxy.com"
	cases := []struct{ in, want string }{
		{"https://github.com/Bet5521/onecloud-panel/releases/download/v1/a", p + "/https://github.com/Bet5521/onecloud-panel/releases/download/v1/a"},
		{"https://api.github.com/repos/x/y", p + "/https://api.github.com/repos/x/y"},
		{"https://example.com/a", "https://example.com/a"},
	}
	for _, c := range cases {
		if got := wrapProxy(p, c.in); got != c.want {
			t.Errorf("wrapProxy(%q)=%q want %q", c.in, got, c.want)
		}
	}
	if got := wrapProxy("", "https://github.com/a"); got != "https://github.com/a" {
		t.Errorf("空代理不应改写: %q", got)
	}
}

// fakeReleaseServer 模拟 GitHub API / 下载 / checksums 服务。
func fakeReleaseServer(t *testing.T, assetContent []byte, digest string) *httptest.Server {
	t.Helper()
	assetName := AssetName("linux", "amd64")
	rel := Release{
		TagName:     "v2.0.0",
		Name:        "v2.0.0",
		Body:        "更新说明",
		PublishedAt: "2026-09-22T08:20:40Z",
		Assets: []Asset{
			{Name: assetName, Size: int64(len(assetContent)), Digest: digest,
				BrowserDownloadURL: "https://github.com/Bet5521/onecloud-panel/releases/download/v2.0.0/" + assetName},
			{Name: checksumsName, Size: 100},
		},
	}
	mux := http.NewServeMux()
	// 单一 catch-all：API（直连 /repos/... 与代理 /https://api.github.com/repos/... 两种形态）、
	// 下载与 checksums 均按后缀匹配。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/repos/Bet5521/onecloud-panel/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case strings.HasSuffix(r.URL.Path, "/"+checksumsName):
			sum := sha256.Sum256(assetContent)
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"))
		case strings.HasSuffix(r.URL.Path, "/"+assetName):
			_, _ = w.Write(assetContent)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchLatest(t *testing.T) {
	srv := fakeReleaseServer(t, []byte("bin"), "sha256:"+strings.Repeat("a", 64))
	pointAPITo(t, srv)

	rel, err := FetchLatest(context.Background(), "")
	if err != nil {
		t.Fatalf("FetchLatest 直连失败: %v", err)
	}
	if rel.TagName != "v2.0.0" || len(rel.Assets) != 2 {
		t.Fatalf("Release 解析异常: %+v", rel)
	}
}

func TestFetchLatestProxyFallback(t *testing.T) {
	srv := fakeReleaseServer(t, []byte("bin"), "")
	// 保持 githubAPIURL 为默认 https://api.github.com/...，注入传输层拦截直连，
	// 使代理改写路径（srv.URL + /https://api.github.com/...）真实生效。
	origClient := httpClient
	t.Cleanup(func() { httpClient = origClient })
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), "https://api.github.com/") {
			return nil, errors.New("直连被拦截")
		}
		return http.DefaultTransport.RoundTrip(req)
	})}

	// 无代理 → 失败
	if _, err := FetchLatest(context.Background(), ""); err == nil {
		t.Fatal("直连失败时应报错")
	}
	// 有代理 → 走代理成功
	rel, err := FetchLatest(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("代理回退失败: %v", err)
	}
	if rel.TagName != "v2.0.0" {
		t.Fatalf("代理回退版本异常: %s", rel.TagName)
	}
}

func TestExpectedHashFromDigest(t *testing.T) {
	content := []byte("new-binary-content")
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	srv := fakeReleaseServer(t, content, "sha256:"+want)
	pointAPITo(t, srv)

	rel, err := FetchLatest(context.Background(), "")
	if err != nil {
		t.Fatalf("FetchLatest: %v", err)
	}
	asset := FindAsset(rel, AssetName("linux", "amd64"))
	if asset == nil {
		t.Fatal("未找到资产")
	}
	got, err := expectedHash(context.Background(), srv.URL, rel, asset)
	if err != nil {
		t.Fatalf("expectedHash: %v", err)
	}
	if got != want {
		t.Fatalf("digest 校验值异常: %s want %s", got, want)
	}
}

func TestExpectedHashFromChecksums(t *testing.T) {
	content := []byte("new-binary-content-2")
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	// digest 留空 → 回退 checksums.txt
	srv := fakeReleaseServer(t, content, "")
	pointAPITo(t, srv)

	rel, err := FetchLatest(context.Background(), "")
	if err != nil {
		t.Fatalf("FetchLatest: %v", err)
	}
	asset := FindAsset(rel, AssetName("linux", "amd64"))
	if asset == nil {
		t.Fatal("未找到资产")
	}
	got, err := expectedHash(context.Background(), srv.URL, rel, asset)
	if err != nil {
		t.Fatalf("expectedHash: %v", err)
	}
	if got != want {
		t.Fatalf("checksums 解析异常: %s want %s", got, want)
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "panel")
	newBin := exe + ".new"
	if err := os.WriteFile(exe, []byte("old-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(exe, newBin); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	b, err := os.ReadFile(exe)
	if err != nil || string(b) != "new-content" {
		t.Fatalf("替换后内容异常: %s %v", b, err)
	}
	if _, err := os.Stat(newBin); !os.IsNotExist(err) {
		t.Fatalf(".new 应已被 rename 走: %v", err)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old 应已删除: %v", err)
	}
}

func TestReplaceBinaryRollback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "panel")
	newBin := filepath.Join(dir, "other", "new") // 所在目录不存在 → rename 必失败 → 回滚
	if err := os.WriteFile(exe, []byte("old-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(exe, newBin); err == nil {
		t.Fatal("应失败")
	}
	b, err := os.ReadFile(exe)
	if err != nil || string(b) != "old-content" {
		t.Fatalf("回滚后旧文件应完整: %s %v", b, err)
	}
}

func TestBuildCheckResult(t *testing.T) {
	rel := &Release{
		TagName: "v2.0.0",
		Assets:  []Asset{{Name: "onecloud-panel-linux-amd64", Size: 123}},
	}
	// dev → 提示可更新
	if r := BuildCheckResultFor("dev", rel, "linux", "amd64"); !r.HasUpdate || r.AssetName != "onecloud-panel-linux-amd64" {
		t.Fatalf("dev 结果异常: %+v", r)
	}
	// 旧版本 → 可更新
	if r := BuildCheckResultFor("v1.0.0", rel, "linux", "amd64"); !r.HasUpdate || r.AssetSize != 123 {
		t.Fatalf("旧版本结果异常: %+v", r)
	}
	// 相同版本 → 不更新
	if r := BuildCheckResultFor("v2.0.0", rel, "linux", "amd64"); r.HasUpdate {
		t.Fatalf("相同版本不应提示更新: %+v", r)
	}
	// 当前版本异常 → 保守提示可更新
	if r := BuildCheckResultFor("v1.2.3-custom", rel, "linux", "amd64"); !r.HasUpdate {
		t.Fatalf("异常版本应提示可更新: %+v", r)
	}
	// 平台无资产 → Unsupported 且不比较更新
	r := BuildCheckResultFor("v1.0.0", rel, "freebsd", "amd64")
	if r.Unsupported == "" || r.AssetName != "" {
		t.Fatalf("不支持平台结果异常: %+v", r)
	}
}
