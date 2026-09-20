package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"onecloud-panel/internal/agent"
	"onecloud-panel/internal/config"
	"onecloud-panel/internal/panel"
	"onecloud-panel/internal/version"
)

const usage = `OneCloud Panel - 玩客云/Armbian 集群管理面板

用法:
  onecloud-panel panel      [参数]   以面板模式运行
  onecloud-panel agent      [参数]   以节点 Agent 模式运行
  onecloud-panel selfsigned [参数]   生成自签名 TLS 证书
  onecloud-panel version             输出版本信息

通用参数 (panel/agent):
  --listen      监听地址 (默认 panel :8000 / agent :9000, env OCP_LISTEN)
  --data-dir    数据目录 (默认 /var/lib/onecloud-panel, env OCP_DATA_DIR)

Panel 参数:
  --tls-cert    TLS 证书路径 (env OCP_TLS_CERT)
  --tls-key     TLS 私钥路径 (env OCP_TLS_KEY)

Agent 参数:
  --server           面板地址
  --register-token   一次性注册令牌（首次注册）
  --token            节点长期 Token（已注册）
`

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(version.Print())
	case "panel":
		cfg, err := config.ParsePanel(args)
		if err != nil {
			os.Exit(2)
		}
		if err := panel.Run(cfg); err != nil && err != http.ErrServerClosed {
			log.Fatalf("面板退出: %v", err)
		}
	case "agent":
		cfg, err := config.ParseAgent(args)
		if err != nil {
			os.Exit(2)
		}
		if err := agent.Run(cfg); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Agent 退出: %v", err)
		}
	case "selfsigned":
		if err := runSelfSigned(args); err != nil {
			log.Fatalf("证书生成失败: %v", err)
		}
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}
