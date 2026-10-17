package agent

import (
	"net/http"
	"testing"
)

// TestPanelHTTPClientIsCached 心跳/注册用的客户端必须复用。
//
// 每次心跳新建 Transport 会持续泄漏：Transport 的空闲连接常驻
// readLoop/writeLoop 两个 goroutine，goroutine 又是 GC 根并反向引用 Transport，
// 于是它永远不被回收；零值 IdleConnTimeout 还意味着空闲连接永不超时。
func TestPanelHTTPClientIsCached(t *testing.T) {
	// 默认配置：直接用 net/http 的共享 Transport，不额外造对象
	def1 := panelHTTPClient(false, "")
	def2 := panelHTTPClient(false, "")
	if def1 != def2 {
		t.Fatal("默认配置应复用同一个客户端实例")
	}
	if def1.Transport != nil {
		t.Fatalf("默认配置应使用 net/http 共享 Transport（nil），实际 %T", def1.Transport)
	}

	// 指纹固定：pin 归一化后（去前缀/小写）应命中同一个客户端
	pinA := panelHTTPClient(false, "sha256:ABCDEF0123")
	pinB := panelHTTPClient(false, "abcdef0123")
	if pinA != pinB {
		t.Fatal("同一指纹（仅大小写/前缀不同）应复用同一个客户端")
	}
	if pinA == def1 {
		t.Fatal("pin 模式不能复用默认客户端（会丢失指纹校验）")
	}
	tr, ok := pinA.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("pin 客户端 Transport 类型异常: %T", pinA.Transport)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Fatal("IdleConnTimeout 必须 > 0，否则空闲连接永不释放")
	}

	// 不同 pin 必须各用各的
	if other := panelHTTPClient(false, "ffffeeee"); other == pinA {
		t.Fatal("不同指纹不应共用客户端")
	}

	// insecure 模式单独缓存
	in1 := panelHTTPClient(true, "")
	in2 := panelHTTPClient(true, "")
	if in1 != in2 {
		t.Fatal("insecure 配置应复用同一个客户端")
	}
	if in1 == def1 || in1 == pinA {
		t.Fatal("insecure 模式不应复用其它模式的客户端")
	}
}
