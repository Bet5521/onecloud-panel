// Package storage 节点块设备检测与挂载管理（SD 卡等可移除介质）。
// 所有操作经节点执行器（本机直连 / Agent 远程统一通道）。
package storage

import (
	"context"
	"fmt"
	"regexp"
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
	System     bool   `json:"system"`     // 系统盘/系统分区（禁止格式化与重新分区）
}

// Manager 存储管理入口。
type Manager struct {
	execFor func(*store.Node) (executor.Executor, error)
}

// New 创建管理器；execFor 提供目标节点的执行器。
func New(execFor func(*store.Node) (executor.Executor, error)) *Manager {
	return &Manager{execFor: execFor}
}

// List 列出节点上的磁盘与分区（排除 loop/ram 等虚拟设备）。
func (m *Manager) List(ctx context.Context, n *store.Node) ([]Device, error) {
	ex, err := m.execFor(n)
	if err != nil {
		return nil, err
	}
	r, err := ex.Exec(ctx, "lsblk", "-b", "-P", "-e", "7",
		"-o", "NAME,PATH,SIZE,TYPE,FSTYPE,MOUNTPOINT,RO,RM,HOTPLUG,MODEL,LABEL")
	if err != nil {
		return nil, fmt.Errorf("lsblk 执行失败: %w", err)
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("lsblk 失败: %s", strings.TrimSpace(r.Output))
	}
	devs := parseLsblk(r.Output)
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		if d.Type == "disk" || d.Type == "part" {
			out = append(out, d)
		}
	}
	// 标记系统盘/系统分区，供前端禁用格式化/分区按钮。
	if rootDisk, err := rootDiskPath(ctx, ex); err == nil && rootDisk != "" {
		for i := range out {
			if isSystemDevice(out[i].Path, rootDisk) {
				out[i].System = true
			}
		}
	}
	return out, nil
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
		})
	}
	return devs
}

// Mount 挂载设备到指定目录（目录不存在时自动创建）。
func (m *Manager) Mount(ctx context.Context, n *store.Node, device, mountpoint string) error {
	if err := validateDevice(device); err != nil {
		return err
	}
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
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

// Unmount 卸载挂载点。
func (m *Manager) Unmount(ctx context.Context, n *store.Node, mountpoint string) error {
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
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
func (m *Manager) Autostart(ctx context.Context, n *store.Node, device, mountpoint string, enabled bool) error {
	if err := validateDevice(device); err != nil {
		return err
	}
	if err := validateMountpoint(mountpoint); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
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
// 安全约束：拒绝系统盘/系统分区；文件系统须为白名单内；无法识别系统盘时拒绝操作。
func (m *Manager) Format(ctx context.Context, n *store.Node, device, fsType, label string) error {
	if err := validateDevice(device); err != nil {
		return err
	}
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
	// 必须能识别系统盘，否则拒绝以防误删数据。
	rootDisk, err := rootDiskPath(ctx, ex)
	if err != nil {
		return fmt.Errorf("无法确定系统盘，为防误删拒绝格式化: %w", err)
	}
	if isSystemDevice(device, rootDisk) {
		return fmt.Errorf("禁止格式化系统盘或系统分区: %s", device)
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
// 安全约束：拒绝系统盘；仅允许对整个磁盘操作；无法识别系统盘时拒绝操作。
func (m *Manager) Partition(ctx context.Context, n *store.Node, device, scheme string) error {
	if err := validateDevice(device); err != nil {
		return err
	}
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "gpt" && scheme != "msdos" {
		return fmt.Errorf("不支持的分区表类型: %s（仅支持 gpt/msdos）", scheme)
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	rootDisk, err := rootDiskPath(ctx, ex)
	if err != nil {
		return fmt.Errorf("无法确定系统盘，为防误删拒绝分区: %w", err)
	}
	if isSystemDevice(device, rootDisk) {
		return fmt.Errorf("禁止对系统盘重新分区: %s", device)
	}
	// 仅允许对整盘操作，拒绝分区。
	if r, e := ex.Exec(ctx, "lsblk", "-no", "TYPE", device); e == nil {
		t := strings.TrimSpace(r.Output)
		if t == "part" {
			return fmt.Errorf("%s 是分区而非整盘，请对整个磁盘（如 /dev/sdb）执行分区", device)
		}
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
