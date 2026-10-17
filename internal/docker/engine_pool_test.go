package docker

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestLocalHTTPClientIsShared 同一 socket 必须返回同一个客户端实例。
//
// 这是内存泄漏的回归防线：每次新建 Transport 都会留下永不回收的空闲连接
// 与其 readLoop/writeLoop goroutine（详见 LocalHTTPClient 注释）。
func TestLocalHTTPClientIsShared(t *testing.T) {
	a := LocalHTTPClient("")
	b := LocalHTTPClient("")
	if a != b {
		t.Fatal("同一 socket 应返回同一个 *http.Client 实例（否则每次调用都会泄漏连接池）")
	}
	if c := LocalHTTPClient("/tmp/other-docker.sock"); c == a {
		t.Fatal("不同 socket 不应共用同一个客户端")
	}
}

// TestLocalTransportHasIdleTimeout 空闲连接必须设上界，避免忘了清理时无限堆积。
func TestLocalTransportHasIdleTimeout(t *testing.T) {
	tr, ok := LocalHTTPClient("").Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型应为 *http.Transport，实际 %T", LocalHTTPClient("").Transport)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Fatal("IdleConnTimeout 必须 > 0：零值意味着空闲连接永不超时")
	}
	if tr.MaxIdleConns <= 0 || tr.MaxIdleConnsPerHost <= 0 {
		t.Fatal("必须限制空闲连接数，否则突发流量后连接池无上界")
	}
}

// TestDetectLocalDoesNotLeakGoroutines 反复探测 Docker 不应线性增长 goroutine。
//
// 用真实 unix socket 起一个假 Engine，连续 Ping 100 轮；修复前每轮泄漏
// 一个连接及其两个 goroutine（100 轮 ≈ +200），修复后应稳定在常数级。
func TestDetectLocalDoesNotLeakGoroutines(t *testing.T) {
	// 不用 t.TempDir()：Windows 上 AF_UNIX 的 socket 文件在句柄释放后仍可能
	// 短时间无法删除，会让 cleanup 失败；这里是可丢弃的临时目录，尽力清理即可。
	dir, err := os.MkdirTemp("", "ocp-docker-pool-")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "docker.sock")
	// 部分平台/旧内核不支持 AF_UNIX，直接跳过（CI 是 Linux，会真正执行）。
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("当前环境不支持 unix socket（%v），跳过连接池泄漏回归", err)
	}
	defer ln.Close()
	_ = os.Chmod(sock, 0o600)

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"24.0.0","ApiVersion":"1.43"}`))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	// 共享客户端会保留一条空闲连接（它持有 socket 文件句柄），
	// 必须在用例结束前主动关掉，否则 Windows 上临时目录无法删除。
	defer func() {
		LocalHTTPClient(sock).CloseIdleConnections()
		_ = srv.Close()
		_ = ln.Close()
		time.Sleep(100 * time.Millisecond)
	}()

	// 预热：让共享连接池先建立那条空闲连接，避免把首次建连算进基线。
	if _, msg := DetectLocalWithSocket(sock); msg != "" {
		t.Fatalf("预热探测失败: %s", msg)
	}
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	base := runtime.NumGoroutine()

	for i := 0; i < 100; i++ {
		if _, msg := DetectLocalWithSocket(sock); msg != "" {
			t.Fatalf("第 %d 轮探测失败: %s", i, msg)
		}
	}
	// 给可能存在的 goroutine 退出留点时间，再采样
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	// 允许少量波动（GC/网络轮询等），但绝不能是「每轮 +2」的线性增长。
	if after-base > 10 {
		t.Fatalf("goroutine 数量从 %d 增长到 %d（+%d）：探活路径仍在泄漏连接", base, after, after-base)
	}
}

// TestDetectLocalWithSocketTimeout 探测必须有超时上界，不能因为 socket 无响应挂死。
func TestDetectLocalWithSocketTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// 不存在的 socket：应快速失败而不是阻塞
	_, msg := detectBySocket(ctx, filepath.Join(t.TempDir(), "missing.sock"))
	if msg == "" {
		t.Fatal("不可达的 socket 应返回失败原因")
	}
}
