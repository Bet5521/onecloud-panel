package apps

import (
	"testing"

	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
)

// 每份内置 docker 配方的端口/规格必须能生成合法的容器创建请求体。
func TestBuiltinDockerRecipesCreateBody(t *testing.T) {
	reg, err := recipes.LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg.List() {
		for _, arch := range []string{"armv7l", "aarch64", "x86_64", "i386"} {
			ok, _ := r.Supports("docker", arch)
			if !ok {
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
			rendered, err := r.Render(recipes.RenderData{
				Vars: vars, Node: recipes.NodeFactsForArch(arch, "n"),
			})
			if err != nil {
				t.Fatalf("%s render: %v", r.ID, err)
			}
			ds := rendered.Recipe.Docker
			body, err := buildCreateBody(ds.Image, ds, rendered)
			if err != nil {
				t.Fatalf("配方 %s (%s) create body: %v", r.ID, arch, err)
			}
			if len(body) == 0 {
				t.Fatalf("配方 %s (%s) body 为空", r.ID, arch)
			}
		}
	}
}

// 内置 native 配方在兼容架构上应通过 prepare 的渲染与参数填充。
func TestBuiltinNativeRecipesPrepare(t *testing.T) {
	_, _, _, s := dockerAppsSetup(t) // 已建本机节点（armv7l）
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := recipes.LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	mgr := New(s, node.New(s, box), reg, box, nil)
	for _, r := range reg.List() {
		ok, _ := r.Supports("native", "armv7l")
		if !ok {
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
		n, _, _, _, err := mgr.prepare(1, r.ID, "native", vars)
		if err != nil {
			t.Fatalf("配方 %s prepare: %v", r.ID, err)
		}
		if n == nil {
			t.Fatalf("配方 %s node 为空", r.ID)
		}
	}
}
