package store_test

import (
	"path/filepath"
	"testing"

	"onecloud-panel/internal/node"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/store/migrations"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedNode(t *testing.T, s *store.Store, mode, networkType string) int64 {
	t.Helper()
	id, err := s.CreateNode(&store.Node{
		Name: "n-" + mode + "-" + networkType, Mode: mode, Status: "active",
		NetworkType: networkType,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	return id
}

// UpdateNodeInfo 必须把 network_type 落库（此前 UPDATE 缺列导致本机类型永为空）。
func TestUpdateNodeInfoPersistsNetworkType(t *testing.T) {
	s := openStore(t)
	id := seedNode(t, s, "local", "")

	if err := s.UpdateNodeInfo(id, &store.Node{Hostname: "h", NetworkType: "local"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	n, err := s.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if n.NetworkType != "local" {
		t.Fatalf("network_type = %q, want local", n.NetworkType)
	}

	// 心跳路径未载入该字段时（空串）不得清空已有类型
	if err := s.UpdateNodeInfo(id, &store.Node{Hostname: "h2"}); err != nil {
		t.Fatal(err)
	}
	n, _ = s.GetNode(id)
	if n.NetworkType != "local" {
		t.Fatalf("空类型不应覆盖已有值, got %q", n.NetworkType)
	}
}

// EnsureLocalNode 之后，库里与 List 读到的最初节点类型均为 local。
func TestEnsureLocalNodeTypePersisted(t *testing.T) {
	s := openStore(t)
	box, err := secretbox.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := node.New(s, box)

	local, err := svc.EnsureLocalNode(nil)
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}
	// 关掉内存兜底视角，直接查库确认已写入
	row, err := s.GetNode(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.NetworkType != "local" {
		t.Fatalf("库中本机类型 = %q, want local", row.NetworkType)
	}

	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range list {
		if n.ID == local.ID {
			found = true
			if n.NetworkType != "local" {
				t.Fatalf("List 本机类型 = %q, want local", n.NetworkType)
			}
		}
	}
	if !found {
		t.Fatal("List 未包含本机节点")
	}
}

// 旧库未迁移时（空类型）List 需内存兜底：本机 → local，其余 → lan。
func TestListNetworkTypeFallback(t *testing.T) {
	s := openStore(t)
	box, _ := secretbox.New(t.TempDir())
	svc := node.New(s, box)

	localID := seedNode(t, s, "local", "")
	remoteID := seedNode(t, s, "remote", "")
	publicID := seedNode(t, s, "remote", "public")

	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, n := range list {
		got[n.ID] = n.NetworkType
	}
	if got[localID] != "local" {
		t.Fatalf("空类型本机兜底 = %q, want local", got[localID])
	}
	if got[remoteID] != "lan" {
		t.Fatalf("空类型远端兜底 = %q, want lan", got[remoteID])
	}
	if got[publicID] != "public" {
		t.Fatalf("已标注类型被改写为 %q", got[publicID])
	}
}

// 010 迁移：历史 unknown 与空值收敛为 local/lan。
func TestNetworkTypeMigration(t *testing.T) {
	s := openStore(t)

	localEmpty := seedNode(t, s, "local", "")
	localUnknown := seedNode(t, s, "local", "unknown")
	remoteEmpty := seedNode(t, s, "remote", "")
	remoteUnknown := seedNode(t, s, "remote", "unknown")
	remotePublic := seedNode(t, s, "remote", "public")
	remoteWG := seedNode(t, s, "remote", "wireguard")

	sqlBytes, err := migrations.FS.ReadFile("010_network_type.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	if _, err := s.DB.Exec(string(sqlBytes)); err != nil {
		t.Fatalf("执行迁移失败: %v", err)
	}

	want := map[int64]string{
		localEmpty:    "local",
		localUnknown:  "local",
		remoteEmpty:   "lan",
		remoteUnknown: "lan",
		remotePublic:  "public",
		remoteWG:      "wireguard",
	}
	for id, w := range want {
		n, err := s.GetNode(id)
		if err != nil {
			t.Fatal(err)
		}
		if n.NetworkType != w {
			t.Fatalf("节点 #%d 迁移后 = %q, want %q", id, n.NetworkType, w)
		}
	}
}

// 合法类型仅保留四类；known 之外（含 unknown）一律非法。
func TestIsValidNetworkType(t *testing.T) {
	for _, ok := range []string{"lan", "wireguard", "public", "local"} {
		if !node.IsValidNetworkType(ok) {
			t.Fatalf("%q 应为合法类型", ok)
		}
	}
	for _, bad := range []string{"unknown", "", "WAN", "vpn", "lan "} {
		if node.IsValidNetworkType(bad) {
			t.Fatalf("%q 应为非法类型", bad)
		}
	}
}

// 建议类型不再产生 unknown。
func TestSuggestNetworkTypeNoUnknown(t *testing.T) {
	s := openStore(t)
	box, _ := secretbox.New(t.TempDir())
	svc := node.New(s, box)

	cases := map[string]string{
		"8.8.8.8:9000":      "public",
		"192.168.6.20:9000": "lan",
		"not-an-ip:9000":    "lan",
		"":                  "lan",
	}
	for addr, want := range cases {
		if got := svc.SuggestNetworkType(addr); got != want {
			t.Fatalf("SuggestNetworkType(%q) = %q, want %q", addr, got, want)
		}
		if !node.IsValidNetworkType(svc.SuggestNetworkType(addr)) {
			t.Fatalf("建议类型 %q 非法", svc.SuggestNetworkType(addr))
		}
	}
}
