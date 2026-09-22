// Package apps 应用生命周期管理（直装与容器两种方式）。
package apps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"onecloud-panel/internal/agentclient"
	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
)

// Manager 应用管理器。
type Manager struct {
	store      *store.Store
	nodes      *node.Service
	recipes    *recipes.Registry
	box        *secretbox.Box
	audit      *audit.Service
	execHook   func(*store.Node) (executor.Executor, error) // 仅测试用
	engineHook func(*store.Node) (*docker.Engine, error)    // 仅测试用
	binaryDir  string                                       // 自定义二进制目录
}

// New 创建管理器。
func New(s *store.Store, nodes *node.Service, reg *recipes.Registry,
	box *secretbox.Box, auditSvc *audit.Service) *Manager {
	return &Manager{store: s, nodes: nodes, recipes: reg, box: box, audit: auditSvc}
}

// SetExecutorHook 注入执行器工厂（测试使用）。
func (m *Manager) SetExecutorHook(h func(*store.Node) (executor.Executor, error)) {
	m.execHook = h
}

// SetEngineHook 注入 Docker Engine 工厂（测试使用）。
func (m *Manager) SetEngineHook(h func(*store.Node) (*docker.Engine, error)) {
	m.engineHook = h
}

// ExecutorFor 返回节点对应的执行器（本机直执行 / 远程走 Agent）。
func (m *Manager) ExecutorFor(n *store.Node) (executor.Executor, error) {
	if m.execHook != nil {
		return m.execHook(n)
	}
	if n.Mode == "local" {
		return executor.NewLocal(nil), nil
	}
	ac, err := m.nodeAgentClient(n)
	if err != nil {
		return nil, err
	}
	return ac, nil
}

// InfoFor 返回节点实时主机信息（本机直接采集 / 远程 GET Agent /v1/info）。
func (m *Manager) InfoFor(ctx context.Context, n *store.Node) (*system.HostInfo, error) {
	if n.Mode == "local" {
		return system.Collect()
	}
	ac, err := m.nodeAgentClient(n)
	if err != nil {
		return nil, err
	}
	return ac.Info(ctx)
}

// nodeAgentClient 构造远程节点 Agent 客户端。
func (m *Manager) nodeAgentClient(n *store.Node) (*agentclient.Client, error) {
	if n.Address == "" {
		return nil, errors.New("节点缺少 Agent 地址")
	}
	enc, err := m.store.GetNodeTokenEncrypted(n.ID)
	if err != nil || enc == "" {
		return nil, fmt.Errorf("节点 Token 不可用: %w", err)
	}
	token, err := m.box.Open(enc)
	if err != nil {
		return nil, fmt.Errorf("节点 Token 解密失败: %w", err)
	}
	return agentclient.New(n.Address, token), nil
}

// EngineFor 返回节点 Docker Engine 客户端（本机 unix socket / Agent 隧道）。
func (m *Manager) EngineFor(n *store.Node) (*docker.Engine, error) {
	if m.engineHook != nil {
		return m.engineHook(n)
	}
	if n.Mode == "local" {
		// 不设客户端超时：拉取镜像等流式操作由调用方 context 控制生命周期
		return docker.NewEngineWithClient(docker.NewLocalClient("", 0)), nil
	}
	ac, err := m.nodeAgentClient(n)
	if err != nil {
		return nil, err
	}
	return docker.NewEngine(func(req *http.Request) (*http.Response, error) {
		return ac.Tunnel(req.Context(), req.Method, req.URL.Path,
			req.URL.Query(), req.Body, req.Header.Get("Content-Type"))
	}), nil
}

// DockerStatus 探测节点 Docker：available 与版本。
func (m *Manager) DockerStatus(ctx context.Context, n *store.Node) (bool, string, error) {
	eng, err := m.EngineFor(n)
	if err != nil {
		return false, "", err
	}
	if err := eng.Ping(ctx); err != nil {
		return false, "", nil
	}
	v, err := eng.Version(ctx)
	if err != nil {
		return true, "", nil
	}
	return true, v.Version, nil
}

// githubProxy 读取 GitHub 加速代理设置（形如 https://gh-proxy.com），空为直连。
func (m *Manager) githubProxy() string {
	v, _, _ := m.store.GetSetting("github_proxy")
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

// withProxy 为 GitHub 系 URL 拼接加速代理前缀。
func withProxy(proxy, rawURL string) string {
	if proxy == "" {
		return rawURL
	}
	switch {
	case strings.HasPrefix(rawURL, "https://github.com/"),
		strings.HasPrefix(rawURL, "https://raw.githubusercontent.com/"),
		strings.HasPrefix(rawURL, "https://codeload.github.com/"),
		strings.HasPrefix(rawURL, "https://objects.githubusercontent.com/"):
		return proxy + "/" + rawURL
	}
	return rawURL
}

// Store/Registry 访问器（供 API 层）。
func (m *Manager) Store() *store.Store { return m.store }

// Registry 暴露配方注册表。
func (m *Manager) Registry() *recipes.Registry { return m.recipes }
