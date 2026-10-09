package system

import "time"

// NetInterface 网络接口。
type NetInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
	Up        bool     `json:"up"`
	WireGuard bool     `json:"wireguard"`
}

// HostInfo 节点主机信息与实时资源水位。
type HostInfo struct {
	Hostname    string         `json:"hostname"`
	OSName      string         `json:"os_name"`
	OSVersion   string         `json:"os_version"`
	PrettyName  string         `json:"pretty_name"`
	Kernel      string         `json:"kernel"`
	Arch        string         `json:"arch"`
	CPUModel    string         `json:"cpu_model"`
	CPUCores    int            `json:"cpu_cores"`
	MemTotal    int64          `json:"mem_total"` // bytes
	MemUsed     int64          `json:"mem_used"`
	DiskTotal   int64          `json:"disk_total"`
	DiskUsed    int64          `json:"disk_used"`
	LoadAvg     [3]float64     `json:"load_avg"`
	Uptime      int64          `json:"uptime_seconds"`
	Interfaces  []NetInterface `json:"interfaces"`
	Docker      bool           `json:"docker"`
	DockerVer   string         `json:"docker_version"`
	AgentVersion string        `json:"agent_version"`
	Storage     []StorageDevice `json:"storage,omitempty"`
	CollectedAt time.Time      `json:"collected_at"`
}

// StorageDevice 节点块存储设备（节点详情展示用）。
// Class 取值：emmc / sd / usb / ssd / hdd / nvme / disk / unknown。
type StorageDevice struct {
	Name       string `json:"name"`        // 设备名，如 mmcblk0 / sda / nvme0n1
	Model      string `json:"model"`       // 型号（可能为空）
	Class      string `json:"class"`       // 分类关键字
	ClassLabel string `json:"class_label"` // 中文展示：内置存储(eMMC)/SD卡/...
	Removable  bool   `json:"removable"`
	SizeBytes  int64  `json:"size_bytes"`
	IsBoot     bool   `json:"is_boot"` // 是否为根文件系统所在设备
}

// CPUPercent 粗估 CPU 占用（百分比），由 load/cores 推算（0-100）。
func (h *HostInfo) CPUPercent() float64 {
	if h.CPUCores == 0 {
		return 0
	}
	p := h.LoadAvg[0] / float64(h.CPUCores) * 100
	if p > 100 {
		p = 100
	}
	return p
}

// MemPercent 内存占用百分比。
func (h *HostInfo) MemPercent() float64 {
	if h.MemTotal == 0 {
		return 0
	}
	return float64(h.MemUsed) / float64(h.MemTotal) * 100
}

// DiskPercent 磁盘占用百分比。
func (h *HostInfo) DiskPercent() float64 {
	if h.DiskTotal == 0 {
		return 0
	}
	return float64(h.DiskUsed) / float64(h.DiskTotal) * 100
}
