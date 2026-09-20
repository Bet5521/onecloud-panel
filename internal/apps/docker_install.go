package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

// dockerArchMap 面板架构 → Docker apt 仓库架构。
var dockerArchMap = map[string]string{
	"armv7l":  "armhf",
	"aarch64": "arm64",
	"x86_64":  "amd64",
}

// osRelease 解析 /etc/os-release 的必要字段。
type osRelease struct {
	ID       string
	Codename string
	IDLike   string
}

// InstallDocker 在节点上按需安装 Docker Engine（containerd + engine + cli，
// 不含 compose、buildx 与桌面组件），安装后启用 systemd 自启并验证 Engine API。
func (m *Manager) InstallDocker(ctx context.Context, w io.Writer, nodeID int64) error {
	n, err := m.store.GetNode(nodeID)
	if err != nil {
		return err
	}
	ex, err := m.ExecutorFor(n)
	if err != nil {
		return err
	}
	dpkgArch, ok := dockerArchMap[n.Arch]
	if !ok {
		return fmt.Errorf("节点架构 %q 无官方 Docker 仓库支持（可手动安装后由面板自动识别）", n.Arch)
	}

	// 已安装则只刷新状态
	if ok2, ver, _ := m.DockerStatus(ctx, n); ok2 {
		_ = m.store.SetNodeDockerVersion(n.ID, ver)
		return fmt.Errorf("Docker 已安装（%s），无需重复安装", ver)
	}

	// 识别发行版
	rel, err := readOSRelease(ctx, ex)
	if err != nil {
		return err
	}
	family := "debian"
	if rel.ID == "ubuntu" || strings.Contains(rel.IDLike, "ubuntu") {
		family = "ubuntu"
	}
	if rel.Codename == "" {
		return fmt.Errorf("无法识别系统版本代号（/etc/os-release VERSION_CODENAME 为空）")
	}
	fmt.Fprintf(w, "目标系统: %s (%s/%s, %s)\n", rel.ID, family, rel.Codename, dpkgArch)

	run := func(step, script string) error {
		fmt.Fprintf(w, "→ %s\n", step)
		r, err := ex.Exec(ctx, "sh", "-c", script)
		if err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
		if r.Output != "" {
			fmt.Fprint(w, indentLines(r.Output))
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("%s 失败（退出码 %d）: %s", step, r.ExitCode, tail(r.Output, 300))
		}
		return nil
	}

	if err := run("刷新 apt 索引", "apt-get update -qq"); err != nil {
		return err
	}
	if err := run("安装基础依赖（ca-certificates/curl/gnupg）",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "+
			"ca-certificates curl gnupg"); err != nil {
		return err
	}
	if err := run("准备密钥目录", "install -m 0755 -d /etc/apt/keyrings"); err != nil {
		return err
	}
	// 官方源不可达时自动切换清华镜像（CN 网络常见）
	aptBase := "https://download.docker.com/linux/" + family
	probe, err := ex.Exec(ctx, "sh", "-c",
		"curl -fsS --connect-timeout 5 -m 10 -o /dev/null '"+aptBase+"/gpg'")
	if err != nil || probe.ExitCode != 0 {
		aptBase = "https://mirrors.tuna.tsinghua.edu.cn/docker-ce/linux/" + family
		fmt.Fprintln(w, "  官方源不可达，切换清华镜像: "+aptBase)
	}

	if err := run("导入 Docker GPG 密钥",
		"curl -fsSL '"+aptBase+
			"/gpg' -o /etc/apt/keyrings/docker.asc && chmod a+r /etc/apt/keyrings/docker.asc"); err != nil {
		return err
	}

	sources := fmt.Sprintf(
		"deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] %s %s stable\n",
		dpkgArch, aptBase, rel.Codename)
	if err := ex.WriteFile("/etc/apt/sources.list.d/docker.list", []byte(sources)); err != nil {
		return fmt.Errorf("写入 apt 源失败: %w", err)
	}
	fmt.Fprintln(w, "→ 写入 /etc/apt/sources.list.d/docker.list")

	if err := run("刷新 Docker 仓库索引", "apt-get update -qq"); err != nil {
		return err
	}
	// 仅 engine/cli/containerd；显式不装 compose 与 buildx
	if err := run("安装 containerd + Docker Engine（无 compose/桌面组件）",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "+
			"docker-ce docker-ce-cli containerd.io"); err != nil {
		return err
	}
	// 先写镜像加速配置，再首次启动 docker（避免写配置后的二次重启）
	mirrors, insecure := m.mergedDockerConfig(n)
	if err := writeDockerDaemonConfig(ctx, w, ex, mirrors, insecure); err != nil {
		return err
	}
	if err := run("启用并启动 docker 服务（开机自启）",
		"systemctl enable docker && systemctl start docker"); err != nil {
		return err
	}

	// 等待 Engine API 就绪
	eng, err := m.EngineFor(n)
	if err != nil {
		return err
	}
	var ver string
	for i := 0; i < 15; i++ {
		if err := eng.Ping(ctx); err == nil {
			if v, verr := eng.Version(ctx); verr == nil {
				ver = v.Version
			}
			break
		}
		fmt.Fprintln(w, "  等待 Docker Engine 就绪…")
		time.Sleep(2 * time.Second)
	}
	if ver == "" {
		if err := eng.Ping(ctx); err != nil {
			return fmt.Errorf("安装完成但 Engine API 不可用: %w", err)
		}
	}
	if err := m.store.SetNodeDockerVersion(n.ID, ver); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ Docker Engine %s 就绪\n", ver)
	return nil
}

// defaultDockerMirrors 默认追加的 CN 镜像加速。
var defaultDockerMirrors = []string{
	"https://docker.1ms.run",
	"https://docker.xuanyuan.me",
	"https://docker.m.daocloud.io",
}

// mergedDockerConfig 合并面板级与节点级 Docker 镜像加速/第三方仓库配置。
// 节点级优先于面板级；最终合并默认镜像。
func (m *Manager) mergedDockerConfig(n *store.Node) (mirrors, insecure []string) {
	mirrors = append(mirrors, defaultDockerMirrors...)
	// 面板级
	if v, _, _ := m.store.GetSetting("docker_registry_mirrors"); v != "" {
		mirrors = mergeMirrors(nil, splitList(v))
	}
	// 节点级覆盖
	if n.DockerMirrors != "" {
		mirrors = splitList(n.DockerMirrors)
	}
	if v, _, _ := m.store.GetSetting("docker_insecure_registries"); v != "" {
		insecure = append(insecure, splitList(v)...)
	}
	if n.DockerInsecureRegistries != "" {
		insecure = splitList(n.DockerInsecureRegistries)
	}
	insecure = dedup(insecure)
	return
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	}) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// writeDockerDaemonConfig 合并写入镜像加速与第三方仓库配置（保留用户已有配置与其它字段，
// 首次覆盖前备份一次 daemon.json.bak）；在 docker 首次启动前调用，无需重启。
func writeDockerDaemonConfig(ctx context.Context, w io.Writer, ex executor.Executor, mirrors, insecure []string) error {
	if err := mkdirAll(ctx, ex, "/etc/docker"); err != nil {
		return err
	}
	cfg := map[string]any{}
	var existing []byte
	if b, err := ex.ReadFile("/etc/docker/daemon.json"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		existing = b
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("现有 /etc/docker/daemon.json 解析失败，请手工检查后重试: %w", err)
		}
	}
	cfg["registry-mirrors"] = mergeMirrors(cfg["registry-mirrors"], mirrors)
	if len(insecure) > 0 {
		cfg["insecure-registries"] = mergeStringList(cfg["insecure-registries"], insecure)
	} else {
		// 未配置时不主动清空用户已有的 insecure-registries
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if existing != nil && bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(out)) {
		fmt.Fprintln(w, "→ /etc/docker/daemon.json 已包含镜像加速配置，无需修改")
		return nil
	}
	if existing != nil {
		if _, err := ex.Exec(ctx, "sh", "-c",
			"cp -n /etc/docker/daemon.json /etc/docker/daemon.json.bak 2>/dev/null || true"); err != nil {
			return err
		}
		fmt.Fprintln(w, "→ 已备份原配置为 /etc/docker/daemon.json.bak")
	}
	fmt.Fprintln(w, "→ 写入镜像加速与仓库配置 /etc/docker/daemon.json")
	if err := ex.WriteFile("/etc/docker/daemon.json", out); err != nil {
		return fmt.Errorf("写入 daemon.json 失败: %w", err)
	}
	return nil
}

func mergeStringList(cur any, defaults []string) []string {
	seen := map[string]bool{}
	var out []string
	if arr, ok := cur.([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, s := range defaults {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mergeMirrors 将默认 mirrors 并入已有列表（已有在前、去重）。
func mergeMirrors(cur any, defaults []string) []string {
	seen := map[string]bool{}
	var out []string
	if arr, ok := cur.([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, s := range defaults {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func readOSRelease(ctx context.Context, ex executor.Executor) (*osRelease, error) {
	r, err := ex.Exec(ctx, "sh", "-c",
		`. /etc/os-release 2>/dev/null && echo "${ID}|${VERSION_CODENAME}|${ID_LIKE}"`)
	if err != nil {
		return nil, fmt.Errorf("读取 /etc/os-release 失败: %w", err)
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("非 systemd/Debian 系系统暂不支持自动安装 Docker（os-release 读取失败）")
	}
	parts := strings.SplitN(strings.TrimSpace(r.Output), "|", 3)
	rel := &osRelease{ID: parts[0]}
	if len(parts) >= 2 {
		rel.Codename = parts[1]
	}
	if len(parts) == 3 {
		rel.IDLike = parts[2]
	}
	if rel.ID == "" {
		return nil, fmt.Errorf("无法识别系统发行版（ID 为空）")
	}
	if !strings.Contains(rel.ID, "debian") && !strings.Contains(rel.IDLike, "debian") &&
		!strings.Contains(rel.ID, "ubuntu") && !strings.Contains(rel.IDLike, "ubuntu") {
		return nil, fmt.Errorf("自动安装仅支持 Debian/Ubuntu 系（当前 %s）", rel.ID)
	}
	return rel, nil
}
