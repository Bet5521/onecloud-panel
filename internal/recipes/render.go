package recipes

import (
	"bytes"
	"fmt"
	"text/template"
)

// NodeFacts 渲染时可用的节点事实。
type NodeFacts struct {
	Arch       string
	Hostname   string
	ArchGO     string // go 工具链风格（arm-7/arm64/amd64/386），供 Gitea 等使用
	ArchRel    string // 常见发布包后缀（arm/arm64/amd64/386），供 cloudflared/verysync/syncthing 等使用
	ArchV7     string // armv7 显式风格（armv7/arm64/amd64/386），供 AdGuard/mihomo 等使用
	ArchDocker string
}

// RenderData 模板上下文。
type RenderData struct {
	Vars map[string]string
	Node NodeFacts
}

func tmplFuncs() template.FuncMap {
	return template.FuncMap{
		"default": func(def, v string) string {
			if v == "" {
				return def
			}
			return v
		},
	}
}

// ValidateTemplate 解析模板（语法/函数名校验）。
func ValidateTemplate(_ *Recipe, text, where string) error {
	if _, err := template.New(where).Funcs(tmplFuncs()).Parse(text); err != nil {
		return fmt.Errorf("模板 %s 解析失败: %w", where, err)
	}
	return nil
}

func render(text string, data RenderData) (string, error) {
	t, err := template.New("").Funcs(tmplFuncs()).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// NodeFactsForArch 架构映射。
func NodeFactsForArch(arch, hostname string) NodeFacts {
	f := NodeFacts{Arch: arch, Hostname: hostname}
	switch arch {
	case "armv7l":
		f.ArchGO = "arm-7"
		f.ArchRel = "arm"
		f.ArchV7 = "armv7"
		f.ArchDocker = "arm/v7"
	case "aarch64":
		f.ArchGO = "arm64"
		f.ArchRel = "arm64"
		f.ArchV7 = "arm64"
		f.ArchDocker = "arm64/v8"
	case "x86_64":
		f.ArchGO = "amd64"
		f.ArchRel = "amd64"
		f.ArchV7 = "amd64"
		f.ArchDocker = "amd64"
	case "i386":
		f.ArchGO = "386"
		f.ArchRel = "386"
		f.ArchV7 = "386"
		f.ArchDocker = "386"
	default:
		f.ArchGO = arch
		f.ArchRel = arch
		f.ArchV7 = arch
		f.ArchDocker = arch
	}
	return f
}

// RenderedRecipe 渲染后的配方（供执行器直接使用）。
type RenderedRecipe struct {
	Recipe *Recipe
	Node   NodeFacts
}

// Render 按变量与节点事实渲染所有模板字段，返回可执行视图。
func (r *Recipe) Render(data RenderData) (*RenderedRecipe, error) {
	// 深拷贝：简单做法是再次 yaml round-trip（启动/安装频率低，成本可接受）
	out, err := cloneRecipe(r)
	if err != nil {
		return nil, err
	}
	renderStr := func(s string, where string) (string, error) {
		if s == "" {
			return s, nil
		}
		v, err := render(s, data)
		if err != nil {
			return "", fmt.Errorf("渲染 %s 失败: %w", where, err)
		}
		return v, nil
	}

	if out.Native != nil {
		if out.Native.UnitTemplate, err = renderStr(out.Native.UnitTemplate, "unit_template"); err != nil {
			return nil, err
		}
		if out.Native.Download != nil {
			if out.Native.Download.URLTemplate, err =
				renderStr(out.Native.Download.URLTemplate, "download.url_template"); err != nil {
				return nil, err
			}
		}
		if err := renderSteps(out.Native.InstallSteps, renderStr); err != nil {
			return nil, err
		}
		if err := renderSteps(out.Native.UninstallSteps, renderStr); err != nil {
			return nil, err
		}
	}
	if out.Docker != nil {
		if out.Docker.Image, err = renderStr(out.Docker.Image, "docker.image"); err != nil {
			return nil, err
		}
		for i, e := range out.Docker.Env {
			if out.Docker.Env[i], err = renderStr(e, "docker.env"); err != nil {
				return nil, err
			}
		}
		if err := renderSteps(out.Docker.InstallSteps, renderStr); err != nil {
			return nil, err
		}
		if err := renderSteps(out.Docker.UninstallSteps, renderStr); err != nil {
			return nil, err
		}
	}
	return &RenderedRecipe{Recipe: out, Node: data.Node}, nil
}

func renderSteps(steps []Step, renderStr func(string, string) (string, error)) error {
	for i := range steps {
		s := &steps[i]
		if s.Download != nil {
			v, err := renderStr(s.Download.URL, "download.url")
			if err != nil {
				return err
			}
			s.Download.URL = v
		}
		if s.Write != nil {
			v, err := renderStr(s.Write.Content, "write.content")
			if err != nil {
				return err
			}
			s.Write.Content = v
		}
		if s.Exec != nil {
			for j, a := range s.Exec.Args {
				v, err := renderStr(a, "exec.args")
				if err != nil {
					return err
				}
				s.Exec.Args[j] = v
			}
		}
	}
	return nil
}
