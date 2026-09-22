package apps

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/store"
)

// Install 直装安装（幂等：已安装直接拒绝）。
func (m *Manager) Install(ctx context.Context, w io.Writer,
	nodeID int64, recipeID string, vars map[string]string) error {

	n, recipe, rendered, ex, err := m.prepare(nodeID, recipeID, "native", vars)
	if err != nil {
		return err
	}
	if err := validateVariables(recipe, vars); err != nil {
		return err
	}
	ns := rendered.Recipe.Native

	// 防重复
	if in, err := m.store.GetInstallation(nodeID, recipeID); err == nil && in != nil {
		return fmt.Errorf("该应用已安装（%s），请先卸载", in.Status)
	}

	// 执行安装步骤
	if err := m.runSteps(ctx, w, ex, ns.InstallSteps); err != nil {
		return err
	}

	// 安装 systemd 单元
	unitPath := "/etc/systemd/system/" + ns.UnitName
	fmt.Fprintf(w, "→ 写入 systemd 单元 %s\n", unitPath)
	if err := ex.WriteFile(unitPath, []byte(strings.TrimSpace(ns.UnitTemplate)+"\n")); err != nil {
		return fmt.Errorf("写入单元失败: %w", err)
	}
	if _, err := ex.Exec(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := ex.Exec(ctx, "systemctl", "enable", ns.UnitName); err != nil {
		return err
	}
	if _, err := ex.Exec(ctx, "systemctl", "start", ns.UnitName); err != nil {
		return err
	}

	// 健康检查（最多等待 20s）
	if rendered.Recipe.Healthcheck != nil {
		if err := m.waitHealthy(ctx, w, ex, rendered.Recipe.Healthcheck); err != nil {
			if _, cerr := m.store.CreateInstallation(&store.AppInstallation{
				NodeID: nodeID, AppID: recipeID, Method: "native",
				Status: "error", ServiceName: ns.UnitName,
				Params: paramsJSON(vars),
			}); cerr != nil {
				fmt.Fprintf(w, "  警告: 异常安装记录写入失败: %v\n", cerr)
			}
			return err
		}
	} else {
		// 无声明：以 is-active 为准
		if ok, _ := isActive(ctx, ex, ns.UnitName); !ok {
			return fmt.Errorf("服务 %s 未处于 active", ns.UnitName)
		}
	}

	params := paramsJSON(vars)
	if _, err := m.store.CreateInstallation(&store.AppInstallation{
		NodeID: nodeID, AppID: recipeID, Method: "native",
		Status: "installed", ServiceName: ns.UnitName, Params: params,
	}); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ %s 安装完成（节点：%s）\n", recipe.Name, n.Name)
	return nil
}

// Uninstall 直装卸载；purgeData=true 时清除 Volumes 声明的数据目录。
func (m *Manager) Uninstall(ctx context.Context, w io.Writer,
	nodeID int64, recipeID string, purgeData bool) error {

	in, err := m.store.GetInstallation(nodeID, recipeID)
	if err != nil {
		return fmt.Errorf("应用未安装")
	}
	n, recipe, _, ex, err := m.prepare(nodeID, recipeID, "native", installationVars(in))
	if err != nil {
		return err
	}
	unit := in.ServiceName
	if unit == "" {
		unit = recipe.Native.UnitName
	}

	fmt.Fprintf(w, "→ 停止并禁用 %s\n", unit)
	_, _ = ex.Exec(ctx, "systemctl", "stop", unit)
	_, _ = ex.Exec(ctx, "systemctl", "disable", unit)

	if err := m.runSteps(ctx, w, ex, recipe.Native.UninstallSteps); err != nil {
		return err
	}

	_, _ = ex.Exec(ctx, "rm", "-f", "/etc/systemd/system/"+unit)
	_, _ = ex.Exec(ctx, "systemctl", "daemon-reload")

	if purgeData && len(recipe.Volumes) > 0 {
		for _, v := range recipe.Volumes {
			dir := strings.SplitN(v, ":", 2)[0]
			fmt.Fprintf(w, "→ 清除数据目录 %s\n", dir)
			_, _ = ex.Exec(ctx, "rm", "-rf", dir)
		}
	} else if !purgeData {
		fmt.Fprintln(w, "→ 已保留数据目录")
	}

	if err := m.store.DeleteInstallation(nodeID, recipeID); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ %s 已卸载（节点：%s）\n", recipe.Name, n.Name)
	return nil
}

// ServiceAction start/stop/restart（按安装方式分发）。
func (m *Manager) ServiceAction(ctx context.Context, nodeID int64, recipeID, action string) (string, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return "", fmt.Errorf("非法动作")
	}
	in, err := m.store.GetInstallation(nodeID, recipeID)
	if err != nil {
		return "", fmt.Errorf("应用未安装")
	}
	if in.Method == "docker" {
		return m.dockerServiceAction(ctx, nodeID, in, action)
	}
	return m.nativeServiceAction(ctx, nodeID, recipeID, in, action)
}

func (m *Manager) nativeServiceAction(ctx context.Context, nodeID int64, recipeID string,
	in *store.AppInstallation, action string) (string, error) {
	_, _, _, ex, err := m.prepare(nodeID, recipeID, "native", installationVars(in))
	if err != nil {
		return "", err
	}
	r, err := ex.Exec(ctx, "systemctl", action, in.ServiceName)
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("%s 失败: %s", action, strings.TrimSpace(r.Output))
	}
	return in.ServiceName, nil
}

// Status 返回运行状态与健康检查结果（按安装方式分发）。
func (m *Manager) Status(ctx context.Context, nodeID int64, recipeID string) (map[string]any, error) {
	in, err := m.store.GetInstallation(nodeID, recipeID)
	if err != nil {
		return nil, fmt.Errorf("应用未安装")
	}
	if in.Method == "docker" {
		return m.dockerStatus(ctx, nodeID, recipeID, in)
	}
	return m.nativeStatus(ctx, nodeID, recipeID, in)
}

func (m *Manager) nativeStatus(ctx context.Context, nodeID int64, recipeID string,
	in *store.AppInstallation) (map[string]any, error) {
	_, recipe, _, ex, err := m.prepare(nodeID, recipeID, "native", installationVars(in))
	if err != nil {
		return nil, err
	}
	active, out := isActive(ctx, ex, in.ServiceName)
	res := map[string]any{
		"active":       active,
		"systemctl":    strings.TrimSpace(out),
		"service_name": in.ServiceName,
		"status":       in.Status,
		"method":       "native",
	}
	if recipe.Healthcheck != nil {
		ok, detail := m.probe(ctx, ex, recipe.Healthcheck)
		res["healthy"] = ok
		res["health_detail"] = detail
	}
	if active && recipeID == "wireguard" {
		m.wireguardStatus(ctx, ex, in, res)
	}
	return res, nil
}

// Journal 读取日志（按安装方式分发）。
func (m *Manager) Journal(ctx context.Context, nodeID int64, recipeID string, lines int) (string, error) {
	in, err := m.store.GetInstallation(nodeID, recipeID)
	if err != nil {
		return "", fmt.Errorf("应用未安装")
	}
	if in.Method == "docker" {
		return m.dockerJournal(ctx, nodeID, in, lines)
	}
	return m.nativeJournal(ctx, nodeID, in, lines)
}

func (m *Manager) nativeJournal(ctx context.Context, nodeID int64,
	in *store.AppInstallation, lines int) (string, error) {
	_, _, _, ex, err := m.prepare(nodeID, in.AppID, "native", installationVars(in))
	if err != nil {
		return "", err
	}
	r, err := ex.Exec(ctx, "journalctl", "--no-pager", "-u", in.ServiceName,
		"-n", strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	return r.Output, nil
}

// ---- 内部 ----

func (m *Manager) prepare(nodeID int64, recipeID, method string, vars map[string]string) (
	*store.Node, *recipes.Recipe, *recipes.RenderedRecipe, executor.Executor, error) {

	n, err := m.nodes.Get(nodeID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("节点不存在")
	}
	recipe, ok := m.recipes.Get(recipeID)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("配方不存在")
	}
	ex, err := m.ExecutorFor(n)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	{
		ok, reason := recipe.Supports(method, n.Arch)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%s", reason)
		}
	}
	if vars == nil {
		vars = map[string]string{}
	}
	// 填充变量默认值；必填校验仅在安装路径（checkRequiredVars）执行，
	// 状态/卸载等路径允许空值以完成模板渲染。
	for _, v := range recipe.Variables {
		if _, exists := vars[v.Key]; !exists {
			vars[v.Key] = v.Default
		}
	}
	data := recipes.RenderData{
		Vars: vars,
		Node: recipes.NodeFactsForArch(n.Arch, n.Hostname),
	}
	rendered, err := recipe.Render(data)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return n, recipe, rendered, ex, nil
}

func (m *Manager) runSteps(ctx context.Context, w io.Writer, ex executor.Executor, steps []recipes.Step) error {
	proxy := m.githubProxy()
	for _, s := range steps {
		label := s.Name
		if label == "" {
			label = "步骤"
		}
		fmt.Fprintf(w, "→ %s\n", label)
		if err := execOneStep(ctx, w, ex, s, proxy); err != nil {
			return fmt.Errorf("步骤 %q 失败: %w", label, err)
		}
	}
	return nil
}

// execLong 优先使用执行器的长任务能力（executor.LongRunner），
// 未实现时回落为普通 Exec。用于 apt/dpkg/systemctl 等可能耗时数分钟的步骤。
func execLong(ctx context.Context, ex executor.Executor, name string, args ...string) (*executor.Result, error) {
	if lr, ok := ex.(executor.LongRunner); ok {
		return lr.ExecLong(ctx, name, args...)
	}
	return ex.Exec(ctx, name, args...)
}

func execOneStep(ctx context.Context, w io.Writer, ex executor.Executor, s recipes.Step, proxy string) error {
	switch {
	case len(s.Apt) > 0:
		// 先更新索引（轻量提示），再非推荐安装
		if _, err := execLong(ctx, ex, "apt-get", "update", "-qq"); err != nil {
			fmt.Fprintf(w, "  apt update 警告: %v\n", err)
		}
		args := append([]string{"install", "-y", "--no-install-recommends"}, s.Apt...)
		r, err := execLong(ctx, ex, "apt-get", args...)
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("%s", strings.TrimSpace(r.Output))
		}
	case s.Download != nil:
		dl, ok := ex.(executor.Downloader)
		if !ok {
			return fmt.Errorf("该节点执行器不支持下载")
		}
		if err := mkdirAll(ctx, ex, filepath.Dir(s.Download.Dest)); err != nil {
			return err
		}
		mode := osMode(s.Download.Mode, 0o755)
		return dl.Download(ctx, withProxy(proxy, s.Download.URL), s.Download.Dest, mode, "", w)
	case len(s.Mkdir) > 0:
		for _, d := range s.Mkdir {
			if err := mkdirAll(ctx, ex, d); err != nil {
				return err
			}
		}
	case s.Write != nil:
		if err := mkdirAll(ctx, ex, filepath.Dir(s.Write.Path)); err != nil {
			return err
		}
		mode := osMode(s.Write.Mode, 0o644)
		if err := ex.WriteFile(s.Write.Path, []byte(s.Write.Content)); err != nil {
			return err
		}
		_, _ = ex.Exec(ctx, "chmod", strconv.FormatUint(uint64(mode.Perm()), 8), s.Write.Path)
	case s.Exec != nil:
		r, err := execLong(ctx, ex, s.Exec.Command, s.Exec.Args...)
		if err != nil {
			return err
		}
		if r.Output != "" {
			fmt.Fprint(w, indentLines(r.Output))
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("退出码 %d", r.ExitCode)
		}
	case s.Systemctl != nil:
		r, err := execLong(ctx, ex, "systemctl", s.Systemctl.Action, s.Systemctl.Unit)
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("%s", strings.TrimSpace(r.Output))
		}
	}
	return nil
}

func mkdirAll(ctx context.Context, ex executor.Executor, dir string) error {
	if dir == "" || dir == "/" {
		return nil
	}
	r, err := ex.Exec(ctx, "mkdir", "-p", dir)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("mkdir %s: %s", dir, strings.TrimSpace(r.Output))
	}
	return nil
}

func isActive(ctx context.Context, ex executor.Executor, unit string) (bool, string) {
	r, err := ex.Exec(ctx, "systemctl", "is-active", unit)
	if err != nil {
		return false, err.Error()
	}
	return strings.TrimSpace(r.Output) == "active", r.Output
}

func (m *Manager) probe(ctx context.Context, ex executor.Executor, hc *recipes.Healthcheck) (bool, string) {
	if hc.Type == "command" {
		r, err := ex.Exec(ctx, hc.Command[0], hc.Command[1:]...)
		if err != nil {
			return false, err.Error()
		}
		return r.ExitCode == 0, strings.TrimSpace(r.Output)
	}
	checker, ok := ex.(executor.HealthChecker)
	if !ok {
		return false, "执行器不支持健康检查"
	}
	return checker.Healthcheck(ctx, hc.Type, hc.Port, hc.Path)
}

func (m *Manager) waitHealthy(ctx context.Context, w io.Writer,
	ex executor.Executor, hc *recipes.Healthcheck) error {
	for i := 0; i < 10; i++ {
		if ok, detail := m.probe(ctx, ex, hc); ok {
			fmt.Fprintln(w, "✓ 健康检查通过")
			return nil
		} else {
			fmt.Fprintf(w, "  等待健康检查… (%s)\n", detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("健康检查未通过")
}
