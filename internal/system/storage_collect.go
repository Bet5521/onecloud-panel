package system

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// 块设备的枚举与分类。
//
// 本文件刻意不带 build tag：分类判定只依赖「设备名 + sysfs 属性」，
// 与运行平台无关。而 /sys/block 在 Linux 上才有内容、在开发机（Windows/macOS）
// 上不存在，一旦把这些逻辑放进 *_linux.go，测试文件也必须叫 *_linux_test.go，
// 于是本地 `go test ./...` 会静默跳过它们 —— 出现「本地全绿、CI 才报错」。
// 因此这里保持平台无关，测试用伪 sysfs 目录覆盖 sysBlockRoot 即可在任何平台运行。

// blockDevicePrefixes 参与枚举的块设备前缀。
var blockDevicePrefixes = []string{"mmcblk", "sd", "vd", "hd", "nvme"}

// sysBlockRoot 是 sysfs 块设备根目录，测试可临时改写为伪 sysfs 目录。
var sysBlockRoot = "/sys/block"

// procMountsPath 是挂载表路径，测试可临时改写。
var procMountsPath = "/proc/mounts"

func isBlockDevice(name string) bool {
	for _, p := range blockDevicePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// storageAttrs 是分类判定所需的 sysfs 原始属性。
type storageAttrs struct {
	MMCType    string // device/type：MMC（eMMC 内置）/ SD（SD 卡）
	Removable  bool   // removable：1 表示可移动（读卡器 / USB）
	Rotational string // queue/rotational：0 表示非机械盘
}

// readStorageAttrs 从 sysfs 读取分类属性；缺项按零值处理。
func readStorageAttrs(name string) storageAttrs {
	var a storageAttrs
	if b, err := os.ReadFile(filepath.Join(sysBlockRoot, name, "device", "type")); err == nil {
		a.MMCType = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(sysBlockRoot, name, "removable")); err == nil {
		a.Removable = strings.TrimSpace(string(b)) == "1"
	}
	if b, err := os.ReadFile(filepath.Join(sysBlockRoot, name, "queue", "rotational")); err == nil {
		a.Rotational = strings.TrimSpace(string(b))
	}
	return a
}

// classifyBlockDevice 依据设备名与 sysfs 属性确定分类与中文标签。
//
// 关键修复：不再以设备名假定类型（旧实现把所有 mmcblk* 一律当内置 eMMC，
// 导致玩客云的 mmcblk0 —— 实际是 SD 卡 —— 被显示为「内置存储」）。
// 现在以内核导出的 device/type 为准：SD → SD 卡，MMC 或读取失败 → 内置 eMMC。
func classifyBlockDevice(name string, a storageAttrs, dev *StorageDevice) {
	switch {
	case strings.HasPrefix(name, "nvme"):
		dev.Class = "nvme"
		dev.ClassLabel = "NVMe 固态"
	case strings.HasPrefix(name, "mmcblk"):
		if strings.EqualFold(a.MMCType, "SD") {
			dev.Class = "sd"
			dev.ClassLabel = "SD 卡"
		} else {
			// MMC 或无法读取 type：安全退化为内置 eMMC，不 panic、不产生空标签。
			dev.Class = "emmc"
			dev.ClassLabel = "内置存储(eMMC)"
		}
	case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "vd"), strings.HasPrefix(name, "hd"):
		if a.Removable {
			dev.Class = "usb"
			dev.ClassLabel = "USB 存储"
			return
		}
		if a.Rotational == "0" {
			dev.Class = "ssd"
			dev.ClassLabel = "固态硬盘(SSD)"
			return
		}
		dev.Class = "hdd"
		dev.ClassLabel = "机械硬盘(HDD)"
	default:
		dev.Class = "unknown"
		dev.ClassLabel = "未知"
	}
}

// collectStorage 枚举 /sys/block 下的块设备并逐项分类。
func collectStorage() []StorageDevice {
	var out []StorageDevice
	entries, err := os.ReadDir(sysBlockRoot)
	if err != nil {
		return out
	}
	boot := bootBlockDevice()
	for _, e := range entries {
		name := e.Name()
		if !isBlockDevice(name) {
			continue
		}
		dev := StorageDevice{Name: name}
		if b, err := os.ReadFile(filepath.Join(sysBlockRoot, name, "size")); err == nil {
			if sectors, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
				dev.SizeBytes = sectors * 512
			}
		}
		if b, err := os.ReadFile(filepath.Join(sysBlockRoot, name, "device", "model")); err == nil {
			dev.Model = strings.TrimSpace(string(b))
		}
		// 属性必须先在分类之前读取：classifyBlockDevice 依赖 Removable 判定 USB 存储，
		// 若顺序反了，可移动设备会被误判为 HDD/SSD。
		attrs := readStorageAttrs(name)
		dev.Removable = attrs.Removable
		classifyBlockDevice(name, attrs, &dev)
		dev.IsBoot = name == boot
		out = append(out, dev)
	}
	return out
}

// bootBlockDevice 返回根文件系统（/）所在设备的顶层块设备名。
func bootBlockDevice() string {
	b, err := os.ReadFile(procMountsPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "/" {
			return topBlockDevice(f[0])
		}
	}
	return ""
}

// topBlockDevice 将 /dev/sda1、/dev/mmcblk0p1、/dev/nvme0n1p3 归一为顶层设备名。
func topBlockDevice(devPath string) string {
	if strings.TrimSpace(devPath) == "" {
		return ""
	}
	// 用 path.Clean（POSIX 语义）而非 filepath.Clean：后者在 Windows 上会把
	// "/dev/sda1" 规范化成 "\dev\sda1"，导致 TrimPrefix("/dev/") 失配。
	name := strings.TrimPrefix(path.Clean(devPath), "/dev/")
	if strings.HasPrefix(name, "mmcblk") || strings.HasPrefix(name, "nvme") {
		if idx := strings.LastIndex(name, "p"); idx > 0 {
			if _, err := strconv.Atoi(name[idx+1:]); err == nil {
				return name[:idx]
			}
		}
		return name
	}
	return strings.TrimRight(name, "0123456789")
}
