// Package scripts 内嵌一键安装脚本。
package scripts

import "embed"

// Files 安装脚本资源。
//
//go:embed files/install.sh
var Files embed.FS

// InstallScript 读取 install.sh 原文。
func InstallScript() ([]byte, error) {
	return Files.ReadFile("files/install.sh")
}
