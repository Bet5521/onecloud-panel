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
		"-o", "NAME,PATH,SIZE,TYPE,FSTYPE,MOUNTPOINT,RO,RM,HOTPLUG,MODEL")
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
	return out, nil
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
