package apps

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

// WGPeer 单个 WireGuard peer 的运行态（解析自 wg show dump）。
type WGPeer struct {
	PublicKey          string   `json:"public_key"`
	Endpoint           string   `json:"endpoint,omitempty"`
	AllowedIPs         []string `json:"allowed_ips,omitempty"`
	LatestHandshake    int64    `json:"latest_handshake"` // Unix 秒，0=从未
	HandshakeAgeSecond int64    `json:"handshake_age_seconds"`
	Online             bool     `json:"online"` // 最近 3 分钟有握手
	RxBytes            int64    `json:"rx_bytes"`
	TxBytes            int64    `json:"tx_bytes"`
	Keepalive          int      `json:"keepalive_interval,omitempty"`
}

// WGInfo 接口运行态。
type WGInfo struct {
	Interface  string   `json:"interface"`
	ListenPort int      `json:"listen_port"`
	PublicKey  string   `json:"public_key,omitempty"`
	Peers      []WGPeer `json:"peers"`
}

// wgHandshakeOnlineWindow 最近握手窗口。
const wgHandshakeOnlineWindow = 3 * time.Minute

// parseWGShowDump 解析 `wg show <iface> dump` 输出：
// 首行 = 私钥 公钥 监听端口 fwmark；其余行每个 peer 8 列。
func parseWGShowDump(iface, out string, now time.Time) (*WGInfo, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("wg show 输出为空")
	}
	hdr := strings.Fields(lines[0])
	if len(hdr) < 3 {
		return nil, fmt.Errorf("wg show 接口行格式异常: %q", lines[0])
	}
	info := &WGInfo{Interface: iface, Peers: []WGPeer{}}
	if p, err := strconv.Atoi(hdr[2]); err == nil {
		info.ListenPort = p
	}
	if len(hdr) >= 2 && hdr[1] != "(none)" {
		info.PublicKey = hdr[1]
	}

	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		peer := WGPeer{
			PublicKey: f[0],
			Endpoint:  noneToEmpty(f[2]),
		}
		if f[3] != "(none)" {
			peer.AllowedIPs = strings.Split(f[3], ",")
		} else {
			peer.AllowedIPs = []string{}
		}
		hs, _ := strconv.ParseInt(f[4], 10, 64)
		peer.LatestHandshake = hs
		if hs > 0 {
			age := now.Unix() - hs
			peer.HandshakeAgeSecond = age
			peer.Online = age >= 0 && time.Duration(age)*time.Second <= wgHandshakeOnlineWindow
		}
		peer.RxBytes, _ = strconv.ParseInt(f[5], 10, 64)
		peer.TxBytes, _ = strconv.ParseInt(f[6], 10, 64)
		if f[7] != "off" {
			if k, err := strconv.Atoi(f[7]); err == nil {
				peer.Keepalive = k
			}
		}
		info.Peers = append(info.Peers, peer)
	}
	return info, nil
}

func noneToEmpty(s string) string {
	if s == "(none)" {
		return ""
	}
	return s
}

// wireguardStatus 通过执行器收集 wg 运行态，失败时以 error 字段返回（不影响整体状态查询）。
func (m *Manager) wireguardStatus(ctx context.Context, ex executor.Executor,
	in *store.AppInstallation, res map[string]any) {
	const iface = "wg0"
	r, err := ex.Exec(ctx, "wg", "show", iface, "dump")
	if err != nil {
		res["wireguard"] = map[string]any{"interface": iface, "error": err.Error()}
		return
	}
	if r.ExitCode != 0 {
		res["wireguard"] = map[string]any{
			"interface": iface,
			"error":     strings.TrimSpace(r.Output),
		}
		return
	}
	info, err := parseWGShowDump(iface, r.Output, time.Now())
	if err != nil {
		res["wireguard"] = map[string]any{"interface": iface, "error": err.Error()}
		return
	}
	res["wireguard"] = info
}
