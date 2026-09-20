package recipes

import (
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
