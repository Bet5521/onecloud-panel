package apps

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// ---- 容器应用配方 ----

const dockerRecipeYAML = `
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
  restart_policy: unless-stopped
  install_steps:
    - {name: 建目录, mkdir: [/var/lib/dtest]}
`

// ---- 内存态假 Engine ----

type fakeContainer struct {
	id      string
	name    string
	running bool
	image   string
	created map[string]any // create 请求体解析结果
}

type fakeContainers struct {
	mu             sync.Mutex
	images         map[string]string // ref → 架构
	containers     map[string]*fakeContainer
	removedVolumes []string
	counter        int
	pullError      string
	pullArch       string // 空=arm
}

func newFakeContainers() *fakeContainers {
	return &fakeContainers{images: map[string]string{}, containers: map[string]*fakeContainer{}}
}

func (f *fakeContainers) engine() *docker.Engine {
	return docker.NewEngine(func(req *http.Request) (*http.Response, error) {
		return f.handle(req)
	})
}

func resp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status,
		Body: io.NopCloser(strings.NewReader(body))}
}

func (f *fakeContainers) handle(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := req.URL.Path
	q := req.URL.Query()

	switch {
	case path == "/_ping":
		return resp(200, "OK"), nil
	case path == "/version":
		return resp(200, `{"Version":"27.0.0","Arch":"arm"}`), nil

	case path == "/images/create": // 拉取
		if f.pullError != "" {
			return resp(200, `{"error":"`+f.pullError+`"}`+"\n"), nil
		}
		ref := q.Get("fromImage") + ":" + q.Get("tag")
		arch := f.pullArch
		if arch == "" {
			arch = "arm"
		}
		f.images[ref] = arch
		return resp(200, `{"status":"Pulling from demo"}`+"\n"+
			`{"status":"Status: Downloaded newer image"}`+"\n"), nil

	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		refEnc := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
		ref, _ := url.PathUnescape(refEnc)
		arch, ok := f.images[ref]
		if !ok {
			return resp(404, "no such image"), nil
		}
		return resp(200, fmt.Sprintf(`{"Id":"sha256:abc","Architecture":%q,"Os":"linux"}`, arch)), nil

	case path == "/containers/json":
		var list []map[string]any
		for _, c := range f.containers {
			list = append(list, map[string]any{
				"Id": c.id, "Names": []string{"/" + c.name},
			})
		}
		b, _ := json.Marshal(list)
		return resp(200, string(b)), nil

	case path == "/containers/create":
		var body map[string]any
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
		name := q.Get("name")
		for _, c := range f.containers {
			if c.name == name {
				return resp(409, `{"message":"name in use"}`), nil
			}
		}
		f.counter++
		id := fmt.Sprintf("cid%024d", f.counter)
		f.containers[id] = &fakeContainer{
			id: id, name: name, image: fmt.Sprint(body["Image"]), created: body,
		}
		return resp(201, `{"Id":"`+id+`","Warnings":null}`), nil

	case strings.Contains(path, "/start"), strings.Contains(path, "/stop"),
		strings.Contains(path, "/restart"):
		id := strings.Split(path, "/")[2]
		c := f.containers[id]
		if c == nil {
			return resp(404, "no such container"), nil
		}
		c.running = !strings.Contains(path, "/stop")
		return resp(204, ""), nil

	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		id := strings.Split(path, "/")[2]
		c := f.containers[id]
		if c == nil {
			return resp(404, "no such container"), nil
		}
		state := "exited"
		if c.running {
			state = "running"
		}
		return resp(200, fmt.Sprintf(
			`{"Id":%q,"Name":"/%s","State":{"Status":%q,"Running":%v}}`,
			c.id, c.name, state, c.running)), nil

	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/logs"):
		id := strings.Split(path, "/")[2]
		if f.containers[id] == nil {
			return resp(404, "no such container"), nil
		}
		payload := []byte("hello logs\n")
		var buf bytes.Buffer
		// Docker 非 TTY 帧：stream(1) + 保留(3) + 长度(4)
		buf.Write([]byte{1, 0, 0, 0})
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(payload)))
		buf.Write(payload)
		return resp(200, buf.String()), nil

	case req.Method == "DELETE" && strings.HasPrefix(path, "/containers/"):
		id := strings.Split(path, "/")[2]
		if f.containers[id] == nil {
			return resp(404, "no such container"), nil
		}
		delete(f.containers, id)
		return resp(204, ""), nil

	case req.Method == "DELETE" && strings.HasPrefix(path, "/volumes/"):
		name := strings.TrimPrefix(path, "/volumes/")
		f.removedVolumes = append(f.removedVolumes, name)
		return resp(204, ""), nil
	}
	return resp(404, "unhandled "+path), nil
}

// ---- 装配 ----

func dockerAppsSetup(t *testing.T) (*Manager, *fakeExec, *fakeContainers, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	mfs := fstest.MapFS{
		"demo.yaml":  &fstest.MapFile{Data: []byte(demoRecipeYAML)},
		"dtest.yaml": &fstest.MapFile{Data: []byte(dockerRecipeYAML)},
	}
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
	fe := newFakeExec()
	mgr.SetExecutorHook(func(*store.Node) (executor.Executor, error) { return fe, nil })
	fc := newFakeContainers()
	mgr.SetEngineHook(func(*store.Node) (*docker.Engine, error) { return fc.engine(), nil })
	return mgr, fe, fc, s
}

// TR-13.1 容器安装：拉镜像→建容器→启动→健康→落库；create 参数正确。
func TestDockerAppInstall(t *testing.T) {
	mgr, _, fc, s := dockerAppsSetup(t)
	var buf bytes.Buffer
	if err := mgr.DockerInstall(context.Background(), &buf, 1, "dtest", nil); err != nil {
		t.Fatalf("DockerInstall: %v\n输出:\n%s", err, buf.String())
	}
	in, err := s.GetInstallation(1, "dtest")
	if err != nil {
		t.Fatal(err)
	}
	if in.Method != "docker" || in.Status != "installed" ||
		in.ContainerName != "ocp-dtest" || in.ContainerID == "" {
		t.Fatalf("安装记录异常: %+v", in)
	}

	// create 请求体断言
	var created *fakeContainer
	for _, c := range fc.containers {
		created = c
	}
	if created.image != "demoimg:1.0" {
		t.Fatalf("镜像异常: %q", created.image)
	}
	hc := created.created["HostConfig"].(map[string]any)
	pb := hc["PortBindings"].(map[string]any)
	bind80 := pb["80/tcp"].([]any)[0].(map[string]any)
	if bind80["HostPort"] != "8080" {
		t.Fatalf("端口映射异常: %v", bind80)
	}
	binds := hc["Binds"].([]any)
	if binds[0] != "demodata:/data" {
		t.Fatalf("卷异常: %v", binds)
	}
	env := created.created["Env"].([]any)
	if env[0] != "FOO=bar" {
		t.Fatalf("环境变量异常: %v", env)
	}
	rp := hc["RestartPolicy"].(map[string]any)
	if rp["Name"] != "unless-stopped" {
		t.Fatalf("重启策略异常: %v", rp)
	}

	// 重复安装拒绝
	if err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil); err == nil {
		t.Fatal("重复安装应拒绝")
	}
}

// TR-13.2 卸载：默认保留卷；purgeData 删除命名卷；镜像保留；记录删除。
func TestDockerAppUninstall(t *testing.T) {
	mgr, _, fc, s := dockerAppsSetup(t)
	if err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := mgr.DockerUninstall(context.Background(), &buf, 1, "dtest", false); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, buf.String())
	}
	if len(fc.removedVolumes) != 0 {
		t.Fatalf("不应删除卷: %v", fc.removedVolumes)
	}
	if _, ok := fc.images["demoimg:1.0"]; !ok {
		t.Fatal("镜像应保留")
	}
	if _, err := s.GetInstallation(1, "dtest"); err == nil {
		t.Fatal("安装记录应删除")
	}

	// 再次安装后 purgeData=true
	if err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DockerUninstall(context.Background(), &bytes.Buffer{}, 1, "dtest", true); err != nil {
		t.Fatal(err)
	}
	if len(fc.removedVolumes) != 1 || fc.removedVolumes[0] != "demodata" {
		t.Fatalf("命名卷未删除: %v", fc.removedVolumes)
	}
}

// TR-13.1 架构不匹配镜像提前拦截。
func TestDockerAppArchMismatch(t *testing.T) {
	mgr, _, fc, _ := dockerAppsSetup(t)
	// 先正常安装并卸载
	err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 卸载后将后续拉取的镜像架构改为 amd64，再装
	if err := mgr.DockerUninstall(context.Background(), &bytes.Buffer{}, 1, "dtest", false); err != nil {
		t.Fatal(err)
	}
	fc.pullArch = "amd64"
	err = mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil)
	if err == nil || !strings.Contains(err.Error(), "架构") {
		t.Fatalf("架构不匹配应拦截: %v", err)
	}
}

// TR-13.1 动作/状态/日志走容器实现。
func TestDockerAppActions(t *testing.T) {
	mgr, _, _, _ := dockerAppsSetup(t)
	if err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil); err != nil {
		t.Fatal(err)
	}
	name, err := mgr.ServiceAction(context.Background(), 1, "dtest", "stop")
	if err != nil || name != "ocp-dtest" {
		t.Fatalf("stop: %v %q", err, name)
	}
	res, err := mgr.Status(context.Background(), 1, "dtest")
	if err != nil {
		t.Fatal(err)
	}
	if res["running"] != false {
		t.Fatalf("stop 后应非运行: %v", res["running"])
	}
	if _, err := mgr.ServiceAction(context.Background(), 1, "dtest", "start"); err != nil {
		t.Fatal(err)
	}
	res, _ = mgr.Status(context.Background(), 1, "dtest")
	if res["running"] != true {
		t.Fatalf("start 后应运行: %v", res["running"])
	}
	out, err := mgr.Journal(context.Background(), 1, "dtest", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello logs") {
		t.Fatalf("日志异常: %q", out)
	}
}

// TR-13.1 容器对账：运行→成功；容器缺失→失败；卸载对账。
func TestDockerAppReconcile(t *testing.T) {
	mgr, _, fc, s := dockerAppsSetup(t)
	payload := `{"node_id":1,"app_id":"dtest","method":"docker"}`
	tid, _ := s.CreateTask(&store.BackgroundTask{
		Type: "app_install", NodeID: i64ptr(1), AppID: "dtest", Payload: payload,
	})
	_ = s.MarkTaskRunning(tid)
	task, _ := s.GetTask(tid)

	// 容器不存在 → failure
	d, err := mgr.installReconcile(context.Background(), task)
	if err != nil || d.Status != store.TaskFailure {
		t.Fatalf("无容器应 failure: %+v %v", d, err)
	}

	if err := mgr.DockerInstall(context.Background(), &bytes.Buffer{}, 1, "dtest", nil); err != nil {
		t.Fatal(err)
	}
	// 容器运行中 → success
	d, _ = mgr.installReconcile(context.Background(), task)
	if d.Status != store.TaskSuccess {
		t.Fatalf("运行中应 success: %+v", d)
	}

	// 卸载对账：容器仍在 → requeue
	utid, _ := s.CreateTask(&store.BackgroundTask{
		Type: "app_uninstall", NodeID: i64ptr(1), AppID: "dtest", Payload: payload,
	})
	_ = s.MarkTaskRunning(utid)
	utask, _ := s.GetTask(utid)
	d, _ = mgr.uninstallReconcile(context.Background(), utask)
	if !d.Requeue {
		t.Fatalf("容器仍在应重排: %+v", d)
	}
	// 删除容器后 → success
	for id := range fc.containers {
		delete(fc.containers, id)
	}
	d, _ = mgr.uninstallReconcile(context.Background(), utask)
	if d.Status != store.TaskSuccess {
		t.Fatalf("容器已删应 success: %+v", d)
	}
}
