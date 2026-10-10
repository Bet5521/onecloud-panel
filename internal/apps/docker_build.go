package apps

import (
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/recipes"
)

// commitRefRe 形如提交哈希的引用（40/64 位十六进制）。
// git clone --branch 不接受裸提交号，这类引用需完整克隆后再 checkout。
var commitRefRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)

// buildImage 在节点上从源码构建容器镜像，返回产物镜像 tag。
//
// 流程：解析产物 tag → （可选）跳过已存在的本地镜像 → 清理工作目录 →
// 获取源码（git 克隆 / 下载归档）→ 执行 `docker build` → 校验产物存在。
//
// 与「拉取远端镜像」互斥：DockerInstall 在 DockerSpec.Build 非空时走本函数，
// 否则仍按原逻辑 pullImage。构建产物交由 Engine 管理，后续创建容器完全一致。
func (m *Manager) buildImage(ctx context.Context, w io.Writer, eng *docker.Engine,
	ex executor.Executor, recipeID string, ds *recipes.DockerSpec) (string, error) {

	b := ds.Build
	tag := strings.TrimSpace(b.Image)
	if tag == "" {
		tag = strings.TrimSpace(ds.Image)
	}
	if tag == "" {
		return "", fmt.Errorf("从源码构建缺少产物镜像名（docker.build.image 或 docker.image）")
	}

	// 每次安装都按当前源码重新构建：同一提交的源码内容一致，Docker 层缓存会让
	// 重复构建非常快；源码有更新时才会真正重跑编译，避免「静默沿用旧镜像」。
	if ii, err := inspectImage(ctx, eng, tag); err == nil && ii != nil && ii.ID != "" {
		fmt.Fprintf(w, "→ 本地已存在镜像 %s（%s），将按当前源码重建\n", tag, shortID(ii.ID))
	}

	// docker CLI 是构建的必要条件（构建产物仍由 Engine API 管理）。
	if !commandOK(ctx, ex, "docker", "--version") {
		return "", fmt.Errorf("节点未安装 docker 命令行，无法从源码构建镜像")
	}

	buildType := strings.TrimSpace(b.Type)
	if buildType == "" {
		buildType = "git"
	}
	workdir := strings.TrimSpace(b.Workdir)
	if workdir == "" {
		workdir = "/var/tmp/ocp-build-" + strings.ReplaceAll(recipeID, "_", "-")
	}

	fmt.Fprintf(w, "→ 从源码构建镜像 %s（方式：%s，工作目录：%s）\n", tag, buildType, workdir)

	switch buildType {
	case "git":
		if !commandOK(ctx, ex, "git", "--version") {
			return "", fmt.Errorf("节点未安装 git，无法克隆源码（可在 build.source 改用 archive 方式）")
		}
	default: // archive
		if _, ok := ex.(executor.Downloader); !ok {
			return "", fmt.Errorf("该节点执行器不支持下载，无法获取源码归档")
		}
	}

	// 清理并重建工作目录，保证每次构建都是干净源码。
	if err := mustRun(ctx, nil, ex, "rm", "-rf", workdir); err != nil {
		return "", fmt.Errorf("清理工作目录失败: %w", err)
	}
	if err := mustRun(ctx, nil, ex, "mkdir", "-p", workdir); err != nil {
		return "", fmt.Errorf("创建工作目录失败: %w", err)
	}

	switch buildType {
	case "git":
		if err := m.fetchGitSource(ctx, w, ex, b.Source, b.Ref, workdir); err != nil {
			return "", err
		}
	default:
		if err := m.fetchArchiveSource(ctx, w, ex, recipeID, b.Source, workdir); err != nil {
			return "", err
		}
	}

	if err := m.dockerBuild(ctx, w, ex, b, workdir, tag); err != nil {
		return "", err
	}

	// 产物自检：确认 Engine 侧确实存在该镜像。
	if ii, err := inspectImage(ctx, eng, tag); err != nil || ii == nil || ii.ID == "" {
		return "", fmt.Errorf("镜像 %s 构建后未在本地找到，请检查构建日志", tag)
	}

	// 构建成功后清理源码，避免在低配设备上长期占用磁盘。
	if err := mustRun(ctx, nil, ex, "rm", "-rf", workdir); err != nil {
		fmt.Fprintf(w, "  警告: 清理工作目录失败（可忽略）: %v\n", err)
	}
	fmt.Fprintf(w, "✓ 镜像 %s 构建完成\n", tag)
	return tag, nil
}

// fetchGitSource 克隆 git 源码到 workdir。
func (m *Manager) fetchGitSource(ctx context.Context, w io.Writer,
	ex executor.Executor, source, ref, workdir string) error {

	src := strings.TrimSpace(source)
	ref = strings.TrimSpace(ref)
	fmt.Fprintf(w, "→ 克隆 %s（%s）\n", src, ref)

	if commitRefRe.MatchString(ref) {
		if err := mustRun(ctx, w, ex, "git", "clone", src, workdir); err != nil {
			return fmt.Errorf("克隆源码失败: %w", err)
		}
		if err := mustRun(ctx, w, ex, "git", "-C", workdir, "checkout", "--force", ref); err != nil {
			return fmt.Errorf("检出提交 %s 失败: %w", ref, err)
		}
		return nil
	}
	if err := mustRun(ctx, w, ex, "git", "clone", "--depth", "1", "--branch", ref, src, workdir); err != nil {
		return fmt.Errorf("克隆源码失败: %w", err)
	}
	return nil
}

// fetchArchiveSource 下载源码归档并解包到 workdir（GitHub 归档已带一层顶层目录，故 strip 1）。
func (m *Manager) fetchArchiveSource(ctx context.Context, w io.Writer,
	ex executor.Executor, recipeID, source, workdir string) error {

	dl, _ := ex.(executor.Downloader)
	url := withProxy(m.githubProxy(), strings.TrimSpace(source))
	archive := "/var/tmp/ocp-src-" + strings.ReplaceAll(recipeID, "_", "-") + ".tar.gz"
	fmt.Fprintf(w, "→ 下载源码归档 %s\n", url)
	if err := dl.Download(ctx, url, archive, 0o644, "", w); err != nil {
		return fmt.Errorf("下载源码归档失败: %w", err)
	}
	if err := mustRun(ctx, w, ex, "tar", "-xzf", archive, "--strip-components=1", "-C", workdir); err != nil {
		return fmt.Errorf("解包源码归档失败: %w", err)
	}
	if err := mustRun(ctx, nil, ex, "rm", "-f", archive); err != nil {
		fmt.Fprintf(w, "  警告: 清理源码归档失败（可忽略）: %v\n", err)
	}
	return nil
}

// dockerBuild 在 workdir 上执行 docker build。
func (m *Manager) dockerBuild(ctx context.Context, w io.Writer,
	ex executor.Executor, b *recipes.DockerBuildSpec, workdir, tag string) error {

	dockerfile := strings.TrimSpace(b.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	contextDir := strings.TrimSpace(b.Context)
	if contextDir == "" {
		contextDir = "."
	}
	file := path.Join(workdir, dockerfile)
	buildCtx := path.Join(workdir, contextDir)

	// 构建环境变量：默认启用 BuildKit（多阶段 Dockerfile 的 --mount / TARGETARCH 依赖它）。
	env := map[string]string{"DOCKER_BUILDKIT": "1"}
	for k, v := range b.Env {
		env[k] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	args := []string{}
	for _, k := range keys {
		args = append(args, k+"="+env[k])
	}
	args = append(args, "docker", "build", "-t", tag, "-f", file)
	if t := strings.TrimSpace(b.Target); t != "" {
		args = append(args, "--target", t)
	}
	if len(b.Args) > 0 {
		bk := make([]string, 0, len(b.Args))
		for k := range b.Args {
			bk = append(bk, k)
		}
		sort.Strings(bk)
		for _, k := range bk {
			args = append(args, "--build-arg", k+"="+b.Args[k])
		}
	}
	args = append(args, buildCtx)

	fmt.Fprintf(w, "→ 构建镜像（Dockerfile: %s，上下文: %s）\n", file, buildCtx)
	if err := mustRun(ctx, w, ex, "env", args...); err != nil {
		return fmt.Errorf("docker build 失败: %w", err)
	}
	return nil
}

// commandOK 探测命令是否存在且可执行。
func commandOK(ctx context.Context, ex executor.Executor, name string, args ...string) bool {
	r, err := ex.Exec(ctx, name, args...)
	return err == nil && r != nil && r.ExitCode == 0
}

// mustRun 执行命令；w 非空时回显输出，退出码非 0 视为失败。
func mustRun(ctx context.Context, w io.Writer, ex executor.Executor, name string, args ...string) error {
	r, err := execLong(ctx, ex, name, args...)
	if err != nil {
		return err
	}
	if w != nil && r.Output != "" {
		fmt.Fprint(w, indentLines(r.Output))
	}
	if r.ExitCode != 0 {
		detail := strings.TrimSpace(r.Output)
		if detail != "" {
			return fmt.Errorf("退出码 %d: %s", r.ExitCode, tail(detail, 300))
		}
		return fmt.Errorf("退出码 %d", r.ExitCode)
	}
	return nil
}
