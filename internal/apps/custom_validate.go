package apps

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"onecloud-panel/internal/store"
)

// 自定义应用的合成配方会把用户输入拼进 systemd 单元文件、/bin/sh -c 字符串与
// 节点文件路径。这里统一做字符集与路径约束：
//   - systemd 单元注入：换行/回车可写入新指令（如 ExecStartPre=），% 会被展开为
//     说明符（%i/%n 等），因此拼进单元文件的字段一律禁止；
//   - 路径穿越/任意写：workdir + exec_name 决定以 root 身份写入的落点，必须是
//     绝对路径且位于允许根之下，exec_name 只能是纯文件名。
//
// 校验在 SynthRecipe 入口统一执行，创建 / 更新 / 安装三条链路全覆盖。

// allowedWorkRoots 允许的应用工作目录根前缀（默认 /opt/onecloud-apps/<id>）。
var allowedWorkRoots = []string{
	"/opt/onecloud-apps",
	"/opt",
	"/srv",
	"/var/lib",
	"/data",
	"/home",
}

var (
	execNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	userNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	imageRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]{0,255}$`)
	repoPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	branchRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,64}$`)
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`) // docker network/restart 等短标识
)

// safeText 禁止控制字符与 systemd 说明符等危险字符（用于拼进单元文件或 shell 字符串的字段）。
func safeText(name, val string, extraForbidden ...rune) error {
	if !utf8.ValidString(val) {
		return fmt.Errorf("%s 含非法字符编码", name)
	}
	forbidden := map[rune]bool{'%': true}
	for _, r := range extraForbidden {
		forbidden[r] = true
	}
	for _, r := range val {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s 含非法控制字符", name)
		}
		if forbidden[r] {
			return fmt.Errorf("%s 含不允许的字符 %q", name, r)
		}
	}
	return nil
}

// safeExecName 二进制文件名：纯文件名（不含路径分隔符与穿越）。
func safeExecName(v string) error {
	if !execNameRe.MatchString(v) || v == "." || v == ".." {
		return fmt.Errorf("exec_name 只能是纯文件名（字母/数字/._-，1-64 位），收到 %q", v)
	}
	return nil
}

// safeWorkDir 规范化并校验应用工作目录：绝对、无穿越、且位于允许根之下。
// 返回清洗后的路径供后续拼接使用；空值表示使用默认目录。
func safeWorkDir(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if err := safeText("workdir", v); err != nil {
		return "", err
	}
	// workdir 是 Linux 节点上的 POSIX 路径，必须用 path 包校验；
	// filepath 在 Windows 语义下会把 /data/... 判为非绝对、Clean 会转反斜杠。
	if !path.IsAbs(v) {
		return "", fmt.Errorf("workdir 必须为绝对路径，收到 %q", v)
	}
	if strings.Contains(v, "..") {
		return "", fmt.Errorf("workdir 含非法路径穿越，收到 %q", v)
	}
	clean := path.Clean(v)
	for _, root := range allowedWorkRoots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return clean, nil
		}
	}
	return "", fmt.Errorf("workdir 必须位于 %s 之下，收到 %q",
		strings.Join(allowedWorkRoots, " / "), v)
}

// ValidateCustomConfig 按类型校验自定义应用配置（名称 + 类型化配置）。
// 公开给 API 层做前置校验，SynthRecipe 内部同样强制执行。
func ValidateCustomConfig(app *store.CustomApp) error {
	name := strings.TrimSpace(app.Name)
	if name == "" {
		return fmt.Errorf("应用名称必填")
	}
	if len(name) > 64 {
		return fmt.Errorf("应用名称长度需不超过 64")
	}
	// 名称会写进 systemd Description=，同样禁止引号/说明符/换行
	if err := safeText("应用名称", name, '"', '\''); err != nil {
		return err
	}
	switch app.Type {
	case "docker":
		var c dockerAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return fmt.Errorf("docker 配置解析失败: %w", err)
		}
		if !imageRe.MatchString(strings.TrimSpace(c.Image)) {
			return fmt.Errorf("docker image 格式非法: %q", c.Image)
		}
		if c.Restart != "" && !tokenRe.MatchString(c.Restart) {
			return fmt.Errorf("restart 策略格式非法: %q", c.Restart)
		}
		if c.Network != "" && !tokenRe.MatchString(c.Network) {
			return fmt.Errorf("network 格式非法: %q", c.Network)
		}
		for _, p := range c.Ports {
			if err := safeText("ports", p); err != nil {
				return err
			}
		}
		for _, e := range c.Env {
			if err := safeText("env", e); err != nil {
				return err
			}
		}
		for _, v := range c.Volumes {
			if err := safeText("volumes", v); err != nil {
				return err
			}
		}
		return validateHealth(c.Health)
	case "binary":
		var c binaryAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return fmt.Errorf("二进制配置解析失败: %w", err)
		}
		if err := safeExecName(strings.TrimSpace(c.ExecName)); err != nil {
			return err
		}
		if _, err := safeWorkDir(c.WorkDir); err != nil {
			return err
		}
		if err := safeText("args", c.Args); err != nil {
			return err
		}
		if u := strings.TrimSpace(c.User); u != "" && !userNameRe.MatchString(u) {
			return fmt.Errorf("user 格式非法: %q", c.User)
		}
		return validateHealth(c.Health)
	case "github":
		var c githubAppConfig
		if err := json.Unmarshal([]byte(app.ConfigJSON), &c); err != nil {
			return fmt.Errorf("GitHub 配置解析失败: %w", err)
		}
		if err := safeRepo(c.Repo); err != nil {
			return err
		}
		if b := strings.TrimSpace(c.Branch); b != "" {
			if !branchRe.MatchString(b) || strings.HasPrefix(b, "-") {
				return fmt.Errorf("branch 格式非法: %q", c.Branch)
			}
		}
		// run 会被包进 /bin/sh -c "..."，禁止引号/控制字符/说明符，杜绝单元与 shell 注入
		if err := safeText("run", c.Run, '"', '\''); err != nil {
			return err
		}
		return validateHealth(c.Health)
	default:
		return fmt.Errorf("未知自定义应用类型: %s", app.Type)
	}
}

// safeRepo GitHub 仓库来源：完整 http(s) URL 或 owner/repo 简写；
// 既避免被当作 git 命令行选项（前导 -），也避免拼进加速代理后产生异常地址。
func safeRepo(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("GitHub 应用需指定 repo")
	}
	if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
		return safeText("repo", v)
	}
	if strings.HasPrefix(v, "-") || !repoPathRe.MatchString(v) {
		return fmt.Errorf("repo 需为 https(s) 地址或 owner/repo 形式，收到 %q", v)
	}
	return nil
}

// validateHealth 健康检查配置：命令型逐参数做字符集约束。
func validateHealth(h healthCfg) error {
	if h.Type == "" {
		return nil
	}
	switch h.Type {
	case "http", "tcp":
		if h.Port < 0 || h.Port > 65535 {
			return fmt.Errorf("健康检查端口非法: %d", h.Port)
		}
		if h.Path != "" {
			return safeText("health.path", h.Path)
		}
	case "command":
		if len(h.Cmd) == 0 {
			return fmt.Errorf("command 型健康检查需提供 cmd")
		}
		for _, a := range h.Cmd {
			if err := safeText("health.cmd", a); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("健康检查类型仅支持 http/tcp/command，收到 %q", h.Type)
	}
	return nil
}
