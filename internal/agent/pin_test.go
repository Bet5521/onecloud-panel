package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"
)

// genTestCert 生成一张自签名证书并返回其 DER 与 SHA256 指纹。
func genTestCert(t *testing.T) ([]byte, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成私钥失败: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "panel.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("生成证书失败: %v", err)
	}
	sum := sha256.Sum256(der)
	return der, hex.EncodeToString(sum[:])
}

func TestNormalizePin(t *testing.T) {
	if got := normalizePin("  SHA256:AbCd  "); got != "abcd" {
		t.Fatalf("normalizePin 期望 abcd，实际 %q", got)
	}
	if got := normalizePin("sha256:abcd"); got != "abcd" {
		t.Fatalf("normalizePin 应去除前缀，实际 %q", got)
	}
}

func TestMatchPin(t *testing.T) {
	der, pin := genTestCert(t)

	if !matchPin([][]byte{der}, pin) {
		t.Fatal("期望匹配归一化十六进制指纹")
	}
	if !matchPin([][]byte{der}, "sha256:"+pin) {
		t.Fatal("期望匹配带前缀指纹")
	}
	if !matchPin([][]byte{der}, " SHA256:"+pin+" ") {
		t.Fatal("期望容忍空白与大小写")
	}
	if matchPin([][]byte{der}, "deadbeef") {
		t.Fatal("不应匹配错误指纹")
	}
	if matchPin(nil, pin) {
		t.Fatal("空证书链不应匹配")
	}
	if matchPin([][]byte{der}, "") {
		t.Fatal("空指纹不应匹配")
	}
}

func TestPinnedTransportRejectsMismatch(t *testing.T) {
	// 仅校验传输层构造不 panic 且 VerifyPeerCertificate 逻辑由 matchPin 覆盖；
	// 这里确保 Transport 可被创建（真实 TLS 握手在集成测试中覆盖）。
	tr := pinnedTransport("sha256:abcd")
	if tr == nil || tr.TLSClientConfig == nil {
		t.Fatal("pinnedTransport 应返回可用 Transport")
	}
	if tr.TLSClientConfig.VerifyPeerCertificate == nil {
		t.Fatal("pinnedTransport 应注册 VerifyPeerCertificate")
	}
}
