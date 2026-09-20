//go:build !linux

package system

import "errors"

// Collect 非 Linux 平台不可用（开发/测试环境）。
func Collect() (*HostInfo, error) {
	return nil, errors.New("主机信息采集仅支持 Linux")
}
