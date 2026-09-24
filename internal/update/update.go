// Package update 面板在线更新：
// 从 GitHub Releases（Bet5521/onecloud-panel）检查新版本、下载对应架构二进制、
// sha256 校验后原子替换自身，最终由调用方通过 systemd 重启加载新版本。
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 仓库地址（版本校验与下载均基于此仓库）。
var (
	githubAPIURL  = "https://api.github.com/repos/Bet5521/onecloud-panel/releases/latest"
	githubDLBase  = "https://github.com/Bet5521/onecloud-panel/releases/download"
	checksumsName = "checksums.txt"
)

// Release GitHub Release 信息。
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
}

// Asset Release 资产文件。
type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"` // 形如 sha256:xxx（旧版 API 可能为空）
	BrowserDownloadURL string `json:"browser_download_url"`
}

// CheckResult 检查更新结果（API 响应）。
type CheckResult struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	HasUpdate   bool   `json:"has_update"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	AssetName   string `json:"asset_name,omitempty"`
	AssetSize   int64  `json:"asset_size,omitempty"`
	Unsupported string `json:"unsupported,omitempty"` // 当前平台无对应资产时的说明
}

// httpClient 复用连接；超时覆盖最慢的代理下载链路。
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// wrapProxy GitHub 加速代理改写（与 apps.withProxy 语义一致，额外覆盖 api.github.com）。
func wrapProxy(proxy, rawURL string) string {
	if proxy == "" {
		return rawURL
	}
	for _, p := range []string{
		"https://github.com/",
		"https://api.github.com/",
		"https://objects.githubusercontent.com/",
		"https://raw.githubusercontent.com/",
	} {
		if strings.HasPrefix(rawURL, p) {
			return strings.TrimSuffix(proxy, "/") + "/" + rawURL
		}
	}
	return rawURL
}

// FetchLatest 获取最新 Release：优先直连 GitHub API，失败且配置了代理时走代理重试。
func FetchLatest(ctx context.Context, proxy string) (*Release, error) {
	rel, directErr := fetchLatestOnce(ctx, "")
	if directErr == nil {
		return rel, nil
	}
	if proxy == "" {
		return nil, fmt.Errorf("查询最新版本失败: %w", directErr)
	}
	rel, err := fetchLatestOnce(ctx, proxy)
	if err != nil {
		return nil, fmt.Errorf("直连与代理均查询失败（直连: %v；代理: %w）", directErr, err)
	}
	return rel, nil
}

func fetchLatestOnce(ctx context.Context, proxy string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wrapProxy(proxy, githubAPIURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 Release 信息失败: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("Release 信息缺少 tag_name")
	}
	return &rel, nil
}

// AssetName 按当前 GOOS/GOARCH 计算 Release 资产名（与发布产物命名一致）；
// 返回空串表示当前平台无发布产物。GOARCH=arm 对应玩客云 armv7。
func AssetName(goos, goarch string) string {
	switch goos {
	case "linux":
		arch := goarch
		if goarch == "arm" {
			arch = "armv7"
		}
		if arch != "armv7" && arch != "arm64" && arch != "amd64" && arch != "386" {
			return ""
		}
		return "onecloud-panel-linux-" + arch
	case "windows":
		if goarch != "amd64" && goarch != "arm64" {
			return ""
		}
		return "onecloud-panel-windows-" + goarch + ".exe"
	case "darwin":
		if goarch != "amd64" && goarch != "arm64" {
			return ""
		}
		return "onecloud-panel-darwin-" + goarch
	default:
		return ""
	}
}

// parseSemver 解析 v1.2.3 / 1.2.3 形式的版本号。
func parseSemver(v string) ([3]int, error) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("非法版本号 %q", v)
	}
	for i, p := range parts {
		n := 0
		if p == "" {
			return out, fmt.Errorf("非法版本号 %q", v)
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return out, fmt.Errorf("非法版本号 %q", v)
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, nil
}

// CompareVersion 比较 a 与 b（a<b 返回 -1，相等 0，a>b 返回 1）；任一方非法返回错误。
func CompareVersion(a, b string) (int, error) {
	av, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

// BuildCheckResult 组装检查结果：对比当前版本与最新 Release，并定位当前平台资产。
func BuildCheckResult(current string, rel *Release) *CheckResult {
	return BuildCheckResultFor(current, rel, runtime.GOOS, runtime.GOARCH)
}

// BuildCheckResultFor 同上，平台参数可注入（测试使用）。
func BuildCheckResultFor(current string, rel *Release, goos, goarch string) *CheckResult {
	res := &CheckResult{
		Current:     current,
		Latest:      rel.TagName,
		Notes:       rel.Body,
		PublishedAt: rel.PublishedAt,
	}
	name := AssetName(goos, goarch)
	if name == "" {
		res.Unsupported = fmt.Sprintf("当前平台 %s/%s 暂无发布产物，请手动下载更新", goos, goarch)
		return res
	}
	res.AssetName = name
	if asset := FindAsset(rel, name); asset != nil {
		res.AssetSize = asset.Size
	}
	// dev 版本无法比较，一律提示可更新（由用户自行决定）。
	if current == "dev" {
		res.HasUpdate = true
		return res
	}
	if c, err := CompareVersion(current, rel.TagName); err == nil {
		res.HasUpdate = c < 0
	} else {
		// 当前版本号异常（如自定义构建），保守提示可更新。
		res.HasUpdate = true
	}
	return res
}

// FindAsset 按名称在 Release 中查找资产。
func FindAsset(rel *Release, name string) *Asset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// download 下载 URL 内容到 dest，返回实际下载字节数。
func download(ctx context.Context, url, dest string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载 %s 返回 %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	cerr := f.Close()
	if err != nil {
		return n, err
	}
	if cerr != nil {
		return n, cerr
	}
	return n, nil
}

// sha256File 计算文件 sha256 十六进制值。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expectedHash 确定资产的期望 sha256：优先使用 API digest 字段，
// 缺失时回退下载 checksums.txt 解析（<hash>  <filename>）。
func expectedHash(ctx context.Context, proxy string, rel *Release, asset *Asset) (string, error) {
	if d := asset.Digest; d != "" {
		if h, ok := strings.CutPrefix(d, "sha256:"); ok && len(h) == 64 {
			return strings.ToLower(h), nil
		}
	}
	url := wrapProxy(proxy, githubDLBase+"/"+rel.TagName+"/"+checksumsName)
	tmp, err := os.CreateTemp("", "ocp-checksums-")
	if err != nil {
		return "", err
	}
	dest := tmp.Name()
	tmp.Close()
	defer os.Remove(dest)
	if _, err := download(ctx, url, dest); err != nil {
		return "", fmt.Errorf("获取 %s 失败: %w", checksumsName, err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[1] == asset.Name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s 中未找到 %s 的校验值", checksumsName, asset.Name)
}

// Apply 执行在线更新的下载与替换部分（不负责重启）：
// 1) 获取最新 Release 并定位当前平台资产；2) 下载到可执行文件同目录 .new 临时文件；
// 3) sha256 校验；4) 原子替换当前二进制（旧文件备份为 .old，替换成功后删除）。
// 返回目标版本号。任何失败都会保持旧二进制完整。
func Apply(ctx context.Context, proxy string) (string, error) {
	rel, err := FetchLatest(ctx, proxy)
	if err != nil {
		return "", err
	}
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	if name == "" {
		return rel.TagName, fmt.Errorf("当前平台 %s/%s 暂无发布产物", runtime.GOOS, runtime.GOARCH)
	}
	asset := FindAsset(rel, name)
	if asset == nil {
		return rel.TagName, fmt.Errorf("Release %s 缺少资产 %s", rel.TagName, name)
	}

	exePath, err := os.Executable()
	if err != nil {
		return rel.TagName, fmt.Errorf("定位当前可执行文件失败: %w", err)
	}
	if exePath, err = filepath.Abs(exePath); err != nil {
		return rel.TagName, err
	}

	newPath := exePath + ".new"
	defer func() {
		_ = os.Remove(newPath) // 成功时已被 rename 走，失败时清理
	}()

	if _, err := download(ctx, wrapProxy(proxy, asset.BrowserDownloadURL), newPath); err != nil {
		return rel.TagName, fmt.Errorf("下载更新失败: %w", err)
	}
	want, err := expectedHash(ctx, proxy, rel, asset)
	if err != nil {
		return rel.TagName, err
	}
	got, err := sha256File(newPath)
	if err != nil {
		return rel.TagName, err
	}
	if got != want {
		return rel.TagName, fmt.Errorf("sha256 校验失败（期望 %s，实际 %s），已放弃更新", want, got)
	}

	// 原子替换：Linux/Windows 均允许 rename 正在运行的二进制。
	if err := replaceBinary(exePath, newPath); err != nil {
		return rel.TagName, err
	}
	return rel.TagName, nil
}

// replaceBinary 用 newPath 替换 exePath：旧文件先备份为 .old，替换成功后删除备份；
// 中途失败自动回滚，保证 exePath 始终是可用的完整二进制。
func replaceBinary(exePath, newPath string) error {
	oldPath := exePath + ".old"
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("备份旧二进制失败: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // 回滚
		return fmt.Errorf("替换二进制失败（已回滚）: %w", err)
	}
	if err := os.Chmod(exePath, 0o755); err != nil {
		_ = os.Remove(exePath)
		_ = os.Rename(oldPath, exePath) // 回滚
		return fmt.Errorf("设置执行权限失败（已回滚）: %w", err)
	}
	_ = os.Remove(oldPath)
	return nil
}
