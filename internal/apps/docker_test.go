package apps

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

// ---- 安装脚本用假执行器 ----

type dockerFakeExec struct {
	mu        sync.Mutex
	scripts   []string
	files     map[string]string
	failApt   bool
	installed bool
}

func newDockerFakeExec() *dockerFakeExec {
	return &dockerFakeExec{files: map[string]string{}}
}

func (d *dockerFakeExec) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	script := ""
	if name == "sh" && len(args) >= 2 && args[0] == "-c" {
		script = args[1]
	}
	d.scripts = append(d.scripts, script)

	// os-release 探测
	if strings.Contains(script, "/etc/os-release") {
		return &executor.Result{ExitCode: 0, Output: "debian|bookworm|debian"}, nil
	}
	// 模拟安装完成后引擎出现
	if strings.Contains(script, "apt-get install") &&
		strings.Contains(script, "docker-ce") {
		if d.failApt {
			return &executor.Result{ExitCode: 100, Output: "E: 仓库不可达"}, nil
		}
		d.installed = true
	}
	return &executor.Result{ExitCode: 0}, nil
}

func (d *dockerFakeExec) ExecStream(ctx context.Context, w io.Writer,
	name string, args ...string) (int, error) {
	r, _ := d.Exec(ctx, name, args...)
	return r.ExitCode, nil
}

func (d *dockerFakeExec) ReadFile(path string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.files[path]
	if !ok {
		return nil, io.EOF
	}
	return []byte(v), nil
}

func (d *dockerFakeExec) WriteFile(path string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[path] = string(data)
	return nil
}

func (d *dockerFakeExec) Exists(path string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.files[path]
	return ok, nil
}

var _ executor.Executor = (*dockerFakeExec)(nil)

// ---- fake Engine ----

func fakeEngine(d *dockerFakeExec, version string) *docker.Engine {
	return docker.NewEngine(func(req *http.Request) (*http.Response, error) {
		if !d.installed {
			return nil, io.EOF
		}
		switch req.URL.Path {
		case "/_ping":
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("OK"))}, nil
		case "/version":
			body := `{"Version":"` + version + `","ApiVersion":"1.46","Arch":"arm"}`
			return &http.Response{StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		return &http.Response{StatusCode: 404,
			Body: io.NopCloser(strings.NewReader(""))}, nil
	})
}

// dockerTestSetup 复用应用测试装配，但替换执行器/引擎钩子为 Docker 专用。
func dockerTestSetup(t *testing.T) (*Manager, *dockerFakeExec, *store.Store) {
	t.Helper()
	mgr, _, s := testSetup(t) // 本机节点 arch=armv7l
	df := newDockerFakeExec()
	mgr.SetExecutorHook(func(*store.Node) (executor.Executor, error) { return df, nil })
	mgr.SetEngineHook(func(*store.Node) (*docker.Engine, error) {
		return fakeEngine(df, "27.0.0"), nil
	})
	return mgr, df, s
}

// TR-12.1 完整安装：识别系统→配置仓库→仅装 engine/cli/containerd→启用→验证→落库。
func TestInstallDockerFlow(t *testing.T) {
	mgr, df, s := dockerTestSetup(t)
	var buf bytes.Buffer
	err := mgr.InstallDocker(context.Background(), &buf, 1)
	if err != nil {
		t.Fatalf("InstallDocker: %v\n输出:\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "bookworm") || !strings.Contains(out, "armhf") {
		t.Fatalf("系统识别异常: %s", out)
	}
	// apt 源：arch 与 signed-by 正确
	src := df.files["/etc/apt/sources.list.d/docker.list"]
	if !strings.Contains(src, "arch=armhf") || !strings.Contains(src, "signed-by=") ||
		!strings.Contains(src, "download.docker.com/linux/debian") {
		t.Fatalf("apt 源异常: %q", src)
	}
	// 不包含 compose/buildx
	joined := strings.Join(df.scripts, "\n")
	if strings.Contains(joined, "compose") || strings.Contains(joined, "buildx") {
		t.Fatalf("安装了 compose/buildx: %s", joined)
	}
	// 必须包含 docker-ce / containerd.io / enable
	for _, want := range []string{"docker-ce-cli", "containerd.io", "systemctl enable docker"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 %s", want)
		}
	}
	// 版本落库
	n, _ := s.GetNode(1)
	if n.DockerVersion != "27.0.0" {
		t.Fatalf("docker_version 异常: %q", n.DockerVersion)
	}
}

// i386 架构直接拒绝自动安装。
func TestInstallDockerUnsupportedArch(t *testing.T) {
	mgr, _, s := dockerTestSetup(t)
	// 另建 i386 节点
	id, _ := s.CreateNode(&store.Node{
		Name: "old", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "127.0.0.1:1", Arch: "i386",
	})
	err := mgr.InstallDocker(context.Background(), &bytes.Buffer{}, id)
	if err == nil || !strings.Contains(err.Error(), "无官方 Docker") {
		t.Fatalf("i386 应拒绝: %v", err)
	}
}

// 引擎已在运行 → 不重复安装。
func TestInstallDockerAlreadyThere(t *testing.T) {
	mgr, df, _ := dockerTestSetup(t)
	df.installed = true
	err := mgr.InstallDocker(context.Background(), &bytes.Buffer{}, 1)
	if err == nil || !strings.Contains(err.Error(), "已安装") {
		t.Fatalf("应提示已安装: %v", err)
	}
}

// apt 失败时错误包含输出尾部。
func TestInstallDockerAptFail(t *testing.T) {
	mgr, df, _ := dockerTestSetup(t)
	df.failApt = true
	err := mgr.InstallDocker(context.Background(), &bytes.Buffer{}, 1)
	if err == nil || !strings.Contains(err.Error(), "仓库不可达") {
		t.Fatalf("应报 apt 失败: %v", err)
	}
}

// 对账：安装成功/节点不可达。
func TestDockerInstallReconcile(t *testing.T) {
	mgr, df, _ := dockerTestSetup(t)
	payload := `{"node_id":1}`
	id, _ := s2(mgr).CreateTask(&store.BackgroundTask{
		Type: "docker_install", NodeID: i64ptr(1), Payload: payload,
	})
	_ = s2(mgr).MarkTaskRunning(id)

	// 引擎尚不可用但节点 local 可达 → failure（要求人工查看）
	task, _ := s2(mgr).GetTask(id)
	d, err := mgr.dockerInstallReconcile(context.Background(), task)
	if err != nil || d.Status != store.TaskFailure {
		t.Fatalf("未安装应对账 failure: %+v %v", d, err)
	}

	// 安装完成 → success
	df.installed = true
	d, err = mgr.dockerInstallReconcile(context.Background(), task)
	if err != nil || d.Status != store.TaskSuccess {
		t.Fatalf("已安装应对账 success: %+v %v", d, err)
	}
}

func s2(m *Manager) *store.Store { return m.store }
