package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/apps"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"testing/fstest"
)

const testRecipeNative = `
api_version: 1
id: demo
name: 演示应用
category: test
methods: [native]
ports:
  - {port: 9090, proto: tcp}
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
  uninstall_steps:
    - name: 停服
      systemctl: {action: stop, unit: demo.service}
`

const testRecipeDocker = `
api_version: 1
id: dtest
name: 容器演示
category: test
methods: [docker]
healthcheck:
  {type: http, port: 8080, path: /}
docker:
  arches: [armv7l, aarch64, x86_64]
  image: demoimg:1.0
  ports: ["8080:80/tcp"]
  volumes: ["demodata:/data"]
  env: ["FOO=bar"]
`

func testRegistry(t *testing.T) *recipes.Registry {
	t.Helper()
	mfs := fstest.MapFS{
		"demo.yaml":  {Data: []byte(testRecipeNative)},
		"dtest.yaml": {Data: []byte(testRecipeDocker)},
	}
	reg, err := recipes.Load(mfs)
	if err != nil {
		t.Fatalf("recipes: %v", err)
	}
	return reg
}

func newTestAPI(t *testing.T) (string, *store.Store, *API) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "t.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	sessions := auth.NewManager(s, time.Hour)
	asvc := audit.New(s)
	limiter := auth.NewLoginLimiter(50, time.Hour)
	h := auth.NewHandler(s, sessions, limiter, asvc.Login)
	nodeSvc := node.New(s, box)
	reg := testRegistry(t)
	taskRunner := runner.New(s)
	appManager := apps.New(s, nodeSvc, reg, box, asvc)
	appManager.RegisterRunnerTasks(taskRunner)
	apiObj := New(s, h, auth.NewMiddleware(sessions), asvc, nodeSvc, reg,
		taskRunner, appManager)
	apiObj.SetSessionManager(sessions)
	return dbPath, s, apiObj
}

func newTestServer(t *testing.T) (string, *store.Store, http.Handler) {
	dbPath, s, apiObj := newTestAPI(t)
	return dbPath, s, apiObj.Handler()
}

func do(t *testing.T, h http.Handler, method, path string, body any, jar http.CookieJar) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if jar != nil {
		u := req.URL
		u.Scheme = "http"
		u.Host = "test"
		for _, c := range jar.Cookies(u) {
			req.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if jar != nil {
		u := req.URL
		u.Scheme = "http"
		u.Host = "test"
		jar.SetCookies(u, w.Result().Cookies())
	}
	return w
}

func newJar(t *testing.T) http.CookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return jar
}

// 初始化成功后应直接建立会话（向导 → 仪表盘无缝进入）。
func TestSetupAutoLogin(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := newJar(t)

	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "firstadmin", "password": "FirstPass1"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	// 携带 setup 响应 Cookie 直接访问 me，应已登录
	w = do(t, h, "GET", "/api/auth/me", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("setup 后 me code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"username":"firstadmin"`) {
		t.Fatalf("me 内容不符: %s", w.Body.String())
	}
}

func TestSetupFlow(t *testing.T) {
	dbPath, s, h := newTestServer(t)

	// 初始状态
	w := do(t, h, "GET", "/api/system/status", nil, nil)
	var status map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if status["initialized"] {
		t.Fatal("全新面板不应为 initialized")
	}

	// 未初始化访问业务接口 → 401
	w = do(t, h, "GET", "/api/settings", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("uninit settings code = %d, want 401", w.Code)
	}

	// 初始化
	w = do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup code = %d body=%s", w.Code, w.Body.String())
	}

	// 重复初始化 → 409
	w = do(t, h, "POST", "/api/setup",
		map[string]string{"username": "x", "password": "Sup3rPass!"}, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("repeat setup code = %d, want 409", w.Code)
	}

	// 参数校验
	w = do(t, h, "POST", "/api/setup",
		map[string]string{"username": "ab", "password": "Sup3rPass!"}, nil)
	if w.Code == http.StatusOK {
		t.Fatal("短用户名不应通过")
	}

	// 管理员可登录并读取设置
	jar := newJar(t)
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("login code = %d", w.Code)
	}
	w = do(t, h, "GET", "/api/settings", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("settings code = %d", w.Code)
	}

	// 更新设置
	w = do(t, h, "PUT", "/api/settings",
		map[string]string{"panel_name": "我的集群", "audit_retention_days": "30"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("update settings code = %d body=%s", w.Code, w.Body.String())
	}
	v, _, _ := s.GetSetting("panel_name")
	if v != "我的集群" {
		t.Fatalf("panel_name = %q", v)
	}

	// 非法设置拒绝
	w = do(t, h, "PUT", "/api/settings", map[string]string{"hacked": "1"}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid setting code = %d, want 400", w.Code)
	}

	// 关闭后重新打开（模拟面板重启），设置与管理员保留
	_ = s.Close()
	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	n, _ := s2.CountUsers()
	if n != 1 {
		t.Fatalf("重启后用户数 = %d, want 1", n)
	}
	v2, _, _ := s2.GetSetting("audit_retention_days")
	if v2 != "30" {
		t.Fatalf("重启后 audit_retention_days = %q, want 30", v2)
	}
	_ = s2.Close()
}
