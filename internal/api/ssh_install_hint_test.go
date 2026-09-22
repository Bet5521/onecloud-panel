package api

import (
	"encoding/json"
	"net/http"
	"strconv"
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

// SSH 纳管入队前预检：目标主机已纳管时立即拒绝，不得白跑数分钟 SSH 安装 + 等待超时。
// 未纳管主机必须放行，避免过度拦截。
func TestSSHInstallRejectsManagedHost(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := newJar(t)
	if w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "admin", "password": "Sup3rPass!"}, jar); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}

	now := time.Now().Unix()
	nodeID, err := s.CreateNode(&store.Node{
		Name: "玩客云-200", Mode: "remote", Status: "active",
		NetworkType: "lan", Address: "192.168.6.200:9000",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// 同一主机的不同书写（agent 端口与 SSH 端口不同）也应命中
	w := do(t, h, "POST", "/api/nodes/ssh-install", map[string]any{
		"host": "192.168.6.200", "user": "root", "password": "x",
	}, jar)
	if w.Code != http.StatusConflict {
		t.Fatalf("已纳管主机应 409, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"已纳管为节点 #" + strconv.FormatInt(nodeID, 10), "玩客云-200", agentStatePath,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("错误信息缺少 %q: %s", want, body)
		}
	}

	// 未纳管主机放行并正常入队
	w = do(t, h, "POST", "/api/nodes/ssh-install", map[string]any{
		"host": "192.168.6.201", "user": "root", "password": "x",
	}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("未纳管主机应放行, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["task_id"] == nil {
		t.Fatalf("应返回 task_id: %s", w.Body.String())
	}
}
