package recipes

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// 合法架构集合（与 system.normalizeArch 输出一致）。
var validArches = map[string]bool{
	"armv7l": true, "aarch64": true, "x86_64": true, "i386": true,
}

var validMethods = map[string]bool{"native": true, "docker": true}
var validVarTypes = map[string]bool{"string": true, "int": true, "bool": true, "select": true}
var validStepActions = map[string]bool{
	"apt": true, "download": true, "mkdir": true,
	"write": true, "exec": true, "systemctl": true,
}
var validSystemctlActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "enable": true,
	"disable": true, "daemon-reload": true,
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,32}$`)

// Registry 配方注册表。
type Registry struct {
	mu    sync.RWMutex
	byID  map[string]*Recipe
	order []string
}

// Add 注册（或覆盖）一个配方；用于运行时注入自定义应用合成配方。
func (g *Registry) Add(r *Recipe) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.byID[r.ID]; !exists {
		g.order = append(g.order, r.ID)
	}
	g.byID[r.ID] = r
}

// Remove 移除配方（自定义应用删除时调用）。
func (g *Registry) Remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.byID[id]; !exists {
		return
	}
	delete(g.byID, id)
	for i, x := range g.order {
		if x == id {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
}

// Load 从 fsys 加载并校验全部配方；"_" 前缀文件为内部占位，跳过。
func Load(fsys fs.FS) (*Registry, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	reg := &Registry{byID: map[string]*Recipe{}}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		if strings.HasPrefix(e.Name(), "_") {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: 读取失败 %v", e.Name(), err))
			continue
		}
		r := &Recipe{}
		if err := yaml.Unmarshal(data, r); err != nil {
			errs = append(errs, fmt.Sprintf("%s: YAML 解析失败 %v", e.Name(), err))
			continue
		}
		if err := Validate(r); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if _, dup := reg.byID[r.ID]; dup {
			errs = append(errs, fmt.Sprintf("%s: 配方 ID %q 重复", e.Name(), r.ID))
			continue
		}
		reg.byID[r.ID] = r
		reg.order = append(reg.order, r.ID)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("配方校验失败：\n  - %s", strings.Join(errs, "\n  - "))
	}
	sort.Strings(reg.order)
	return reg, nil
}

// List 全部配方（稳定顺序）。
func (g *Registry) List() []*Recipe {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*Recipe, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.byID[id])
	}
	return out
}

// Get 按 ID 取配方。
func (g *Registry) Get(id string) (*Recipe, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, ok := g.byID[id]
	return r, ok
}

// Validate 校验单份配方；错误信息可直接展示。
func Validate(r *Recipe) error {
	if r.APIVersion != 1 {
		return fmt.Errorf("api_version 必须为 1（当前 %d）", r.APIVersion)
	}
	if !idPattern.MatchString(r.ID) {
		return fmt.Errorf("id %q 非法（2-33 位小写字母数字/_-）", r.ID)
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("name 不能为空")
	}
	if len(r.Methods) == 0 {
		return fmt.Errorf("配方 %s: methods 不能为空", r.ID)
	}
	seenM := map[string]bool{}
	for _, m := range r.Methods {
		if !validMethods[m] {
			return fmt.Errorf("配方 %s: 未知安装方式 %q", r.ID, m)
		}
		if seenM[m] {
			return fmt.Errorf("配方 %s: 安装方式 %q 重复", r.ID, m)
		}
		seenM[m] = true
	}

	for i, p := range r.Ports {
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("配方 %s: ports[%d] 端口越界 %d", r.ID, i, p.Port)
		}
		if p.Proto != "tcp" && p.Proto != "udp" {
			return fmt.Errorf("配方 %s: ports[%d] 协议非法 %q", r.ID, i, p.Proto)
		}
	}

	seenV := map[string]bool{}
	for i, v := range r.Variables {
		if !idPattern.MatchString("v-" + v.Key) {
			return fmt.Errorf("配方 %s: variables[%d] key %q 非法", r.ID, i, v.Key)
		}
		if seenV[v.Key] {
			return fmt.Errorf("配方 %s: 变量 %q 重复", r.ID, v.Key)
		}
		seenV[v.Key] = true
		if !validVarTypes[v.Type] {
			return fmt.Errorf("配方 %s: 变量 %q 类型非法 %q", r.ID, v.Key, v.Type)
		}
		if v.Type == "select" && len(v.Options) == 0 {
			return fmt.Errorf("配方 %s: 变量 %q select 类型需提供 options", r.ID, v.Key)
		}
	}

	for i, c := range r.ConfigFiles {
		if !strings.HasPrefix(c.Path, "/") {
			return fmt.Errorf("配方 %s: config_files[%d] 必须为绝对路径", r.ID, i)
		}
	}

	if r.Healthcheck != nil {
		switch r.Healthcheck.Type {
		case "http", "tcp":
			if r.Healthcheck.Port < 1 || r.Healthcheck.Port > 65535 {
				return fmt.Errorf("配方 %s: healthcheck 端口非法", r.ID)
			}
		case "command":
			if len(r.Healthcheck.Command) == 0 {
				return fmt.Errorf("配方 %s: command 健康检查缺少命令", r.ID)
			}
		default:
			return fmt.Errorf("配方 %s: healthcheck 类型非法 %q", r.ID, r.Healthcheck.Type)
		}
	}

	if seenM["native"] {
		if err := validateNative(r); err != nil {
			return err
		}
	} else if r.Native != nil {
		return fmt.Errorf("配方 %s: 声明了 native 规格但 methods 不含 native", r.ID)
	}
	if seenM["docker"] {
		if err := validateDocker(r); err != nil {
			return err
		}
	} else if r.Docker != nil {
		return fmt.Errorf("配方 %s: 声明了 docker 规格但 methods 不含 docker", r.ID)
	}
	return nil
}

func validateArches(r *Recipe, where string, arches []string) error {
	if len(arches) == 0 {
		return fmt.Errorf("配方 %s: %s arches 为空", r.ID, where)
	}
	for _, a := range arches {
		if !validArches[a] {
			return fmt.Errorf("配方 %s: %s 未知架构 %q", r.ID, where, a)
		}
	}
	return nil
}

func validateSteps(r *Recipe, where string, steps []Step) error {
	for i, s := range steps {
		kinds := 0
		if len(s.Apt) > 0 {
			kinds++
		}
		if s.Download != nil {
			kinds++
		}
		if len(s.Mkdir) > 0 {
			kinds++
		}
		if s.Write != nil {
			kinds++
		}
		if s.Exec != nil {
			kinds++
		}
		if s.Systemctl != nil {
			kinds++
		}
		if kinds != 1 {
			return fmt.Errorf("配方 %s: %s[%d] 必须且只能包含一种动作（当前 %d）",
				r.ID, where, i, kinds)
		}
		prefix := fmt.Sprintf("配方 %s: %s[%d]", r.ID, where, i)
		if s.Download != nil {
			if s.Download.URL == "" || s.Download.Dest == "" {
				return fmt.Errorf("%s download 需包含 url/dest", prefix)
			}
		}
		if s.Write != nil {
			if !strings.HasPrefix(s.Write.Path, "/") {
				return fmt.Errorf("%s write.path 必须为绝对路径", prefix)
			}
		}
		if s.Exec != nil && s.Exec.Command == "" {
			return fmt.Errorf("%s exec.command 不能为空", prefix)
		}
		if s.Systemctl != nil {
			if !validSystemctlActions[s.Systemctl.Action] {
				return fmt.Errorf("%s systemctl 动作非法 %q", prefix, s.Systemctl.Action)
			}
			if s.Systemctl.Unit == "" {
				return fmt.Errorf("%s systemctl.unit 不能为空", prefix)
			}
		}
	}
	return nil
}

func validateNative(r *Recipe) error {
	n := r.Native
	if n == nil {
		return fmt.Errorf("配方 %s: 缺少 native 规格", r.ID)
	}
	if err := validateArches(r, "native", n.Arches); err != nil {
		return err
	}
	if n.UnitName == "" {
		return fmt.Errorf("配方 %s: native.unit_name 不能为空", r.ID)
	}
	if strings.TrimSpace(n.UnitTemplate) == "" {
		return fmt.Errorf("配方 %s: native.unit_template 不能为空", r.ID)
	}
	if err := ValidateTemplate(r, n.UnitTemplate, "unit_template"); err != nil {
		return err
	}
	if err := validateSteps(r, "install_steps", n.InstallSteps); err != nil {
		return err
	}
	if err := validateSteps(r, "uninstall_steps", n.UninstallSteps); err != nil {
		return err
	}
	if n.Download != nil {
		if n.Download.URLTemplate == "" || n.Download.Dest == "" {
			return fmt.Errorf("配方 %s: native.download 需包含 url_template/dest", r.ID)
		}
		if err := ValidateTemplate(r, n.Download.URLTemplate, "download.url_template"); err != nil {
			return err
		}
	}
	return nil
}

func validateDocker(r *Recipe) error {
	d := r.Docker
	if d == nil {
		return fmt.Errorf("配方 %s: 缺少 docker 规格", r.ID)
	}
	if err := validateArches(r, "docker", d.Arches); err != nil {
		return err
	}
	if d.Image == "" {
		return fmt.Errorf("配方 %s: docker.image 不能为空", r.ID)
	}
	if err := ValidateTemplate(r, d.Image, "docker.image"); err != nil {
		return err
	}
	if err := validateDockerUser(r.ID, d.User); err != nil {
		return err
	}
	if err := validateSteps(r, "docker.install_steps", d.InstallSteps); err != nil {
		return err
	}
	if err := validateSteps(r, "docker.uninstall_steps", d.UninstallSteps); err != nil {
		return err
	}
	return nil
}

// validateDockerUser 校验 docker.user 的基本形态。
// Docker 允许 "uid"、"uid:gid"、"name"、"name:group" 四种写法，故不做纯数字限制，
// 只拦掉空白与空段这类必然导致 docker create 失败的写法。
func validateDockerUser(id, user string) error {
	if user == "" {
		return nil
	}
	if strings.ContainsAny(user, " \t\r\n") {
		return fmt.Errorf("配方 %s: docker.user %q 含空白字符", id, user)
	}
	parts := strings.Split(user, ":")
	if len(parts) > 2 {
		return fmt.Errorf("配方 %s: docker.user %q 格式非法（应为 UID 或 UID:GID）", id, user)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("配方 %s: docker.user %q 存在空段", id, user)
		}
	}
	return nil
}
