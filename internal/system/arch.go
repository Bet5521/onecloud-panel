package system

import (
	"runtime"
	"strings"
)

func runtimeGOARCH() string { return runtime.GOARCH }

// normalizeArch 将 Go GOARCH 映射为对外的架构标识。
func normalizeArch(goarch string) string {
	switch goarch {
	case "arm":
		return "armv7l"
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	case "386":
		return "i386"
	default:
		return goarch
	}
}

// AssetArch 把对外架构标识（uname -m / normalizeArch 风格）归一到发布产物后缀
// （armv7/arm64/amd64/386）；返回空串表示无对应发布产物。
// 同时兼容 GOARCH 风格（arm/arm64/amd64/386）以便复用。
func AssetArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "armv7l", "armv6l", "armv7", "arm", "armhf":
		return "armv7"
	case "aarch64", "arm64":
		return "arm64"
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "i386", "i486", "i586", "i686", "386", "x86":
		return "386"
	}
	return ""
}
