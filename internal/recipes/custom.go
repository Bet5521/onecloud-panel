package recipes

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CustomAppData 自定义应用的原始数据（来自数据库）。
type CustomAppData struct {
	ID          int64
	AppID       string
	Name        string
	Category    string
	Icon        string
	Description string
	Homepage    string
	Method      string

	Ports     string // JSON
	Variables string // JSON

	DownloadURL     string
	UnitName        string
	UnitTemplate    string
	InstallScript   string
	UninstallScript string

	DockerImage     string
	DockerPorts     string // JSON
	DockerVolumes   string // JSON
	DockerEnv       string // JSON
	DockerNetwork   string
	DockerRestart   string
	DockerPrivileged bool

	HealthcheckType string
	HealthcheckPort int
	HealthcheckPath string
	HealthcheckCmd  string // JSON
}

// RecipeFromCustomApp 将自定义应用数据转换为 Recipe。
func RecipeFromCustomApp(data *CustomAppData) (*Recipe, error) {
	r := &Recipe{
		APIVersion:  1,
		ID:          data.AppID,
		Name:        data.Name,
		Category:    data.Category,
		Icon:        data.Icon,
		Description: data.Description,
		Homepage:    data.Homepage,
		Methods:     []string{data.Method},
	}

	// 解析端口
	if data.Ports != "" && data.Ports != "[]" {
		var ports []PortSpec
		if err := json.Unmarshal([]byte(data.Ports), &ports); err != nil {
			return nil, fmt.Errorf("解析端口失败: %w", err)
		}
		r.Ports = ports
	}

	// 解析变量
	if data.Variables != "" && data.Variables != "[]" {
		var vars []Variable
		if err := json.Unmarshal([]byte(data.Variables), &vars); err != nil {
			return nil, fmt.Errorf("解析变量失败: %w", err)
		}
		r.Variables = vars
	}

	// 健康检查
	if data.HealthcheckType != "" {
		r.Healthcheck = &Healthcheck{
			Type: data.HealthcheckType,
			Port: data.HealthcheckPort,
			Path: data.HealthcheckPath,
		}
		if data.HealthcheckCmd != "" && data.HealthcheckCmd != "[]" {
			var cmd []string
			if err := json.Unmarshal([]byte(data.HealthcheckCmd), &cmd); err == nil {
				r.Healthcheck.Command = cmd
			}
		}
	}

	// 构建 native 规格
	if data.Method == "native" {
		ns := &NativeSpec{
			Arches:   []string{"armv7l", "aarch64", "x86_64", "i386"},
			UnitName: data.UnitName,
		}

		// 构建 unit template
		if data.UnitTemplate != "" {
			ns.UnitTemplate = data.UnitTemplate
		} else {
			// 生成默认 unit template
			ns.UnitTemplate = generateDefaultUnitTemplate(data.UnitName, data.DownloadURL)
		}

		// 构建安装步骤
		if data.DownloadURL != "" {
			// 支持 GitHub release URL 模板
			dlURL := data.DownloadURL
			if strings.Contains(dlURL, "github.com") && strings.Contains(dlURL, "/releases/") {
				// GitHub release URL，使用模板变量
				dlURL = expandGitHubURL(dlURL)
			}
			ns.InstallSteps = []Step{
				{
					Name: "下载程序",
					Download: &Download{
						URL:  dlURL,
						Dest: "/usr/local/bin/" + data.UnitName,
						Mode: 0o755,
					},
				},
			}
		}

		// 自定义安装脚本
		if data.InstallScript != "" {
			ns.InstallSteps = append(ns.InstallSteps, Step{
				Name: "自定义安装",
				Exec: &ExecStep{
					Command: "sh",
					Args:    []string{"-c", data.InstallScript},
				},
			})
		}

		// 卸载步骤
		ns.UninstallSteps = []Step{
			{
				Name: "删除程序",
				Exec: &ExecStep{
					Command: "rm",
					Args:    []string{"-f", "/usr/local/bin/" + data.UnitName},
				},
			},
		}
		if data.UninstallScript != "" {
			ns.UninstallSteps = append(ns.UninstallSteps, Step{
				Name: "自定义卸载",
				Exec: &ExecStep{
					Command: "sh",
					Args:    []string{"-c", data.UninstallScript},
				},
			})
		}

		r.Native = ns
	}

	// 构建 docker 规格
	if data.Method == "docker" {
		ds := &DockerSpec{
			Arches: []string{"armv7l", "aarch64", "x86_64", "i386"},
			Image:  data.DockerImage,
		}

		// 端口映射
		if data.DockerPorts != "" && data.DockerPorts != "[]" {
			var ports []string
			if err := json.Unmarshal([]byte(data.DockerPorts), &ports); err == nil {
				ds.Ports = ports
			}
		}

		// 卷挂载
		if data.DockerVolumes != "" && data.DockerVolumes != "[]" {
			var volumes []string
			if err := json.Unmarshal([]byte(data.DockerVolumes), &volumes); err == nil {
				ds.Volumes = volumes
			}
		}

		// 环境变量
		if data.DockerEnv != "" && data.DockerEnv != "[]" {
			var env []string
			if err := json.Unmarshal([]byte(data.DockerEnv), &env); err == nil {
				ds.Env = env
			}
		}

		ds.NetworkMode = data.DockerNetwork
		ds.RestartPolicy = data.DockerRestart
		ds.Privileged = data.DockerPrivileged

		r.Docker = ds
	}

	return r, nil
}

// generateDefaultUnitTemplate 生成默认的 systemd unit 模板。
func generateDefaultUnitTemplate(unitName, downloadURL string) string {
	execStart := "/usr/local/bin/" + unitName
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5s
[Install]
WantedBy=multi-user.target`, unitName, execStart)
}

// expandGitHubURL 将 GitHub release URL 转换为模板。
// 例如: https://github.com/user/repo/releases/latest/download/app-linux-amd64
// 变为: https://github.com/user/repo/releases/latest/download/app-linux-{{.Node.ArchRel}}
func expandGitHubURL(url string) string {
	// 常见架构替换
	replacements := map[string]string{
		"amd64":  "{{.Node.ArchGO}}",
		"x86_64": "{{.Node.ArchRel}}",
		"arm64":  "{{.Node.ArchRel}}",
		"aarch64": "{{.Node.ArchRel}}",
		"armv7l": "{{.Node.ArchV7}}",
		"arm-7":  "{{.Node.ArchV7}}",
		"armhf":  "{{.Node.ArchV7}}",
	}
	for arch, tmpl := range replacements {
		if strings.Contains(url, arch) {
			return strings.Replace(url, arch, tmpl, 1)
		}
	}
	return url
}