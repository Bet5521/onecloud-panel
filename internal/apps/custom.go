package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/store"
)

// 自定义应用合成配方的统一架构集合（二进制/源码类应用由用户保证架构匹配，
// 这里放行全部架构以允许安装尝试；运行期架构不匹配会以 exec format 错误暴露）。
var customArches = []string{"armv7l", "aarch64", "x86_64", "i386"}

// binaryDir 服务端保存已上传自定义二进制文件的目录。
func (m *Manager) BinaryDir() string { return m.binaryDir }

// SetBinaryDir 注入自定义二进制目录（默认 <data-dir>/custom-apps）。
func (m *Manager) SetBinaryDir(dir string) { m.binaryDir = dir }

// customRecipeID 自定义应用 → 合成配方 ID（同时作为安装记录 AppID）。
func customRecipeID(id int64) string { return "custom-" + strconv.FormatInt(id, 10) }

// ---- 配置结构 ----

type healthCfg struct {
	Type string   `json:"type"` // http / tcp / command
	Port int      `json:"port"`
	Path string   `json:"path"`
	Cmd  []string `json:"cmd"`
}

type dockerAppConfig struct {
	Image      string    `json:"image"`
	Ports      []string  `json:"ports"`
	Env        []string  `json:"env"`
	Volumes    []string  `json:"volumes"`
	Restart    string    `json:"restart"`
	Privileged bool      `json:"privileged"`
	Network    string    `json:"network"`
	Health     healthCfg `json:"health"`
}

type binaryAppConfig struct {
	ExecName string    `json:"exec_name"`
	Args     string    `json:"args"`
	WorkDir  string    `json:"workdir"`
	User     string    `json:"user"`
	Health   healthCfg `json:"health"`
}

type githubAppConfig struct {
	Repo   string    `json:"repo"`
	Branch string    `json:"branch"`
	Run    string    `json:"run"`
	Health healthCfg `json:"health"`
}

// SynthRecipe 由自定义应用记录合成一份可安装的 *Recipe。
func (m *Manager) SynthRecipe(app *store.CustomApp) (*recipes.Recipe, error) {
	// 入口统一校验：名称/路径/字符集约束（防 systemd 单元注入与任意路径写入）
	if err := ValidateCustomConfig(app); err != nil {
		return nil, err
	}
	id := customRecipeID(app.ID)
	base := recipes.Recipe{
		APIVersion:  1,
		ID:          id,
		Name:        app.Name,
		Category:    app.Category,
		Icon:        app.Icon,
		Description: app.Description,
		Homepage:    app.Homepage,
	}
	switch app.Type {
	case "docker":
		var c dockerAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return nil, fmt.Errorf("docker 配置解析失败: %w", err)
		}
		if strings.TrimSpace(c.Image) == "" {
			return nil, errors.New("docker 应用需指定 image")
		}
		base.Methods = []string{"docker"}
		base.Docker = &recipes.DockerSpec{
			Arches:        customArches,
			Image:         strings.TrimSpace(c.Image),
			Ports:         c.Ports,
			Env:           c.Env,
			Volumes:       c.Volumes,
			Privileged:    c.Privileged,
			NetworkMode:   strings.TrimSpace(c.Network),
			RestartPolicy: strings.TrimSpace(c.Restart),
		}
		base.Healthcheck = synthHealth(c.Health)
	case "binary":
		var c binaryAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return nil, fmt.Errorf("二进制配置解析失败: %w", err)
		}
		if strings.TrimSpace(c.ExecName) == "" {
			return nil, errors.New("二进制应用需指定 exec_name")
		}
		work := strings.TrimSpace(c.WorkDir)
		if work == "" {
			work = "/opt/onecloud-apps/" + strconv.FormatInt(app.ID, 10)
		} else if norm, err := safeWorkDir(work); err == nil {
			work = norm
		}
		base.Methods = []string{"native"}
		base.Native = &recipes.NativeSpec{
			Arches:       customArches,
			UnitName:     "ocp-" + id + ".service",
			UnitTemplate: buildBinaryUnit(app.Name, work, c.ExecName, c.Args, c.User),
			InstallSteps: []recipes.Step{{Mkdir: []string{work}}},
		}
		// 把工作目录（二进制所在目录）声明为数据目录，使卸载时
		// purge_data=true 能真正清理已推送的二进制，purge_data=false 则保留。
		base.Volumes = []string{work}
		base.Healthcheck = synthHealth(c.Health)
	case "github":
		var c githubAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return nil, fmt.Errorf("GitHub 配置解析失败: %w", err)
		}
		if strings.TrimSpace(c.Repo) == "" {
			return nil, errors.New("GitHub 应用需指定 repo")
		}
		branch := strings.TrimSpace(c.Branch)
		if branch == "" {
			branch = "main"
		}
		run := strings.TrimSpace(c.Run)
		if run == "" {
			run = "docker compose up -d"
		}
		src := "/opt/onecloud-apps/" + strconv.FormatInt(app.ID, 10) + "/src"
		repo := withProxy(m.githubProxy(), strings.TrimSpace(c.Repo))
		base.Methods = []string{"native"}
		base.Native = &recipes.NativeSpec{
			Arches:       customArches,
			UnitName:     "ocp-" + id + ".service",
			UnitTemplate: buildRunUnit(app.Name, src, run),
			InstallSteps: []recipes.Step{
				{Mkdir: []string{src}},
				{Exec: &recipes.ExecStep{Command: "git", Args: []string{"clone", "--depth", "1", "-b", branch, repo, src}}},
			},
			UninstallSteps: []recipes.Step{
				{Exec: &recipes.ExecStep{Command: "rm", Args: []string{"-rf", src}}},
			},
		}
		base.Healthcheck = synthHealth(c.Health)
	default:
		return nil, fmt.Errorf("未知自定义应用类型: %s", app.Type)
	}
	return &base, nil
}

func synthHealth(h healthCfg) *recipes.Healthcheck {
	switch h.Type {
	case "http", "tcp":
		if h.Port > 0 {
			hc := &recipes.Healthcheck{Type: h.Type, Port: h.Port}
			if h.Type == "http" {
				hc.Path = h.Path
			}
			return hc
		}
	case "command":
		if len(h.Cmd) > 0 {
			return &recipes.Healthcheck{Type: "command", Command: h.Cmd}
		}
	}
	return nil
}

func buildBinaryUnit(name, work, execName, args, user string) string {
	bin := path.Join(work, execName)
	execStart := bin
	if a := strings.TrimSpace(args); a != "" {
		execStart = bin + " " + a
	}
	u := strings.TrimSpace(user)
	if u == "" {
		u = "root"
	}
	return fmt.Sprintf(`[Unit]
Description=%s (OneCloud 自定义应用)
After=network.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, name, u, work, execStart)
}

func buildRunUnit(name, src, run string) string {
	// 防止破坏 shell 字符串：剔除双引号（入口校验已禁止引号/换行/%，这里再兜一层）
	run = strings.ReplaceAll(run, `"`, "")
	return fmt.Sprintf(`[Unit]
Description=%s (OneCloud 自定义应用)
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=/bin/sh -c "%s"
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, name, src, run)
}

// RegisterCustomApps 启动时把全部自定义应用合成配方注入注册表。
func (m *Manager) RegisterCustomApps() error {
	apps, err := m.store.ListCustomApps(nil)
	if err != nil {
		return err
	}
	for i := range apps {
		r, err := m.SynthRecipe(&apps[i])
		if err != nil {
			log.Printf("自定义应用 %d 合成配方失败（跳过）: %v", apps[i].ID, err)
			continue
		}
		m.recipes.Add(r)
	}
	return nil
}

// RegisterCustomApp 注册（覆盖）单个自定义应用的合成配方。
func (m *Manager) RegisterCustomApp(app *store.CustomApp) error {
	r, err := m.SynthRecipe(app)
	if err != nil {
		return err
	}
	m.recipes.Add(r)
	return nil
}

// UnregisterCustomApp 移除自定义应用配方（删除时调用）。
func (m *Manager) UnregisterCustomApp(id int64) {
	m.recipes.Remove(customRecipeID(id))
}

// PushCustomBinary 把服务端已上传的二进制推送到目标节点并置为可执行。
// 安装前调用，确保 systemd 单元启动时有可执行文件。
func (m *Manager) PushCustomBinary(ctx context.Context, w io.Writer,
	nodeID int64, app *store.CustomApp) error {

	if m.binaryDir == "" {
		return errors.New("二进制目录未配置")
	}
	var c binaryAppConfig
	if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
		return err
	}
	execName := strings.TrimSpace(c.ExecName)
	if execName == "" {
		return errors.New("未指定 exec_name")
	}
	if err := safeExecName(execName); err != nil {
		return err
	}
	src := filepath.Join(m.binaryDir, strconv.FormatInt(app.ID, 10), "app")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("未找到已上传的二进制（请先上传）: %w", err)
	}
	n, err := m.nodes.Get(nodeID)
	if err != nil {
		return err
	}
	ex, err := m.ExecutorFor(n)
	if err != nil {
		return err
	}
	work := strings.TrimSpace(c.WorkDir)
	if work == "" {
		work = "/opt/onecloud-apps/" + strconv.FormatInt(app.ID, 10)
	} else {
		norm, err := safeWorkDir(work)
		if err != nil {
			return err
		}
		work = norm
	}
	if _, err := ex.Exec(ctx, "mkdir", "-p", work); err != nil {
		return fmt.Errorf("创建应用目录失败: %w", err)
	}
	dest := path.Join(work, execName)
	if err := ex.WriteFile(dest, data); err != nil {
		return fmt.Errorf("推送二进制到节点失败: %w", err)
	}
	if _, err := ex.Exec(ctx, "chmod", "0755", dest); err != nil {
		fmt.Fprintf(w, "  警告: chmod 失败: %v\n", err)
	}
	fmt.Fprintf(w, "→ 二进制已推送至 %s\n", dest)
	return nil
}
