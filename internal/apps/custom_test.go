package apps

import (
	"path"
	"strings"
	"testing"

	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/store"
)

// TestCustomRecipeID 合成配方 ID 格式。
func TestCustomRecipeID(t *testing.T) {
	cases := map[int64]string{1: "custom-1", 42: "custom-42", 999: "custom-999"}
	for id, want := range cases {
		if got := customRecipeID(id); got != want {
			t.Errorf("customRecipeID(%d) = %q, want %q", id, got, want)
		}
	}
}

// TestBuildBinaryUnit systemd 单元模板：默认用户、参数拼接、WorkingDirectory/ExecStart。
func TestBuildBinaryUnit(t *testing.T) {
	u := buildBinaryUnit("My App", "/opt/apps/1", "mybin", "--flag x", "")
	if !strings.Contains(u, "[Unit]") || !strings.Contains(u, "Type=simple") {
		t.Error("缺少 [Unit]/Type=simple")
	}
	if !strings.Contains(u, "User=root") {
		t.Error("默认用户应为 root")
	}
	if !strings.Contains(u, "WorkingDirectory=/opt/apps/1") {
		t.Error("WorkingDirectory 应为传入 work")
	}
	bin := path.Join("/opt/apps/1", "mybin")
	if !strings.Contains(u, "ExecStart="+bin+" --flag x") {
		t.Errorf("ExecStart 应含 %q，实际:\n%s", bin+" --flag x", u)
	}
	if !strings.Contains(u, "Restart=on-failure") {
		t.Error("缺少 Restart=on-failure")
	}

	// 自定义用户
	u2 := buildBinaryUnit("A", "/w", "bin", "", "svcuser")
	if !strings.Contains(u2, "User=svcuser") {
		t.Error("自定义用户未生效")
	}
	if !strings.Contains(u2, "ExecStart="+path.Join("/w", "bin")) {
		t.Error("无参数时 ExecStart 应仅为二进制路径")
	}
}

// TestBuildRunUnit run 单元模板：/bin/sh -c 包装、双引号剔除。
func TestBuildRunUnit(t *testing.T) {
	u := buildRunUnit("R", "/src", "python server.py")
	if !strings.Contains(u, `ExecStart=/bin/sh -c "python server.py"`) {
		t.Errorf("run 单元未正确包装 /bin/sh -c，实际:\n%s", u)
	}
	if !strings.Contains(u, "WorkingDirectory=/src") {
		t.Error("WorkingDirectory 应为 src")
	}

	// 双引号应被剔除，避免破坏 shell 字符串
	u2 := buildRunUnit("R", "/src", `echo "hi"`)
	if strings.Contains(u2, `echo "hi"`) {
		t.Error("run 中的双引号未被剔除")
	}
	if !strings.Contains(u2, `ExecStart=/bin/sh -c "echo hi"`) {
		t.Errorf("剔除双引号后应为 echo hi，实际:\n%s", u2)
	}
}

// TestSynthHealth 三种健康检查的生成与边界（非法/缺失返回 nil）。
func TestSynthHealth(t *testing.T) {
	if hc := synthHealth(healthCfg{Type: "http", Port: 8080, Path: "/health"}); hc == nil ||
		hc.Type != "http" || hc.Port != 8080 || hc.Path != "/health" {
		t.Error("http 健康检查生成错误")
	}
	if hc := synthHealth(healthCfg{Type: "tcp", Port: 9090}); hc == nil || hc.Type != "tcp" || hc.Port != 9090 {
		t.Error("tcp 健康检查生成错误")
	}
	if hc := synthHealth(healthCfg{Type: "command", Cmd: []string{"true"}}); hc == nil ||
		hc.Type != "command" || len(hc.Command) == 0 {
		t.Error("command 健康检查生成错误")
	}
	if hc := synthHealth(healthCfg{Type: "http", Port: 0}); hc != nil {
		t.Error("http 但端口为 0 应返回 nil")
	}
	if hc := synthHealth(healthCfg{Type: "command"}); hc != nil {
		t.Error("command 但无 cmd 应返回 nil")
	}
	if hc := synthHealth(healthCfg{Type: "bogus"}); hc != nil {
		t.Error("未知类型应返回 nil")
	}
}

// TestWithProxyJoin GitHub 加速代理拼接（仅 github 系域名生效）。
func TestWithProxyJoin(t *testing.T) {
	if got := withProxy("", "https://github.com/u/r"); got != "https://github.com/u/r" {
		t.Errorf("无代理应原样返回，got %q", got)
	}
	if got := withProxy("https://p.com", "https://github.com/u/r"); got != "https://p.com/https://github.com/u/r" {
		t.Errorf("github 域名应拼接代理前缀，got %q", got)
	}
	if got := withProxy("https://p.com", "https://gitlab.com/u/r"); got != "https://gitlab.com/u/r" {
		t.Errorf("非 github 域名不应拼接代理，got %q", got)
	}
}

// TestSynthRecipe 三种自定义应用合成配方的结构与校验。
func TestSynthRecipe(t *testing.T) {
	mgr, _, _ := testSetup(t)

	// docker：合法
	a := &store.CustomApp{ID: 10, Type: "docker", Name: "nginx",
		ConfigJSON: `{"image":"nginx:latest","ports":["80:80"],"health":{"type":"tcp","port":80}}`}
	r, err := mgr.SynthRecipe(a)
	if err != nil {
		t.Fatalf("docker 合成失败: %v", err)
	}
	if r.ID != "custom-10" {
		t.Errorf("合成配方 ID 应为 custom-10，got %q", r.ID)
	}
	if len(r.Methods) != 1 || r.Methods[0] != "docker" {
		t.Errorf("Methods 应为 [docker]，got %v", r.Methods)
	}
	if r.Docker == nil || r.Docker.Image != "nginx:latest" {
		t.Error("Docker.Image 未正确设置")
	}
	if len(r.Docker.Arches) != 4 {
		t.Errorf("Docker.Arches 应为 customArches(4)，got %v", r.Docker.Arches)
	}
	if r.Healthcheck == nil || r.Healthcheck.Type != "tcp" || r.Healthcheck.Port != 80 {
		t.Error("docker 健康检查未合成")
	}

	// docker：缺 image 应报错
	if _, err := mgr.SynthRecipe(&store.CustomApp{ID: 11, Type: "docker", Name: "x", ConfigJSON: `{}`}); err == nil {
		t.Error("docker 缺 image 应报错")
	}

	// binary：合法（含自定义 workdir/user/args）
	a2 := &store.CustomApp{ID: 12, Type: "binary", Name: "b",
		ConfigJSON: `{"exec_name":"svc","workdir":"/data/svc","user":"svcuser","args":"-c conf","health":{"type":"command","cmd":["pgrep","svc"]}}`}
	r2, err := mgr.SynthRecipe(a2)
	if err != nil {
		t.Fatalf("binary 合成失败: %v", err)
	}
	if r2.Methods[0] != "native" {
		t.Errorf("binary Methods 应为 [native]，got %v", r2.Methods)
	}
	if r2.Native == nil {
		t.Fatal("Native 不应为 nil")
	}
	if r2.Native.UnitName != "ocp-custom-12.service" {
		t.Errorf("UnitName 应为 ocp-custom-12.service，got %q", r2.Native.UnitName)
	}
	if !strings.Contains(r2.Native.UnitTemplate, "User=svcuser") {
		t.Error("binary 单元未使用自定义用户")
	}
	bin := path.Join("/data/svc", "svc")
	if !strings.Contains(r2.Native.UnitTemplate, "ExecStart="+bin+" -c conf") {
		t.Errorf("binary ExecStart 应含 %q，实际:\n%s", bin+" -c conf", r2.Native.UnitTemplate)
	}
	if len(r2.Native.InstallSteps) == 0 || len(r2.Native.InstallSteps[0].Mkdir) == 0 ||
		r2.Native.InstallSteps[0].Mkdir[0] != "/data/svc" {
		t.Error("binary InstallSteps 未创建自定义 workdir")
	}
	// workdir 必须声明为数据目录，卸载时 purge_data=true 才会真正清理已推送的二进制
	if len(r2.Volumes) != 1 || r2.Volumes[0] != "/data/svc" {
		t.Errorf("binary 应把 workdir 声明为数据目录（Volumes），got %v", r2.Volumes)
	}
	if r2.Healthcheck == nil || r2.Healthcheck.Type != "command" {
		t.Error("binary 命令健康检查未合成")
	}

	// binary：缺 exec_name 应报错
	if _, err := mgr.SynthRecipe(&store.CustomApp{ID: 13, Type: "binary", Name: "x", ConfigJSON: `{}`}); err == nil {
		t.Error("binary 缺 exec_name 应报错")
	}

	// binary：默认 workdir 兜底（custom.go 用字符串拼接，固定正斜杠）
	a3 := &store.CustomApp{ID: 14, Type: "binary", Name: "b", ConfigJSON: `{"exec_name":"x"}`}
	r3, err := mgr.SynthRecipe(a3)
	if err != nil {
		t.Fatalf("binary(默认 workdir) 合成失败: %v", err)
	}
	def := "/opt/onecloud-apps/14"
	if !strings.Contains(r3.Native.UnitTemplate, "WorkingDirectory="+def) {
		t.Errorf("默认 workdir 应为 %q，实际:\n%s", def, r3.Native.UnitTemplate)
	}
	if r3.Native.InstallSteps[0].Mkdir[0] != def {
		t.Errorf("默认 InstallSteps 应为 %q，got %q", def, r3.Native.InstallSteps[0].Mkdir[0])
	}

	// github：合法（指定 branch/run）
	a4 := &store.CustomApp{ID: 15, Type: "github", Name: "g",
		ConfigJSON: `{"repo":"https://github.com/u/r","branch":"dev","run":"make run","health":{"type":"http","port":3000,"path":"/ping"}}`}
	r4, err := mgr.SynthRecipe(a4)
	if err != nil {
		t.Fatalf("github 合成失败: %v", err)
	}
	if r4.Methods[0] != "native" || r4.Native == nil {
		t.Fatal("github 应为 native 且 Native 非 nil")
	}
	if r4.Native.UnitName != "ocp-custom-15.service" {
		t.Errorf("github UnitName 应为 ocp-custom-15.service，got %q", r4.Native.UnitName)
	}
	var clone *recipes.ExecStep
	for _, st := range r4.Native.InstallSteps {
		if st.Exec != nil && st.Exec.Command == "git" {
			clone = st.Exec
		}
	}
	if clone == nil {
		t.Fatal("github 缺少 git clone 步骤")
	}
	if clone.Args[4] != "dev" {
		t.Errorf("clone 分支应为 dev，got %q", clone.Args[4])
	}
	if clone.Args[5] != "https://github.com/u/r" {
		t.Errorf("clone 仓库应为原始 URL(无代理)，got %q", clone.Args[5])
	}
	if !strings.Contains(r4.Native.UnitTemplate, "make run") {
		t.Error("github 单元未使用自定义 run 命令")
	}
	var hasRm bool
	for _, st := range r4.Native.UninstallSteps {
		if st.Exec != nil && st.Exec.Command == "rm" {
			hasRm = true
		}
	}
	if !hasRm {
		t.Error("github 缺少卸载 rm 步骤")
	}
	if r4.Healthcheck == nil || r4.Healthcheck.Type != "http" || r4.Healthcheck.Port != 3000 {
		t.Error("github http 健康检查未合成")
	}

	// github：默认值（branch=main，run=docker compose up -d）
	a5 := &store.CustomApp{ID: 16, Type: "github", Name: "g", ConfigJSON: `{"repo":"https://github.com/u/r"}`}
	r5, err := mgr.SynthRecipe(a5)
	if err != nil {
		t.Fatalf("github(默认) 合成失败: %v", err)
	}
	for _, st := range r5.Native.InstallSteps {
		if st.Exec != nil && st.Exec.Command == "git" && st.Exec.Args[4] != "main" {
			t.Errorf("默认分支应为 main，got %q", st.Exec.Args[4])
		}
	}
	if !strings.Contains(r5.Native.UnitTemplate, "docker compose up -d") {
		t.Error("默认 run 应为 docker compose up -d")
	}

	// 未知类型应报错
	if _, err := mgr.SynthRecipe(&store.CustomApp{ID: 17, Type: "weird", Name: "x", ConfigJSON: `{}`}); err == nil {
		t.Error("未知应用类型应报错")
	}
}
