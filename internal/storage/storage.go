// Package storage 节点块设备检测与挂载管理（SD 卡等可移除介质）。
// 所有操作经节点执行器（本机直连 / Agent 远程统一通道）。
package storage

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

const (
	fstabPath   = "/etc/fstab"
	fstabBackup = "/etc/fstab.ocp.bak"
)

// Device lsblk 解析出的块设备信息。
type Device struct {
	Name       string `json:"name"`       // 内核名，如 sda / mmcblk1p1
	Path       string `json:"path"`       // 设备路径，如 /dev/mmcblk1p1
	Size       int64  `json:"size"`       // 字节
	Type       string `json:"type"`       // disk / part
	FSType     string `json:"fstype"`     // 文件系统类型（可为空=未格式化）
	MountPoint string `json:"mountpoint"` // 已挂载点（可为空）
	Removable  bool   `json:"removable"`  // 可移除介质（RM=1）
	HotPlug    bool   `json:"hotplug"`    // 热插拔（HOTPLUG=1）
	Model      string `json:"model"`      // 型号（部分设备为空）
	ReadOnly   bool   `json:"read_only"`  // 只读（RO=1，如写保护 SD 卡）
	Label      string `json:"label"`      // 卷标（LABEL）
	Transport  string `json:"transport"`  // 传输总线：usb / mmc / sata / nvme / ...
	Parent     string `json:"parent"`     // 所属磁盘内核名（分区非空）
	System     bool   `json:"system"`     // 系统盘 / 系统分区 / 启动分区
	Kind       string `json:"kind"`       // usb / sd / system / internal / unknown
	Operable   bool   `json:"operable"`   // 是否允许挂载/格式化/分区（仅 USB 与 SD 卡）
}

// 设备类型（Kind）取值。
const (
	kindUSB      = "usb"      // USB 存储设备
	kindSD       = "sd"       // SD / TF 卡（可移除 mmc）
	kindSystem   = "system"   // 系统盘 / 系统分区 / 启动分区
	kindInternal = "internal" // 内置非系统存储（eMMC、SATA、NVMe 等）
	kindUnknown  = "unknown"  // 无法识别系统盘时的兜底（一律不可操作）
)

// KindLabel 返回设备类型的中文说明。
func KindLabel(kind string) string {
	switch kind {
	case kindUSB:
		return "USB 设备"
	case kindSD:
		return "SD 卡"
	case kindSystem:
		return "系统盘/启动分区"
	case kindInternal:
		return "内置存储"
	default:
		return "未知设备"
	}
}

// Manager 存储管理入口。
type Manager struct {
	execFor func(*store.Node) (executor.Executor, error)
}

// New 创建管理器；execFor 提供目标节点的执行器。
func New(execFor func(*store.Node) (executor.Executor, error)) *Manager {
	return &Manager{execFor: execFor}
}

// List 列出节点上的块设备（排除 ram/zram/loop/光驱/启动分区等虚拟或不可操作设备），
// 并对每个设备标注 kind / system / operable。
// 注意：返回全部已过滤设备；是否隐藏「不可操作设备」由调用方决定（Operable 字段）。
func (m *Manager) List(ctx context.Context, n *store.Node) ([]Device, error) {
	ex, err := m.execFor(n)
	if err != nil {
		return nil, err
	}
	return listDevices(ctx, ex)
}

// listDevices 读取并分类节点上的块设备。
func listDevices(ctx context.Context, ex executor.Executor) ([]Device, error) {
	r, err := ex.Exec(ctx, "lsblk", "-b", "-P", "-e", "7",
		"-o", "NAME,PATH,SIZE,TYPE,FSTYPE,MOUNTPOINT,RO,RM,HOTPLUG,MODEL,LABEL,TRAN,PKNAME")
	if err != nil {
		return nil, fmt.Errorf("lsblk 执行失败: %w", err)
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("lsblk 失败: %s", strings.TrimSpace(r.Output))
	}
	out := filterBlockDevices(parseLsblk(r.Output))
	// 系统盘识别失败时一律标记为不可操作，宁可失败也不误伤。
	rootDisk, rerr := rootDiskPath(ctx, ex)
	classifyDevices(out, rootDisk, rerr == nil)
	return out, nil
}

// filterBlockDevices 仅保留真实磁盘与分区，剔除虚拟/启动类设备。
func filterBlockDevices(devs []Device) []Device {
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		if d.Type != "disk" && d.Type != "part" {
			continue
		}
		if isNonOperableDeviceName(d.Name) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// isNonOperableDeviceName 判断内核名是否为 RAM 盘、loop、光驱、软驱、
// 设备映射/软 RAID，以及 eMMC 的 boot/rpmb 等启动分区——这些一律不可操作。
func isNonOperableDeviceName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"ram", "zram", "loop", "sr", "fd", "dm-", "md", "nbd"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	// eMMC 启动分区：mmcblk0boot0 / mmcblk0boot1 / mmcblk0rpmb
	if strings.Contains(n, "boot") || strings.HasSuffix(n, "rpmb") {
		return true
	}
	return false
}

// classifyDevices 依据传输总线、可移除标记与系统盘位置，标注每个设备的
// system / kind / operable。rootOK=false 表示系统盘识别失败。
func classifyDevices(devs []Device, rootDisk string, rootOK bool) {
	rootName := strings.TrimPrefix(rootDisk, "/dev/")
	byName := make(map[string]*Device, len(devs))
	for i := range devs {
		byName[devs[i].Name] = &devs[i]
	}
	for i := range devs {
		d := &devs[i]
		transport, removable, hotplug := d.Transport, d.Removable, d.HotPlug
		diskName := d.Name
		if d.Type == "part" && d.Parent != "" {
			diskName = d.Parent
			// 分区的可移除/总线属性继承自所属磁盘。
			if p, ok := byName[d.Parent]; ok {
				transport = p.Transport
				removable = p.Removable
				hotplug = p.HotPlug
			}
		}
		d.Transport = transport
		// 主判据：设备所属磁盘是否即系统盘；兜底：路径前缀判断（兼容缺失 PKNAME 的场景）。
		d.System = rootOK && rootName != "" &&
			(diskName == rootName || isSystemDevice(d.Path, rootDisk))

		if !rootOK {
			d.System = false
			d.Kind = kindUnknown
			d.Operable = false
			continue
		}
		switch {
		case d.System:
			d.Kind = kindSystem
		case transport == "usb":
			d.Kind = kindUSB
		case transport == "mmc" && (removable || hotplug || mmcDiskIndexAtLeast1(diskName)):
			d.Kind = kindSD
		case transport == "" && removable && hotplug:
			// 个别 USB 桥接芯片不报告 TRAN，但「可移除 + 热插拔」足以判定为外接 USB 介质。
			d.Kind = kindUSB
		default:
			d.Kind = kindInternal
		}
		d.Operable = d.Kind == kindUSB || d.Kind == kindSD
	}
}

// mmcDiskIndexAtLeast1 判断是否为次级 MMC 控制器上的磁盘（mmcblk1+）。
// 0 号通常为板载 eMMC，1 号及以后一般为 SD 卡槽。
func mmcDiskIndexAtLeast1(name string) bool {
	if !strings.HasPrefix(name, "mmcblk") {
		return false
	}
	rest := name[len("mmcblk"):]
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	idx, err := strconv.Atoi(rest[:i])
	return err == nil && idx >= 1
}

// rootDiskPath 返回当前系统的根设备所在磁盘路径（如 /dev/sda、/dev/mmcblk0）。
// 用于识别系统盘，防止误格式化/误分区。检测失败时返回错误。
func rootDiskPath(ctx context.Context, ex executor.Executor) (string, error) {
	r, err := ex.Exec(ctx, "findmnt", "-n", "-o", "SOURCE", "-T", "/")
	if err != nil {
		return "", fmt.Errorf("findmnt 执行失败: %w", err)
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("无法确定根设备: %s", strings.TrimSpace(r.Output))
	}
	src := strings.TrimSpace(r.Output)
	if src == "" {
		return "", fmt.Errorf("无法确定根设备（findmnt 无输出）")
	}
	// 解析父磁盘（PKNAME）。/dev/root 等符号链接由 lsblk 自动展开。
	pk, err := ex.Exec(ctx, "lsblk", "-no", "PKNAME", src)
	if err != nil {
		return "", fmt.Errorf("lsblk 执行失败: %w", err)
	}
	if pk.ExitCode != 0 {
		return "", fmt.Errorf("无法确定系统盘: %s", strings.TrimSpace(pk.Output))
	}
	pkName := strings.TrimSpace(pk.Output)
	if pkName == "" {
		return "", fmt.Errorf("无法确定系统盘（根设备 %s 无父磁盘，可能位于逻辑卷/ overlay）", src)
	}
	return "/dev/" + pkName, nil
}

// isSystemDevice 判定 device 是否为系统盘本身，或其上的任一分区。
// 保留作为系统盘识别的兼容兜底（parseLsblk 平台可能缺失 PKNAME）。
func isSystemDevice(device, rootDisk string) bool {
	if rootDisk == "" || device == "" {
		return false
	}
	if device == rootDisk {
		return true
	}
	// /dev/mmcblk0p1 / /dev/nvme0n1p1 形式（带 p 分隔）
	if strings.HasPrefix(device, rootDisk+"p") {
		return true
	}
	// /dev/sda1 / /dev/vda1 形式（直接拼接数字）
	if len(device) > len(rootDisk) && strings.HasPrefix(device, rootDisk) {
		rest := device[len(rootDisk):]
		if rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

var lsblkKV = regexp.MustCompile(`([A-Z_]+)="([^"]*)"`)

func parseLsblk(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, `="`) {
			continue
		}
		kv := map[string]string{}
		for _, m := range lsblkKV.FindAllStringSubmatch(line, -1) {
			kv[m[1]] = m[2]
		}
		size := 0
		fmt.Sscan(kv["SIZE"], &size)
		ro := kv["RO"] == "1"
		label := kv["LABEL"]
		// lsblk 对空字段可能输出字面量 "none"，归一化为空。
		if label == "none" {
			label = ""
		}
		devs = append(devs, Device{
			Name:       kv["NAME"],
			Path:       kv["PATH"],
			Size:       int64(size),
			Type:       kv["TYPE"],
			FSType:     kv["FSTYPE"],
			MountPoint: kv["MOUNTPOINT"],
			Removable:  kv["RM"] == "1",
			HotPlug:    kv["HOTPLUG"] == "1",
			Model:      kv["MODEL"],
			ReadOnly:   ro,
			Label:      label,
			Transport:  strings.ToLower(kv["TRAN"]),
			Parent:     kv["PKNAME"],
		})
	}
	return devs
}

// requireOperable 校验设备属于可操作的 USB / SD 卡，且非系统盘/启动分区。
// 这是所有写操作（挂载/卸载/自启/格式化/分区）的统一安全前置。
func requireOperable(ctx context.Context, ex executor.Executor, device string) (*Device, error) {
	if err := validateDevice(device); err != nil {
		return nil, err
	}
	devs, err := listDevices(ctx, ex)
	if err != nil {
		return nil, err
	}
	for i := range devs {
		if devs[i].Path != device {
			continue
		}
		d := &devs[i]
		switch {
		case d.System:
			return nil, fmt.Errorf("禁止操作系统盘、系统分区或启动分区: %s", device)
		case d.Kind == kindUnknown:
			return nil, fmt.Errorf("无法识别系统盘，为防误操作拒绝操作 %s", device)
		case !d.Operable:
			return nil, fmt.Errorf("仅允许对 USB 设备与 SD 卡操作，%s 属于「%s」", device, KindLabel(d.Kind))
		}
		return d, nil
	}
	return nil, fmt.Errorf("设备不存在或不受支持: %s", device)
}

// Mount 挂载设备到指定目录（目录不存在时自动创建）。仅允许 USB / SD 卡。
func (m *Manager) Mount(ctx context.Context, n *store.Node, device, mountpoint string) error {
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	if _, err := requireOperable(ctx, ex, device); err != nil {
		return err
	}
	if r, err := ex.Exec(ctx, "mkdir", "-p", mountpoint); err != nil {
		return fmt.Errorf("创建挂载点失败: %w", err)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("创建挂载点失败: %s", strings.TrimSpace(r.Output))
	}
	if r, err := ex.Exec(ctx, "mount", device, mountpoint); err != nil {
		return fmt.Errorf("挂载失败: %w", err)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("挂载失败: %s", strings.TrimSpace(r.Output))
	}
	return nil
}

// Unmount 卸载挂载点。设备须为可操作的 USB / SD 卡。
func (m *Manager) Unmount(ctx context.Context, n *store.Node, device, mountpoint string) error {
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	if _, err := requireOperable(ctx, ex, device); err != nil {
		return err
	}
	if r, err := ex.Exec(ctx, "umount", mountpoint); err != nil {
		return fmt.Errorf("卸载失败: %w", err)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("卸载失败: %s", strings.TrimSpace(r.Output))
	}
	return nil
}

// Autostart 设置（enabled=true）或取消（enabled=false）设备开机自动挂载。
// 写 /etc/fstab 前先备份到 /etc/fstab.ocp.bak；nofail 保证设备缺失不阻塞启动。
// 仅允许 USB / SD 卡。
func (m *Manager) Autostart(ctx context.Context, n *store.Node, device, mountpoint string, enabled bool) error {
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	if _, err := requireOperable(ctx, ex, device); err != nil {
		return err
	}
	uuid, err := blkidField(ctx, ex, device, "UUID")
	if err != nil {
		return err
	}
	fstype, err := blkidField(ctx, ex, device, "TYPE")
	if err != nil {
		return err
	}
	exists, err := ex.Exists(fstabPath)
	if err != nil {
		return fmt.Errorf("检查 fstab 失败: %w", err)
	}
	var original string
	if exists {
		b, err := ex.ReadFile(fstabPath)
		if err != nil {
			return fmt.Errorf("读取 fstab 失败: %w", err)
		}
		original = string(b)
	}
	updated, changed := updateFstab(original, uuid, mountpoint, fstype, enabled)
	if !changed {
		return nil
	}
	if exists {
		if err := ex.WriteFile(fstabBackup, []byte(original)); err != nil {
			return fmt.Errorf("备份 fstab 失败: %w", err)
		}
	}
	if err := ex.WriteFile(fstabPath, []byte(updated)); err != nil {
		return fmt.Errorf("写入 fstab 失败: %w", err)
	}
	return nil
}

// updateFstab 按挂载点/UUID 去重后写入或删除 fstab 条目。
// 返回新内容与是否有变化；其余行（含注释）原样保留。
func updateFstab(original, uuid, mountpoint, fstype string, enabled bool) (string, bool) {
	entry := fmt.Sprintf("UUID=%s %s %s defaults,nofail,noatime 0 2", uuid, mountpoint, fstype)
	var kept []string
	found := false
	for _, line := range strings.Split(original, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			f := strings.Fields(trimmed)
			// 命中同挂载点或同 UUID 的旧条目：移除（enabled 且内容一致则视为已存在）
			if len(f) >= 2 && (f[1] == mountpoint || f[0] == "UUID="+uuid) {
				if enabled && trimmed == entry {
					found = true
				}
				continue
			}
		}
		kept = append(kept, line)
	}
	if !enabled {
		return joinLines(kept), len(kept) != len(strings.Split(original, "\n"))
	}
	if found {
		return original, false
	}
	// 裁掉尾部空行，避免条目间出现多余空行
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	kept = append(kept, entry)
	return joinLines(kept), true
}

func joinLines(lines []string) string {
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

func blkidField(ctx context.Context, ex executor.Executor, device, field string) (string, error) {
	r, err := ex.Exec(ctx, "blkid", "-s", field, "-o", "value", device)
	if err != nil {
		return "", fmt.Errorf("blkid 执行失败: %w", err)
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("读取设备 %s 失败: %s", field, strings.TrimSpace(r.Output))
	}
	v := strings.TrimSpace(r.Output)
	if v == "" {
		return "", fmt.Errorf("设备未包含 %s（可能未格式化）", field)
	}
	return v, nil
}

// forbiddenChars 设备路径/挂载点中禁止的空白与 shell 元字符。
const forbiddenChars = " \t\r\n'\"\\;&|<>()$`"

// validateDevice 限定设备路径形式 /dev/xxx，杜绝注入。
func validateDevice(device string) error {
	if !strings.HasPrefix(device, "/dev/") || len(device) <= len("/dev/") {
		return fmt.Errorf("设备路径非法: %q", device)
	}
	if strings.Contains(device, "..") || strings.ContainsAny(device, forbiddenChars) {
		return fmt.Errorf("设备路径非法: %q", device)
	}
	return nil
}

// validateMountpoint 限定绝对路径、禁 .. 与空白，杜绝注入。
func validateMountpoint(mp string) error {
	if !strings.HasPrefix(mp, "/") || len(mp) <= 1 {
		return fmt.Errorf("挂载点非法: %q", mp)
	}
	if strings.Contains(mp, "..") || strings.ContainsAny(mp, forbiddenChars) {
		return fmt.Errorf("挂载点非法: %q", mp)
	}
	return nil
}

// 允许格式化的文件系统白名单（ext4/vfat/ntfs/exfat）。
var allowedFS = map[string]bool{"ext4": true, "vfat": true, "ntfs": true, "exfat": true}

// mkfsToolName 返回某文件系统对应的 mkfs 工具名。
func mkfsToolName(fsType string) string {
	switch fsType {
	case "ext4":
		return "mkfs.ext4"
	case "vfat":
		return "mkfs.vfat"
	case "ntfs":
		return "mkfs.ntfs"
	case "exfat":
		return "mkfs.exfat"
	}
	return "mkfs." + fsType
}

// mkfsPkg 返回安装对应 mkfs 工具所需的系统包名（用于错误提示）。
func mkfsPkg(fsType string) string {
	switch fsType {
	case "ext4":
		return "e2fsprogs"
	case "vfat":
		return "dosfstools"
	case "ntfs":
		return "ntfs-3g"
	case "exfat":
		return "exfatprogs"
	}
	return fsType
}

// mkfsArgs 构造 mkfs 命令行参数；label 为空时省略卷标。
func mkfsArgs(fsType, label, device string) []string {
	switch fsType {
	case "ext4":
		if label == "" {
			return []string{"-F", device}
		}
		return []string{"-F", "-L", label, device}
	case "vfat":
		if label == "" {
			return []string{"-F", "32", device}
		}
		return []string{"-F", "32", "-n", label, device}
	case "ntfs":
		if label == "" {
			return []string{"-f", "-q", device}
		}
		return []string{"-f", "-q", "-L", label, device}
	case "exfat":
		if label == "" {
			return []string{device}
		}
		return []string{"-n", label, device}
	}
	return []string{device}
}

// hasTool 检查节点上是否存在某命令（command -v）。
func hasTool(ctx context.Context, ex executor.Executor, tool string) bool {
	r, err := ex.Exec(ctx, "sh", "-c", "command -v "+tool+" >/dev/null 2>&1")
	if err != nil {
		return false
	}
	return r.ExitCode == 0
}

// Format 格式化设备为指定文件系统（可选卷标）。
// 安全约束：仅允许 USB / SD 卡；拒绝系统盘/系统分区/启动分区；
// 文件系统须为白名单内；无法识别系统盘时拒绝操作。
func (m *Manager) Format(ctx context.Context, n *store.Node, device, fsType, label string) error {
	fsType = strings.ToLower(strings.TrimSpace(fsType))
	if !allowedFS[fsType] {
		return fmt.Errorf("不支持的文件系统: %s（仅支持 ext4/vfat/ntfs/exfat）", fsType)
	}
	if label != "" {
		if strings.HasPrefix(label, "-") || strings.ContainsAny(label, forbiddenChars) {
			return fmt.Errorf("卷标非法: %q", label)
		}
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	if _, err := requireOperable(ctx, ex, device); err != nil {
		return err
	}
	// 若已挂载则先卸载，避免设备忙。
	if r, e := ex.Exec(ctx, "umount", "-f", device); e == nil && r.ExitCode != 0 {
		_ = r // 可能本来未挂载，忽略
	}
	tool := mkfsToolName(fsType)
	if !hasTool(ctx, ex, tool) {
		return fmt.Errorf("节点缺少格式化工具 %s（请先执行 apt install %s 后重试）", tool, mkfsPkg(fsType))
	}
	if r, e := ex.Exec(ctx, tool, mkfsArgs(fsType, label, device)...); e != nil {
		return fmt.Errorf("格式化失败: %w", e)
	} else if r.ExitCode != 0 {
		out := strings.TrimSpace(r.Output)
		if r.ExitCode == 127 || strings.Contains(out, "not found") {
			return fmt.Errorf("节点缺少格式化工具 %s（请先执行 apt install %s 后重试）", tool, mkfsPkg(fsType))
		}
		return fmt.Errorf("格式化失败: %s", out)
	}
	return nil
}

// Partition 在整盘上重建分区表（gpt/msdos）并创建一个占满全盘的主分区。
// 安全约束：仅允许 USB / SD 卡整盘；拒绝系统盘/启动分区；无法识别系统盘时拒绝操作。
func (m *Manager) Partition(ctx context.Context, n *store.Node, device, scheme string) error {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "gpt" && scheme != "msdos" {
		return fmt.Errorf("不支持的分区表类型: %s（仅支持 gpt/msdos）", scheme)
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	dev, err := requireOperable(ctx, ex, device)
	if err != nil {
		return err
	}
	// 仅允许对整盘操作，拒绝分区。
	if dev.Type != "disk" {
		return fmt.Errorf("%s 是分区而非整盘，请对整个磁盘（如 /dev/sdb）执行分区", device)
	}
	// 尽力卸载该盘上的已有分区。
	ex.Exec(ctx, "sh", "-c", "for p in $(lsblk -ln -o PATH "+device+" | tail -n +2); do umount -f \"$p\" 2>/dev/null; done")
	if r, e := ex.Exec(ctx, "parted", "-s", device, "mklabel", scheme); e != nil {
		return fmt.Errorf("创建分区表失败: %w", e)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("创建分区表失败: %s", strings.TrimSpace(r.Output))
	}
	if r, e := ex.Exec(ctx, "parted", "-s", device, "mkpart", "primary", "ext4", "0%", "100%"); e != nil {
		return fmt.Errorf("创建分区失败: %w", e)
	} else if r.ExitCode != 0 {
		return fmt.Errorf("创建分区失败: %s", strings.TrimSpace(r.Output))
	}
	ex.Exec(ctx, "partprobe", device)
	return nil
}
