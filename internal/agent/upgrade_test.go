package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"onecloud-panel/internal/executor"
)

// errStubVerify 用于模拟「新二进制试运行失败（异架构/损坏）」。
var errStubVerify = errors.New("stub: 新二进制无法运行")

// fakeExec 实现 executor.Executor + Downloader，用于在不接触真实文件系统的
// 前提下驱动自升级流程（记录参数、按需注入错误）。
type fakeExec struct {
	mu           sync.Mutex
	downloadDest string // Download 收到的目标路径（用于断言同目录约束）
	downloadURL  string
	downloadErr  error
	execCalls    [][]string
	execResult   *executor.Result
	execErr      error
}

func (f *fakeExec) Exec(_ context.Context, name string, args ...string) (*executor.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCalls = append(f.execCalls, append([]string{name}, args...))
	if f.execErr != nil {
		return nil, f.execErr
	}
	if f.execResult != nil {
		return f.execResult, nil
	}
	return &executor.Result{ExitCode: 0}, nil
}

func (f *fakeExec) ExecStream(context.Context, io.Writer, string, ...string) (int, error) {
	return 0, nil
}
func (f *fakeExec) ReadFile(string) ([]byte, error) { return nil, nil }
func (f *fakeExec) WriteFile(string, []byte) error  { return nil }
func (f *fakeExec) Exists(string) (bool, error)     { return false, nil }

func (f *fakeExec) Download(_ context.Context, url, dest string, _ os.FileMode, _ string, _ io.Writer) error {
	f.mu.Lock()
	f.downloadDest = dest
	f.downloadURL = url
	err := f.downloadErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(dest, []byte("NEW-BINARY"), 0o755)
}

func (f *fakeExec) dest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadDest
}

// 核心回归：升级临时文件必须落在**目标可执行文件同目录**。
//
// 这正是历史 bug 的根因——原实现用 os.CreateTemp("", ...) 落在 os.TempDir()，
// 而 systemd 单元的 PrivateTmp=true 让 /tmp 成为服务私有 tmpfs，与
// /usr/local/bin 不同文件系统，rename 必然 EXDEV，随后的覆盖写又必然 ETXTBSY，
// 导致升级永远无法完成。
func TestApplyUpgradeTempFileLivesInExeDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "usr-local-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "onecloud-panel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	fe := &fakeExec{}
	old := verifyBinaryFn
	verifyBinaryFn = func(context.Context, string) error { return nil } // 测试不依赖真实可执行文件
	defer func() { verifyBinaryFn = old }()

	if err := applyUpgrade(context.Background(), exe, fe, "/api/agent-binary?t=x", ""); err != nil {
		t.Fatalf("applyUpgrade: %v", err)
	}

	dest := fe.dest()
	if dest == "" {
		t.Fatal("未触发下载")
	}
	if got, want := filepath.Dir(dest), filepath.Dir(exe); got != want {
		t.Fatalf("临时文件目录 = %q，期望与目标同目录 %q（跨文件系统会导致 rename EXDEV）", got, want)
	}
	if !strings.HasPrefix(filepath.Base(dest), "."+filepath.Base(exe)+".new-") {
		t.Fatalf("临时文件名 = %q，应为本实现自己的 .new-* 命名", filepath.Base(dest))
	}
	// 替换完成后目标内容应为新二进制，且临时文件不应残留。
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW-BINARY" {
		t.Fatalf("替换后内容 = %q，期望 NEW-BINARY", got)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("替换后不应残留临时文件 %s", dest)
	}
}

// 下载失败必须作为错误返回（由 handler 转成 HTTP 500 给面板），而不是静默跳过。
func TestApplyUpgradeSurfacesDownloadError(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "onecloud-panel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	fe := &fakeExec{downloadErr: io.ErrUnexpectedEOF}

	err := applyUpgrade(context.Background(), exe, fe, "/x", "")
	if err == nil {
		t.Fatal("下载失败应返回错误")
	}
	if !strings.Contains(err.Error(), "下载升级包失败") {
		t.Fatalf("错误信息应点明下载失败，实际 %v", err)
	}
	// 失败时目标二进制必须保持原样（保证任何失败都不破坏现有 Agent）。
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD" {
		t.Fatalf("失败后不应改动目标二进制，实际 %q", got)
	}
}

// 试运行校验失败（异架构/损坏产物）必须中止替换，避免把节点 Agent 换成起不来的二进制。
func TestApplyUpgradeAbortsWhenVerifyFails(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "onecloud-panel")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := verifyBinaryFn
	verifyBinaryFn = func(context.Context, string) error { return errStubVerify }
	defer func() { verifyBinaryFn = old }()

	err := applyUpgrade(context.Background(), exe, &fakeExec{}, "/x", "")
	if err == nil {
		t.Fatal("试运行失败应返回错误")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD" {
		t.Fatalf("校验失败时不应改动目标二进制，实际 %q", got)
	}
}

// verifyBinary 对非面板二进制（无法运行 / 输出不含产品标识）必须报错。
func TestVerifyBinaryRejectsForeignExecutable(t *testing.T) {
	// 用普通文本文件充当「损坏/异架构的产物」：执行它必然失败。
	// （刻意不拿测试二进制本身做样本——它可能真的跑起来，浪费一个测试周期。）
	p := filepath.Join(t.TempDir(), "not-a-binary")
	if err := os.WriteFile(p, []byte("this is not an executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyBinary(context.Background(), p); err == nil {
		t.Fatal("非可执行文件应校验失败")
	}
}

// swapExecutable 主路径：同目录 rename，原子替换且保留可执行权限。
func TestSwapExecutableReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "onecloud-panel")
	tmp := filepath.Join(dir, ".onecloud-panel.new-1")
	if err := os.WriteFile(target, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapExecutable(tmp, target); err != nil {
		t.Fatalf("swapExecutable: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW" {
		t.Fatalf("目标内容 = %q，期望 NEW", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("临时文件应已被 rename 走")
	}
}

// swapExecutable 退路：首次 rename 失败时走「备份让位 → 就位」，且不得覆盖写目标。
func TestSwapExecutableFallbackKeepsTargetUsable(t *testing.T) {
	dir := t.TempDir()
	// 目标做成目录，迫使第一次 os.Rename(tmp, target) 失败，从而进入退路分支。
	target := filepath.Join(dir, "onecloud-panel")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".onecloud-panel.new-1")
	if err := os.WriteFile(tmp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapExecutable(tmp, target); err != nil {
		t.Fatalf("退路应能完成替换: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("替换后目标应可读: %v", err)
	}
	if string(got) != "NEW" {
		t.Fatalf("目标内容 = %q，期望 NEW", got)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatal("备份文件应已清理")
	}
}

// 升级结果落盘 → 读回，且重启后上报的 AppliedVersion 取当前运行版本。
func TestUpgradeResultPersistAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir, unit: "onecloud-panel-agent"}
	s.setUpgradeResult(UpgradeResult{TargetVersion: "v2.2.8", OK: true, At: 1700000000})

	loaded := loadUpgradeResult(dir)
	if loaded == nil {
		t.Fatal("应能读回落盘结果")
	}
	if loaded.TargetVersion != "v2.2.8" || !loaded.OK || loaded.At != 1700000000 {
		t.Fatalf("读回结果不符: %+v", loaded)
	}
	snap := s.upgradeSnapshot()
	if snap == nil || snap.AppliedVersion == "" {
		t.Fatalf("上报快照应带上当前运行版本: %+v", snap)
	}
}

func TestUpgradeResultEffective(t *testing.T) {
	cases := []struct {
		name string
		res  *UpgradeResult
		want bool
	}{
		{"nil", nil, false},
		{"failed", &UpgradeResult{OK: false, TargetVersion: "2.2.8"}, false},
		{"applied", &UpgradeResult{OK: true, TargetVersion: "v2.2.8", AppliedVersion: "2.2.8"}, true},
		{"no target", &UpgradeResult{OK: true}, true},
		{"replaced but old process still running", &UpgradeResult{OK: true, TargetVersion: "2.2.8", AppliedVersion: "2.2.7"}, false},
	}
	for _, c := range cases {
		if got := c.res.Effective(); got != c.want {
			t.Errorf("%s: Effective()=%v，期望 %v", c.name, got, c.want)
		}
	}
}

// 关键回归：升级失败必须**同步**回给面板（HTTP 500 + 真实原因），
// 而不是像旧实现那样先回 200 再把错误丢进日志。
func TestAgentUpgradeReturnsFailureToPanel(t *testing.T) {
	fe := &fakeExec{downloadErr: io.ErrUnexpectedEOF}
	s := &server{exec: fe, tok: &tokenHolder{}, unit: "onecloud-panel-agent", dataDir: t.TempDir()}

	r := httptest.NewRequest(http.MethodPost, "/v1/agent-upgrade",
		bytes.NewReader([]byte(`{"url":"/api/agent-binary?t=x","version":"9.9.9"}`)))
	w := httptest.NewRecorder()
	s.agentUpgrade(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("失败应返回 500，实际 %d（body=%s）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "下载升级包失败") {
		t.Fatalf("响应应带上真实失败原因，实际 %s", w.Body.String())
	}
	// 失败也要留下可上报的结果，便于界面展示。
	res := loadUpgradeResult(s.dataDir)
	if res == nil || res.OK || res.Error == "" {
		t.Fatalf("失败结果应落盘: %+v", res)
	}
	if res.TargetVersion != "9.9.9" {
		t.Fatalf("应记录面板指定的目标版本，实际 %q", res.TargetVersion)
	}
	// 失败后必须释放升级互斥，允许重试。
	if !s.upgrading.CompareAndSwap(false, true) {
		t.Fatal("失败后应释放升级互斥")
	}
}

// 升级中的并发请求应被拒绝，避免两个升级任务互相踩踏。
func TestAgentUpgradeRejectsConcurrentRequest(t *testing.T) {
	s := &server{exec: &fakeExec{}, tok: &tokenHolder{}, unit: "onecloud-panel-agent", dataDir: t.TempDir()}
	s.upgrading.Store(true)
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-upgrade",
		bytes.NewReader([]byte(`{"url":"/x"}`)))
	w := httptest.NewRecorder()
	s.agentUpgrade(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("并发升级应返回 409，实际 %d", w.Code)
	}
}

func TestAgentUpgradeRejectsBadRequest(t *testing.T) {
	s := &server{exec: &fakeExec{}, tok: &tokenHolder{}, dataDir: t.TempDir()}
	for _, body := range []string{`{`, `{"url":""}`, `{}`} {
		r := httptest.NewRequest(http.MethodPost, "/v1/agent-upgrade", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		s.agentUpgrade(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s 应返回 400，实际 %d", body, w.Code)
		}
	}
}

// Linux 专有回归：运行中的可执行文件**可以**被 rename 覆盖（修复后的做法），
// 但**不能**被带 O_TRUNC 的写入覆盖（旧实现的跨设备回退正是如此 → ETXTBSY，
// 所以那条回退路径在任何情况下都不可能成功）。
//
// 该用例只在 Linux 上跑（CI 为 ubuntu runner），Windows 无此语义故跳过。
func TestSwapExecutableHandlesRunningBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY 语义仅存在于 Linux")
	}
	src, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("找不到可用于充当运行中二进制的 sleep: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "running-tool")
	if err := copyFileBytes(src, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(target, "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("无法启动待测进程: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// 前提（旧回退路径的死因）：覆盖写运行中的二进制必然失败。
	if err := os.WriteFile(target, []byte("overwrite"), 0o755); err == nil {
		t.Error("覆盖写运行中的二进制居然成功了？旧实现的回退路径前提不成立，需重新分析")
	} else {
		t.Logf("确认：覆盖写运行中的二进制被拒绝（%v），旧回退路径不可能成功", err)
	}

	// 修复后的做法：同目录 rename 覆盖目录项，可安全替换运行中的二进制。
	tmp := filepath.Join(dir, "."+filepath.Base(target)+".new-1")
	if err := os.WriteFile(tmp, []byte("NEW-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapExecutable(tmp, target); err != nil {
		t.Fatalf("swapExecutable 应能替换运行中的二进制: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW-BINARY" {
		t.Fatalf("替换后内容 = %q，期望 NEW-BINARY", got)
	}
}

func copyFileBytes(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

// 心跳必须携带升级结果，面板才能把失败原因展示出来。
func TestHeartbeatCarriesUpgradeResult(t *testing.T) {
	var got HeartbeatRequest
	mux := http.NewServeMux()
	mux.HandleFunc(pathHeartbeat, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(&HeartbeatResponse{OK: true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	up := &UpgradeResult{TargetVersion: "2.2.8", OK: false, Error: "text file busy", At: 1700000000}
	if _, err := Heartbeat(context.Background(), srv.URL, "tok", nil, up, false, ""); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if got.Upgrade == nil {
		t.Fatal("心跳应带上升级结果")
	}
	if got.Upgrade.Error != "text file busy" || got.Upgrade.TargetVersion != "2.2.8" {
		t.Fatalf("升级结果不符: %+v", got.Upgrade)
	}
}
