//go:build linux && !arm

package system

// amd64/arm64/386 等 syscall.Utsname 字符字段为 int8。
type utsField = [65]int8
