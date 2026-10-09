//go:build linux

package system

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"onecloud-panel/internal/version"
)

// Collect 采集当前主机信息（Linux）。
func Collect() (*HostInfo, error) {
	h := &HostInfo{Arch: normalizeArch(runtimeGOARCH()), CollectedAt: time.Now()}

	if name, err := os.Hostname(); err == nil {
		h.Hostname = name
	}

	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		m := parseOSRelease(b)
		h.OSName = m["NAME"]
		h.OSVersion = m["VERSION"]
		h.PrettyName = m["PRETTY_NAME"]
	}

	var u syscall.Utsname
	if err := syscall.Uname(&u); err == nil {
		h.Kernel = utsString(u.Release)
	}

	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		h.CPUModel, h.CPUCores = parseCPUInfo(b)
	}

	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		m := parseMemInfo(b)
		h.MemTotal = m["MemTotal"] * 1024
		avail := m["MemAvailable"]
		if avail == 0 {
			avail = m["MemFree"] + m["Buffers"] + m["Cached"]
		}
		h.MemUsed = h.MemTotal - avail*1024
	}

	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		bsize := int64(st.Bsize)
		h.DiskTotal = int64(st.Blocks) * bsize
		h.DiskUsed = (int64(st.Blocks) - int64(st.Bfree)) * bsize
	}

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.LoadAvg = parseLoadAvg(b)
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		h.Uptime = parseUptime(b)
	}

	h.Interfaces = collectInterfaces()
	h.Storage = collectStorage()
	h.AgentVersion = version.Version
	return h, nil
}

// blockDevicePrefixes 参与枚举的块设备前缀。
var blockDevicePrefixes = []string{"mmcblk", "sd", "vd", "hd", "nvme"}

func isBlockDevice(name string) bool {
	for _, p := range blockDevicePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// collectStorage 枚举 /sys/block 下的块设备，按 sysfs 真实类型分类。
// 关键修复：不再以设备名假定（如 mmcblk0=内置），而是通过
// /sys/block/<dev>/device/type（MMC/SD）与 /sys/block/<dev>/removable
// 判定，使 SD 卡正确显示为「SD卡」而非「内置存储」。
// sysBlockRoot 是 sysfs 块设备根目录，测试可临时改写为临时目录。
var sysBlockRoot = "/sys/block"

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
		classifyBlockDevice(name, &dev)
		if b, err := os.ReadFile(sysBlockRoot + "/" + name + "/size"); err == nil {
			if sectors, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
				dev.SizeBytes = sectors * 512
			}
		}
		if b, err := os.ReadFile(sysBlockRoot + "/" + name + "/device/model"); err == nil {
			dev.Model = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(sysBlockRoot + "/" + name + "/removable"); err == nil {
			dev.Removable = strings.TrimSpace(string(b)) == "1"
		}
		dev.IsBoot = name == boot
		out = append(out, dev)
	}
	return out
}

// classifyBlockDevice 依据 sysfs 信息确定设备分类与中文标签。
func classifyBlockDevice(name string, dev *StorageDevice) {
	switch {
	case strings.HasPrefix(name, "nvme"):
		dev.Class = "nvme"
		dev.ClassLabel = "NVMe 固态"
	case strings.HasPrefix(name, "mmcblk"):
		// device/type: "MMC" 为 eMMC（内置），"SD" 为 SD 卡。
		if b, err := os.ReadFile(sysBlockRoot + "/" + name + "/device/type"); err == nil {
			if strings.EqualFold(strings.TrimSpace(string(b)), "SD") {
				dev.Class = "sd"
				dev.ClassLabel = "SD 卡"
			} else {
				dev.Class = "emmc"
				dev.ClassLabel = "内置存储(eMMC)"
			}
		} else {
			dev.Class = "emmc"
			dev.ClassLabel = "内置存储(eMMC)"
		}
	case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "vd"), strings.HasPrefix(name, "hd"):
		if dev.Removable {
			dev.Class = "usb"
			dev.ClassLabel = "USB 存储"
			return
		}
		if b, err := os.ReadFile(sysBlockRoot + "/" + name + "/queue/rotational"); err == nil {
			if strings.TrimSpace(string(b)) == "0" {
				dev.Class = "ssd"
				dev.ClassLabel = "固态硬盘(SSD)"
				return
			}
		}
		dev.Class = "hdd"
		dev.ClassLabel = "机械硬盘(HDD)"
	default:
		dev.Class = "unknown"
		dev.ClassLabel = "未知"
	}
}

// bootBlockDevice 返回根文件系统（/）所在的顶层块设备名。
func bootBlockDevice() string {
	b, err := os.ReadFile("/proc/mounts")
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
	name := strings.TrimPrefix(filepath.Clean(devPath), "/dev/")
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

func collectInterfaces() []NetInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []NetInterface
	for _, ifc := range ifaces {
		ni := NetInterface{
			Name: ifc.Name,
			MAC:  ifc.HardwareAddr.String(),
			Up:   ifc.Flags&net.FlagUp != 0,
		}
		if strings.HasPrefix(ifc.Name, "wg") || isSysfsWG(ifc.Name) {
			ni.WireGuard = true
		}
		addrs, err := ifc.Addrs()
		if err == nil {
			for _, a := range addrs {
				ni.Addresses = append(ni.Addresses, a.String())
			}
		}
		out = append(out, ni)
	}
	return out
}

// isSysfsWG 通过 sysfs 判断接口是否为 WireGuard（接口名不以 wg 开头时也能识别）。
func isSysfsWG(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name + "/wireguard")
	return err == nil
}

func utsString(b utsField) string {
	var sb strings.Builder
	for _, c := range b {
		if c == 0 {
			break
		}
		sb.WriteByte(byte(c))
	}
	return sb.String()
}
