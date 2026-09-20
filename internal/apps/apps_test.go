package apps

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
)

// ---- fake 执行器 ----

type fakeExec struct {
	mu        sync.Mutex
	cmds      []string
	files     map[string]string
	active    map[string]string // unit → active 状态
	healthy   bool
	failCmds  map[string]bool
	customOut map[string]string // 命令行前缀 → 输出
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		files:     map[string]string{},
		active:    map[string]string{"demo.service": "active"},
		healthy:   true,
		failCmds:  map[string]bool{},
		customOut: map[string]string{},
	}
}

func (f *fakeExec) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := name + " " + strings.Join(args, " ")
	f.cmds = append(f.cmds, strings.TrimSpace(line))

	if f.failCmds[name] {
		return &executor.Result{ExitCode: 1, Output: "fail"}, nil
	}
	// 模拟 rm -f 系统单元：移除 active 状态
	if name == "rm" && len(args) >= 2 && args[0] == "-f" {
		for _, p := range args[1:] {
			delete(f.files, p)
			if base := filepath.Base(p); strings.HasSuffix(base, ".service") {
				delete(f.active, base)
			}
		}
		return &executor.Result{ExitCode: 0}, nil
	}
	out := ""
	if name == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
		out = f.active[args[1]]
		if out == "" {
			out = "inactive"
			return &executor.Result{ExitCode: 3, Output: out}, nil
		}
	}
	if out == "" {
		for prefix, v := range f.customOut {
			if strings.HasPrefix(line, prefix) {
				out = v
				break
			}
		}
	}
	return &executor.Result{ExitCode: 0, Output: out}, nil
}

func (f *fakeExec) ExecStream(ctx context.Context, w io.Writer,
	name string, args ...string) (int, error) {
	r, err := f.Exec(ctx, name, args...)
	if r.Output != "" {
		_, _ = w.Write([]byte(r.Output))
	}
	return r.ExitCode, err
}

func (f *fakeExec) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(v), nil
}

func (f *fakeExec) WriteFile(path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = string(data)
	return nil
}

func (f *fakeExec) Exists(path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[path]
	return ok, nil
}

func (f *fakeExec) Healthcheck(ctx context.Context, kind string, port int, path string) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy, ""
}

var _ executor.Executor = (*fakeExec)(nil)
var _ executor.HealthChecker = (*fakeExec)(nil)

// ---- 装配 ----

const demoRecipeYAML = `
api_version: 1
id: demo
name: 演示应用
category: test
methods: [native]
ports:
  - {port: 9090, proto: tcp}
config_files:
  - {path: /etc/demo/app.conf}
variables:
  - {key: domain, name: 域名, type: string, default: default.local}
healthcheck:
  {type: http, port: 9090, path: /healthz}
native:
  arches: [armv7l, aarch64, x86_64]
  unit_name: demo.service
  unit_template: |
    [Unit]
    Description={{.Vars.domain}}
    [Service]
    ExecStart=/usr/local/bin/demo
  install_steps:
    - name: 建目录
      mkdir: [/etc/demo]
    - name: 写配置
      write:
        path: /etc/demo/app.conf
        content: "DOMAIN={{.Vars.domain}}\n"
    - name: 检查
      exec: {command: echo, args: ["hello"]}
  uninstall_steps:
    - name: 停服
      systemctl: {action: stop, unit: demo.service}
`

func testSetup(t *testing.T) (*Manager, *fakeExec, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	mfs := fstest.MapFS{"demo.yaml": &fstest.MapFile{Data: []byte(demoRecipeYAML)}}
	reg, err := recipes.Load(mfs)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nodeSvc := node.New(s, box)
	mgr := New(s, nodeSvc, reg, box, nil)

	// 本机节点（local），arch armv7l
	if _, err := nodeSvc.EnsureLocalNode(nil); err != nil {
		t.Fatal(err)
	}
	// local 节点默认 arch 为空，直接改为 armv7l
	nodes, _ := s.ListNodes()
	for _, n := range nodes {
		if n.Mode == "local" {
			n.Arch = "armv7l"
			_ = s.UpdateNodeInfo(n.ID, &n)
		}
	}

	fe := newFakeExec()
	mgr.SetExecutorHook(func(*store.Node) (executor.Executor, error) { return fe, nil })

	return mgr, fe, s
}

// TR-11.1 安装：步骤执行、单元落库、健康检查、安装记录；重复安装拒绝。
func TestNativeInstall(t *testing.T) {
	mgr, fe, s := testSetup(t)
	var buf bytes.Buffer
	err := mgr.Install(context.Background(), &buf, 1, "demo",
		map[string]string{"domain": "my.home"})
	if err != nil {
		t.Fatalf("install: %v\n输出:\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "建目录") || !strings.Contains(out, "健康检查通过") {
		t.Fatalf("输出异常: %s", out)
	}
	// systemd 单元含渲染变量
	unit := fe.files["/etc/systemd/system/demo.service"]
	if !strings.Contains(unit, "my.home") {
		t.Fatalf("单元渲染异常: %q", unit)
	}
	// 配置文件渲染
	if fe.files["/etc/demo/app.conf"] != "DOMAIN=my.home\n" {
		t.Fatalf("配置渲染异常: %q", fe.files["/etc/demo/app.conf"])
	}
	// 关键命令
	wantCmds := []string{"mkdir -p /etc/demo", "systemctl daemon-reload",
		"systemctl enable demo.service", "systemctl start demo.service"}
	all := strings.Join(fe.cmds, "\n")
	for _, w := range wantCmds {
		if !strings.Contains(all, w) {
			t.Fatalf("缺少命令 %q\n%s", w, all)
		}
	}
	// 安装记录
	in, err := s.GetInstallation(1, "demo")
	if err != nil || in.Status != "installed" || in.ServiceName != "demo.service" {
		t.Fatalf("安装记录异常: %+v", in)
	}

	// 重复安装 → 拒绝
	if err := mgr.Install(context.Background(), &bytes.Buffer{}, 1, "demo", nil); err == nil {
		t.Fatal("重复安装应拒绝")
	}
}

// TR-11.1/11.2 卸载：stop/disable、rm 单元、保留数据、删记录。
func TestNativeUninstall(t *testing.T) {
	mgr, fe, s := testSetup(t)
	if err := mgr.Install(context.Background(), &bytes.Buffer{}, 1, "demo",
		map[string]string{"domain": "x"}); err != nil {
		t.Fatal(err)
	}
	fe.cmds = nil

	err := mgr.Uninstall(context.Background(), &bytes.Buffer{}, 1, "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(fe.cmds, "\n")
	if !strings.Contains(all, "systemctl stop demo.service") ||
		!strings.Contains(all, "systemctl disable demo.service") ||
		!strings.Contains(all, "rm -f /etc/systemd/system/demo.service") {
		t.Fatalf("卸载命令异常:\n%s", all)
	}
	if _, err := s.GetInstallation(1, "demo"); err == nil {
		t.Fatal("卸载后记录仍存在")
	}
}

// start/stop/restart 动作。
func TestServiceAction(t *testing.T) {
	mgr, fe, _ := testSetup(t)
	if err := mgr.Install(context.Background(), &bytes.Buffer{}, 1, "demo", nil); err != nil {
		t.Fatal(err)
	}
	fe.cmds = nil
	if _, err := mgr.ServiceAction(context.Background(), 1, "demo", "restart"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(fe.cmds, "\n"), "systemctl restart demo.service") {
		t.Fatal("未执行 restart")
	}
	if _, err := mgr.ServiceAction(context.Background(), 1, "demo", "explode"); err == nil {
		t.Fatal("非法动作应拒绝")
	}
}

// Status 聚合 active + 健康。
func TestStatus(t *testing.T) {
	mgr, _, _ := testSetup(t)
	if err := mgr.Install(context.Background(), &bytes.Buffer{}, 1, "demo", nil); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.Status(context.Background(), 1, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if res["active"] != true || res["healthy"] != true {
		t.Fatalf("status 异常: %+v", res)
	}
}

// TR-9.2 重启对账：遗留 running 安装任务，本地节点 active → success。
func TestInstallReconcile(t *testing.T) {
	mgr, _, s := testSetup(t)
	payload := `{"node_id":1,"app_id":"demo","method":"native"}`
	id, err := s.CreateTask(&store.BackgroundTask{
		Type: "app_install", NodeID: i64ptr(1), AppID: "demo", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTaskRunning(id); err != nil {
		t.Fatal(err)
	}

	task, _ := s.GetTask(id)
	d, err := mgr.installReconcile(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if d.Requeue || d.Status != "success" {
		t.Fatalf("对账决定异常: %+v", d)
	}
}

// 节点不可达 → 对账重排。
func TestReconcileNodeUnreachable(t *testing.T) {
	mgr, _, s := testSetup(t)
	// 再加一个无地址远程节点
	remoteID, _ := s.CreateNode(&store.Node{
		Name: "r", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "127.0.0.1:1",
	})
	payload := `{"node_id":` + strconv.FormatInt(remoteID, 10) + `,"app_id":"demo","method":"native"}`
	id, _ := s.CreateTask(&store.BackgroundTask{
		Type: "app_install", NodeID: &remoteID, AppID: "demo", Payload: payload,
	})
	_ = s.MarkTaskRunning(id)
	task, _ := s.GetTask(id)
	d, err := mgr.installReconcile(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Requeue {
		t.Fatal("不可达节点应对账重排")
	}
}

// 卸载对账：单元已不存在 → success。
func TestUninstallReconcile(t *testing.T) {
	mgr, _, s := testSetup(t)
	if err := mgr.Install(context.Background(), &bytes.Buffer{}, 1, "demo", nil); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Uninstall(context.Background(), &bytes.Buffer{}, 1, "demo", false); err != nil {
		t.Fatal(err)
	}
	// 构造中断的卸载任务
	payload := `{"node_id":1,"app_id":"demo","method":"native"}`
	id, _ := s.CreateTask(&store.BackgroundTask{
		Type: "app_uninstall", NodeID: i64ptr(1), AppID: "demo", Payload: payload,
	})
	_ = s.MarkTaskRunning(id)
	task, _ := s.GetTask(id)
	d, err := mgr.uninstallReconcile(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if d.Requeue || d.Status != "success" {
		t.Fatalf("卸载对账异常: %+v", d)
	}
}

func i64ptr(i int64) *int64 { return &i }
