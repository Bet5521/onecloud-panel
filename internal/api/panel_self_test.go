package api

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/self"
)

// ---- 面板自身端点测试用执行器 ----

type selfFakeExec struct {
	outputs  map[string]string
	launched []string
}

func (f *selfFakeExec) key(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *selfFakeExec) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	out := f.outputs[f.key(name, args...)]
	return &executor.Result{ExitCode: 0, Output: out}, nil
}
func (f *selfFakeExec) ExecStream(ctx context.Context, w io.Writer, name string, args ...string) (int, error) {
	return 0, nil
}
func (f *selfFakeExec) ReadFile(path string) ([]byte, error)     { return nil, fs.ErrNotExist }
func (f *selfFakeExec) WriteFile(path string, data []byte) error { return nil }
func (f *selfFakeExec) Exists(path string) (bool, error)         { return false, nil }

var _ executor.Executor = (*selfFakeExec)(nil)

func TestPanelSelfEndpoints(t *testing.T) {
	fx := &selfFakeExec{outputs: map[string]string{
		"systemctl is-active onecloud-panel.service":             "active",
		"journalctl --no-pager -u onecloud-panel.service -n 200": "启动日志\n",
	}}
	_, s, apiObj := newTestAPI(t)
	svc := self.New(s, fx, "/var/lib/onecloud-panel", ":8000", "onecloud-panel.service")
	svc.SetLaunch(func(unit string) error {
		fx.launched = append(fx.launched, unit)
		return nil
	})
	apiObj.SetSelfService(svc)
	h := apiObj.Handler()
	jar := newJar(t)

	// 未登录 → 401
	w := do(t, h, "GET", "/api/panel/status", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 code=%d", w.Code)
	}

	// 初始化并登录
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "selfadmin", "password": "SelfPass123"}, nil); w.Code != 200 {
		t.Fatalf("setup: %d", w.Code)
	}
	if w := do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "selfadmin", "password": "SelfPass123"}, jar); w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}

	// 状态
	w = do(t, h, "GET", "/api/panel/status", nil, jar)
	if w.Code != 200 {
		t.Fatalf("status code=%d body=%s", w.Code, w.Body.String())
	}
	var st map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st["systemd_active"] != true || st["unit"] != "onecloud-panel.service" {
		t.Fatalf("状态字段错误: %+v", st)
	}
	if st["data_dir"] != "/var/lib/onecloud-panel" || st["listen"] != ":8000" {
		t.Fatalf("路径信息错误: %+v", st)
	}

	// 日志
	w = do(t, h, "GET", "/api/panel/journal?lines=200", nil, jar)
	if w.Code != 200 {
		t.Fatalf("journal code=%d", w.Code)
	}
	var j map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &j)
	if !strings.Contains(j["journal"], "启动日志") {
		t.Fatalf("日志内容错误: %q", j["journal"])
	}

	// 重启：返回任务 id 且触发 launch
	w = do(t, h, "POST", "/api/panel/restart", nil, jar)
	if w.Code != 200 {
		t.Fatalf("restart code=%d body=%s", w.Code, w.Body.String())
	}
	var rr map[string]int64
	_ = json.Unmarshal(w.Body.Bytes(), &rr)
	if rr["task_id"] <= 0 {
		t.Fatalf("缺少任务 id: %s", w.Body.String())
	}
	if len(fx.launched) != 1 || fx.launched[0] != "onecloud-panel.service" {
		t.Fatalf("重启未触发: %v", fx.launched)
	}

	// 任务应在 FinalizeBoot 后收口为 success
	if n, err := svc.FinalizeBoot(); err != nil || n != 1 {
		t.Fatalf("FinalizeBoot n=%d err=%v", n, err)
	}
	task, err := s.GetTask(rr["task_id"])
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "success" {
		t.Fatalf("重启任务应收口 success，实际 %s", task.Status)
	}
}
