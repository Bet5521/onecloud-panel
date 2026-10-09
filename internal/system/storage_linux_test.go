package system

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSys 在伪 sysfs 中写入一个属性文件。
func writeSys(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("创建目录失败 %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写入失败 %s: %v", p, err)
	}
}

// TestClassifyMMCDevices 覆盖用户反馈的缺陷：玩客云的 mmcblk0 实为 SD 卡，
// 但旧实现把所有 mmcblk* 一律标成"内置存储"。
func TestClassifyMMCDevices(t *testing.T) {
	root := t.TempDir()
	old := sysBlockRoot
	sysBlockRoot = root
	defer func() { sysBlockRoot = old }()

	// mmcblk0：SD 卡（device/type = SD）
	os.MkdirAll(filepath.Join(root, "mmcblk0"), 0o755)
	writeSys(t, root, "mmcblk0/device/type", "SD\n")
	writeSys(t, root, "mmcblk0/device/model", "SD Card\n")
	writeSys(t, root, "mmcblk0/size", "31116288\n")
	writeSys(t, root, "mmcblk0/removable", "0\n")

	// mmcblk1：eMMC 内置（device/type = MMC）
	os.MkdirAll(filepath.Join(root, "mmcblk1"), 0o755)
	writeSys(t, root, "mmcblk1/device/type", "MMC\n")
	writeSys(t, root, "mmcblk1/size", "15269888\n")

	// mmcblk2：无 device/type（内核未导出），应安全退化为 eMMC，不能 panic 或空标签
	os.MkdirAll(filepath.Join(root, "mmcblk2"), 0o755)

	// 干扰项：非块设备目录不应被采集
	os.MkdirAll(filepath.Join(root, "loop7"), 0o755)

	devs := collectStorage()
	byName := map[string]StorageDevice{}
	for _, d := range devs {
		byName[d.Name] = d
	}

	sd, ok := byName["mmcblk0"]
	if !ok {
		t.Fatal("未采集到 mmcblk0")
	}
	if sd.Class != "sd" || sd.ClassLabel != "SD 卡" {
		t.Errorf("mmcblk0 分类 = %q/%q, 期望 sd/SD 卡", sd.Class, sd.ClassLabel)
	}
	if sd.SizeBytes != 31116288*512 {
		t.Errorf("mmcblk0 容量 = %d, 期望 %d", sd.SizeBytes, 31116288*512)
	}
	if sd.Model != "SD Card" {
		t.Errorf("mmcblk0 型号 = %q, 期望 SD Card", sd.Model)
	}

	emmc, ok := byName["mmcblk1"]
	if !ok {
		t.Fatal("未采集到 mmcblk1")
	}
	if emmc.Class != "emmc" || emmc.ClassLabel != "内置存储(eMMC)" {
		t.Errorf("mmcblk1 分类 = %q/%q, 期望 emmc/内置存储(eMMC)", emmc.Class, emmc.ClassLabel)
	}

	fallback, ok := byName["mmcblk2"]
	if !ok {
		t.Fatal("未采集到 mmcblk2")
	}
	if fallback.Class != "emmc" {
		t.Errorf("mmcblk2 应退化为 emmc, 实际 %q", fallback.Class)
	}

	if _, ok := byName["loop7"]; ok {
		t.Error("loop7 不应被当作块设备采集")
	}
}

func TestClassifySataAndUSB(t *testing.T) {
	root := t.TempDir()
	old := sysBlockRoot
	sysBlockRoot = root
	defer func() { sysBlockRoot = old }()

	os.MkdirAll(filepath.Join(root, "sda"), 0o755)
	writeSys(t, root, "sda/queue/rotational", "1\n")
	writeSys(t, root, "sda/removable", "0\n")

	os.MkdirAll(filepath.Join(root, "sdb"), 0o755)
	writeSys(t, root, "sdb/queue/rotational", "0\n")
	writeSys(t, root, "sdb/removable", "0\n")

	os.MkdirAll(filepath.Join(root, "sdc"), 0o755)
	writeSys(t, root, "sdc/removable", "1\n")

	os.MkdirAll(filepath.Join(root, "nvme0n1"), 0o755)

	devs := collectStorage()
	byName := map[string]StorageDevice{}
	for _, d := range devs {
		byName[d.Name] = d
	}

	if got := byName["sda"]; got.Class != "hdd" || got.ClassLabel != "机械硬盘(HDD)" {
		t.Errorf("sda = %q/%q, 期望 hdd/机械硬盘(HDD)", got.Class, got.ClassLabel)
	}
	if got := byName["sdb"]; got.Class != "ssd" || got.ClassLabel != "固态硬盘(SSD)" {
		t.Errorf("sdb = %q/%q, 期望 ssd/固态硬盘(SSD)", got.Class, got.ClassLabel)
	}
	if got := byName["sdc"]; got.Class != "usb" || got.ClassLabel != "USB 存储" {
		t.Errorf("sdc = %q/%q, 期望 usb/USB 存储", got.Class, got.ClassLabel)
	}
	if !byName["sdc"].Removable {
		t.Error("sdc 应标记为可移动")
	}
	if got := byName["nvme0n1"]; got.Class != "nvme" || got.ClassLabel != "NVMe 固态" {
		t.Errorf("nvme0n1 = %q/%q, 期望 nvme/NVMe 固态", got.Class, got.ClassLabel)
	}
}

func TestTopBlockDevice(t *testing.T) {
	cases := map[string]string{
		"/dev/mmcblk0p1": "mmcblk0",
		"/dev/mmcblk0":   "mmcblk0",
		"/dev/sda1":      "sda",
		"/dev/sda":       "sda",
		"/dev/nvme0n1p3": "nvme0n1",
		"/dev/nvme0n1":   "nvme0n1",
		"":               "",
	}
	for in, want := range cases {
		if got := topBlockDevice(in); got != want {
			t.Errorf("topBlockDevice(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
