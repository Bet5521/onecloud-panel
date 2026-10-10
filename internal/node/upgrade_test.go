package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/agentclient"
	"onecloud-panel/internal/system"
)

// fakeAgent 模拟节点上的 Agent HTTP 服务，用于驱动「面板下发升级」全链路。
type fakeAgent struct {
	proto        int    // 自升级应答里的 proto（0 = 旧版 Agent，不返回该字段）
	upgradeErr   string // 非空时 /v1/agent-upgrade 返回 500 + 该原因
	selfPathOut  string // readlink 返回的自身可执行文件路径
	execExitCode int    // 就位脚本的退出码

	mu           sync.Mutex
	upgradeCalls int
	downloadDest []string
	execScripts  []string
	systemctl    []string
}

func (f *fakeAgent) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/agent-upgrade", func(w http.ResponseWriter, r *http.Request) {
		var req agent.UpgradeReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.upgradeCalls++
		f.mu.Unlock()
		if f.upgradeErr != "" {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": f.upgradeErr})
			return
		}
		body := map[string]any{"status": "upgrade_started", "current_version": "0.0.1"}
		if f.proto > 0 {
			body["proto"] = f.proto
		}
		_ = json.NewEncoder(w).Encode(body)
	})

	mux.HandleFunc("POST /v1/exec", func(w http.ResponseWriter, r *http.Request) {
		var req agent.ExecReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		script := ""
		if len(req.Args) > 1 {
			script = req.Args[1]
		}
		f.mu.Lock()
		f.execScripts = append(f.execScripts, script)
		f.mu.Unlock()

		out := ""
		code := 0
		switch {
		case strings.Contains(script, "readlink"):
			out = f.selfPathOut + "\n"
		default:
			code = f.execExitCode
			out = "replaced=" + f.selfPathOut + " unit=onecloud-panel-agent.service\n"
		}
		_ = json.NewEncoder(w).Encode(agent.ExecResp{ExitCode: code, Output: out})
	})

	mux.HandleFunc("POST /v1/download", func(w http.ResponseWriter, r *http.Request) {
		var req agent.DownloadReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.downloadDest = append(f.downloadDest, req.Dest)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(agent.DownloadResp{OK: true})
	})

	mux.HandleFunc("POST /v1/systemctl", func(w http.ResponseWriter, r *http.Request) {
		var req agent.SystemctlReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.systemctl = append(f.systemctl, req.Action+" "+req.Unit)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(agent.ExecResp{ExitCode: 0})
	})

	return mux
}

// newFakeNode 注册一个节点并把它的地址指向 fakeAgent 服务。
func newFakeNode(t *testing.T, fa *fakeAgent) (*Service, int64) {
	t.Helper()
	svc, _ := newSvc(t)
	srv := httptest.NewServer(fa.handler())
	t.Cleanup(srv.Close)

	raw, _, err := svc.CreateToken("t", time.Hour, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var hostPort string
	var agentPort int
	if _, err := fmt.Sscanf(strings.TrimPrefix(srv.URL, "http://"), "%s", &hostPort); err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := strings.Cut(hostPort, ":")
	if _, err := fmt.Sscanf(portStr, "%d", &agentPort); err != nil {
		t.Fatal(err)
	}
	id, _, err := svc.Register(&agent.RegisterRequest{
		RegisterToken: raw, AgentPort: agentPort,
		Host: &system.HostInfo{Hostname: "node1", Arch: "armv7l", AgentVersion: "0.0.1"},
	}, host+":1234")
	if err != nil {
		t.Fatal(err)
	}
	return svc, id
}

// 新版 Agent：走它自己的同步升级，面板不再插手（不下载、不 exec、不重启）。
func TestUpgradeAgentUsesAgentSelfUpgradeWhenProtoSupported(t *testing.T) {
	fa := &fakeAgent{proto: agent.UpgradeProto, selfPathOut: "/usr/local/bin/onecloud-panel"}
	svc, id := newFakeNode(t, fa)

	if err := svc.UpgradeAgent(context.Background(), id); err != nil {
		t.Fatalf("UpgradeAgent: %v", err)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if fa.upgradeCalls != 1 {
		t.Fatalf("应下发一次自升级，实际 %d", fa.upgradeCalls)
	}
	if len(fa.downloadDest) != 0 || len(fa.execScripts) != 0 || len(fa.systemctl) != 0 {
		t.Fatalf("新版 Agent 不应触发面板驱动替换: dest=%v scripts=%v systemctl=%v",
			fa.downloadDest, fa.execScripts, fa.systemctl)
	}
}

// 旧版 Agent（升级实现必然失败）：面板必须改走冷替换，否则节点永远升不上去。
func TestUpgradeAgentColdSwapsLegacyAgent(t *testing.T) {
	exe := "/usr/local/bin/onecloud-panel"
	fa := &fakeAgent{selfPathOut: exe}
	svc, id := newFakeNode(t, fa)

	if err := svc.UpgradeAgent(context.Background(), id); err != nil {
		t.Fatalf("UpgradeAgent: %v", err)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()

	// 1) 升级包必须下到**同目录的 .new**（不同名 → 规避 ETXTBSY）
	if len(fa.downloadDest) != 1 || fa.downloadDest[0] != exe+".new" {
		t.Fatalf("升级包落点 = %v，期望 %s.new", fa.downloadDest, exe)
	}
	// 2) 就位脚本必须用 mv（rename 覆盖目录项）而非覆盖写
	if len(fa.execScripts) != 2 {
		t.Fatalf("应有两段脚本（定位路径 + 就位），实际 %d: %v", len(fa.execScripts), fa.execScripts)
	}
	script := fa.execScripts[1]
	if !strings.Contains(script, `mv -f "$NEW" "$EXE"`) {
		t.Fatalf("就位脚本应使用 mv 原子替换，实际：%s", script)
	}
	if strings.Contains(script, `>"$EXE"`) || strings.Contains(script, "cp ") {
		t.Fatalf("就位脚本不得覆盖写目标（会 ETXTBSY）：%s", script)
	}
	if !strings.Contains(script, `"$NEW" version`) {
		t.Fatalf("就位前应试运行校验架构：%s", script)
	}
	// 3) 替换后重启单元（单元名取自 cgroup 反查结果）
	if len(fa.systemctl) != 1 || fa.systemctl[0] != "restart onecloud-panel-agent.service" {
		t.Fatalf("应重启 Agent 单元，实际 %v", fa.systemctl)
	}
}

// 新版 Agent 明确报错时，真实原因必须原样透出给调用方（界面据此提示）。
func TestUpgradeAgentPropagatesAgentError(t *testing.T) {
	fa := &fakeAgent{proto: agent.UpgradeProto, upgradeErr: "下载升级包失败：在 /usr/local/bin 创建临时文件失败"}
	svc, id := newFakeNode(t, fa)

	err := svc.UpgradeAgent(context.Background(), id)
	if err == nil {
		t.Fatal("Agent 返回失败时 UpgradeAgent 应报错")
	}
	if !strings.Contains(err.Error(), "创建临时文件失败") {
		t.Fatalf("应透出 Agent 的真实失败原因，实际：%v", err)
	}
	// 失败时不应自行改走冷替换（原因是明确的，不需要兜底掩盖）。
	if len(fa.downloadDest) != 0 {
		t.Fatalf("明确失败时不应触发冷替换：%v", fa.downloadDest)
	}
}

// 冷替换中若 Agent 返回非零退出码（如新包架构不匹配），必须报错且带上原因。
func TestColdSwapAgentReportsFailure(t *testing.T) {
	fa := &fakeAgent{selfPathOut: "/usr/local/bin/onecloud-panel", execExitCode: 10}
	svc, id := newFakeNode(t, fa)

	err := svc.UpgradeAgent(context.Background(), id)
	if err == nil {
		t.Fatal("退出码非零时 UpgradeAgent 应报错")
	}
	if !strings.Contains(err.Error(), "替换二进制失败") {
		t.Fatalf("错误信息应点明替换失败，实际：%v", err)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if len(fa.systemctl) != 0 {
		t.Fatalf("替换失败时不应重启服务：%v", fa.systemctl)
	}
}

func TestParseUnitFromOutput(t *testing.T) {
	cases := []struct{ in, want string }{
		{"replaced=/usr/local/bin/x unit=onecloud-panel-agent.service", "onecloud-panel-agent.service"},
		{"replaced=/usr/local/bin/x unit=my-agent.service", "my-agent.service"},
		{"replaced=/usr/local/bin/x unit=", "onecloud-panel-agent.service"},
		{"", "onecloud-panel-agent.service"},
	}
	for _, c := range cases {
		if got := parseUnitFromOutput(c.in); got != c.want {
			t.Errorf("parseUnitFromOutput(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestShQuoteEscapesSingleQuote(t *testing.T) {
	if got := shQuote("/a/b"); got != `'/a/b'` {
		t.Fatalf("shQuote = %q", got)
	}
	if got := shQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("含单引号应正确转义，实际 %q", got)
	}
}

// 冷替换的 Client 必须能正常构造（地址归一化：私网回退 http）。
func TestColdSwapClientTargetsAgent(t *testing.T) {
	c := agentclient.New("10.0.0.9:9000", "tok")
	if c == nil {
		t.Fatal("client 为空")
	}
}
