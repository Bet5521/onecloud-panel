package system

import "runtime"

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
