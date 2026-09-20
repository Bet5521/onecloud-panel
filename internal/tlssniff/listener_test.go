package tlssniff

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"onecloud-panel/internal/tlsutil"
)

// 同一端口应同时承载明文 HTTP 与 HTTPS。
func TestHTTPAndHTTPSOnSamePort(t *testing.T) {
	certPEM, keyPEM, err := tlsutil.Generate(tlsutil.LocalHosts(nil))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	rawLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := New(rawLn, tlsCfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		_, _ = io.WriteString(w, scheme)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	addr := rawLn.Addr().String()

	// 明文 HTTP
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "http" {
		t.Fatalf("明文请求得到 %q, want http", body)
	}

	// HTTPS（自签证书，跳过校验）
	tc := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err = tc.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("https get: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "https" {
		t.Fatalf("TLS 请求得到 %q, want https", body)
	}
}

// TLSConfig 为 nil 时应表现为普通 Listener（纯 HTTP）。
func TestNilConfigPlainHTTP(t *testing.T) {
	rawLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := New(rawLn, nil)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := http.Get("http://" + rawLn.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("got %q", body)
	}
}
