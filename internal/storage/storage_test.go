package storage

import (
	"strings"
	"testing"
)

func TestParseLsblk(t *testing.T) {
	out := strings.Join([]string{
		`NAME="mmcblk1" PATH="/dev/mmcblk1" SIZE="31267481600" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="SD128 "`,
		`NAME="mmcblk1p1" PATH="/dev/mmcblk1p1" SIZE="31267459072" TYPE="part" FSTYPE="ext4" MOUNTPOINT="/mnt/sd" RO="0" RM="1" HOTPLUG="1" MODEL=""`,
		`NAME="sda" PATH="/dev/sda" SIZE="80026361856" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="SSD"`,
	}, "\n")
	devs, err := parseAndFilter(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 3 {
		t.Fatalf("设备数 = %d, want 3", len(devs))
	}
	sd := devs[0]
	if !sd.Removable || !sd.HotPlug || sd.Type != "disk" || sd.Size != 31267481600 {
		t.Fatalf("SD 盘解析错误: %+v", sd)
	}
	if devs[1].FSType != "ext4" || devs[1].MountPoint != "/mnt/sd" {
		t.Fatalf("分区解析错误: %+v", devs[1])
	}
	if devs[2].Removable {
		t.Fatalf("固定盘不应标记可移除: %+v", devs[2])
	}
}

// parseAndFilter 与 List 内部逻辑一致的辅助（供测试）。
func parseAndFilter(out string) ([]Device, error) {
	return filterBlockDevices(parseLsblk(out)), nil
}

// TestClassifyDevices 校验设备分类：仅 USB 与 SD 卡可操作，
// 系统盘/启动分区/内置盘/RAM/loop 一律不可操作。
func TestClassifyDevices(t *testing.T) {
	out := strings.Join([]string{
		`NAME="ram0" PATH="/dev/ram0" SIZE="8388608" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="" TRAN="" PKNAME=""`,
		`NAME="zram0" PATH="/dev/zram0" SIZE="1073741824" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="" TRAN="" PKNAME=""`,
		`NAME="loop0" PATH="/dev/loop0" SIZE="1024" TYPE="loop" FSTYPE="squashfs" MOUNTPOINT="/snap" RO="1" RM="0" HOTPLUG="0" MODEL="" LABEL="" TRAN="" PKNAME=""`,
		`NAME="mmcblk0" PATH="/dev/mmcblk0" SIZE="7818182656" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="eMMC" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk0boot0" PATH="/dev/mmcblk0boot0" SIZE="4194304" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="1" RM="0" HOTPLUG="0" MODEL="" LABEL="" TRAN="mmc" PKNAME="mmcblk0"`,
		`NAME="mmcblk0p1" PATH="/dev/mmcblk0p1" SIZE="7340032000" TYPE="part" FSTYPE="ext4" MOUNTPOINT="/" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="root" TRAN="" PKNAME="mmcblk0"`,
		`NAME="mmcblk1" PATH="/dev/mmcblk1" SIZE="31267481600" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="SD128" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk1p1" PATH="/dev/mmcblk1p1" SIZE="31267459072" TYPE="part" FSTYPE="ext4" MOUNTPOINT="/mnt/sd" RO="0" RM="1" HOTPLUG="1" MODEL="" LABEL="SDCARD" TRAN="" PKNAME="mmcblk1"`,
		`NAME="sda" PATH="/dev/sda" SIZE="80026361856" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="USB DISK" LABEL="" TRAN="usb" PKNAME=""`,
		`NAME="sda1" PATH="/dev/sda1" SIZE="80025218048" TYPE="part" FSTYPE="vfat" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="" LABEL="UDISK" TRAN="" PKNAME="sda"`,
		`NAME="nvme0n1" PATH="/dev/nvme0n1" SIZE="512110190592" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="NVMe" LABEL="" TRAN="nvme" PKNAME=""`,
		`NAME="sdb" PATH="/dev/sdb" SIZE="16000000000" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="USB Bridge" LABEL="" TRAN="" PKNAME=""`,
		`NAME="mmcblk2" PATH="/dev/mmcblk2" SIZE="32000000000" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="SDCARD" LABEL="" TRAN="mmc" PKNAME=""`,
	}, "\n")

	devs := filterBlockDevices(parseLsblk(out))
	// ram / zram / loop / boot 分区应被整体剔除
	for _, name := range []string{"ram0", "zram0", "loop0", "mmcblk0boot0"} {
		for _, d := range devs {
			if d.Name == name {
				t.Fatalf("%s 应被剔除，但仍在列表中: %+v", name, d)
			}
		}
	}

	classifyDevices(devs, "/dev/mmcblk0", true)
	idx := map[string]Device{}
	for _, d := range devs {
		idx[d.Name] = d
	}

	cases := []struct {
		name     string
		kind     string
		system   bool
		operable bool
	}{
		{"mmcblk0", kindSystem, true, false},    // 系统盘（eMMC）
		{"mmcblk0p1", kindSystem, true, false},  // 系统根分区
		{"mmcblk1", kindSD, false, true},        // SD 卡
		{"mmcblk1p1", kindSD, false, true},      // SD 卡分区
		{"sda", kindUSB, false, true},           // USB 设备
		{"sda1", kindUSB, false, true},          // USB 分区
		{"nvme0n1", kindInternal, false, false}, // 内置 NVMe
		{"sdb", kindUSB, false, true},           // 未上报 TRAN 的 USB（可移除+热插拔兜底）
		{"mmcblk2", kindSD, false, true},        // 未上报 RM 的次级 MMC（SD 卡槽兜底）
	}
	for _, c := range cases {
		d, ok := idx[c.name]
		if !ok {
			t.Fatalf("%s 不在列表中", c.name)
		}
		if d.Kind != c.kind || d.System != c.system || d.Operable != c.operable {
			t.Fatalf("%s: got kind=%s system=%v operable=%v; want kind=%s system=%v operable=%v",
				c.name, d.Kind, d.System, d.Operable, c.kind, c.system, c.operable)
		}
	}

	// 系统盘识别失败时，一切设备均不可操作。
	devs2 := filterBlockDevices(parseLsblk(out))
	classifyDevices(devs2, "", false)
	for _, d := range devs2 {
		if d.Operable {
			t.Fatalf("系统盘未知时 %s 不应可操作", d.Name)
		}
	}
}

func TestMMCDiskIndex(t *testing.T) {
	for _, yes := range []string{"mmcblk1", "mmcblk2", "mmcblk10"} {
		if !mmcDiskIndexAtLeast1(yes) {
			t.Fatalf("%s 应判定为次级 MMC 控制器", yes)
		}
	}
	for _, no := range []string{"mmcblk0", "sda", "nvme0n1", "mmcblk"} {
		if mmcDiskIndexAtLeast1(no) {
			t.Fatalf("%s 不应判定为次级 MMC 控制器", no)
		}
	}
}

func TestParseLsblkTransport(t *testing.T) {
	out := `NAME="sda" PATH="/dev/sda" SIZE="100" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="U" LABEL="" TRAN="usb" PKNAME=""`
	devs := parseLsblk(out)
	if len(devs) != 1 || devs[0].Transport != "usb" {
		t.Fatalf("TRAN 解析失败: %+v", devs)
	}
	out2 := `NAME="mmcblk1p1" PATH="/dev/mmcblk1p1" SIZE="100" TYPE="part" FSTYPE="ext4" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="" LABEL="" TRAN="" PKNAME="mmcblk1"`
	devs2 := parseLsblk(out2)
	if len(devs2) != 1 || devs2[0].Parent != "mmcblk1" {
		t.Fatalf("PKNAME 解析失败: %+v", devs2)
	}
}

func TestUpdateFstab(t *testing.T) {
	orig := "# /etc/fstab\n/dev/mmcblk0p2  /  ext4  defaults,noatime  0 1\n"
	uuid := "1111-2222"

	// 启用：追加条目
	got, changed := updateFstab(orig, uuid, "/mnt/sd", "ext4", true)
	want := orig + "UUID=1111-2222 /mnt/sd ext4 defaults,nofail,noatime 0 2\n"
	if !changed || got != want {
		t.Fatalf("enable:\n got=%q\nwant=%q changed=%v", got, want, changed)
	}

	// 重复启用：幂等
	got, changed = updateFstab(want, uuid, "/mnt/sd", "ext4", true)
	if changed || got != want {
		t.Fatalf("重复 enable 应无变化")
	}

	// 同挂载点换 UUID：替换旧行
	got, changed = updateFstab(want, "3333-4444", "/mnt/sd", "vfat", true)
	if !changed {
		t.Fatal("换 UUID 应有变化")
	}
	if strings.Contains(got, "1111-2222") || !strings.Contains(got, "UUID=3333-4444") {
		t.Fatalf("旧 UUID 应被移除: %q", got)
	}
	if !strings.Contains(got, "/dev/mmcblk0p2") {
		t.Fatalf("无关行不应丢失: %q", got)
	}

	// 禁用：移除条目
	got, changed = updateFstab(got, "3333-4444", "/mnt/sd", "vfat", false)
	if !changed || strings.Contains(got, "3333-4444") || !strings.Contains(got, "/dev/mmcblk0p2") {
		t.Fatalf("disable 移除失败: changed=%v got=%q", changed, got)
	}

	// 禁用无条目：无变化
	got, changed = updateFstab(orig, uuid, "/mnt/sd", "ext4", false)
	if changed || got != orig {
		t.Fatalf("disable 不存在的条目不应变化")
	}
}

func TestValidate(t *testing.T) {
	for _, ok := range []string{"/dev/sda1", "/dev/mmcblk1p1"} {
		if err := validateDevice(ok); err != nil {
			t.Fatalf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"sda", "/dev/", "/dev/a b", "/dev/a;b", "/dev/..", "", "/dev/x'y"} {
		if err := validateDevice(bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
	for _, ok := range []string{"/mnt/sd", "/mnt/sd-abc/data"} {
		if err := validateMountpoint(ok); err != nil {
			t.Fatalf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"mnt/sd", "/", "/mnt/a b", "/mnt/../etc", "/mnt/a'b"} {
		if err := validateMountpoint(bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}
