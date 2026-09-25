package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"onecloud-panel/internal/node"
	"onecloud-panel/internal/runner"
	"onecloud-panel/internal/store"
)

// TaskPayload 应用任务参数。
type TaskPayload struct {
	NodeID    int64             `json:"node_id"`
	AppID     string            `json:"app_id"`
	Method    string            `json:"method"`
	PurgeData bool              `json:"purge_data"`
	Vars      map[string]string `json:"vars"`
	Docker    *DockerOverride   `json:"docker,omitempty"` // 容器安装的端口/卷/ENV/重启策略覆盖
}

// RegisterRunnerTasks 把应用生命周期挂到后台任务运行器。
func (m *Manager) RegisterRunnerTasks(r *runner.Runner) {
	r.Register("app_install", m.installTask)
	r.Register("app_uninstall", m.uninstallTask)
	r.RegisterReconciler("app_install", m.installReconcile)
	r.RegisterReconciler("app_uninstall", m.uninstallReconcile)

	// Docker Engine 按需安装
	r.Register("docker_install", m.dockerInstallTask)
	r.RegisterReconciler("docker_install", m.dockerInstallReconcile)

	// 已装 Docker 节点应用镜像加速/仓库配置
	r.Register("docker_apply_config", m.dockerApplyConfigTask)

	// 任务真实终态写审计
	r.OnFinish(m.taskAudit)
}

// DockerTaskPayload Docker 安装任务参数。
type DockerTaskPayload struct {
	NodeID int64 `json:"node_id"`
}

func (m *Manager) dockerInstallTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	var p DockerTaskPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 {
		return fmt.Errorf("任务参数错误")
	}
	return m.InstallDocker(ctx, w, p.NodeID)
}

func (m *Manager) dockerInstallReconcile(ctx context.Context, t *store.BackgroundTask) (runner.Decision, error) {
	var p DockerTaskPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 {
		return runner.Decision{Status: store.TaskFailure, Error: "任务参数错误"}, nil
	}
	n, err := m.store.GetNode(p.NodeID)
	if err != nil {
		return runner.Decision{Status: store.TaskFailure, Error: "节点不存在"}, nil
	}
	if !reachable(n) {
		return runner.Decision{Requeue: true, Note: "节点不可达，稍后重试"}, nil
	}
	ok, ver, _ := m.DockerStatus(ctx, n)
	if ok {
		_ = m.store.SetNodeDockerVersion(n.ID, ver)
		return runner.Decision{Status: store.TaskSuccess,
			Note: "Engine API 可用" + nonEmpty("（", ver, "）")}, nil
	}
	return runner.Decision{Status: store.TaskFailure,
		Error: "对账时 Engine API 仍不可用，请查看任务输出或手动安装"}, nil
}

// dockerApplyConfigTask 为已装 Docker 的节点重写 daemon.json（镜像加速+第三方仓库）并 reload。
func (m *Manager) dockerApplyConfigTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	var p DockerTaskPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil || p.NodeID == 0 {
		return fmt.Errorf("任务参数错误")
	}
	n, err := m.store.GetNode(p.NodeID)
	if err != nil {
		return fmt.Errorf("节点不存在: %w", err)
	}
	ex, err := m.ExecutorFor(n)
	if err != nil {
		return err
	}
	mirrors, insecure := m.mergedDockerConfig(n)
	if err := writeDockerDaemonConfig(ctx, w, ex, mirrors, insecure); err != nil {
		return err
	}
	// 校验 daemon.json（dockerd --validate 仅较新版本支持，失败不阻塞）
	if r, err := ex.Exec(ctx, "sh", "-c", "dockerd --validate 2>/dev/null || true"); err == nil {
		fmt.Fprintln(w, "  配置校验: "+strings.TrimSpace(r.Output))
	}
	fmt.Fprintln(w, "→ 重新加载 Docker 配置")
	if r, err := ex.Exec(ctx, "sh", "-c", "systemctl reload docker 2>/dev/null || systemctl restart docker"); err != nil {
		return fmt.Errorf("重载 docker 失败: %w", err)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("重载 docker 失败（%d）: %s", r.ExitCode, tail(r.Output, 300))
	}
	fmt.Fprintln(w, "✓ Docker 配置已应用")
	return nil
}

func nonEmpty(prefix, s, suffix string) string {
	if s == "" {
		return ""
	}
	return prefix + s + suffix
}

func decodePayload(t *store.BackgroundTask) (*TaskPayload, error) {
	var p TaskPayload
	if err := json.Unmarshal([]byte(t.Payload), &p); err != nil {
		return nil, err
	}
	if p.Method == "" {
		p.Method = "native"
	}
	return &p, nil
}

func (m *Manager) installTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	p, err := decodePayload(t)
	if err != nil {
		return err
	}
	switch p.Method {
	case "native":
		return m.Install(ctx, w, p.NodeID, p.AppID, p.Vars)
	case "docker":
		return m.DockerInstall(ctx, w, p.NodeID, p.AppID, p.Vars, p.Docker)
	default:
		return fmt.Errorf("暂不支持 %s 安装方式", p.Method)
	}
}

func (m *Manager) uninstallTask(ctx context.Context, w io.Writer, t *store.BackgroundTask) error {
	p, err := decodePayload(t)
	if err != nil {
		return err
	}
	switch p.Method {
	case "native":
		return m.Uninstall(ctx, w, p.NodeID, p.AppID, p.PurgeData)
	case "docker":
		return m.DockerUninstall(ctx, w, p.NodeID, p.AppID, p.PurgeData)
	default:
		return fmt.Errorf("暂不支持 %s 卸载方式", p.Method)
	}
}

// installReconcile 面板重启后核查安装任务的节点实际状态。
func (m *Manager) installReconcile(ctx context.Context, t *store.BackgroundTask) (runner.Decision, error) {
	p, err := decodePayload(t)
	if err != nil {
		return runner.Decision{}, err
	}
	switch p.Method {
	case "native":
		return m.nativeReconcile(ctx, p, true)
	case "docker":
		return m.dockerReconcile(ctx, p, true)
	default:
		return runner.Decision{Status: store.TaskFailure, Error: "未知安装方式 " + p.Method}, nil
	}
}

func (m *Manager) uninstallReconcile(ctx context.Context, t *store.BackgroundTask) (runner.Decision, error) {
	p, err := decodePayload(t)
	if err != nil {
		return runner.Decision{}, err
	}
	switch p.Method {
	case "native":
		return m.nativeReconcile(ctx, p, false)
	case "docker":
		return m.dockerReconcile(ctx, p, false)
	default:
		return runner.Decision{Status: store.TaskFailure, Error: "未知安装方式 " + p.Method}, nil
	}
}

// nativeReconcile 直装任务对账：节点不可达→重排；install 时服务 active+健康→成功；
// uninstall 时单元不存在→成功。
func (m *Manager) nativeReconcile(ctx context.Context, p *TaskPayload, isInstall bool) (runner.Decision, error) {
	n, err := m.nodes.Get(p.NodeID)
	if err != nil {
		return runner.Decision{Status: store.TaskFailure, Error: "对账时节点记录不存在"}, nil
	}
	if !reachable(n) {
		return runner.Decision{Requeue: true, Note: "节点不可达，待恢复后对账"}, nil
	}
	ex, err := m.ExecutorFor(n)
	if err != nil {
		return runner.Decision{Requeue: true, Note: err.Error()}, nil
	}

	recipe, ok := m.recipes.Get(p.AppID)
	if !ok {
		return runner.Decision{Status: store.TaskFailure, Error: "配方不存在"}, nil
	}
	unit := recipe.Native.UnitName

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if isInstall {
		active, out := isActive(rctx, ex, unit)
		if active {
			// 补建崩溃窗口（start 后、CreateInstallation 前）丢失的安装记录
			if err := m.store.UpsertInstallation(&store.AppInstallation{
				NodeID: p.NodeID, AppID: p.AppID, Method: "native",
				Status: "installed", ServiceName: unit,
				Params: paramsJSON(p.Vars, nil),
			}); err != nil {
				return runner.Decision{Status: store.TaskFailure,
					Error: "补建安装记录失败: " + err.Error()}, nil
			}
			return runner.Decision{Status: store.TaskSuccess,
				Note: "节点服务 " + unit + " 为 active，判定安装成功"}, nil
		}
		// 服务存在但非 active：判定失败，让管理员看输出
		if strings.Contains(out, "inactive") || strings.Contains(out, "failed") {
			return runner.Decision{Status: store.TaskFailure,
				Error: "对账时服务未运行: " + out}, nil
		}
		return runner.Decision{Status: store.TaskFailure, Error: "单元状态未知: " + out}, nil
	}

	// 卸载任务对账：单元仍在 → 重排执行卸载；不存在 → 成功
	if active, _ := isActive(rctx, ex, unit); active {
		return runner.Decision{Requeue: true, Note: "服务仍在，重新执行卸载"}, nil
	}
	exists, _ := ex.Exists("/etc/systemd/system/" + unit)
	if exists {
		return runner.Decision{Requeue: true, Note: "单元文件仍存在"}, nil
	}
	return runner.Decision{Status: store.TaskSuccess, Note: "单元已不存在，判定卸载成功"}, nil
}

// dockerReconcile 容器任务对账：install 时容器在且运行→成功；
// uninstall 时容器不存在→成功、仍存在→重排。
func (m *Manager) dockerReconcile(ctx context.Context, p *TaskPayload, isInstall bool) (runner.Decision, error) {
	n, err := m.nodes.Get(p.NodeID)
	if err != nil {
		return runner.Decision{Status: store.TaskFailure, Error: "对账时节点记录不存在"}, nil
	}
	if !reachable(n) {
		return runner.Decision{Requeue: true, Note: "节点不可达，待恢复后对账"}, nil
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return runner.Decision{Requeue: true, Note: err.Error()}, nil
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cname := containerName(p.AppID)
	cid, _ := findContainer(rctx, eng, cname)
	if isInstall {
		if cid == "" {
			return runner.Decision{Status: store.TaskFailure,
				Error: "对账时容器 " + cname + " 不存在"}, nil
		}
		if running, _ := containerRunning(rctx, eng, cid); running {
			// 补建崩溃窗口丢失的安装记录
			if err := m.store.UpsertInstallation(&store.AppInstallation{
				NodeID: p.NodeID, AppID: p.AppID, Method: "docker",
				Status: "installed", ContainerID: cid, ContainerName: cname,
				Params: paramsJSON(p.Vars, p.Docker),
			}); err != nil {
				return runner.Decision{Status: store.TaskFailure,
					Error: "补建安装记录失败: " + err.Error()}, nil
			}
			return runner.Decision{Status: store.TaskSuccess,
				Note: "容器 " + cname + " 运行中，判定安装成功"}, nil
		}
		return runner.Decision{Status: store.TaskFailure,
			Error: "对账时容器 " + cname + " 未运行"}, nil
	}
	if cid == "" {
		return runner.Decision{Status: store.TaskSuccess, Note: "容器已不存在，判定卸载成功"}, nil
	}
	return runner.Decision{Requeue: true, Note: "容器仍存在，重新执行卸载"}, nil
}

func reachable(n *store.Node) bool {
	if n.Mode == "local" {
		return true
	}
	if n.Address == "" {
		return false
	}
	return node.CheckReachability(n.Address, 3*time.Second)
}
