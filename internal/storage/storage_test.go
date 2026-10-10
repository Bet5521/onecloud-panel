package storage

import (
	"context"
	"io"
	"strings"
	"testing"

	"onecloud-panel/internal/executor"
)

// fakeExec 依据命令（含首个参数）返回预设输出，用于覆盖 listDevices 完整链路。
type fakeExec struct {
	fn func(name string, args []string) executor.Result
}

func (f *fakeExec) Exec(_ context.Context, name string, args ...string) (*executor.Result, error) {
	r := f.fn(name, args)
	return &r, nil
}
func (f *fakeExec) ExecStream(context.Context, io.Writer, string, ...string) (int, error) {
	return 0, nil
}
func (f *fakeExec) ReadFile(string) ([]byte, error) { return nil, nil }
func (f *fakeExec) WriteFile(string, []byte) error  { return nil }
func (f *fakeExec) Exists(string) (bool, error)     { return false, nil }

// TestListDevicesOneCloud 覆盖玩客云抽样：mmcblk0 是 SD 卡（sysfs type=SD），
// 系统盘在 eMMC(mmcbkl1)。面板应把 SD 卡判为可操作、eMMC/系统盘判为不可操作。
func TestListDevicesOneCloud(t *testing.T) {
	lsblkOut := strings.Join([]string{
		`NAME="mmcblk0" PATH="/dev/mmcblk0" SIZE="31267481600" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="SD128" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk0p1" PATH="/dev/mmcblk0p1" SIZE="31267459072" TYPE="part" FSTYPE="exfat" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="" LABEL="SD" TRAN="" PKNAME="mmcblk0"`,
		`NAME="mmcblk1" PATH="/dev/mmcblk1" SIZE="7818182656" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="eMMC" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk1p2" PATH="/dev/mmcblk1p2" SIZE="7340032000" TYPE="part" FSTYPE="ext4" MOUNTPOINT="/" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="root" TRAN="" PKNAME="mmcblk1"`,
		`NAME="sda" PATH="/dev/sda" SIZE="16000000000" TYPE="disk" FSTYPE="vfat" MOUNTPOINT="/mnt/usb" RO="0" RM="1" HOTPLUG="1" MODEL="USB DISK" LABEL="UDISK" TRAN="usb" PKNAME=""`,
		`NAME="zram0" PATH="/dev/zram0" SIZE="1073741824" TYPE="disk" FSTYPE="" MOUNTPOINT="[SWAP]" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="" TRAN="" PKNAME=""`,
	}, "\n")
	ex := &fakeExec{fn: func(name string, args []string) executor.Result {
		switch {
		case name == "findmnt":
			return executor.Result{ExitCode: 0, Output: "/dev/mmcblk1p2\n"}
		case name == "lsblk" && len(args) > 0 && args[0] == "-no":
			return executor.Result{ExitCode: 0, Output: "mmcblk1\n"}
		case name == "lsblk":
			return executor.Result{ExitCode: 0, Output: lsblkOut}
		case name == "sh":
			return executor.Result{ExitCode: 0, Output: "mmcblk0 SD\nmmcblk1 MMC\n"}
		}
		return executor.Result{ExitCode: 0}
	}}

	devs, err := listDevices(context.Background(), ex)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	idx := map[string]Device{}
	for _, d := range devs {
		idx[d.Name] = d
	}
	if _, ok := idx["zram0"]; ok {
		t.Fatal("zram 应被过滤")
	}
	if d := idx["mmcblk0"]; d.Kind != kindSD || !d.Operable {
		t.Fatalf("SD 卡应可操作: %+v", d)
	}
	if d := idx["mmcblk1"]; d.Kind != kindSystem || d.Operable {
		t.Fatalf("eMMC 系统盘应不可操作: %+v", d)
	}
	if d := idx["sda"]; d.Kind != kindUSB || !d.Operable {
		t.Fatalf("USB 应可操作: %+v", d)
	}
}

// mmcDiskTypes 应解析出 name→type（大写）。
func TestMMCDiskTypesParse(t *testing.T) {
	ex := &fakeExec{fn: func(name string, args []string) executor.Result {
		return executor.Result{ExitCode: 0, Output: "mmcblk0 sd\nmmcblk1 MMC\n"}
	}}
	got := mmcDiskTypes(context.Background(), ex)
	if got["mmcblk0"] != "SD" || got["mmcblk1"] != "MMC" {
		t.Fatalf("解析结果错误: %+v", got)
	}
}

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

	// 模拟 sysfs：mmcblk0 是板载 eMMC（MMC），mmcblk1/2 是 SD 卡（SD）。
	mmcTypes := map[string]string{"mmcblk0": "MMC", "mmcblk1": "SD", "mmcblk2": "SD"}
	classifyDevices(devs, "/dev/mmcblk0", true, mmcTypes)
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
		{"mmcblk2", kindSD, false, true},        // 次级 MMC 且 sysfs 判定为 SD 卡
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

	// sysfs 明确为 eMMC 的 mmcblk* 不得因序号启发式被误判为 SD。
	devsE := filterBlockDevices(parseLsblk(out))
	classifyDevices(devsE, "/dev/mmcblk0", true, map[string]string{"mmcblk1": "MMC", "mmcblk2": "MMC"})
	for _, d := range devsE {
		if (d.Name == "mmcblk1" || d.Name == "mmcblk2") && d.Kind != kindInternal {
			t.Fatalf("%s 明确为 MMC，应判为内置，实际 %s", d.Name, d.Kind)
		}
	}

	// 玩客云关键场景：mmcblk0 是 SD 卡（sysfs type=SD）、系统盘在 eMMC(mmcbkl1)。
	// 旧实现按「序号 0 即 eMMC」会把它误判为内置、不可操作。
	oc := strings.Join([]string{
		`NAME="mmcblk0" PATH="/dev/mmcblk0" SIZE="31267481600" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="SD128" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk1" PATH="/dev/mmcblk1" SIZE="7818182656" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="eMMC" LABEL="" TRAN="mmc" PKNAME=""`,
		`NAME="mmcblk1p2" PATH="/dev/mmcblk1p2" SIZE="7340032000" TYPE="part" FSTYPE="ext4" MOUNTPOINT="/" RO="0" RM="0" HOTPLUG="0" MODEL="" LABEL="root" TRAN="" PKNAME="mmcblk1"`,
	}, "\n")
	odev := filterBlockDevices(parseLsblk(oc))
	classifyDevices(odev, "/dev/mmcblk1", true, map[string]string{"mmcblk0": "SD", "mmcblk1": "MMC"})
	oidx := map[string]Device{}
	for _, d := range odev {
		oidx[d.Name] = d
	}
	if d := oidx["mmcblk0"]; d.Kind != kindSD || !d.Operable {
		t.Fatalf("玩客云 mmcblk0(SD) 应可操作，实际 kind=%s operable=%v", d.Kind, d.Operable)
	}
	if d := oidx["mmcblk1"]; d.Kind != kindSystem || d.Operable {
		t.Fatalf("玩客云 mmcblk1(eMMC/系统盘) 应不可操作，实际 kind=%s operable=%v", d.Kind, d.Operable)
	}

	// sysfs 不可用（空 mmcTypes）时回退序号启发式：mmcblk1 仍判为 SD。
	devsF := filterBlockDevices(parseLsblk(out))
	classifyDevices(devsF, "/dev/mmcblk0", true, nil)
	for _, d := range devsF {
		if d.Name == "mmcblk1" && d.Kind != kindSD {
			t.Fatalf("sysfs 缺失时应回退序号启发式判为 SD，实际 %s", d.Kind)
		}
	}

	// 系统盘识别失败时，一切设备均不可操作。
	devs2 := filterBlockDevices(parseLsblk(out))
	classifyDevices(devs2, "", false, mmcTypes)
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

// parseByteSize 必须用 int64 承载：面板跑在 armv7（32 位）时，
// 用 int 会让超过 2^31-1 的容量溢出并回落成 0——这正是 mmcblk0 显示 0 B 的原因。
// 该断言与平台字长无关，32 位机器上同样会失败（若实现退化为 int）。
func TestParseByteSizeHandlesOver32Bit(t *testing.T) {
	gib := float64(1 << 30)
	// 58.2 GiB ≈ 62537072640 B，远超 int32 上限 2147483647
	want58G := int64(58.2 * gib)
	cases := []struct {
		in   string
		want int64
	}{
		{"62537072640", 62537072640},
		{"31267481600", 31267481600},
		{"2147483648", 2147483648}, // 恰好越过 int32 上限
		{"2147483647", 2147483647}, // int32 上限本身
		{"58.2G", want58G},
		{"512M", 512 << 20},
		{"1T", 1 << 40},
		{"0", 0},
		{"", 0},
		{"none", 0},
		{"abc", 0},
		{"12X", 0},
	}
	for _, c := range cases {
		got := parseByteSize(c.in)
		if got != c.want {
			t.Fatalf("parseByteSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if v := parseByteSize("62537072640"); v <= 2147483647 {
		t.Fatalf("大容量必须保留完整 int64 值，实际 %d", v)
	}
}

// 回归：58.2GB 的 mmcblk0 必须解析出真实容量（此前在 32 位面板上为 0）。
func TestParseLsblkLargeDeviceSize(t *testing.T) {
	out := `NAME="mmcblk0" PATH="/dev/mmcblk0" SIZE="62537072640" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="0" HOTPLUG="0" MODEL="eMMC" LABEL="" TRAN="mmc" PKNAME=""`
	devs := parseLsblk(out)
	if len(devs) != 1 {
		t.Fatalf("设备数 = %d, want 1", len(devs))
	}
	if devs[0].Size != 62537072640 {
		t.Fatalf("容量 = %d, want 62537072640（0 表示仍存在 32 位溢出）", devs[0].Size)
	}
}

// lsblk 未给出 SIZE 时，应回退到 sysfs 的扇区数（512 字节/扇区）。
func TestFillSizesFromSysfsFallback(t *testing.T) {
	calls := 0
	ex := &fakeExec{fn: func(name string, args []string) executor.Result {
		calls++
		if name == "sh" {
			return executor.Result{ExitCode: 0, Output: strings.Join([]string{
				"mmcblk0 122144282",  // 122144282 * 512 = 62537872384
				"mmcblk0p1 122136576",
				"sda1 31250000",
				"garbage",
				"mmcblk2 notanumber",
			}, "\n")}
		}
		return executor.Result{ExitCode: 0}
	}}

	devs := []Device{
		{Name: "mmcblk0", Size: 0},
		{Name: "mmcblk0p1", Size: 0},
		{Name: "sda1", Size: 0},
		{Name: "mmcblk2", Size: 0},   // 扇区数非法 → 保持 0
		{Name: "unknown", Size: 0},   // sysfs 里没有 → 保持 0
		{Name: "mmcblk1", Size: 100}, // 已有大小 → 不被覆盖
	}
	fillSizesFromSysfs(context.Background(), ex, devs)

	if devs[0].Size != 122144282*512 {
		t.Fatalf("mmcblk0 = %d, want %d", devs[0].Size, int64(122144282*512))
	}
	if devs[1].Size != 122136576*512 {
		t.Fatalf("mmcblk0p1 = %d", devs[1].Size)
	}
	if devs[2].Size != 31250000*512 {
		t.Fatalf("sda1 = %d", devs[2].Size)
	}
	if devs[3].Size != 0 || devs[4].Size != 0 {
		t.Fatalf("非法/缺失数据不应写入: %+v", devs)
	}
	if devs[5].Size != 100 {
		t.Fatalf("已有大小不应被覆盖: %d", devs[5].Size)
	}
	if calls != 1 {
		t.Fatalf("应只额外执行一次命令，实际 %d", calls)
	}
}

// 全部设备都已有大小时，不应产生额外的 sysfs 查询开销。
func TestFillSizesFromSysfsSkippedWhenSizesPresent(t *testing.T) {
	calls := 0
	ex := &fakeExec{fn: func(string, []string) executor.Result {
		calls++
		return executor.Result{ExitCode: 0}
	}}
	devs := []Device{{Name: "mmcblk0", Size: 62537072640}, {Name: "sda", Size: 16000000000}}
	fillSizesFromSysfs(context.Background(), ex, devs)
	if calls != 0 {
		t.Fatalf("无需兜底时不应执行命令，实际 %d 次", calls)
	}
}

// 端到端：lsblk 返回空 SIZE 时，listDevices 仍应给出正确容量。
func TestListDevicesSizeFallbackEndToEnd(t *testing.T) {
	lsblkOut := strings.Join([]string{
		`NAME="mmcblk0" PATH="/dev/mmcblk0" SIZE="" TYPE="disk" FSTYPE="" MOUNTPOINT="" RO="0" RM="1" HOTPLUG="1" MODEL="SD128" LABEL="" TRAN="mmc" PKNAME=""`,
	}, "\n")
	ex := &fakeExec{fn: func(name string, args []string) executor.Result {
		switch {
		case name == "findmnt":
			return executor.Result{ExitCode: 0, Output: "/dev/mmcblk0p1\n"}
		case name == "lsblk" && len(args) > 0 && args[0] == "-no":
			return executor.Result{ExitCode: 0, Output: "mmcblk0\n"}
		case name == "lsblk":
			return executor.Result{ExitCode: 0, Output: lsblkOut}
		case name == "sh" && len(args) >= 2 && strings.Contains(args[1], "/size"):
			return executor.Result{ExitCode: 0, Output: "mmcblk0 122144282\n"}
		case name == "sh":
			return executor.Result{ExitCode: 0, Output: "mmcblk0 SD\n"}
		}
		return executor.Result{ExitCode: 0}
	}}

	devs, err := listDevices(context.Background(), ex)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("设备数 = %d, want 1", len(devs))
	}
	if devs[0].Size != 122144282*512 {
		t.Fatalf("容量应来自 sysfs 兜底，实际 %d", devs[0].Size)
	}
}
