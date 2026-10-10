package recipes

import (
	"strings"
	"testing"
)

// TR-14.1 内置配方必须全部通过 Schema 校验，且能在每个声明架构上完成模板渲染。
func TestBuiltinRecipesLoadAndRender(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("内置配方加载失败: %v", err)
	}
	all := reg.List()
	if len(all) < 14 {
		t.Fatalf("内置配方数量不足: %d", len(all))
	}
	arches := []string{"armv7l", "aarch64", "x86_64", "i386"}
	for _, r := range all {
		for _, arch := range arches {
			// 该架构上至少一种安装方式才做渲染测试
			compat := r.Compatibility(arch)
			supported := false
			for _, c := range compat {
				if c.Supported {
					supported = true
				}
			}
			if !supported {
				continue
			}
			vars := map[string]string{}
			for _, v := range r.Variables {
				val := v.Default
				if val == "" {
					val = "test-value"
				}
				vars[v.Key] = val
			}
			rendered, err := r.Render(RenderData{
				Vars: vars, Node: NodeFactsForArch(arch, "node1"),
			})
			if err != nil {
				t.Fatalf("配方 %s 在 %s 渲染失败: %v", r.ID, arch, err)
			}
			for _, m := range compat {
				if !m.Supported {
					continue
				}
				if m.Method == "native" {
					if rendered.Recipe.Native.UnitName == "" {
						t.Fatalf("配方 %s 渲染后 unit_name 为空", r.ID)
					}
				}
				if m.Method == "docker" && rendered.Recipe.Docker.Image == "" {
					t.Fatalf("配方 %s 渲染后 image 为空", r.ID)
				}
			}
		}
	}
}

// 全部内置配方必须声明至少一种安装方式与非空描述。
func TestBuiltinRecipesMetadata(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.List() {
		if r.Description == "" || r.Homepage == "" {
			t.Fatalf("配方 %s 缺少描述或主页", r.ID)
		}
		if len(r.Methods) == 0 {
			t.Fatalf("配方 %s methods 为空", r.ID)
		}
		if len(r.Ports) > 0 {
			for _, p := range r.Ports {
				if p.Proto != "tcp" && p.Proto != "udp" {
					t.Fatalf("配方 %s 端口协议非法", r.ID)
				}
			}
		}
	}
}

// 关键应用必须进入内置清单，且直装（native）为第一优先级、Docker 作为兜底。
func TestKeyAppsNativeFirst(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]bool{}
	for _, r := range reg.List() {
		idx[r.ID] = true
	}
	for _, id := range []string{"cups", "cups-web", "lucky", "ddns-go"} {
		r, ok := reg.Get(id)
		if !ok {
			t.Fatalf("应用 %s 未进入内置清单", id)
		}
		if len(r.Methods) < 2 || r.Methods[0] != "native" || r.Methods[1] != "docker" {
			t.Fatalf("应用 %s methods=%v，应为 [native docker]（直装优先）", id, r.Methods)
		}
		if !idx[id] {
			t.Fatalf("应用 %s 未出现在 List() 结果中", id)
		}
	}
}

// defaultVars 取配方的默认变量值（空默认值用占位串填充，便于模板渲染）。
func defaultVars(r *Recipe) map[string]string {
	vars := map[string]string{}
	for _, v := range r.Variables {
		val := v.Default
		if val == "" {
			val = "test-value"
		}
		vars[v.Key] = val
	}
	return vars
}

// mi-gpt 必须改为从 Bet5521/MI-GPT-NEW 源码构建镜像，而不再拉取上游 idootop/mi-gpt 镜像。
func TestMIGPTBuildsFromSource(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reg.Get("migpt")
	if !ok {
		t.Fatal("migpt 未进入内置清单")
	}
	if r.Docker == nil || r.Docker.Build == nil {
		t.Fatal("migpt 应声明 docker.build（从源码构建镜像）")
	}
	b := r.Docker.Build
	if b.Source != "https://github.com/Bet5521/MI-GPT-NEW" {
		t.Fatalf("docker.build.source = %q", b.Source)
	}
	if b.Type != "git" {
		t.Fatalf("docker.build.type = %q，期望 git", b.Type)
	}
	if strings.Contains(r.Docker.Image, "idootop") || strings.Contains(b.Source, "idootop") {
		t.Fatalf("仍引用上游 idootop 镜像/仓库：image=%q source=%q", r.Docker.Image, b.Source)
	}
	// armv7l（玩客云）走仓库自带的 Dockerfile.arm，其余架构走多阶段 Dockerfile。
	for arch, want := range map[string]string{
		"armv7l": "Dockerfile.arm", "aarch64": "Dockerfile", "x86_64": "Dockerfile",
	} {
		rendered, err := r.Render(RenderData{Vars: defaultVars(r), Node: NodeFactsForArch(arch, "n1")})
		if err != nil {
			t.Fatalf("%s 渲染失败: %v", arch, err)
		}
		rb := rendered.Recipe.Docker.Build
		if rb.Dockerfile != want {
			t.Fatalf("%s dockerfile = %q，期望 %q", arch, rb.Dockerfile, want)
		}
		if rb.Ref == "" || strings.Contains(rb.Source, "{{") {
			t.Fatalf("%s 渲染后 build 字段未展开: ref=%q source=%q", arch, rb.Ref, rb.Source)
		}
	}
}

// Gitea 1.2x 起以 root 运行会 log.Fatal 直接退出，且启动强制依赖 git 可执行文件。
// 配方必须：以非 root 账号运行 + 安装 git。
func TestGiteaNonRootAndGitDependency(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reg.Get("gitea")
	if !ok {
		t.Fatal("gitea 未进入内置清单")
	}
	if r.Native == nil {
		t.Fatal("gitea 应有 native 规格")
	}
	rendered, err := r.Render(RenderData{Vars: defaultVars(r), Node: NodeFactsForArch("x86_64", "n1")})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	unit := rendered.Recipe.Native.UnitTemplate
	if !strings.Contains(unit, "User=git") {
		t.Fatalf("systemd 单元未以非 root 账号运行（Gitea 会拒绝启动）:\n%s", unit)
	}
	hasGit := false
	for _, s := range r.Native.InstallSteps {
		for _, p := range s.Apt {
			if p == "git" {
				hasGit = true
			}
		}
	}
	if !hasGit {
		t.Fatal("gitea install_steps 未声明 git 依赖（缺少 git 时启动即退出）")
	}
}
