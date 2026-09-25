// Package scriptsvc SH 脚本管理服务：脚本落盘、按需开机自启与远程执行。
package scriptsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/store"
)

const (
	scriptDir     = "/etc/onecloud-scripts" // 脚本落盘目录
	unitDir       = "/etc/systemd/system"   // systemd 单元目录
	unitPrefix    = "ocp-script-"           // 自启单元名前缀
	maxScriptSize = 256 << 10               // 脚本内容上限 256KB
)

// Manager 脚本管理服务。
type Manager struct {
	store   *store.Store
	execFor func(*store.Node) (executor.Executor, error)
	audit   *audit.Service
}

// New 创建脚本管理服务；execFor 通常注入 apps.Manager.ExecutorFor。
func New(s *store.Store, execFor func(*store.Node) (executor.Executor, error), a *audit.Service) *Manager {
	return &Manager{store: s, execFor: execFor, audit: a}
}

// ScriptPath 脚本在节点上的落盘路径。
func ScriptPath(id int64) string { return scriptDir + "/" + strconv.FormatInt(id, 10) + ".sh" }

// UnitName 脚本的自启 systemd 单元名。
func UnitName(id int64) string { return unitPrefix + strconv.FormatInt(id, 10) + ".service" }

// ValidateContent 校验脚本内容（非空、大小上限、禁 NUL）。
func ValidateContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("脚本内容不能为空")
	}
	if len(content) > maxScriptSize {
		return fmt.Errorf("脚本过大（上限 %dKB）", maxScriptSize/1024)
	}
	if strings.ContainsRune(content, 0) {
		return fmt.Errorf("脚本包含非法的 NUL 字符")
	}
	return nil
}

// ContentHash 脚本内容 SHA-256 摘要，用于部署记录比对。
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Deploy 把脚本写入节点并按需配置开机自启；同时更新部署记录。
func (m *Manager) Deploy(ctx context.Context, w io.Writer, n *store.Node, sc *store.ShellScript, autoStart bool) error {
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	path := ScriptPath(sc.ID)

	fmt.Fprintln(w, "→ 写入脚本 "+path)
	if r, err := ex.Exec(ctx, "mkdir", "-p", scriptDir); err != nil {
		return fmt.Errorf("创建脚本目录失败: %w", err)
	} else if err := execOK(r, nil, "创建脚本目录"); err != nil {
		return err
	}
	if err := ex.WriteFile(path, []byte(sc.Content)); err != nil {
		return fmt.Errorf("写入脚本失败: %w", err)
	}
	if r, err := ex.Exec(ctx, "chmod", "750", path); err != nil {
		return fmt.Errorf("设置脚本权限失败: %w", err)
	} else if err := execOK(r, nil, "设置脚本权限"); err != nil {
		return err
	}

	unit := UnitName(sc.ID)
	unitPath := unitDir + "/" + unit
	if autoStart {
		fmt.Fprintln(w, "→ 配置开机自启（"+unit+"）")
		if err := ex.WriteFile(unitPath, []byte(unitFile(sc))); err != nil {
			return fmt.Errorf("写入自启单元失败: %w", err)
		}
		if r, err := ex.Exec(ctx, "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("daemon-reload 失败: %w", err)
		} else if err := execOK(r, nil, "daemon-reload"); err != nil {
			return err
		}
		if r, err := ex.Exec(ctx, "systemctl", "enable", unit); err != nil {
			return fmt.Errorf("设置自启失败: %w", err)
		} else if err := execOK(r, nil, "设置自启"); err != nil {
			return err
		}
	} else if ok, _ := ex.Exists(unitPath); ok {
		// 关闭自启：移除已有单元
		fmt.Fprintln(w, "→ 移除已有自启单元")
		_, _ = ex.Exec(ctx, "systemctl", "disable", unit)
		if _, err := ex.Exec(ctx, "rm", "-f", unitPath); err != nil {
			return fmt.Errorf("删除自启单元失败: %w", err)
		}
		_, _ = ex.Exec(ctx, "systemctl", "daemon-reload")
	}

	if err := m.store.UpsertShellScriptDeployment(&store.ShellScriptDeployment{
		ScriptID: sc.ID, NodeID: n.ID, AutoStart: autoStart, ContentHash: ContentHash(sc.Content),
	}); err != nil {
		return fmt.Errorf("记录部署状态失败: %w", err)
	}
	fmt.Fprintln(w, "✓ 部署完成（开机自启: "+onOff(autoStart)+"）")
	return nil
}

// Run 部署（按已保存的自启设置）后在节点上执行脚本，实时输出。
func (m *Manager) Run(ctx context.Context, w io.Writer, n *store.Node, sc *store.ShellScript) error {
	auto := false
	if d, err := m.store.GetShellScriptDeployment(sc.ID, n.ID); err == nil && d != nil {
		auto = d.AutoStart
	}
	if err := m.Deploy(ctx, w, n, sc, auto); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "→ 执行脚本")
	code, err := ex.ExecStream(ctx, w, "sh", ScriptPath(sc.ID))
	if err != nil {
		return fmt.Errorf("脚本执行失败（退出码 %d）", code)
	}
	fmt.Fprintln(w, "✓ 脚本执行完成")
	return nil
}

// CleanupNode 清理脚本在节点上的部署（自启单元 + 脚本文件），尽力而为。
// 删除脚本前对每个部署过该脚本的节点调用；部署记录由 DeleteShellScript 级联删除。
func (m *Manager) CleanupNode(ctx context.Context, n *store.Node, scriptID int64) error {
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	unit := UnitName(scriptID)
	unitPath := unitDir + "/" + unit
	if ok, _ := ex.Exists(unitPath); ok {
		_, _ = ex.Exec(ctx, "systemctl", "disable", unit)
		if _, err := ex.Exec(ctx, "rm", "-f", unitPath); err != nil {
			return fmt.Errorf("删除自启单元失败: %w", err)
		}
		_, _ = ex.Exec(ctx, "systemctl", "daemon-reload")
	}
	if _, err := ex.Exec(ctx, "rm", "-f", ScriptPath(scriptID)); err != nil {
		return fmt.Errorf("删除脚本文件失败: %w", err)
	}
	return nil
}

// RunPayload script_run 任务参数。
type RunPayload struct {
	NodeID   int64 `json:"node_id"`
	ScriptID int64 `json:"script_id"`
}

// RegisterRunnerTasks 把脚本运行任务挂到后台任务运行器。
func (m *Manager) RegisterRunnerTasks(r *runner.Runner) {
	r.Register("script_run", m.runTask)
	r.OnFinish(m.taskAudit)
}

func (m *Manager) runTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	var p RunPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 || p.ScriptID == 0 {
		return fmt.Errorf("任务参数错误")
	}
	sc, err := m.store.GetShellScript(p.ScriptID)
	if err != nil {
		return fmt.Errorf("脚本不存在")
	}
	n, err := m.store.GetNode(p.NodeID)
	if err != nil {
		return fmt.Errorf("节点不存在")
	}
	fmt.Fprintf(w, "脚本: %s\n节点: %s\n\n", sc.Name, n.Name)
	return m.Run(ctx, w, n, sc)
}

// taskAudit 任务真实终态写审计。
func (m *Manager) taskAudit(t *store.BackgroundTask, status string) {
	if m.audit == nil || t.Type != "script_run" {
		return
	}
	var p RunPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil {
		return
	}
	result := audit.ResultSuccess
	if status != store.TaskSuccess {
		result = audit.ResultFailure
	}
	m.audit.RecordTask(t.CreatedBy, "app", "script_run", "script",
		strconv.FormatInt(p.ScriptID, 10), result,
		"node_id="+strconv.FormatInt(p.NodeID, 10))
}

// unitFile 生成 oneshot 自启单元内容。
func unitFile(sc *store.ShellScript) string {
	return fmt.Sprintf(`[Unit]
Description=OneCloud script: %s
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/bin/sh %s

[Install]
WantedBy=multi-user.target
`, unitDesc(sc.Name), ScriptPath(sc.ID))
}

// unitDesc 过滤单元描述中的换行/引号/百分号，避免破坏 unit 文件结构。
func unitDesc(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '"', '\'', '%':
			return ' '
		}
		return r
	}, strings.TrimSpace(name))
}

// execOK 统一处理命令执行结果。
func execOK(r *executor.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s失败: %w", what, err)
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("%s失败（退出码 %d）: %s", what, r.ExitCode, tail(r.Output, 300))
	}
	return nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}
