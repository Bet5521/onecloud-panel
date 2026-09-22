package api

import (
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/store"
)

// SSH 纳管失败诊断：目标机残留旧身份时会跳过注册，错误信息必须可操作。
// 命中既有节点时要指出该节点与本地身份文件；未命中时也要提示清理身份文件。
func TestSSHNoRegisterHint(t *testing.T) {
	_, s, a := newTestAPI(t)

	now := time.Now().Unix()
	if _, err := s.CreateNode(&store.Node{
		Name: "玩客云-200", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "192.168.6.200:9000",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// 按「地址主机部分」匹配（忽略端口差异）
	hit := a.nodeByAddrHost("192.168.6.200")
	if hit == nil {
		t.Fatal("应按地址主机匹配到既有节点")
	}
	if hit.Name != "玩客云-200" {
		t.Fatalf("匹配到错误节点: %+v", hit)
	}
	if a.nodeByAddrHost("192.168.6.201") != nil {
		t.Fatal("不同主机不应匹配")
	}
	if a.nodeByAddrHost("") != nil {
		t.Fatal("空主机不应匹配")
	}

	hint := a.noRegisterHint("192.168.6.200")
	if !strings.Contains(hint, "玩客云-200") || !strings.Contains(hint, agentStatePath) {
		t.Fatalf("命中既有节点时须含节点名与身份文件路径: %s", hint)
	}

	other := a.noRegisterHint("192.168.6.201")
	if !strings.Contains(other, agentStatePath) {
		t.Fatalf("未命中时也须提示清理身份文件: %s", other)
	}
	if strings.Contains(other, "已纳管为节点") {
		t.Fatalf("未命中时不应声称已纳管: %s", other)
	}
}
