// Package docker 提供 Docker Engine API 的极简客户端与本机检测。
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultSocket Docker Engine 默认 unix socket。
const DefaultSocket = "/var/run/docker.sock"

// VersionInfo Engine 版本信息。
type VersionInfo struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	Arch          string `json:"Arch"`
	OS            string `json:"Os"`
}

// Requester 发起 HTTP 请求（本地 unix / Agent 隧道各自实现）。
type Requester func(*http.Request) (*http.Response, error)

// Engine Engine API 客户端。
type Engine struct {
	do Requester
}

// NewEngine 创建客户端。
func NewEngine(do Requester) *Engine { return &Engine{do: do} }

// localClients 按 socket 缓存共享客户端（见 LocalHTTPClient）。
var (
	localMu      sync.Mutex
	localClients = map[string]*http.Client{}
)

// LocalHTTPClient 返回按 socket 复用的 HTTP 客户端（进程内单例）。
//
// **必须复用，不能每次新建 Transport。** http.Transport 会为每条空闲连接常驻
// readLoop/writeLoop 两个 goroutine，而 goroutine 是 GC 根、又持有 Transport 引用，
// 于是「用完就丢」的 Transport **永远不会被回收**：连接不关、goroutine 不退、
// 收发缓冲区不释放；且零值 Transport 的 IdleConnTimeout 是 0（空闲连接永不超时），
// 连兜底回收都没有。
//
// 历史实现每次 DetectLocal / EngineFor / docker 代理都新建一个 Transport：
//   - 面板：每 30s 一轮本机节点刷新 + 应用状态探测，每个节点各建一个；
//   - Agent：每 30s 一次心跳各建一个。
//
// 结果就是内存、goroutine、fd 随运行时间线性增长——这正是「面板和客户端都内存泄漏」。
func LocalHTTPClient(socket string) *http.Client {
	if socket == "" {
		socket = DefaultSocket
	}
	localMu.Lock()
	defer localMu.Unlock()
	if c, ok := localClients[socket]; ok {
		return c
	}
	c := newLocalClient(socket, 0)
	localClients[socket] = c
	return c
}

// NewLocal 本机 Engine 客户端（走共享连接池）。
func NewLocal(socket string) *Engine {
	return NewEngineWithClient(LocalHTTPClient(socket))
}

// NewLocalClient 新建**独立**的 unix socket 客户端；timeout<=0 表示不限制（流式）。
//
// 独立客户端不会复用连接池。调用方必须在结束时 CloseIdleConnections()，
// 否则会泄漏连接与 goroutine；常规路径请直接用 LocalHTTPClient。
func NewLocalClient(socket string, timeout time.Duration) *http.Client {
	return newLocalClient(socket, timeout)
}

func newLocalClient(socket string, timeout time.Duration) *http.Client {
	if socket == "" {
		socket = DefaultSocket
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		// 显式给空闲连接设上界：即使调用方忘了清理，也不会无限堆积。
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// NewEngineWithClient 用给定 HTTP client 构造 Engine。
func NewEngineWithClient(hc *http.Client) *Engine {
	return NewEngine(func(req *http.Request) (*http.Response, error) { return hc.Do(req) })
}

// Do 发起任意 Engine API 请求；path 如 /containers/json。
func (e *Engine) Do(ctx context.Context, method, path string, query url.Values,
	body io.Reader, contentType string) (*http.Response, error) {
	u := "http://docker" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return e.do(req)
}

func (e *Engine) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	return e.do(req)
}

// Ping 探测 Engine（/_ping 应返回 OK）。
func (e *Engine) Ping(ctx context.Context) error {
	resp, err := e.get(ctx, "/_ping")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping 状态码 %d", resp.StatusCode)
	}
	return nil
}

// Version 读取 Engine 版本。
func (e *Engine) Version(ctx context.Context) (VersionInfo, error) {
	resp, err := e.get(ctx, "/version")
	if err != nil {
		return VersionInfo{}, err
	}
	defer resp.Body.Close()
	var v VersionInfo
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("version 状态码 %d", resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}

// DetectLocal 检测本机 Docker：可用时返回版本号，否则返回空串与原因。
func DetectLocal(ctx context.Context) (string, string) {
	return detectBySocket(ctx, DefaultSocket)
}

// DetectLocalWithSocket 同 DetectLocal，但指定 socket 路径（自定义 daemon / 测试）。
func DetectLocalWithSocket(socket string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return detectBySocket(ctx, socket)
}

func detectBySocket(ctx context.Context, socket string) (string, string) {
	e := NewLocal(socket)
	if err := e.Ping(ctx); err != nil {
		return "", err.Error()
	}
	v, err := e.Version(ctx)
	if err != nil {
		return "", err.Error()
	}
	return strings.TrimSpace(v.Version), ""
}
