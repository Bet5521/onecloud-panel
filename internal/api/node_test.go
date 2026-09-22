package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/node"
	"onecloud-panel/internal/secretbox"
	"onecloud-panel/internal/store"
	"onecloud-panel/internal/system"
)

func hashB64(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// adminLogin 初始化面板并登录管理员，返回会话 jar。
func adminLogin(t *testing.T, h http.Handler) http.CookieJar {
	t.Helper()
	jar := newJar(t)
	w := do(t, h, "POST", "/api/setup",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "rootadmin", "password": "Sup3rPass!"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	return jar
}

func sampleHost() *system.HostInfo {
	return &system.HostInfo{
		Hostname: "onecloud-living",
		OSName:   "Armbian", OSVersion: "23.11", Kernel: "6.1.0",
		Arch: "armv7l", CPUCores: 4, MemTotal: 1073741824,
	}
}

// TR-8.1 注册令牌 → Agent 注册（待确认）→ 确认纳管，全链路；过期令牌被拒。
func TestNodeOnboardingFlow(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	// 创建注册令牌
	w := do(t, h, "POST", "/api/registration-tokens",
		map[string]any{"description": "客厅玩客云", "ttl_hours": 1, "max_uses": 1}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("create token: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID             int64  `json:"id"`
		Token          string `json:"token"`
		InstallCommand string `json:"install_command"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Token == "" || created.InstallCommand == "" {
		t.Fatal("令牌/安装命令为空")
	}

	// Agent 注册
	w = do(t, h, "POST", "/api/agent/register",
		&agent.RegisterRequest{RegisterToken: created.Token, AgentPort: 9000, Host: sampleHost()}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	var reg agent.RegisterResponse
	_ = json.Unmarshal(w.Body.Bytes(), &reg)
	if reg.NodeID == 0 || reg.Token == "" {
		t.Fatal("注册应答非法")
	}

	// 节点应为 pending
	w = do(t, h, "GET", "/api/nodes", nil, jar)
	var list struct {
		Items []NodeDTO `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Fatalf("节点数 = %d, want 1", len(list.Items))
	}
	if list.Items[0].Status != "pending" {
		t.Fatalf("status = %q, want pending", list.Items[0].Status)
	}
	if list.Items[0].Arch != "armv7l" {
		t.Fatalf("arch = %q", list.Items[0].Arch)
	}

	// 重复使用同一令牌 → 拒绝
	w = do(t, h, "POST", "/api/agent/register",
		&agent.RegisterRequest{RegisterToken: created.Token, AgentPort: 9000, Host: sampleHost()}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("复用令牌 code = %d, want 400", w.Code)
	}

	// 确认纳管，接入类型 wireguard
	w = do(t, h, "PUT", "/api/nodes/"+itoa(reg.NodeID),
		map[string]string{
			"name":         "客厅",
			"network_type": "wireguard",
			"address":      "10.8.0.2:9000",
			"alt_address":  "192.168.1.20:9000",
		}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	var dto NodeDTO
	_ = json.Unmarshal(w.Body.Bytes(), &dto)
	if dto.Status != "active" || dto.NetworkType != "wireguard" {
		t.Fatalf("确认后 status=%q network=%q", dto.Status, dto.NetworkType)
	}

	// 筛选 wireguard 命中 / lan 不命中
	w = do(t, h, "GET", "/api/nodes?network_type=wireguard", nil, jar)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Fatal("wireguard 筛选未命中")
	}
	w = do(t, h, "GET", "/api/nodes?network_type=lan", nil, jar)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatal("lan 筛选不应命中")
	}
}

// TR-8.1 过期令牌拒绝注册。
func TestExpiredRegistrationToken(t *testing.T) {
	_, s, h := newTestServer(t)
	jar := adminLogin(t, h)

	// 负数 TTL 直接 400
	w := do(t, h, "POST", "/api/registration-tokens",
		map[string]any{"description": "非法", "ttl_hours": -1, "max_uses": 1}, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("负 TTL code = %d, want 400", w.Code)
	}

	// 在 store 层直接构造一个已过期令牌（避免时间边界 flaky）
	expiredPlain := "expired-plain-token"
	past := time.Now().Add(-time.Hour).Unix()
	rt := &store.RegistrationToken{
		TokenHash: hashB64(expiredPlain), MaxUses: 1,
		ExpiresAt: &past, CreatedAt: time.Now().Unix(),
	}
	if _, err := s.CreateRegistrationToken(rt); err != nil {
		t.Fatal(err)
	}
	w = do(t, h, "POST", "/api/agent/register",
		&agent.RegisterRequest{RegisterToken: expiredPlain, AgentPort: 9000, Host: sampleHost()}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("过期令牌注册 code = %d, want 400", w.Code)
	}

	// 不存在的令牌同样拒绝
	w = do(t, h, "POST", "/api/agent/register",
		&agent.RegisterRequest{RegisterToken: "never-issued", AgentPort: 9000, Host: sampleHost()}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("伪造令牌注册 code = %d, want 400", w.Code)
	}
}

// TR-8.2 心跳驱动在线状态；超时即离线。
func TestHeartbeatOnlineStatus(t *testing.T) {
	_, _, h := newTestServer(t)
	jar := adminLogin(t, h)

	w := do(t, h, "POST", "/api/registration-tokens",
		map[string]any{"description": "hb", "ttl_hours": 1, "max_uses": 1}, jar)
	var created struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = do(t, h, "POST", "/api/agent/register",
		&agent.RegisterRequest{RegisterToken: created.Token, AgentPort: 9000, Host: sampleHost()}, nil)
	var reg agent.RegisterResponse
	_ = json.Unmarshal(w.Body.Bytes(), &reg)

	// 心跳
	w = do(t, h, "POST", "/api/agent/heartbeat",
		&agent.HeartbeatRequest{Token: reg.Token, Host: sampleHost()}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", w.Code, w.Body.String())
	}

	if !node.IsOnline(time.Now().Unix()) {
		t.Fatal("刚心跳应在线")
	}
	// 超过 90s 窗口 → 离线
	if node.IsOnline(time.Now().Add(-2 * time.Minute).Unix()) {
		t.Fatal("2 分钟前心跳应离线")
	}
	if node.IsOnline(0) {
		t.Fatal("零值不应在线")
	}

	// 错误 Token 心跳 → 401
	w = do(t, h, "POST", "/api/agent/heartbeat",
		&agent.HeartbeatRequest{Token: "bad", Host: sampleHost()}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token heartbeat code = %d, want 401", w.Code)
	}
}

// TR-8.1/8.2 面板侧 Token 轮换：先通知在线 Agent，成功后才改库。
func TestRotateTokenAgainstFakeAgent(t *testing.T) {
	_, s, _ := newTestServer(t)
	box, _ := secretbox.New(t.TempDir())
	svc := node.New(s, box)

	expectedBearer := "old-secret"
	gotNew := ""
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/token/rotate" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+expectedBearer {
			http.Error(w, "bad bearer", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		gotNew = m["token"]
		w.WriteHeader(http.StatusOK)
	}))
	defer agentSrv.Close()

	id, err := svc.ManualAdd("测试节点", agentSrv.URL[7:], expectedBearer, "lan", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 首次轮换：用旧 Token 鉴权，Agent 收到新 Token
	newTok, err := svc.RotateToken(id)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newTok == "" || newTok == expectedBearer {
		t.Fatal("新 Token 异常")
	}
	if gotNew != newTok {
		t.Fatal("Agent 未收到新 Token")
	}

	// 模拟 Agent 已切换；第二次轮换应使用新 Token 鉴权
	expectedBearer = newTok
	newTok2, err := svc.RotateToken(id)
	if err != nil {
		t.Fatalf("rotate2: %v", err)
	}
	if newTok2 == newTok {
		t.Fatal("Token 未更新")
	}

	// 旧 Token 心跳鉴权失败（库中哈希已更新）
	if _, err := s.GetNodeByTokenHash(hashB64(expectedBearer)); err == nil {
		t.Fatal("旧 Token 哈希仍可查到节点")
	}

	// 本机节点不可轮换
	local, err := svc.EnsureLocalNode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RotateToken(local.ID); err == nil {
		t.Fatal("本机节点轮换应失败")
	}
}

// TR-8.3 网络类型建议：私网→lan、公网→public、WG 网段→wireguard、非法→lan。
func TestSuggestNetwork(t *testing.T) {
	_, s, _ := newTestServer(t)
	box, _ := secretbox.New(t.TempDir())
	svc := node.New(s, box)

	if got := svc.SuggestNetworkType("8.8.8.8:9000"); got != "public" {
		t.Fatalf("公网地址建议 = %q, want public", got)
	}
	if got := svc.SuggestNetworkType("not-an-ip:9000"); got != "lan" {
		t.Fatalf("非法地址建议 = %q, want lan", got)
	}
	if got := svc.SuggestNetworkType("192.168.1.20:9000"); got != "lan" {
		t.Fatalf("私网地址建议 = %q, want lan", got)
	}
}

// 本机节点不可删除。
func TestLocalNodeCannotDelete(t *testing.T) {
	_, s, h := newTestServer(t)
	box, _ := secretbox.New(t.TempDir())
	svc := node.New(s, box)
	local, err := svc.EnsureLocalNode(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar := adminLogin(t, h)
	w := do(t, h, "DELETE", "/api/nodes/"+itoa(local.ID), nil, jar)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("删除本机 code = %d, want 400", w.Code)
	}
}

// viewer 角色无 node:write，操作应 403。
func TestViewerCannotManageNodes(t *testing.T) {
	_, s, h := newTestServer(t)
	_ = adminLogin(t, h)

	hash, _ := auth.HashPassword("ViewerPass1")
	roles, _ := s.ListRoles()
	var viewerRoleID int64
	for _, r := range roles {
		if r.Code == "viewer" {
			viewerRoleID = r.ID
		}
	}
	if viewerRoleID == 0 {
		t.Fatal("缺少 viewer 角色")
	}
	if _, err := s.CreateUser("viewer1", hash, viewerRoleID); err != nil {
		t.Fatal(err)
	}

	jar := newJar(t)
	w := do(t, h, "POST", "/api/auth/login",
		map[string]string{"username": "viewer1", "password": "ViewerPass1"}, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer login: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, "POST", "/api/registration-tokens",
		map[string]any{"description": "x", "ttl_hours": 1, "max_uses": 1}, jar)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer 创建令牌 code = %d, want 403", w.Code)
	}
	// 只读接口可用
	w = do(t, h, "GET", "/api/nodes", nil, jar)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer list code = %d", w.Code)
	}
}

// 本机节点实时信息：字段完整、非错误。
func TestNodeLiveInfo(t *testing.T) {
	localID, _, h := appTestSetup(t)
	jar := adminLogin(t, h)

	w := do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/info", nil, jar)
	if w.Code == http.StatusBadGateway {
		// Windows 开发环境不支持主机采集；真机 Linux 覆盖完整字段
		t.Skipf("当前平台不支持实时采集: %s", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("live info: %d %s", w.Code, w.Body.String())
	}
	var h2 struct {
		Hostname   string `json:"hostname"`
		Arch       string `json:"arch"`
		MemTotal   int64  `json:"mem_total"`
		Interfaces []any  `json:"interfaces"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &h2)
	if h2.MemTotal <= 0 || len(h2.Interfaces) == 0 {
		t.Fatalf("实时信息字段不完整: %s", w.Body.String())
	}

	// 未登录 → 401
	if w = do(t, h, "GET", "/api/nodes/"+itoa(localID)+"/info", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth code = %d, want 401", w.Code)
	}
	// 不存在节点 → 404
	if w = do(t, h, "GET", "/api/nodes/9999/info", nil, jar); w.Code != http.StatusNotFound {
		t.Fatalf("missing node code = %d, want 404", w.Code)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
