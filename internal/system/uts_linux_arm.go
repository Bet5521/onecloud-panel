//go:build linux && arm

package system

// arm 上 syscall.Utsname 字符字段为 uint8。
type utsField = [65]uint8
