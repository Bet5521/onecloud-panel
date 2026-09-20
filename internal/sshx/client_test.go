package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Quote 必须把单引号安全转义，防止远端 shell 注入。
func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"", "''"},
		{"a'b", `'a'\''b'`},
		{"'; rm -rf / #", `''\''; rm -rf / #'`},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Fatalf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	if got := NormalizeFingerprint("  SHA256:abc=  "); got != "SHA256:abc=" {
		t.Fatalf("NormalizeFingerprint = %q", got)
	}
	if got := NormalizeFingerprint(""); got != "" {
		t.Fatalf("空串归一化 = %q", got)
	}
}

// classifyDialError 应把底层错误转换为可读原因。
func TestClassifyDialError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"DNS 解析失败", &net.DNSError{Err: "no such host", Name: "nope.invalid"}, "解析失败"},
		{"认证失败", errors.New("ssh: handshake failed: ssh: unable to authenticate, " +
			"attempted methods [none password], no supported methods remain"), "认证失败"},
		{"拒绝连接", errors.New("dial tcp 127.0.0.1:22: connect: connection refused"), "拒绝连接"},
		{"主机不可达", errors.New("dial tcp: no route to host"), "不可达"},
		{"超时", errors.New("dial tcp 10.0.0.1:22: i/o timeout"), "超时"},
		{"未知错误", errors.New("boom"), "SSH 连接失败"},
	}
	for _, c := range cases {
		got := classifyDialError(c.err, "10.0.0.1:22")
		if !strings.Contains(got.Error(), c.want) {
			t.Fatalf("%s: got %q, want 含 %q", c.name, got.Error(), c.want)
		}
	}
}

// pin 策略：无指纹时返回 HostKeyError 供用户核对；指纹不符时拒绝连接。
func TestHostKeyCallbackPin(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	want := ssh.FingerprintSHA256(key)

	cb, err := hostKeyCallback(PolicyPin, DialConfig{}, "example.internal", nil)
	if err != nil {
		t.Fatalf("pin 回调构建失败: %v", err)
	}
	var addr net.Addr
	err = cb("example.internal", addr, key)
	var hk *HostKeyError
	if !errors.As(err, &hk) {
		t.Fatalf("首次连接应返回 HostKeyError, got %v", err)
	}
	if hk.Fingerprint != want || hk.Host != "example.internal" {
		t.Fatalf("HostKeyError 内容不符: %+v", hk)
	}

	ok, err := hostKeyCallback(PolicyPin, DialConfig{HostKeyFingerprint: want}, "example.internal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ok("example.internal", addr, key); err != nil {
		t.Fatalf("指纹一致应放行, got %v", err)
	}

	bad, _ := hostKeyCallback(PolicyPin,
		DialConfig{HostKeyFingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"example.internal", nil)
	err = bad("example.internal", addr, key)
	if err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("指纹不符应拒绝, got %v", err)
	}
}

// strict 策略必须有 known_hosts 文件。
func TestHostKeyCallbackStrict(t *testing.T) {
	if _, err := hostKeyCallback(PolicyStrict, DialConfig{}, "h", nil); err == nil {
		t.Fatal("strict 缺少 known_hosts 应报错")
	}
	if _, err := hostKeyCallback(PolicyStrict,
		DialConfig{KnownHostsFile: t.TempDir() + "/nope_known_hosts"}, "h", nil); err == nil {
		t.Fatal("known_hosts 不存在应报错")
	}
}

// Dial 入参校验：主机/用户名/端口/策略/凭据。
func TestDialValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  DialConfig
		want string
	}{
		{"缺主机", DialConfig{User: "u", Password: "p"}, "主机地址必填"},
		{"缺用户名", DialConfig{Host: "h", Password: "p"}, "用户名必填"},
		{"端口越界", DialConfig{Host: "h", User: "u", Port: 70000, Password: "p"}, "端口非法"},
		{"策略非法", DialConfig{Host: "h", User: "u", Password: "p", HostKeyPolicy: "loose"}, "pin/strict"},
		{"缺凭据", DialConfig{Host: "h", User: "u"}, "密码或私钥"},
		{"私钥损坏", DialConfig{Host: "h", User: "u", PrivateKeyPEM: "not-a-key"}, "私钥解析失败"},
	}
	for _, c := range cases {
		_, err := Dial(c.cfg, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: got %v, want 含 %q", c.name, err, c.want)
		}
	}
}

// 私钥加密但未提供口令时给出明确提示。
func TestDialEncryptedKeyNeedsPassphrase(t *testing.T) {
	const encryptedKey = `-----BEGIN RSA PRIVATE KEY-----
Proc-Type: 4,ENCRYPTED
DEK-Info: AES-128-CBC,0123456789ABCDEF0123456789ABCDEF

AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
-----END RSA PRIVATE KEY-----`
	_, err := Dial(DialConfig{Host: "h", User: "u", PrivateKeyPEM: encryptedKey}, nil)
	if err == nil || !strings.Contains(err.Error(), "口令") {
		t.Fatalf("应提示填写私钥口令, got %v", err)
	}
}

// 端口关闭时拨号失败但必须给出可读错误（不 panic、不静默）。
func TestDialUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	var log strings.Builder
	_, err = Dial(DialConfig{
		Host: "127.0.0.1", User: "u", Password: "p", Port: addr.Port,
		Timeout: 2 * time.Second,
	}, &log)
	if err == nil {
		t.Fatal("连接已关闭端口应失败")
	}
	if !strings.Contains(log.String(), "正在连接") {
		t.Fatalf("应把连接过程写入任务输出, got %q", log.String())
	}
}
