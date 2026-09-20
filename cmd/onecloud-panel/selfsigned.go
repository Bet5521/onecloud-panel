package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"onecloud-panel/internal/tlsutil"
)

// runSelfSigned 生成自签证书文件：onecloud-panel selfsigned --cert p --key p [--host h1,h2]
func runSelfSigned(args []string) error {
	fs := flag.NewFlagSet("selfsigned", flag.ContinueOnError)
	certPath := fs.String("cert", "", "证书输出路径")
	keyPath := fs.String("key", "", "私钥输出路径")
	host := fs.String("host", "", "附加 SAN（逗号分隔；默认自动收集 hostname/本机 IP）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *certPath == "" || *keyPath == "" {
		return fmt.Errorf("selfsigned 需要 --cert 与 --key")
	}
	hosts := tlsutil.LocalHosts([]string{*host})
	certPEM, keyPEM, err := tlsutil.Generate(hosts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*certPath), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(*certPath, certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(*keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	fmt.Printf("已生成自签证书: %s\nSAN: %v\n", *certPath, hosts)
	return nil
}
