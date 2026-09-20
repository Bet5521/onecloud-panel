// Package tlsutil 生成面板用的自签名 TLS 证书。
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

// Generate 生成自签名证书（ECDSA P-256，10 年有效），返回 PEM 证书与私钥。
func Generate(hosts []string) ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "onecloud-panel",
			Organization: []string{"OneCloud Panel"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// LocalHosts 收集本机证书 SAN：localhost、hostname、全部非环回/非链路本地
// IPv4/IPv6，并并入 extra（逗号分隔或字符串切片）。
func LocalHosts(extra []string) []string {
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			seen[s] = true
		}
	}
	add("localhost")
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	for _, e := range extra {
		for _, h := range strings.Split(e, ",") {
			add(h)
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipStr, _, err := net.ParseCIDR(a.String())
			if err != nil {
				continue
			}
			if ip4 := ipStr.To4(); ip4 != nil {
				if !ip4.IsLoopback() {
					add(ip4.String())
				}
				continue
			}
			if !ipStr.IsLoopback() && !ipStr.IsLinkLocalUnicast() {
				add(ipStr.String())
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}
