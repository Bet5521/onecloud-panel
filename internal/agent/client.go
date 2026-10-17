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
	"sync"
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
// up 为最近一次自升级结果（可为 nil），面板据此展示升级成败。
func Heartbeat(ctx context.Context, server, token string, host *system.HostInfo,
	up *UpgradeResult, insecure bool, pin string) (*HeartbeatResponse, error) {
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	dockerVer, _ := docker.DetectLocal(dctx)
	cancel()
	body, _ := json.Marshal(&HeartbeatRequest{
		Token: token, Host: host, DockerVersion: dockerVer, Upgrade: up,
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

// panelClients 按 (insecure, pin) 缓存心跳/注册用的 HTTP 客户端。
//
// 每次心跳新建 Transport 会持续泄漏：Transport 的空闲连接常驻两个 goroutine，
// goroutine 又是 GC 根并反向引用 Transport，导致它永远不被回收；零值
// IdleConnTimeout 还意味着空闲连接永不超时。心跳 30s 一次，一天就是数千个
// 永不释放的 TLS 连接与缓冲区。
var (
	panelClientMu sync.Mutex
	panelClients  = map[string]*http.Client{}
)

const panelClientTimeout = 15 * time.Second

// panelHTTPClient 返回复用的客户端；无 pin/insecure 时走 net/http 共享 Transport。
func panelHTTPClient(insecure bool, pin string) *http.Client {
	var key string
	var tr *http.Transport
	switch {
	case pin != "":
		// 优先指纹固定：仅接受证书指纹匹配，无需系统 CA 信任
		key = "pin:" + normalizePin(pin)
		tr = pinnedTransport(pin)
	case insecure:
		// 自签证书场景：仅跳过证书校验，仍完成 TLS 握手（OCP_INSECURE_TLS）
		key = "insecure"
		tr = newAgentTransport(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec
	default:
		// 默认走 http.DefaultTransport 的全局连接池，无需自定义
		key = "default"
		tr = nil
	}
	panelClientMu.Lock()
	defer panelClientMu.Unlock()
	if c, ok := panelClients[key]; ok {
		return c
	}
	var c *http.Client
	if tr == nil {
		// 注意不能写成 Transport: tr —— 值为 nil 的 *http.Transport 赋给接口
		// 会得到「非 nil 接口」，Client 会拿它发请求并 panic。这里保持零值走默认池。
		c = &http.Client{Timeout: panelClientTimeout}
	} else {
		c = &http.Client{Timeout: panelClientTimeout, Transport: tr}
	}
	panelClients[key] = c
	return c
}

func doJSON(req *http.Request, out any, wantStatus int, insecure bool, pin string) error {
	client := panelHTTPClient(insecure, pin)
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

// newAgentTransport 构造带连接池上界的 Transport。
// 显式设置 IdleConnTimeout/连接数上限，避免依赖零值语义（零值 = 空闲连接永不超时）。
func newAgentTransport(tlsCfg *tls.Config) *http.Transport {
	return &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
}

// pinnedTransport 返回一个仅接受指纹匹配的 TLS 传输层。
// 面板使用自签/内部 CA 证书时，可固定其证书 SHA256 指纹，避免 InsecureSkipVerify 的 MITM 风险。
func pinnedTransport(pin string) *http.Transport {
	expected := normalizePin(pin)
	return newAgentTransport(&tls.Config{
		// 自行校验指纹，故跳过标准链校验（gosec 误报，此处为受控场景）
		InsecureSkipVerify: true, //nolint:gosec
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if matchPin(rawCerts, expected) {
				return nil
			}
			return fmt.Errorf("面板证书指纹不匹配（期望 %s）", expected)
		},
	})
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
