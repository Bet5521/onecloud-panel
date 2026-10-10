package apps

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
)

// ---- 源码构建配方 ----

const dockerBuildRecipeYAML = `
api_version: 1
id: btest
name: 源码构建演示
category: test
variables:
  - {key: bt_ref, name: 分支, type: string, default: main}
methods: [docker]
docker:
  arches: [armv7l, aarch64, x86_64]
  image: btest-local:latest
  ports: ["6060:6060"]
  build:
    type: git
    source: https://example.com/repo.git
    ref: "{{.Vars.bt_ref}}"
    dockerfile: Dockerfile
`

const dockerBuildArchiveRecipeYAML = `
api_version: 1
id: barch
name: 归档构建演示
category: test
methods: [docker]
docker:
  arches: [armv7l, aarch64, x86_64]
  image: barch-local:latest
  build:
    type: archive
    source: https://example.com/src.tar.gz
    dockerfile: Dockerfile.arm
`

// ---- 记录命令的假执行器（含 LongRunner / Downloader 能力）----

type buildFakeExec struct {
	mu        sync.Mutex
	cmds      []string
	hasDocker bool
	hasGit    bool
	fc        *fakeContainers
}

func newBuildFakeExec(fc *fakeContainers) *buildFakeExec {
	return &buildFakeExec{hasDocker: true, hasGit: true, fc: fc}
}

func (f *buildFakeExec) run(name string, args []string) *executor.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)

	switch {
	case name == "docker" && len(args) == 1 && args[0] == "--version":
		if !f.hasDocker {
			return &executor.Result{ExitCode: 127, Output: "docker: not found"}
		}
		return &executor.Result{ExitCode: 0, Output: "Docker version 27.0.0"}
	case name == "git" && len(args) == 1 && args[0] == "--version":
		if !f.hasGit {
			return &executor.Result{ExitCode: 127, Output: "git: not found"}
		}
		return &executor.Result{ExitCode: 0, Output: "git version 2.43.0"}
	case name == "env":
		// 模拟 `docker build -t <tag>`：把产物镜像登记进假 Engine。
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				f.fc.mu.Lock()
				f.fc.images[args[i+1]] = "arm"
				f.fc.mu.Unlock()
			}
		}
	}
	return &executor.Result{ExitCode: 0, Output: "ok"}
}

func (f *buildFakeExec) Exec(_ context.Context, name string, args ...string) (*executor.Result, error) {
	return f.run(name, args), nil
}

func (f *buildFakeExec) ExecLong(_ context.Context, name string, args ...string) (*executor.Result, error) {
	return f.run(name, args), nil
}

func (f *buildFakeExec) ExecStream(_ context.Context, w io.Writer, name string, args ...string) (int, error) {
	r := f.run(name, args)
	if r.Output != "" {
		_, _ = w.Write([]byte(r.Output))
	}
	return r.ExitCode, nil
}

func (f *buildFakeExec) ReadFile(string) ([]byte, error) { return nil, nil }
func (f *buildFakeExec) WriteFile(string, []byte) error  { return nil }
func (f *buildFakeExec) Exists(string) (bool, error)     { return false, nil }

func (f *buildFakeExec) Download(_ context.Context, url, dest string,
	_ os.FileMode, _ string, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, "download "+url+" -> "+dest)
	return nil
}

var (
	_ executor.Executor   = (*buildFakeExec)(nil)
	_ executor.LongRunner = (*buildFakeExec)(nil)
	_ executor.Downloader = (*buildFakeExec)(nil)
)

// ---- 装配 ----

func dockerBuildSetup(t *testing.T, recipeYAML string, id string) (*Manager, *buildFakeExec, *fakeContainers) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	reg, err := recipes.Load(fstest.MapFS{
		id + ".yaml": &fstest.MapFile{Data: []byte(recipeYAML)},
	})
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nodeSvc := node.New(s, box)
	mgr := New(s, nodeSvc, reg, box, nil)
	if _, err := nodeSvc.EnsureLocalNode(nil); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes()
	for _, n := range nodes {
		if n.Mode == "local" {
			n.Arch = "armv7l"
			_ = s.UpdateNodeInfo(n.ID, &n)
		}
	}
	fc := newFakeContainers()
	fe := newBuildFakeExec(fc)
	mgr.SetExecutorHook(func(*store.Node) (executor.Executor, error) { return fe, nil })
	mgr.SetEngineHook(func(*store.Node) (*docker.Engine, error) { return fc.engine(), nil })
	return mgr, fe, fc
}

// ---- 用例 ----

// docker.build 存在时应走「节点上源码构建」，不再拉取远端镜像，且容器使用构建产物。
func TestDockerBuildFromSource(t *testing.T) {
	mgr, fe, fc := dockerBuildSetup(t, dockerBuildRecipeYAML, "btest")
	var buf bytes.Buffer
	if err := mgr.DockerInstall(context.Background(), &buf, 1, "btest", nil, nil); err != nil {
		t.Fatalf("DockerInstall: %v\n输出:\n%s", err, buf.String())
	}
	log := strings.Join(fe.cmds, "\n")

	if len(fc.pullCalls) != 0 {
		t.Fatalf("源码构建不应拉取远端镜像: %v", fc.pullCalls)
	}
	if !hasCmd(fe.cmds, "git clone --depth 1 --branch main https://example.com/repo.git") {
		t.Fatalf("缺少 git 克隆步骤:\n%s", log)
	}
	if !hasCmd(fe.cmds, "docker build -t btest-local:latest -f /var/tmp/ocp-build-btest/Dockerfile /var/tmp/ocp-build-btest") {
		t.Fatalf("缺少 docker build 步骤（含 -f/上下文路径）:\n%s", log)
	}
	// 容器必须使用构建产物镜像，而不是配方里可能残留的远端镜像名。
	var created *fakeContainer
	for _, c := range fc.containers {
		created = c
	}
	if created == nil {
		t.Fatal("容器未创建")
	}
	if created.image != "btest-local:latest" {
		t.Fatalf("容器镜像 = %q，期望构建产物 btest-local:latest", created.image)
	}
	if !strings.Contains(buf.String(), "构建完成") {
		t.Fatalf("输出未提示构建完成:\n%s", buf.String())
	}
}

// 变量渲染进 build.ref：改分支即改克隆参数。
func TestDockerBuildRefFromVariable(t *testing.T) {
	mgr, fe, _ := dockerBuildSetup(t, dockerBuildRecipeYAML, "btest")
	var buf bytes.Buffer
	if err := mgr.DockerInstall(context.Background(), &buf, 1, "btest",
		map[string]string{"bt_ref": "v1.9.9"}, nil); err != nil {
		t.Fatalf("DockerInstall: %v\n%s", err, buf.String())
	}
	if !hasCmd(fe.cmds, "git clone --depth 1 --branch v1.9.9") {
		t.Fatalf("build.ref 未按变量渲染:\n%s", strings.Join(fe.cmds, "\n"))
	}
}

// 节点缺少 docker CLI 时必须给出明确错误，而不是留下半成品。
func TestDockerBuildWithoutDockerCLI(t *testing.T) {
	mgr, fe, fc := dockerBuildSetup(t, dockerBuildRecipeYAML, "btest")
	fe.hasDocker = false
	err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "btest", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("应报缺 docker CLI: %v", err)
	}
	if len(fc.containers) != 0 {
		t.Fatal("构建失败时不应创建容器")
	}
}

// archive 方式：下载归档并解包（strip 1），随后按指定 Dockerfile 构建。
func TestDockerBuildFromArchive(t *testing.T) {
	mgr, fe, fc := dockerBuildSetup(t, dockerBuildArchiveRecipeYAML, "barch")
	var buf bytes.Buffer
	if err := mgr.DockerInstall(context.Background(), &buf, 1, "barch", nil, nil); err != nil {
		t.Fatalf("DockerInstall: %v\n%s", err, buf.String())
	}
	if !hasCmd(fe.cmds, "download https://example.com/src.tar.gz") {
		t.Fatalf("未下载源码归档:\n%s", strings.Join(fe.cmds, "\n"))
	}
	if !hasCmd(fe.cmds, "tar -xzf /var/tmp/ocp-src-barch.tar.gz --strip-components=1 -C /var/tmp/ocp-build-barch") {
		t.Fatalf("未正确解包归档:\n%s", strings.Join(fe.cmds, "\n"))
	}
	if !hasCmd(fe.cmds, "docker build -t barch-local:latest -f /var/tmp/ocp-build-barch/Dockerfile.arm /var/tmp/ocp-build-barch") {
		t.Fatalf("未按 Dockerfile.arm 构建:\n%s", strings.Join(fe.cmds, "\n"))
	}
	if len(fc.pullCalls) != 0 {
		t.Fatalf("归档构建同样不应拉取镜像: %v", fc.pullCalls)
	}
}
