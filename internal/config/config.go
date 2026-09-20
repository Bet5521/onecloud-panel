package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Common 面板与 Agent 共有的配置。
type Common struct {
	DataDir string
	Listen  string
}

// Panel 面板模式配置。
type Panel struct {
	Common
	UnitName   string // systemd 单元名（远程重启用）
	ReleaseDir string // 各架构二进制发布目录（/dl）
	TLSCert    string // TLS 证书路径（空为 HTTP）
	TLSKey     string // TLS 私钥路径
}

// Agent 节点模式配置。
type Agent struct {
	Common
	Server        string // 面板地址，如 http://192.168.1.10:8000
	RegisterToken string // 一次性注册令牌
	Token         string // 已注册节点的长期 Token（非注册模式使用）
	InsecureTLS   bool   // 面板为自签 HTTPS 时跳过证书校验
}

func registerCommon(fs *flag.FlagSet, defListen string) *Common {
	c := &Common{}
	defData := envOr("OCP_DATA_DIR", "/var/lib/onecloud-panel")
	fs.StringVar(&c.DataDir, "data-dir", defData, "数据存储目录 (env: OCP_DATA_DIR)")
	fs.StringVar(&c.Listen, "listen", envOr("OCP_LISTEN", defListen), "监听地址 (env: OCP_LISTEN)")
	return c
}

// ParsePanel 解析面板模式参数。
func ParsePanel(args []string) (*Panel, error) {
	fs := flag.NewFlagSet("panel", flag.ContinueOnError)
	c := registerCommon(fs, ":8000")
	var unitName string
	fs.StringVar(&unitName, "unit-name", envOr("OCP_UNIT_NAME", "onecloud-panel.service"),
		"systemd 单元名 (env: OCP_UNIT_NAME)")
	var releaseDir string
	fs.StringVar(&releaseDir, "release-dir", os.Getenv("OCP_RELEASE_DIR"),
		"各架构二进制发布目录，空则 <data-dir>/releases (env: OCP_RELEASE_DIR)")
	var tlsCert, tlsKey string
	fs.StringVar(&tlsCert, "tls-cert", os.Getenv("OCP_TLS_CERT"),
		"TLS 证书路径，与 --tls-key 同时提供时启用 HTTPS (env: OCP_TLS_CERT)")
	fs.StringVar(&tlsKey, "tls-key", os.Getenv("OCP_TLS_KEY"),
		"TLS 私钥路径 (env: OCP_TLS_KEY)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &Panel{Common: *c, UnitName: unitName, ReleaseDir: releaseDir,
		TLSCert: tlsCert, TLSKey: tlsKey}, nil
}

// ParseAgent 解析 Agent 模式参数。
func ParseAgent(args []string) (*Agent, error) {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	c := registerCommon(fs, ":9000")
	a := &Agent{Common: *c}
	fs.StringVar(&a.Server, "server", os.Getenv("OCP_SERVER"), "面板地址 (env: OCP_SERVER)")
	fs.StringVar(&a.RegisterToken, "register-token", os.Getenv("OCP_REGISTER_TOKEN"), "一次性注册令牌 (env: OCP_REGISTER_TOKEN)")
	fs.StringVar(&a.Token, "token", os.Getenv("OCP_TOKEN"), "节点长期 Token (env: OCP_TOKEN)")
	fs.BoolVar(&a.InsecureTLS, "insecure", envBool("OCP_INSECURE_TLS"),
		"跳过面板 HTTPS 证书校验（自签证书场景）(env: OCP_INSECURE_TLS)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if a.RegisterToken == "" && a.Token == "" {
		return nil, fmt.Errorf("agent 需要 --register-token（首次注册）或 --token（已注册节点）")
	}
	return a, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool 解析布尔环境变量（1/true/yes/on 视为真）。
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
