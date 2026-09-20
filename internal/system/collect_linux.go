//go:build linux

package system

import (
	"net"
	"os"
	"strings"
	"syscall"
	"time"
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
	return h, nil
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
