package apps

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"onecloud-panel/internal/recipes"
)

// DockerOverride 安装容器应用时允许对配方渲染结果做的覆盖：
//   - Ports/Volumes：全量替换（nil/空 = 使用配方默认值）；
//   - Env：增量合并（KEY=VALUE，同 KEY 时覆盖值生效）；
//   - Restart：可选重启策略覆盖。
type DockerOverride struct {
	Ports   []string `json:"ports,omitempty"`
	Volumes []string `json:"volumes,omitempty"`
	Env     []string `json:"env,omitempty"`
	Restart string   `json:"restart,omitempty"`
}

// ValidateDockerOverride 安装入口前置校验（防注入，任务创建前给出明确错误）。
func (m *Manager) ValidateDockerOverride(recipeID string, ov *DockerOverride) error {
	if ov == nil {
		return nil
	}
	recipe, ok := m.recipes.Get(recipeID)
	if !ok {
		return fmt.Errorf("配方不存在")
	}
	if recipe.Docker == nil {
		return fmt.Errorf("该应用不支持容器方式安装")
	}
	return validateDockerOverride(ov)
}

func validateDockerOverride(ov *DockerOverride) error {
	for _, p := range ov.Ports {
		if _, _, _, err := parsePortSpec(strings.TrimSpace(p)); err != nil {
			return fmt.Errorf("端口映射 %q: %w", p, err)
		}
	}
	for _, v := range ov.Volumes {
		if err := checkVolumeSpec(v); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, e := range ov.Env {
		k, _, err := splitEnvEntry(e)
		if err != nil {
			return err
		}
		if seen[k] {
			return fmt.Errorf("环境变量 %s 重复定义", k)
		}
		seen[k] = true
	}
	switch ov.Restart {
	case "", "no", "always", "unless-stopped", "on-failure":
	default:
		return fmt.Errorf("重启策略非法 %q（可选 no/always/unless-stopped/on-failure）", ov.Restart)
	}
	return nil
}

// checkVolumeSpec 校验卷格式 "宿主路径|命名卷:容器路径[:ro|rw]"。
func checkVolumeSpec(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("数据卷 %q 格式非法（应为 宿主路径:容器路径[:ro]）", v)
	}
	src, dst := parts[0], parts[1]
	if src == "" || dst == "" {
		return fmt.Errorf("数据卷 %q 源/目标路径不能为空", v)
	}
	for _, p := range parts[:2] {
		if strings.ContainsAny(p, " \t\r\n") {
			return fmt.Errorf("数据卷 %q 路径不能包含空白字符", v)
		}
	}
	if dst != "" && (!strings.HasPrefix(dst, "/") || strings.Contains(dst, "..")) {
		return fmt.Errorf("数据卷 %q 容器路径必须为绝对路径", v)
	}
	if !strings.HasPrefix(src, "/") {
		// 命名卷：仅允许字母数字与 [-_.]，不允许路径分隔符
		if strings.Contains(src, "/") {
			return fmt.Errorf("数据卷 %q 命名卷不允许包含路径分隔符", v)
		}
		for _, r := range src {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
				r == '-' || r == '_' || r == '.') {
				return fmt.Errorf("数据卷 %q 命名卷仅允许字母数字与 -_. 字符", v)
			}
		}
	} else if strings.Contains(src, "..") {
		return fmt.Errorf("数据卷 %q 路径不允许 ..", v)
	}
	if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
		return fmt.Errorf("数据卷 %q 挂载选项仅支持 ro/rw", v)
	}
	return nil
}

// splitEnvEntry 校验并拆分 "KEY=VALUE" 环境变量条目。
func splitEnvEntry(e string) (key, value string, err error) {
	k, v, ok := strings.Cut(e, "=")
	if !ok || k == "" {
		return "", "", fmt.Errorf("环境变量 %q 格式非法（应为 KEY=VALUE）", e)
	}
	c := k[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
		return "", "", fmt.Errorf("环境变量名 %q 非法（须以字母或下划线开头）", k)
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", "", fmt.Errorf("环境变量名 %q 非法（仅允许字母数字与下划线）", k)
		}
	}
	if !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl) {
		return "", "", fmt.Errorf("环境变量 %s 的值含非法控制字符", k)
	}
	return k, v, nil
}

// mergeEnv 将覆盖环境变量增量合并到基础列表：基础顺序保留，同 KEY 覆盖，新 KEY 按给定顺序追加。
func mergeEnv(base, ov []string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base)+len(ov))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if _, dup := idx[k]; dup {
			continue
		}
		idx[k] = len(out)
		out = append(out, e)
	}
	for _, e := range ov {
		k, v, err := splitEnvEntry(e)
		if err != nil {
			continue // 已在上游校验
		}
		if i, ok := idx[k]; ok {
			out[i] = k + "=" + v
		} else {
			idx[k] = len(out)
			out = append(out, k+"="+v)
		}
	}
	return out
}

// applyDockerOverride 将覆盖应用到渲染后的 DockerSpec（Render 已深拷贝，可安全修改）。
func applyDockerOverride(ds *recipes.DockerSpec, ov *DockerOverride) error {
	if ds == nil || ov == nil {
		return nil
	}
	if err := validateDockerOverride(ov); err != nil {
		return err
	}
	if len(ov.Ports) > 0 {
		ds.Ports = ov.Ports
	}
	if len(ov.Volumes) > 0 {
		ds.Volumes = ov.Volumes
	}
	if len(ov.Env) > 0 {
		ds.Env = mergeEnv(ds.Env, ov.Env)
	}
	if ov.Restart != "" {
		ds.RestartPolicy = ov.Restart
	}
	return nil
}
