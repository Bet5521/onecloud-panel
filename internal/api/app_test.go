package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
)

func appTestSetup(t *testing.T) (int64, *store.Store, http.Handler) {
	t.Helper()
	_, s, h := newTestServer(t)
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nodeSvc := node.New(s, box)
	local, err := nodeSvc.EnsureLocalNode(nil)
	if err != nil {
		t.Fatal(err)
	}
	// 测试环境按 armv7l 玩客云校验配方兼容性
	local.Arch = "armv7l"
	if err := s.UpdateNodeInfo(local.ID, local); err != nil {
		t.Fatal(err)
	}
	return local.ID, s, h
}

func makeViewer(t *testing.T, h http.Handler, s *store.Store, name string) http.CookieJar {
	t.Helper()
	hash, _ := auth.HashPassword("ViewerPass1")
	roles, _ := s.ListRoles()
	var rid int64
	for _, r := range roles {
		if r.Code == "viewer" {
			rid = r.ID
		}
	}
	if _, err := s.CreateUser(name, hash, rid); err != nil {
		t.Fatal(err)
	}
	jar := newJar(t)
	w := do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": name, "password": "ViewerPass1"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer login: %d %s", w.Code, w.Body.String())
	}
	return jar
}

// 安装/卸载入队，任务参数正确，且 viewer 无写权限被拒。
func TestAppLifecycleAPI(t *testing.T) {
	localID, s, h := appTestSetup(t)
	admin := adminLogin(t, h)

	base := "/api/nodes/" + itoa(localID) + "/apps/demo"
	w := do(t, h, "POST", base+"/install",
		map[string]any{"vars": map[string]string{"domain": "a"}}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("install code = %d %s", w.Code, w.Body.String())
	}
	var res struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.TaskID == 0 {
		t.Fatalf("install 响应异常: %s", w.Body.String())
	}
	// 入队参数正确
	tasks, _, err := s.ListTasks(store.TaskFilter{Type: "app_install", Limit: 50})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("任务查询异常: %v %d", err, len(tasks))
	}
	got := tasks[0].Payload
	if got == "" || !json.Valid([]byte(got)) ||
		!containsAll(got, `"app_id":"demo"`, `"method":"native"`) {
		t.Fatalf("payload 异常: %s", got)
	}

	// 卸载（清除数据）
	w = do(t, h, "POST", base+"/uninstall",
		map[string]any{"purge_data": true}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("uninstall code = %d %s", w.Code, w.Body.String())
	}

	// viewer 只有只读权限
	viewer := makeViewer(t, h, s, "viewer1")
	w = do(t, h, "POST", base+"/install", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer install code = %d, want 403", w.Code)
	}
}

// TR-13.2 安装方式校验与入队：非法方式/配方不支持 docker 均拒绝；
// docker 方式正确入队，审计含 method。
func TestAppInstallMethodAPI(t *testing.T) {
	localID, s, h := appTestSetup(t)
	admin := adminLogin(t, h)

	demoBase := "/api/nodes/" + itoa(localID) + "/apps/demo"
	dtestBase := "/api/nodes/" + itoa(localID) + "/apps/dtest"

	// 非法 method
	w := do(t, h, "POST", demoBase+"/install",
		map[string]any{"method": "snap"}, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 method code = %d, want 400", w.Code)
	}
	// demo 仅支持 native，docker 拒绝
	w = do(t, h, "POST", demoBase+"/install",
		map[string]any{"method": "docker"}, admin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("不支持的 docker code = %d, want 400 (%s)", w.Code, w.Body.String())
	}
	// dtest 支持 docker → 入队
	w = do(t, h, "POST", dtestBase+"/install",
		map[string]any{"method": "docker", "vars": map[string]string{}}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("docker install code = %d %s", w.Code, w.Body.String())
	}
	var enq struct {
		TaskID int64 `json:"task_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &enq)
	tasks, _, _ := s.ListTasks(store.TaskFilter{Type: "app_install", Limit: 50})
	if len(tasks) != 1 || !strings.Contains(tasks[0].Payload, `"method":"docker"`) {
		t.Fatalf("docker 任务异常: %+v", tasks)
	}

	// 启动独立 runner 执行任务（审计在任务真实终态写出）
	box2, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := runner.New(s)
	appMgr := apps.New(s, node.New(s, box2), testRegistry(t), box2, audit.New(s))
	appMgr.RegisterRunnerTasks(r)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Start(ctx)

	// 等待任务终态
	terminal := false
	for i := 0; i < 50; i++ {
		tk, gerr := s.GetTask(enq.TaskID)
		if gerr == nil && (tk.Status == store.TaskSuccess || tk.Status == store.TaskFailure) {
			terminal = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !terminal {
		t.Fatal("任务未在预期时间内到达终态")
	}

	// 审计记录含 method（结果与任务真实终态一致）
	logs, _, qerr := s.QueryAuditLogs(store.AuditFilter{Module: "app", Page: 1, PageSize: 50})
	if qerr != nil || len(logs) == 0 {
		t.Fatalf("审计查询异常: %v %d", qerr, len(logs))
	}
	var found bool
	for _, l := range logs {
		if l.Action == "install" && strings.Contains(l.Detail, `"method":"docker"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到带 method 的安装审计: %+v", logs)
	}
}

// 配置白名单：未知配方不允许读写任意路径。
func TestAppConfigWhitelist(t *testing.T) {
	localID, _, h := appTestSetup(t)
	admin := adminLogin(t, h)
	base := "/api/nodes/" + itoa(localID) + "/apps/no-such-app"

	w := do(t, h, "GET", base+"/config?path=/etc/passwd", nil, admin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("越权读配置 code = %d, want 403", w.Code)
	}
	w = do(t, h, "PUT", base+"/config",
		map[string]string{"path": "/etc/passwd", "content": "x"}, admin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("越权写配置 code = %d, want 403", w.Code)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}
