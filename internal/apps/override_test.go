package apps

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
)

// overrideTestManager 仅用于覆盖校验类单测（无需节点/执行器）。
func overrideTestManager(t *testing.T) *Manager {
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
	return New(s, node.New(s, box), reg, box, nil)
}

func TestValidateDockerOverridePorts(t *testing.T) {
	m := overrideTestManager(t)
	ok := &DockerOverride{Ports: []string{"8081:80", "53:53/udp"}}
	if err := m.ValidateDockerOverride("dtest", ok); err != nil {
		t.Fatalf("合法端口应通过: %v", err)
	}
	bad := &DockerOverride{Ports: []string{"abc:80"}}
	if err := m.ValidateDockerOverride("dtest", bad); err == nil {
		t.Fatal("非法端口应拒绝")
	}
}

func TestValidateDockerOverrideEnv(t *testing.T) {
	m := overrideTestManager(t)
	ok := &DockerOverride{Env: []string{"TZ=Asia/Shanghai", `PASSWORD=p@ss'w"d\x`}}
	if err := m.ValidateDockerOverride("dtest", ok); err != nil {
		t.Fatalf("合法 ENV 应通过（值允许引号等）: %v", err)
	}
	for _, bad := range []string{"NOVALUE", "1BAD=x", "A B=x", "K=with\nnewline"} {
		if err := m.ValidateDockerOverride("dtest", &DockerOverride{Env: []string{bad}}); err == nil {
			t.Fatalf("ENV %q 应拒绝", bad)
		}
	}
	if err := m.ValidateDockerOverride("dtest", &DockerOverride{Env: []string{"A=1", "A=2"}}); err == nil {
		t.Fatal("重复 KEY 应拒绝")
	}
}

func TestValidateDockerOverrideVolumesAndRestart(t *testing.T) {
	m := overrideTestManager(t)
	ok := &DockerOverride{
		Volumes: []string{"/srv/data:/data:ro", "mydata:/var/lib/x"},
		Restart: "always",
	}
	if err := m.ValidateDockerOverride("dtest", ok); err != nil {
		t.Fatalf("合法卷/重启策略应通过: %v", err)
	}
	for _, bad := range []string{"/a b:/data", "/a:/data:xx", "a/../b:/data", "/a:rel"} {
		if err := m.ValidateDockerOverride("dtest", &DockerOverride{Volumes: []string{bad}}); err == nil {
			t.Fatalf("卷 %q 应拒绝", bad)
		}
	}
	if err := m.ValidateDockerOverride("dtest", &DockerOverride{Restart: "sometimes"}); err == nil {
		t.Fatal("非法重启策略应拒绝")
	}
	if err := m.ValidateDockerOverride("demo", &DockerOverride{Ports: []string{"1:1"}}); err == nil {
		t.Fatal("无 DockerSpec 的配方应拒绝")
	}
}

func TestApplyDockerOverride(t *testing.T) {
	ds := &recipes.DockerSpec{
		Ports:   []string{"8080:80"},
		Volumes: []string{"demodata:/data"},
		Env:     []string{"FOO=bar", "TZ=UTC"},
	}
	ov := &DockerOverride{
		Ports:   []string{"9090:8080"},
		Volumes: []string{"/host/x:/x"},
		Env:     []string{"TZ=Asia/Shanghai", "NEW=1"},
		Restart: "always",
	}
	if err := applyDockerOverride(ds, ov); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ds.Ports[0] != "9090:8080" || ds.Volumes[0] != "/host/x:/x" {
		t.Fatalf("替换语义未生效: %v %v", ds.Ports, ds.Volumes)
	}
	if len(ds.Env) != 3 || ds.Env[0] != "FOO=bar" || ds.Env[1] != "TZ=Asia/Shanghai" || ds.Env[2] != "NEW=1" {
		t.Fatalf("ENV 增量合并异常: %v", ds.Env)
	}
	if ds.RestartPolicy != "always" {
		t.Fatalf("重启策略未生效: %q", ds.RestartPolicy)
	}

	// 非法覆盖应整体拒绝且不部分应用
	ds2 := &recipes.DockerSpec{Ports: []string{"8080:80"}, Env: []string{"FOO=bar"}}
	if err := applyDockerOverride(ds2, &DockerOverride{Ports: []string{"bad"}}); err == nil {
		t.Fatal("非法覆盖应拒绝")
	}
	if ds2.Ports[0] != "8080:80" {
		t.Fatal("拒绝时不应修改原值")
	}
	// nil 覆盖不改动
	if err := applyDockerOverride(ds2, nil); err != nil || ds2.Ports[0] != "8080:80" {
		t.Fatal("nil 覆盖应为空操作")
	}
}

func TestMergeEnv(t *testing.T) {
	out := mergeEnv(
		[]string{"A=1", "B=2", "DUP=x", "DUP=y"},
		[]string{"B=9", "C=3"},
	)
	want := []string{"A=1", "B=9", "DUP=x", "C=3"}
	if len(out) != len(want) {
		t.Fatalf("mergeEnv 长度异常: %v", out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("mergeEnv[%d] = %q, want %q", i, out[i], want[i])
		}
	}
}

func TestParamsJSONCompat(t *testing.T) {
	// 无覆盖：保持旧扁平结构
	if got := paramsJSON(map[string]string{"a": "b"}, nil); got != `{"a":"b"}` {
		t.Fatalf("旧结构异常: %s", got)
	}
	// 有覆盖：新结构
	got := paramsJSON(map[string]string{"a": "b"}, &DockerOverride{Ports: []string{"1:2"}})
	if !strings.Contains(got, `"docker"`) || !strings.Contains(got, `"vars"`) {
		t.Fatalf("新结构异常: %s", got)
	}
}

func TestInstallationParamsCompat(t *testing.T) {
	// 旧扁平 JSON
	in := &store.AppInstallation{Params: `{"webPort":"8080"}`}
	vars := installationVars(in)
	if vars["webPort"] != "8080" {
		t.Fatalf("旧格式解析异常: %v", vars)
	}
	if installationDocker(in) != nil {
		t.Fatal("旧格式不应有覆盖")
	}
	// 新结构
	in.Params = paramsJSON(map[string]string{"webPort": "8081"},
		&DockerOverride{Ports: []string{"8081:80"}})
	vars = installationVars(in)
	if vars["webPort"] != "8081" {
		t.Fatalf("新格式 vars 解析异常: %v", vars)
	}
	dv := installationDocker(in)
	if dv == nil || len(dv.Ports) != 1 || dv.Ports[0] != "8081:80" {
		t.Fatalf("新格式 docker 解析异常: %+v", dv)
	}
}
