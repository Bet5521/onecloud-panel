package sshx

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// newProbeTestServer 启动一个仅用于主机密钥探测的最小 SSH 服务端，
// 返回监听地址与主机密钥签名者。
func newProbeTestServer(t *testing.T) (addr string, signer ssh.Signer) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	signer, err = ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("构造签名者失败: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				_ = conn
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(ssh.UnknownChannelType, "test server")
				}
			}()
		}
	}()
	return ln.Addr().String(), signer
}

// splitAddr 拆出 host/port，供 DialConfig 使用。
func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("拆分地址失败: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}
	return host, port
}

// Probe 应能在不认证的情况下拿到主机指纹，并正确判定首次/非首次。
func TestProbeAgainstServer(t *testing.T) {
	addr, signer := newProbeTestServer(t)
	host, port := splitAddr(t, addr)
	file := filepath.Join(t.TempDir(), "known_hosts")

	info, err := Probe(DialConfig{Host: host, Port: port, KnownHostsFile: file}, nil)
	if err != nil {
		t.Fatalf("Probe 首次: %v", err)
	}
	if want := ssh.FingerprintSHA256(signer.PublicKey()); info.Fingerprint != want {
		t.Fatalf("指纹不匹配: got %s want %s", info.Fingerprint, want)
	}
	if info.KeyType == "" {
		t.Fatal("KeyType 不应为空")
	}
	if info.Known || info.Changed {
		t.Fatalf("首次探测不应判定为已知/变更: %+v", info)
	}

	// 记录后再次探测：应自动判定为非首次（known=true, changed=false）。
	if err := AppendKnownHost(file, host, port, signer.PublicKey()); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	info, err = Probe(DialConfig{Host: host, Port: port, KnownHostsFile: file}, nil)
	if err != nil {
		t.Fatalf("Probe 二次: %v", err)
	}
	if !info.Known || info.Changed {
		t.Fatalf("记录后应判定为非首次: %+v", info)
	}
}

// AppendKnownHost 幂等，且 knownHostState 能识别密钥变更。
func TestAppendKnownHostIdempotent(t *testing.T) {
	addr, signer := newProbeTestServer(t)
	host, port := splitAddr(t, addr)
	file := filepath.Join(t.TempDir(), "nested", "known_hosts")

	if known, changed := knownHostState(file, addr, signer.PublicKey()); known || changed {
		t.Fatalf("文件不存在时不应判定为已知: known=%v changed=%v", known, changed)
	}

	if err := AppendKnownHost(file, host, port, signer.PublicKey()); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	known, changed := knownHostState(file, addr, signer.PublicKey())
	if !known || changed {
		t.Fatalf("写入后应判定为已知: known=%v changed=%v", known, changed)
	}

	// 幂等：重复写入不产生新行。
	if err := AppendKnownHost(file, host, port, signer.PublicKey()); err != nil {
		t.Fatalf("AppendKnownHost 重复: %v", err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("读取 known_hosts: %v", err)
	}
	lines := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("重复写入应保持 1 行，实际 %d 行: %q", lines, string(b))
	}

	// 密钥变更：应判定 changed（而非 known）。
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成替代密钥失败: %v", err)
	}
	otherSigner, err := ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatalf("构造替代签名者失败: %v", err)
	}
	known, changed = knownHostState(file, addr, otherSigner.PublicKey())
	if known || !changed {
		t.Fatalf("密钥变化应判定 changed: known=%v changed=%v", known, changed)
	}
}

// pin 策略校验通过后应触发 OnHostKey 回调（供落库记录）。
func TestHostKeyCallbackPinFiresOnHostKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("构造签名者失败: %v", err)
	}
	want := ssh.FingerprintSHA256(signer.PublicKey())

	cfg := DialConfig{HostKeyPolicy: PolicyPin, HostKeyFingerprint: want}
	var captured ssh.PublicKey
	cfg.OnHostKey = func(k ssh.PublicKey) { captured = k }

	cb, err := hostKeyCallback(PolicyPin, cfg, "example.local", nil)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb("example.local", nil, signer.PublicKey()); err != nil {
		t.Fatalf("校验应通过: %v", err)
	}
	if captured == nil || ssh.FingerprintSHA256(captured) != want {
		t.Fatal("校验通过后未回调 OnHostKey")
	}
}

// 未提供指纹时应回报 HostKeyError（且不触发 OnHostKey）。
func TestHostKeyCallbackPinUnknownReportsFingerprint(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("构造签名者失败: %v", err)
	}
	called := false
	cfg := DialConfig{
		HostKeyPolicy: PolicyPin,
		OnHostKey:     func(ssh.PublicKey) { called = true },
	}
	cb, err := hostKeyCallback(PolicyPin, cfg, "example.local", nil)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	err = cb("example.local", nil, signer.PublicKey())
	var hk *HostKeyError
	if err == nil {
		t.Fatal("未知指纹应返回错误")
	}
	if !errors.As(err, &hk) {
		t.Fatalf("错误类型应为 HostKeyError，实际 %T", err)
	}
	if hk.Fingerprint != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatalf("回报指纹不符: %s", hk.Fingerprint)
	}
	if called {
		t.Fatal("未确认时不应触发 OnHostKey")
	}
}

// KnownHostRecorder 在文件路径为空时返回 nil（视为不记录）。
func TestKnownHostRecorderEmptyPath(t *testing.T) {
	if fn := KnownHostRecorder("  ", "h", 22); fn != nil {
		t.Fatal("空路径应返回 nil")
	}
}
