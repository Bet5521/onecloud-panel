package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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
func Register(ctx context.Context, server, registerToken string, port int, insecure bool, pin string) (*RegisterResponse, error) {
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
	if err := doJSON(req, &resp, http.StatusOK, insecure, pin); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Heartbeat 发送心跳，返回面板要求轮换的新 Token（可能为空）。
func Heartbeat(ctx context.Context, server, token string, host *system.HostInfo, insecure bool, pin string) (*HeartbeatResponse, error) {
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
	if err := doJSON(req, &resp, http.StatusOK, insecure, pin); err != nil {
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

func doJSON(req *http.Request, out any, wantStatus int, insecure bool, pin string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	switch {
	case pin != "":
		// 优先指纹固定：仅接受证书指纹匹配，无需系统 CA 信任
		client.Transport = pinnedTransport(pin)
	case insecure:
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

// pinnedTransport 返回一个仅接受指纹匹配的 TLS 传输层。
// 面板使用自签/内部 CA 证书时，可固定其证书 SHA256 指纹，避免 InsecureSkipVerify 的 MITM 风险。
func pinnedTransport(pin string) *http.Transport {
	expected := normalizePin(pin)
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			// 自行校验指纹，故跳过标准链校验（gosec 误报，此处为受控场景）
			InsecureSkipVerify: true, //nolint:gosec
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if matchPin(rawCerts, expected) {
					return nil
				}
				return fmt.Errorf("面板证书指纹不匹配（期望 %s）", expected)
			},
		},
	}
}

// normalizePin 归一化指纹：去除空白、可选 "sha256:" 前缀，转小写。
func normalizePin(pin string) string {
	p := strings.ToLower(strings.TrimSpace(pin))
	p = strings.TrimPrefix(p, "sha256:")
	return p
}

// matchPin 判定证书链中任一证书的指纹（SHA256 十六进制）是否等于期望指纹。
// 期望指纹先经 normalizePin 归一化，容错 sha256: 前缀、空白与大小写。
func matchPin(rawCerts [][]byte, pin string) bool {
	normalizedPin := normalizePin(pin)
	if normalizedPin == "" {
		return false
	}
	for _, raw := range rawCerts {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) == normalizedPin {
			return true
		}
	}
	return false
}
