package recipes

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

func yamlUnmarshal(s string, v any) error { return yaml.Unmarshal([]byte(s), v) }

func goodRecipeYAML(id string) string {
	return `
api_version: 1
id: ` + id + `
name: 测试应用
category: test
methods: [native, docker]
ports:
  - {port: 8080, proto: tcp, description: Web}
config_files:
  - {path: /etc/test/app.conf}
variables:
  - {key: domain, name: 域名, type: string, default: example.com}
healthcheck:
  {type: http, port: 8080, path: /}
native:
  arches: [armv7l, aarch64, x86_64]
  unit_name: testapp.service
  unit_template: |
    [Unit]
    Description={{.Vars.domain}}
    [Service]
    ExecStart=/usr/local/bin/testapp
  download:
    url_template: https://x/test-{{.Node.ArchGO}}.bin
    dest: /usr/local/bin/testapp
    mode: 0755
  install_steps:
    - name: 建目录
      mkdir: [/etc/test]
    - name: 写配置
      write:
        path: /etc/test/app.conf
        content: "DOMAIN={{.Vars.domain}}\n"
  uninstall_steps:
    - name: 停服
      systemctl: {action: stop, unit: testapp.service}
docker:
  arches: [armv7l, aarch64, x86_64]
  image: x/test:{{.Node.ArchDocker}}
  ports: ["8080:8080"]
  volumes: ["/data:/data"]
`
}

func mapFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

// TR-10.1 好配方加载通过；坏配方逐条报错且可定位。
func TestLoadAndValidate(t *testing.T) {
	reg, err := Load(mapFS(map[string]string{"testapp.yaml": goodRecipeYAML("testapp")}))
	if err != nil {
		t.Fatalf("加载好配方失败: %v", err)
	}
	if len(reg.List()) != 1 {
		t.Fatal("应包含 1 份配方")
	}
	rp, ok := reg.Get("testapp")
	if !ok {
		t.Fatal("按 ID 查询失败")
	}
	if len(rp.Ports) != 1 || rp.Ports[0].Port != 8080 {
		t.Fatal("端口解析异常")
	}

	badCases := []struct {
		name string
		yaml string
		want string
	}{
		{"bad_version", strings.Replace(goodRecipeYAML("badver"), "api_version: 1", "api_version: 9", 1), "api_version"},
		{"bad_id", strings.Replace(goodRecipeYAML("X"), "id: X", "id: 不合法", 1), "id"},
		{"bad_arch", strings.Replace(goodRecipeYAML("badarch"),
			"arches: [armv7l, aarch64, x86_64]", "arches: [mips]", 1), "未知架构"},
		{"bad_port", strings.Replace(goodRecipeYAML("badport"),
			"port: 8080", "port: 99999", 1), "端口越界"},
		{"two_actions", strings.Replace(goodRecipeYAML("twoact"),
			"mkdir: [/etc/test]", "mkdir: [/etc/test]\n      exec: {command: true}", 1), "一种动作"},
		{"missing_unit", strings.Replace(goodRecipeYAML("nounit"),
			"  unit_template: |\n    [Unit]\n    Description={{.Vars.domain}}\n    [Service]\n    ExecStart=/usr/local/bin/testapp\n",
			"  unit_template: \"\"\n", 1), "unit_template"},
		{"dup_id_only", "", ""},
	}
	for _, c := range badCases {
		if c.name == "dup_id_only" {
			continue
		}
		_, err := Load(mapFS(map[string]string{c.name + ".yaml": c.yaml}))
		if err == nil {
			t.Errorf("%s: 应报错", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 错误不含 %q：%v", c.name, c.want, err)
		}
	}
}

// 重复 ID 被拒绝。
func TestDuplicateID(t *testing.T) {
	fsys := mapFS(map[string]string{
		"a.yaml": goodRecipeYAML("dupapp"),
		"b.yaml": goodRecipeYAML("dupapp"),
	})
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 ID 未拒绝: %v", err)
	}
}

// TR-10.2 架构兼容性矩阵与原因；变量渲染注入。
func TestCompatibility(t *testing.T) {
	r := &Recipe{}
	if err := yamlUnmarshal(goodRecipeYAML("matrix"), r); err != nil {
		t.Fatal(err)
	}
	// armv7l：native/docker 均支持
	cs := r.Compatibility("armv7l")
	for _, c := range cs {
		if !c.Supported {
			t.Fatalf("armv7l %s 应支持: %s", c.Method, c.Reason)
		}
	}
	// i386：配方 arches 不含 → 两种方式均禁用且有原因
	cs = r.Compatibility("i386")
	for _, c := range cs {
		if c.Supported || c.Reason == "" {
			t.Fatalf("i386 %s 应禁用并给原因", c.Method)
		}
	}
	if ok, _ := r.Supports("native", "armv7l"); !ok {
		t.Fatal("Supports 判定错误")
	}
}

func TestRender(t *testing.T) {
	r := &Recipe{}
	if err := yamlUnmarshal(goodRecipeYAML("render1"), r); err != nil {
		t.Fatal(err)
	}
	out, err := r.Render(RenderData{
		Vars: map[string]string{"domain": "my.home"},
		Node: NodeFactsForArch("armv7l", "node1"),
	})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if !strings.Contains(out.Recipe.Native.UnitTemplate, "my.home") {
		t.Fatal("变量未注入 unit_template")
	}
	if !strings.Contains(out.Recipe.Native.Download.URLTemplate, "arm-7") {
		t.Fatalf("架构映射未注入下载地址: %s", out.Recipe.Native.Download.URLTemplate)
	}
	if !strings.Contains(out.Recipe.Docker.Image, "arm/v7") {
		t.Fatalf("Docker 架构未注入: %s", out.Recipe.Docker.Image)
	}
	if !strings.Contains(out.Recipe.Native.InstallSteps[1].Write.Content, "my.home") {
		t.Fatal("步骤模板未渲染")
	}
	// 原配方不应被修改
	if strings.Contains(r.Native.UnitTemplate, "my.home") {
		t.Fatal("渲染污染了原配方")
	}
}

// 缺失变量在严格模式下报错（missingkey=error）。
func TestRenderMissingVar(t *testing.T) {
	r := &Recipe{}
	_ = yamlUnmarshal(goodRecipeYAML("miss1"), r)
	_, err := r.Render(RenderData{
		Vars: map[string]string{},
		Node: NodeFactsForArch("armv7l", ""),
	})
	if err == nil {
		t.Fatal("缺少变量应渲染失败")
	}
}

// TR-10.3 可扩展性：新增应用仅需新增一份配方文件，框架自动加载、校验、纳入注册表，
// 无需任何核心代码改动（本测试即演练：一个全新文件被完整识别）。
func TestExtensibilityNewFileOnly(t *testing.T) {
	reg, err := Load(mapFS(map[string]string{
		"_placeholder.yaml": "id: skip", // 占位必须被跳过
		"newapp.yaml":       goodRecipeYAML("brand-new-app"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("brand-new-app"); !ok {
		t.Fatal("新增配方未自动纳入注册表")
	}
	if len(reg.List()) != 1 {
		t.Fatal("占位文件未被跳过")
	}
}
