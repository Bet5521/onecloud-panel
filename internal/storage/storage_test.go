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
	var outDevs []Device
	for _, d := range parseLsblk(out) {
		if d.Type == "disk" || d.Type == "part" {
			outDevs = append(outDevs, d)
		}
	}
	return outDevs, nil
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
