package apps

import (
	"context"
	"strconv"
	"testing"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
)

func TestParseWGShowDump(t *testing.T) {
	now := time.Unix(1_700_000_300, 0)
	// peer1 30 秒前握手（在线）；peer2 从未握手
	fixture := "GNqjQdyEh3pkT8yF4qR6tV2bZ9mK1xH7wYcN8sPdUy0= " +
		"PUBP0XHsX4lY5pI8b3Qr7t2V9zK6mN4qWcE1aSdUy0= 51820 off\n" +
		"PEERA1111111111111111111111111111111111111= (none) 203.0.113.7:51820 10.13.13.2/32 " +
		"1700000270 123456 654321 25\n" +
		"PEERB2222222222222222222222222222222222222= (none) (none) (none) 0 0 0 off\n"
	info, err := parseWGShowDump("wg0", fixture, now)
	if err != nil {
		t.Fatal(err)
	}
	if info.Interface != "wg0" || info.ListenPort != 51820 {
		t.Fatalf("接口信息错误: %+v", info)
	}
	if info.PublicKey != "PUBP0XHsX4lY5pI8b3Qr7t2V9zK6mN4qWcE1aSdUy0=" {
		t.Fatalf("公钥解析错误: %q", info.PublicKey)
	}
	if len(info.Peers) != 2 {
		t.Fatalf("peer 数量错误: %d", len(info.Peers))
	}
	p1 := info.Peers[0]
	if !p1.Online || p1.HandshakeAgeSecond != 30 {
		t.Fatalf("peer1 应在线且握手年龄 30s: %+v", p1)
	}
	if p1.Endpoint != "203.0.113.7:51820" || len(p1.AllowedIPs) != 1 || p1.AllowedIPs[0] != "10.13.13.2/32" {
		t.Fatalf("peer1 endpoint/allowedips 错误: %+v", p1)
	}
	if p1.RxBytes != 123456 || p1.TxBytes != 654321 || p1.Keepalive != 25 {
		t.Fatalf("peer1 流量/keepalive 错误: %+v", p1)
	}
	p2 := info.Peers[1]
	if p2.Online || p2.LatestHandshake != 0 || p2.Endpoint != "" || len(p2.AllowedIPs) != 0 {
		t.Fatalf("peer2 应为离线空状态: %+v", p2)
	}

	// 窗口边界：4 分钟前握手 → 离线
	old := time.Unix(1_700_000_540, 0)
	info2, err := parseWGShowDump("wg0", fixture, old)
	if err != nil {
		t.Fatal(err)
	}
	if info2.Peers[0].Online {
		t.Fatal("超过 3 分钟握手窗口不应判为在线")
	}
}

func TestWireGuardStatusEnrichment(t *testing.T) {
	_, _, _, s := dockerAppsSetup(t) // 本机节点 armv7l
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := recipes.LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	mgr := New(s, node.New(s, box), reg, box, nil)

	fx := newFakeExec()
	fx.active["wg-quick-wg0.service"] = "active"
	now := time.Now().Unix()
	fx.customOut["wg show wg0 dump"] = "PRIVKEY0000000000000000000000000000000000000= " +
		"PUBKEY000000000000000000000000000000000000000= 51820 off\n" +
		"PEER000000000000000000000000000000000000000= (none) 198.51.100.9:51820 10.13.13.2/32 " +
		strconv.FormatInt(now-10, 10) + " 100 200 off\n"
	mgr.SetExecutorHook(func(*store.Node) (executor.Executor, error) { return fx, nil })

	if _, err := s.CreateInstallation(&store.AppInstallation{
		NodeID: 1, AppID: "wireguard", Method: "native",
		Status: "installed", ServiceName: "wg-quick-wg0.service",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.Status(context.Background(), 1, "wireguard")
	if err != nil {
		t.Fatal(err)
	}
	if res["active"] != true {
		t.Fatalf("应处于 active: %+v", res)
	}
	wg, ok := res["wireguard"].(*WGInfo)
	if !ok {
		t.Fatalf("缺少 wireguard 运行态: %+v", res["wireguard"])
	}
	if wg.ListenPort != 51820 || len(wg.Peers) != 1 || !wg.Peers[0].Online {
		t.Fatalf("wg 运行态错误: %+v", wg)
	}
}
