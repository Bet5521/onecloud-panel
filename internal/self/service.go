// Package self 管理面板自身：状态信息、日志读取与异步重启。
package self

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/version"
)

// TaskTypePanelRestart 面板重启任务类型。
const TaskTypePanelRestart = "panel_restart"

// Service 面板自身管理服务。
type Service struct {
	store     *store.Store
	ex        executor.Executor
	dataDir   string
	listen    string
	unitName  string
	startedAt time.Time

	// launch 触发延迟重启（测试可替换）。
	launch func(unit string) error
}

// New 创建服务。
func New(s *store.Store, ex executor.Executor, dataDir, listen, unitName string) *Service {
	return &Service{
		store:     s,
		ex:        ex,
		dataDir:   dataDir,
		listen:    listen,
		unitName:  unitName,
		startedAt: time.Now(),
		launch:    defaultLaunch,
	}
}

// SetLaunch 注入重启触发函数（测试使用）。
func (s *Service) SetLaunch(f func(unit string) error) {
	s.launch = f
}

// Status 返回面板运行状态与版本/路径信息。
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	binPath, _ := os.Executable()
	res := map[string]any{
		"version":        version.Version,
		"commit":         version.Commit,
		"build_date":     version.BuildDate,
		"binary":         binPath,
		"data_dir":       s.dataDir,
		"listen":         s.listen,
		"unit":           s.unitName,
		"started_at":     s.startedAt.Unix(),
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
	}

	r, err := s.ex.Exec(ctx, "systemctl", "is-active", s.unitName)
	if err != nil {
		res["systemd_active"] = false
		res["systemd_detail"] = err.Error()
	} else {
		state := strings.TrimSpace(r.Output)
		res["systemd_active"] = state == "active"
		res["systemd_state"] = state
	}

	if nodes, err := s.store.ListNodes(); err == nil {
		res["node_count"] = len(nodes)
	}
	var instCount int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM app_installations`).Scan(&instCount); err == nil {
		res["installation_count"] = instCount
	}
	return res, nil
}

// Journal 读取面板服务日志末尾 N 行。
func (s *Service) Journal(ctx context.Context, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	r, err := s.ex.Exec(ctx, "journalctl", "--no-pager", "-u", s.unitName,
		"-n", strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("%s", strings.TrimSpace(r.Output))
	}
	return r.Output, nil
}

// Restart 创建重启任务并触发延迟重启（面板进程将被 systemd 重新拉起）。
func (s *Service) Restart(ctx context.Context, userID int64) (int64, error) {
	if err := s.checkSystemd(ctx); err != nil {
		return 0, err
	}
	task := &store.BackgroundTask{
		Type:    TaskTypePanelRestart,
		Status:  store.TaskRunning,
		Payload: "{}",
	}
	if userID > 0 {
		task.CreatedBy = &userID
	}
	id, err := s.store.CreateTask(task)
	if err != nil {
		return 0, err
	}
	if err := s.launch(s.unitName); err != nil {
		_ = s.store.FinishTask(id, store.TaskFailure, err.Error())
		return 0, err
	}
	_ = s.store.AppendTaskOutput(id, "已触发延迟重启，等待 systemd 重新拉起…\n")
	return id, nil
}

// FinalizeBoot 启动时把上次中断的 panel_restart running 任务收口为成功。
func (s *Service) FinalizeBoot() (int, error) {
	running, err := s.store.ListRunningTasks()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range running {
		if t.Type != TaskTypePanelRestart {
			continue
		}
		if err := s.store.AppendTaskOutput(t.ID, "面板已成功重启\n"); err != nil {
			return n, err
		}
		if err := s.store.FinishTask(t.ID, store.TaskSuccess, ""); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) checkSystemd(ctx context.Context) error {
	r, err := s.ex.Exec(ctx, "systemctl", "is-active", s.unitName)
	if err != nil {
		return fmt.Errorf("无法访问 systemd，面板可能未以系统服务方式运行: %w", err)
	}
	state := strings.TrimSpace(r.Output)
	if state != "active" {
		return fmt.Errorf("面板服务 %s 当前为 %q，已取消重启", s.unitName, state)
	}
	return nil
}
