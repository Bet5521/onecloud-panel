//go:build windows

package self

import "fmt"

// defaultLaunch Windows 开发环境不支持远程重启（面板仅运行在 Linux 主机）。
func defaultLaunch(unit string) error {
	return fmt.Errorf("远程重启仅支持 Linux systemd 主机")
}
