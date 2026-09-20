package apps

import (
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"

	"onecloud-panel/internal/recipes"
)

// ValidateInstallVars 安装入口前置校验：填充默认值后做类型与字符集校验，
// 防止用户变量注入 systemd 单元指令、破坏 YAML/配置文件结构。
func (m *Manager) ValidateInstallVars(recipeID string, vars map[string]string) error {
	recipe, ok := m.recipes.Get(recipeID)
	if !ok {
		return fmt.Errorf("配方不存在")
	}
	filled := map[string]string{}
	for k, v := range vars {
		filled[k] = v
	}
	for _, d := range recipe.Variables {
		if _, has := filled[d.Key]; !has {
			filled[d.Key] = d.Default
		}
	}
	return validateVariables(recipe, filled)
}

// validateVariables 对已填充默认值的变量做逐项校验。
func validateVariables(recipe *recipes.Recipe, vars map[string]string) error {
	for _, d := range recipe.Variables {
		val := vars[d.Key]
		name := d.Name
		if name == "" {
			name = d.Key
		}
		if val == "" {
			if d.Required {
				return fmt.Errorf("必填参数「%s」未提供", name)
			}
			continue
		}
		switch d.Type {
		case "int":
			if _, err := strconv.Atoi(val); err != nil {
				return fmt.Errorf("参数「%s」必须为整数", name)
			}
		case "bool":
			if _, err := strconv.ParseBool(val); err != nil {
				return fmt.Errorf("参数「%s」必须为布尔值（true/false）", name)
			}
		case "select":
			ok := false
			for _, opt := range d.Options {
				if opt == val {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("参数「%s」取值非法", name)
			}
		default: // string 及未声明类型
			if err := checkSafeString(name, val); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSafeString 字符串变量安全字符集：
//   - 必须为合法 UTF-8；
//   - 禁止任何控制字符（换行可注入 systemd 新指令）；
//   - 禁止 %（systemd 说明符）、单/双引号、反斜杠（单元参数与 YAML 转义）。
func checkSafeString(name, val string) error {
	if !utf8.ValidString(val) {
		return fmt.Errorf("参数「%s」含非法字符编码", name)
	}
	for _, r := range val {
		if unicode.IsControl(r) {
			return fmt.Errorf("参数「%s」含非法控制字符", name)
		}
		switch r {
		case '%', '\'', '"', '\\':
			return fmt.Errorf("参数「%s」含不允许的字符 %q", name, r)
		}
	}
	return nil
}
