package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"onecloud-panel/internal/docker"
	"onecloud-panel/internal/system"
)

const (
	pathRegister  = "/api/agent/register"
	pathHeartbeat = "/api/agent/heartbeat"
)

// Register 使用一次性令牌向面板注册，返回节点 ID 与长期 Token。
func Register(ctx context.Context, server, registerToken string, port int, insecure bool) (*RegisterResponse, error) {
	host, _ := system.Collect()
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	dockerVer, _ := docker.DetectLocal(dctx)
	cancel()
	body, _ := json.Marshal(&RegisterRequest{
		RegisterToken: registerToken,
		AgentPort:     port,
		Host:          host,
		DockerVersion: dockerVer,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		panelURL(server, pathRegister), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp RegisterResponse
	if err := doJSON(req, &resp, http.StatusOK, insecure); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Heartbeat 发送心跳，返回面板要求轮换的新 Token（可能为空）。
func Heartbeat(ctx context.Context, server, token string, host *system.HostInfo, insecure bool) (*HeartbeatResponse, error) {
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	dockerVer, _ := docker.DetectLocal(dctx)
	cancel()
	body, _ := json.Marshal(&HeartbeatRequest{
		Token: token, Host: host, DockerVersion: dockerVer,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		panelURL(server, pathHeartbeat), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp HeartbeatResponse
	if err := doJSON(req, &resp, http.StatusOK, insecure); err != nil {
		return nil, err
	}
	return &resp, nil
}

// panelURL 归一化面板地址：缺协议时按 HTTP 处理。
func panelURL(server, path string) string {
	s := strings.TrimRight(strings.TrimSpace(server), "/")
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "http://" + s
	}
	return s + path
}

func doJSON(req *http.Request, out any, wantStatus int, insecure bool) error {
	client := &http.Client{Timeout: 15 * time.Second}
	if insecure {
		// 自签证书场景：仅跳过证书校验，仍完成 TLS 握手（OCP_INSECURE_TLS）
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("面板返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, out)
}
