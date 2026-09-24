package agentclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	// execHTTP 用于执行类请求（/v1/exec）。安装步骤里的 apt-get / dpkg /
	// systemctl 在低配 ARM 节点上常需数分钟，控制面的 30s 上限会把正常安装
	// 判成失败（表现为 "context deadline exceeded (Client.Timeout exceeded
	// while awaiting headers)"）。这里取与 ops/download 一致的 30 分钟预算，
	// 仍留一个上界，避免 Agent 失联时任务永久挂起。
	execHTTP *http.Client
}

// execTimeout 执行类请求的客户端超时上限。
const execTimeout = 30 * time.Minute

// New 创建客户端，address 为 [http(s)://]host:port。
// 安全策略：未显式指定 scheme 时，私网/回环/CGNAT 地址回退 http://（内网明文可接受）；
// 公网地址拒绝明文 http 回退，改用 https://——Token 经 Bearer 头传输，公网明文可被窃听。
// 公网节点如确无 TLS 条件，请显式填写 http:// 前缀（自担风险），或为 Agent 配置 TLS 反向代理。
func New(address, token string) *Client {
	base := address
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		if isPublicHost(address) {
			base = "https://" + address
		} else {
			base = "http://" + address
		}
	}
	return &Client{
		baseURL:  strings.TrimRight(base, "/"),
		token:    token,
		http:     &http.Client{Timeout: 30 * time.Second},
		execHTTP: &http.Client{Timeout: execTimeout},
	}
}

// isPublicHost 判断地址的 host 部分是否为公网地址（非回环/私网/链路本地/CGNAT）。
func isPublicHost(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			// CGNAT 共享地址段（100.64.0.0/10，常见于 Tailscale/组网工具）按私网对待
			if v4[0] == 100 && v4[1] >= 64 && v4[1] < 128 {
				return false
			}
		}
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified())
	}
	// 域名：localhost 视为本地，其余保守按公网处理
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h != "localhost" && !strings.HasSuffix(h, ".localhost")
}

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return c.do(c.http, ctx, method, path, body)
}

// do 用指定 client 发起带鉴权的 JSON 请求。
func (c *Client) do(hc *http.Client, ctx context.Context, method, path string, body any) (*http.Response, error) {
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
	return hc.Do(req)
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

// Exec 执行命令并获取完整结果（控制面超时，30s）。
func (c *Client) Exec(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	return c.execCall(ctx, c.http, name, args...)
}

// ExecLong 执行耗时较长的命令（安装步骤：apt-get / dpkg / systemctl 等）。
// 实现 executor.LongRunner，供步骤执行器识别；超时上限见 execTimeout。
func (c *Client) ExecLong(ctx context.Context, name string, args ...string) (*executor.Result, error) {
	return c.execCall(ctx, c.execHTTP, name, args...)
}

func (c *Client) execCall(ctx context.Context, hc *http.Client, name string, args ...string) (*executor.Result, error) {
	resp, err := c.do(hc, ctx, http.MethodPost, "/v1/exec",
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
