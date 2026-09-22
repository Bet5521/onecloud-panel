package node

import (
	"path/filepath"
	"testing"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
)

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(s, box), s
}

// Host 为 nil 时仍应依据 TCP 来源拼装地址（非 Linux 开发环境回归）。
func TestRegisterAddressWithoutHost(t *testing.T) {
	svc, _ := newSvc(t)
	raw, _, err := svc.CreateToken("t", time.Hour, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := svc.Register(&agent.RegisterRequest{
		RegisterToken: raw, AgentPort: 9000, Host: nil,
	}, "192.168.1.50:55001")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("长期 Token 为空")
	}
	n, err := svc.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if n.Address != "192.168.1.50:9000" {
		t.Fatalf("address = %q, want 192.168.1.50:9000", n.Address)
	}
	if n.Status != "pending" || n.Mode != "remote" {
		t.Fatalf("status=%q mode=%q", n.Status, n.Mode)
	}
	enc, _ := svc.store.GetNodeTokenEncrypted(id)
	if enc == "" {
		t.Fatal("Token 密文未保存，轮换功能将不可用")
	}
}

func TestRegisterWithHostInfoAndHeartbeat(t *testing.T) {
	svc, _ := newSvc(t)
	raw, _, _ := svc.CreateToken("t", time.Hour, 1, nil)
	host := &system.HostInfo{Hostname: "wky", Arch: "armv7l", OSName: "Armbian", CPUCores: 4}
	id, token, err := svc.Register(&agent.RegisterRequest{
		RegisterToken: raw, AgentPort: 9000, Host: host,
	}, "10.0.0.2:40000")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := svc.Get(id)
	if n.Name != "wky" || n.Arch != "armv7l" {
		t.Fatalf("name=%q arch=%q", n.Name, n.Arch)
	}

	// 心跳更新
	time.Sleep(time.Second)
	if err := svc.Heartbeat(&agent.HeartbeatRequest{Token: token, Host: host}); err != nil {
		t.Fatal(err)
	}
	n2, _ := svc.Get(id)
	if !IsOnline(n2.LastSeen) {
		t.Fatal("心跳后应在线")
	}

	// 错误 Token
	if err := svc.Heartbeat(&agent.HeartbeatRequest{Token: "bad"}); err == nil {
		t.Fatal("错误 Token 心跳应失败")
	}
}

func TestConfirmAndManualAdd(t *testing.T) {
	svc, _ := newSvc(t)
	id, err := svc.ManualAdd("手动", "10.8.0.9:9000", "plain-token", "wireguard", nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.Confirm(id, "手动改名", "lan", "192.168.1.9:9000", "10.8.0.9:9000")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "手动改名" || n.NetworkType != "lan" || n.Status != "active" {
		t.Fatalf("确认后字段异常: %+v", n)
	}

	// 非法接入类型
	if _, err := svc.Confirm(id, "", "dialup", "", ""); err == nil {
		t.Fatal("非法接入类型应拒绝")
	}
	// 删除本机节点应失败
	local, err := svc.EnsureLocalNode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(local.ID); err == nil {
		t.Fatal("本机节点不应可删除")
	}
	// 远程节点可删
	if err := svc.Remove(id); err != nil {
		t.Fatalf("删除远程节点: %v", err)
	}
}
