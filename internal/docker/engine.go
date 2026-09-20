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

// NewLocal 本机 Engine 客户端（unix socket，10s 超时，用于 ping/version 等短请求）。
func NewLocal(socket string) *Engine {
	return NewEngineWithClient(NewLocalClient(socket, 10*time.Second))
}

// NewLocalClient 走 unix socket 的 HTTP 客户端；timeout<=0 表示不限制（流式）。
func NewLocalClient(socket string, timeout time.Duration) *http.Client {
	if socket == "" {
		socket = DefaultSocket
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
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
	e := NewLocal(DefaultSocket)
	if err := e.Ping(ctx); err != nil {
		return "", err.Error()
	}
	v, err := e.Version(ctx)
	if err != nil {
		return "", err.Error()
	}
	return strings.TrimSpace(v.Version), ""
}
