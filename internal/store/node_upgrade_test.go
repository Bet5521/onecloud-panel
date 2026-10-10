package store_test

import (
	"testing"

	"onecloud-panel/internal/store"
)

// 心跳写入的升级结果必须落库并可读回（迁移 021 新增 upgrade_json）。
func TestUpdateNodeInfoPersistsUpgradeJSON(t *testing.T) {
	s := openStore(t)
	id := seedNode(t, s, "remote", "lan")

	const payload = `{"target_version":"2.2.8","ok":false,"error":"text file busy","at":1700000000}`
	if err := s.UpdateNodeInfo(id, &store.Node{
		Hostname: "h", NetworkType: "lan", UpgradeJSON: payload,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	n, err := s.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if n.UpgradeJSON != payload {
		t.Fatalf("upgrade_json = %q，期望 %q", n.UpgradeJSON, payload)
	}

	// 心跳未携带升级结果（UpgradeJSON 为空）时不得清空已有结论，
	// 否则「上次升级失败原因」会在下一次心跳就消失。
	if err := s.UpdateNodeInfo(id, &store.Node{Hostname: "h2", UpgradeJSON: ""}); err != nil {
		t.Fatalf("update: %v", err)
	}
	n, _ = s.GetNode(id)
	if n.UpgradeJSON != payload {
		t.Fatalf("空值不应覆盖已有升级结果，实际 %q", n.UpgradeJSON)
	}
	if n.Hostname != "h2" {
		t.Fatalf("主机名应正常更新，实际 %q", n.Hostname)
	}
}

// 列表查询同样要带上 upgrade_json，节点列表/详情才都能展示升级结论。
func TestListNodesCarriesUpgradeJSON(t *testing.T) {
	s := openStore(t)
	id := seedNode(t, s, "remote", "lan")
	const payload = `{"ok":true,"target_version":"2.2.8"}`
	if err := s.UpdateNodeInfo(id, &store.Node{UpgradeJSON: payload}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == id {
			if n.UpgradeJSON != payload {
				t.Fatalf("列表 upgrade_json = %q，期望 %q", n.UpgradeJSON, payload)
			}
			return
		}
	}
	t.Fatalf("未找到节点 %d", id)
}
