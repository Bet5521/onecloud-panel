package agentclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/system"
)

// Client 面板侧调用远程 Agent 的客户端。
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New 创建客户端，address 为 host:port。
func New(address, token string) *Client {
	base := address
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.http.Do(req)
}

// Info GET /v1/info — 实时主机信息与资源水位。
func (c *Client) Info(ctx context.Context) (*system.HostInfo, error) {
	resp, err := c.req(ctx, http.MethodGet, "/v1/info", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("agent info %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var h system.HostInfo
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Health GET /healthz（无需 Token）。
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent health %d", resp.StatusCode)
	}
	return nil
}

// Exec 执行命令并获取完整结果。
func (c *Client) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	resp, err := c.req(ctx, http.MethodPost, "/v1/exec",
		agent.ExecReq{Command: name, Args: args})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r agent.ExecResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return &executor.Result{ExitCode: -1, Output: r.Output},
			fmt.Errorf("agent exec 状态码 %d", resp.StatusCode)
	}
	return &executor.Result{ExitCode: r.ExitCode, Output: r.Output}, nil
}

// ExecStream 流式执行，输出实时写入 w。
func (c *Client) ExecStream(ctx context.Context, w io.Writer, name string, args ...string) (int, error) {
	// 流式调用使用独立 client（无整体超时，由 ctx 控制）
	hc := &http.Client{}
	b, _ := json.Marshal(agent.ExecReq{Command: name, Args: args})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/exec-stream", bytes.NewReader(b))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := hc.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, fmt.Errorf("exec-stream: %s", strings.TrimSpace(string(data)))
	}
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// Systemctl systemd 操作。
func (c *Client) Systemctl(ctx context.Context, action, unit string) (*executor.Result, error) {
	resp, err := c.req(ctx, http.MethodPost, "/v1/systemctl",
		agent.SystemctlReq{Action: action, Unit: unit})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r agent.ExecResp
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return &executor.Result{ExitCode: r.ExitCode, Output: r.Output}, nil
}

// ReadFile 读取白名单文件。
func (c *Client) ReadFile(path string) ([]byte, error) {
	resp, err := c.req(context.Background(), http.MethodGet,
		"/v1/file?path="+url.QueryEscape(path), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("读取失败 %d: %s", resp.StatusCode, string(data))
	}
	var r agent.FileResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	// 优先使用 base64（二进制安全）；回落 Content 以兼容仅返回文本的服务端。
	if r.ContentB64 != "" {
		dec, derr := base64.StdEncoding.DecodeString(r.ContentB64)
		if derr != nil {
			return nil, fmt.Errorf("content_b64 解码失败: %w", derr)
		}
		return dec, nil
	}
	return []byte(r.Content), nil
}

// WriteFile 写入白名单文件。二进制内容以 base64 传输，避免 JSON 字符串
// 对非法 UTF-8 字节的替换（\ufffd）造成静默损坏。
func (c *Client) WriteFile(path string, data []byte) error {
	resp, err := c.req(context.Background(), http.MethodPut, "/v1/file",
		agent.FileReq{Path: path, ContentB64: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("写入失败 %d", resp.StatusCode)
	}
	return nil
}

// Exists 通过 test -e 判断远程路径。
func (c *Client) Exists(path string) (bool, error) {
	r, err := c.Exec(context.Background(), "sh", "-c",
		"test -e '"+strings.ReplaceAll(path, "'", "'\\''")+"' && echo yes || echo no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(r.Output) == "yes", nil
}

// Download 远程下载到节点。
func (c *Client) Download(ctx context.Context, urlStr, dest string, mode os.FileMode,
	sha256Hex string, progress io.Writer) error {
	resp, err := c.req(ctx, http.MethodPost, "/v1/download", agent.DownloadReq{
		URL: urlStr, Dest: dest, Mode: uint32(mode.Perm()), SHA256: sha256Hex,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r agent.DownloadResp
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if !r.OK {
		return fmt.Errorf("远程下载失败: %s", r.Error)
	}
	return nil
}

// Healthcheck 远程节点本机健康探测。
func (c *Client) Healthcheck(ctx context.Context, kind string, port int, path string) (bool, string) {
	resp, err := c.req(ctx, http.MethodPost, "/v1/healthcheck",
		agent.HealthcheckReq{Type: kind, Port: port, Path: path})
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	var r struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return r.OK, r.Detail
}

// RotateToken 请求 Agent 切换长期 Token。
func (c *Client) RotateToken(ctx context.Context, newToken string) error {
	resp, err := c.req(ctx, http.MethodPost, "/v1/token/rotate",
		map[string]string{"token": newToken})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent 轮换返回 %d", resp.StatusCode)
	}
	return nil
}

// Tunnel 透传 Docker Engine API 请求到远程节点（无整体超时，由 ctx 控制）。
// enginePath 形如 /containers/json。
func (c *Client) Tunnel(ctx context.Context, method, enginePath string,
	query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := c.baseURL + "/v1/docker" + enginePath
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	return (&http.Client{}).Do(req)
}

// JournalURL 构造日志流地址（供前端跳转式读取或面板代理）。
func (c *Client) JournalURL(unit string, lines int, follow bool) string {
	q := url.Values{}
	q.Set("unit", unit)
	q.Set("lines", strconv.Itoa(lines))
	if follow {
		q.Set("follow", "1")
	}
	return c.baseURL + "/v1/journal?" + q.Encode()
}
