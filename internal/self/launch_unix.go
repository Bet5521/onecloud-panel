//go:build !windows

package self

import (
	"os/exec"
	"syscall"
)

// defaultLaunch 以新会话方式触发：1 秒后由 systemctl 重启面板。
// 子进程脱离当前进程组，面板被杀掉不影响该命令完成。
func defaultLaunch(unit string) error {
	cmd := exec.Command("sh", "-c", "sleep 1; systemctl restart "+unit)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
