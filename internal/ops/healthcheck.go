package ops

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Healthcheck 在本机（节点本地）探测 TCP 端口或 HTTP 端点。
func Healthcheck(ctx context.Context, kind string, port int, path string) (bool, string) {
	if port < 1 || port > 65535 {
		return false, "健康检查端口非法"
	}
	switch kind {
	case "tcp":
		d := net.Dialer{Timeout: 3 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, ""
	case "http":
		if path == "" {
			path = "/"
		}
		c := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://127.0.0.1:"+fmt.Sprint(port)+path, nil)
		if err != nil {
			return false, err.Error()
		}
		resp, err := c.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return true, ""
	default:
		return false, "未知健康检查类型: " + kind
	}
}
