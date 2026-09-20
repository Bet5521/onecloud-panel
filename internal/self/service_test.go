package self

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

// ---- 测试用执行器 ----

type fakeExec struct {
	outputs map[string]string
	calls   []string
}

func (f *fakeExec) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if out, ok := f.outputs[strings.Join(append([]string{name}, args...), " ")]; ok {
		return &executor.Result{ExitCode: 0, Output: out}, nil
	}
	return &executor.Result{ExitCode: 0, Output: ""}, nil
}
func (f *fakeExec) ExecStream(ctx context.Context, w io.Writer, name string, args ...string) (int, error) {
	return 0, nil
}
func (f *fakeExec) ReadFile(path string) ([]byte, error)     { return nil, fs.ErrNotExist }
func (f *fakeExec) WriteFile(path string, data []byte) error { return nil }
func (f *fakeExec) Exists(path string) (bool, error)         { return false, nil }

var _ executor.Executor = (*fakeExec)(nil)

func testService(t *testing.T, fx *fakeExec) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s, fx, "/var/lib/onecloud-panel", ":8000", "onecloud-panel.service"), s
}

func TestStatus(t *testing.T) {
	fx := &fakeExec{outputs: map[string]string{
		"systemctl is-active onecloud-panel.service": "active",
	}}
	svc, _ := testService(t, fx)
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st["systemd_active"] != true {
		t.Fatalf("systemd_active 应为 true: %+v", st)
	}
	if st["data_dir"] != "/var/lib/onecloud-panel" || st["listen"] != ":8000" {
		t.Fatalf("基础信息错误: %+v", st)
	}
	if up, _ := st["uptime_seconds"].(int64); up < 0 {
		t.Fatalf("uptime 异常")
	}
}

func TestRestartSuccess(t *testing.T) {
	fx := &fakeExec{outputs: map[string]string{
		"systemctl is-active onecloud-panel.service": "active",
	}}
	svc, s := testService(t, fx)

	var launchedUnit string
	svc.SetLaunch(func(unit string) error {
		launchedUnit = unit
		return nil
	})

	id, err := svc.Restart(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if launchedUnit != "onecloud-panel.service" {
		t.Fatalf("未触发重启: %q", launchedUnit)
	}
	task, err := s.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != store.TaskRunning {
		t.Fatalf("重启任务应保持 running（待启动后收口）: %s", task.Status)
	}
}

func TestRestartNotSystemd(t *testing.T) {
	// 空执行器输出 systemctl 行不匹配 → 但 is-active 命令仍成功返回空串
	fx := &fakeExec{}
	svc, _ := testService(t, fx)
	called := false
	svc.SetLaunch(func(unit string) error {
		called = true
		return nil
	})
	if _, err := svc.Restart(context.Background(), 1); err == nil {
		t.Fatal("非 active 状态应拒绝重启")
	}
	if called {
		t.Fatal("拒绝重启时不应触发 launch")
	}
}

func TestRestartLaunchFailure(t *testing.T) {
	fx := &fakeExec{outputs: map[string]string{
		"systemctl is-active onecloud-panel.service": "active",
	}}
	svc, s := testService(t, fx)
	svc.SetLaunch(func(unit string) error {
		return errBoom
	})
	id, err := svc.Restart(context.Background(), 1)
	if err == nil {
		t.Fatal("应返回 launch 错误")
	}
	if id != 0 {
		task, _ := s.GetTask(id)
		if task.Status != store.TaskFailure {
			t.Fatalf("任务应标记 failure")
		}
	}
}

func TestFinalizeBoot(t *testing.T) {
	fx := &fakeExec{outputs: map[string]string{
		"systemctl is-active onecloud-panel.service": "active",
	}}
	svc, s := testService(t, fx)
	svc.SetLaunch(func(string) error { return nil })

	// 一个重启任务（running），一个无关 running 任务
	id1, err := svc.Restart(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateTask(&store.BackgroundTask{
		Type: "app_install", Status: store.TaskRunning,
		NodeID: i64ptr(1), Payload: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := svc.FinalizeBoot()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应收口 1 个重启任务，实际 %d", n)
	}
	t1, _ := s.GetTask(id1)
	if t1.Status != store.TaskSuccess {
		t.Fatalf("重启任务应 success，实际 %s", t1.Status)
	}
	t2, _ := s.GetTask(id2)
	if t2.Status != store.TaskRunning {
		t.Fatalf("无关任务不应被动: %s", t2.Status)
	}
}

func TestJournal(t *testing.T) {
	fx := &fakeExec{outputs: map[string]string{
		"journalctl --no-pager -u onecloud-panel.service -n 100": "log-line\n",
	}}
	svc, _ := testService(t, fx)
	out, err := svc.Journal(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if out != "log-line\n" {
		t.Fatalf("日志输出错误: %q", out)
	}
}

func i64ptr(v int64) *int64 { return &v }

var errBoom = errors.New("boom")
