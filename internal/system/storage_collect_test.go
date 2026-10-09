package system

import (
	"os"
	"path/filepath"
	"testing"
)

// 这些用例刻意不带 _linux 后缀：storage 采集/分类逻辑本身是平台无关的
// （见 storage_collect.go 顶部注释），因此必须在开发机上也能被 `go test ./...`
// 执行到。此前测试叫 storage_linux_test.go，本地（Windows）会被静默跳过，
// 结果缺陷只在 CI 上暴露。

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

// fakeSysFS 把 sysBlockRoot 指向临时目录，用例结束后自动还原。
func fakeSysFS(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := sysBlockRoot
	sysBlockRoot = root
	t.Cleanup(func() { sysBlockRoot = old })
	return root
}

// fakeMounts 把 procMountsPath 指向临时挂载表，避免读取真实 /proc/mounts。
func fakeMounts(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写入挂载表失败: %v", err)
	}
	old := procMountsPath
	procMountsPath = p
	t.Cleanup(func() { procMountsPath = old })
}

// collectByName 采集伪 sysfs 中的块设备并按设备名索引。
func collectByName(t *testing.T) map[string]StorageDevice {
	t.Helper()
	byName := map[string]StorageDevice{}
	for _, d := range collectStorage() {
		byName[d.Name] = d
	}
	return byName
}

// TestClassifyMMCDevices 覆盖用户反馈的缺陷：玩客云的 mmcblk0 实为 SD 卡，
// 但旧实现把所有 mmcblk* 一律标成「内置存储」。
func TestClassifyMMCDevices(t *testing.T) {
	root := fakeSysFS(t)
	fakeMounts(t, "")

	// mmcblk0：SD 卡（device/type = SD）—— 玩客云的实际情形
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

	byName := collectByName(t)

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
	if sd.IsBoot {
		t.Error("挂载表为空，mmcblk0 不应被标记为启动盘")
	}

	if emmc, ok := byName["mmcblk1"]; !ok {
		t.Fatal("未采集到 mmcblk1")
	} else if emmc.Class != "emmc" || emmc.ClassLabel != "内置存储(eMMC)" {
		t.Errorf("mmcblk1 分类 = %q/%q, 期望 emmc/内置存储(eMMC)", emmc.Class, emmc.ClassLabel)
	}

	if fallback, ok := byName["mmcblk2"]; !ok {
		t.Fatal("未采集到 mmcblk2")
	} else if fallback.Class != "emmc" || fallback.ClassLabel == "" {
		t.Errorf("mmcblk2 应退化为带标签的 emmc, 实际 %q/%q", fallback.Class, fallback.ClassLabel)
	}

	if _, ok := byName["loop7"]; ok {
		t.Error("loop7 不应被当作块设备采集")
	}
}

// TestClassifySataAndUSB 覆盖 sd*/nvme* 判定。
//
// sdc 是本次缺陷的回归用例：collectStorage 曾在读取 removable 之前调用
// classifyBlockDevice，使 dev.Removable 恒为 false，可移动设备被判成
// HDD/SSD 而不是「USB 存储」。
func TestClassifySataAndUSB(t *testing.T) {
	root := fakeSysFS(t)
	fakeMounts(t, "")

	os.MkdirAll(filepath.Join(root, "sda"), 0o755)
	writeSys(t, root, "sda/queue/rotational", "1\n")
	writeSys(t, root, "sda/removable", "0\n")

	os.MkdirAll(filepath.Join(root, "sdb"), 0o755)
	writeSys(t, root, "sdb/queue/rotational", "0\n")
	writeSys(t, root, "sdb/removable", "0\n")

	// 可移动但无 rotational 属性：只能靠 removable 判定
	os.MkdirAll(filepath.Join(root, "sdc"), 0o755)
	writeSys(t, root, "sdc/removable", "1\n")

	os.MkdirAll(filepath.Join(root, "nvme0n1"), 0o755)

	byName := collectByName(t)

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

// TestCollectStorageMarksBoot 覆盖启动盘识别（依赖挂载表设备名归一化）。
func TestCollectStorageMarksBoot(t *testing.T) {
	root := fakeSysFS(t)
	fakeMounts(t, "# comment\n/dev/mmcblk0p1 / ext4 rw,relatime 0 0\n/dev/sda1 /boot vfat rw 0 0\n")

	os.MkdirAll(filepath.Join(root, "mmcblk0"), 0o755)
	writeSys(t, root, "mmcblk0/device/type", "SD\n")
	os.MkdirAll(filepath.Join(root, "sda"), 0o755)
	writeSys(t, root, "sda/queue/rotational", "1\n")

	byName := collectByName(t)
	if !byName["mmcblk0"].IsBoot {
		t.Error("mmcblk0p1 挂载在 /，mmcblk0 应标记为启动盘")
	}
	if byName["sda"].IsBoot {
		t.Error("sda 未挂载在 /，不应标记为启动盘")
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
		"/dev/sdb12":     "sdb",
		"":               "",
		"   ":            "",
	}
	for in, want := range cases {
		if got := topBlockDevice(in); got != want {
			t.Errorf("topBlockDevice(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestCollectStorageMissingSysFS 确认 sysfs 不可用时安全返回空而不是 panic。
func TestCollectStorageMissingSysFS(t *testing.T) {
	old := sysBlockRoot
	sysBlockRoot = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { sysBlockRoot = old })

	if got := collectStorage(); len(got) != 0 {
		t.Errorf("sysfs 缺失时应返回空列表, 实际 %d 项", len(got))
	}
}
