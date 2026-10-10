package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/version"
)

const (
	// upgradeTimeout 单次自升级（下载→校验→试运行→替换）的总预算。
	// 跨架构场景下面板要先从在线 Release 拉产物再转发，低带宽节点上可能偏慢。
	upgradeTimeout = 10 * time.Minute
	// verifyTimeout 新二进制试运行的超时。
	verifyTimeout = 30 * time.Second
	// resultFile 最近一次自升级结果的落盘文件名（位于 Agent 数据目录）。
	resultFile = "upgrade-result.json"

	// UpgradeProto 自升级应答的协议版本。面板据它判断节点 Agent 是否具备
	// 「同步替换 + 如实回报」的能力：旧版 Agent（proto 缺失）的升级实现存在
	// 必然失败的缺陷，面板会改走自己驱动的冷替换来引导升级。
	UpgradeProto = 2
)

// UpgradeReq 面板下发的自升级请求。
type UpgradeReq struct {
	URL     string `json:"url"`     // 相对面板地址的下载路径（如 /api/agent-binary?t=...）
	SHA256  string `json:"sha256"`  // 可选：下载内容校验
	Version string `json:"version"` // 可选：面板期望的目标版本
}

// UpgradeResult 最近一次自升级的结果，随心跳上报给面板。
//
// 之所以落盘而不是只放内存：升级成功后进程会被 systemd 重启，内存态不跨重启
// 存活，面板将永远看不到结果——这正是「点了升级，节点没升级也没报错」的
// 另一半成因（第一半见 applyUpgrade 的注释）。
type UpgradeResult struct {
	TargetVersion  string `json:"target_version,omitempty"`  // 面板指定的目标版本
	AppliedVersion string `json:"applied_version,omitempty"` // 上报时本进程实际运行的版本
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	At             int64  `json:"at,omitempty"` // Unix 秒
}

// Effective 判定升级是否真正生效：二进制替换成功且当前运行版本已等于目标版本。
// 替换成功但服务未重启（systemctl 失败）时，AppliedVersion 仍是旧版本，
// 这里会返回 false，面板据此提示「已替换但未生效」，而不是一直显示升级成功。
func (r *UpgradeResult) Effective() bool {
	if r == nil || !r.OK {
		return false
	}
	if r.TargetVersion == "" {
		return true // 面板未指定目标版本，无法比对，按成功处理
	}
	return normalizeVersion(r.AppliedVersion) == normalizeVersion(r.TargetVersion)
}

func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	return s
}

// ---- 结果落盘 / 读取 ----

func loadUpgradeResult(dataDir string) *UpgradeResult {
	if dataDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dataDir, resultFile))
	if err != nil {
		return nil
	}
	var res UpgradeResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil
	}
	return &res
}

func persistUpgradeResult(dataDir string, res UpgradeResult) {
	if dataDir == "" {
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		return
	}
	p := filepath.Join(dataDir, resultFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("升级结果落盘失败: %v", err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		log.Printf("升级结果落盘失败: %v", err)
	}
}

// ---- 替换逻辑 ----

// selfPath 返回当前可执行文件的真实路径。
// /usr/local/bin/onecloud-panel 可能是符号链接，替换链接本身会把链接变成普通
// 文件，而 systemd 的 ExecStart 仍按链接路径启动，容易出现“换了却没生效”。
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位自身二进制失败：%w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
		return real, nil
	}
	return exe, nil
}

// applyUpgrade 下载新二进制并原子替换 target（Agent 自身可执行文件）。
//
// 历史 bug 的两处根因都在这里，缺一不可：
//
//  1. 临时文件位置。原实现用 os.CreateTemp("", ...) 落在 os.TempDir()，而
//     install.sh 生成的 systemd 单元带 PrivateTmp=true —— 服务看到的是私有
//     独立 tmpfs，与 /usr/local/bin 分属不同文件系统，os.Rename 必然返回
//     EXDEV（invalid cross-device link）。
//  2. 跨设备回退方式。原回退是「覆盖写目标文件」；Linux 上对**正在运行**的
//     可执行文件做带 O_TRUNC 的写入会返回 ETXTBSY（text file busy），
//     因此该回退在任何情况下都不可能成功，升级永远停在“下载了、没替换”。
//
// 结果：替换失败只在 Agent 日志里留一行，面板只看到“指令已下发”，
// 表现为「点了升级，节点没升级，也没有报错」。
//
// 正确做法：把新文件放到目标同目录（同文件系统）后 rename 覆盖目录项——
// 运行中的旧进程继续持有旧 inode，不受影响，且替换是原子的。
func applyUpgrade(ctx context.Context, target string, dl executor.Downloader, fullURL, sha string) error {
	dir := filepath.Dir(target)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".new-*")
	if err != nil {
		return fmt.Errorf("在 %s 创建临时文件失败（目录不可写？）：%w", dir, err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	// ops.Download 以 "<dest>.part" 为中转再改名到 dest；先移除占位文件，
	// 避免 CreateTemp 建出的空文件与下载目标重名造成混淆。
	_ = os.Remove(tmpPath)
	defer func() { _ = os.Remove(tmpPath) }() // 替换后该路径已不存在，静默失败

	if err := dl.Download(ctx, fullURL, tmpPath, 0o755, sha, nil); err != nil {
		return fmt.Errorf("下载升级包失败：%w", err)
	}
	// 替换前先试运行：挡住异架构产物（把 Agent 换成无法启动的二进制 → 节点失联）。
	if err := verifyBinaryFn(ctx, tmpPath); err != nil {
		return err
	}
	return swapExecutable(tmpPath, target)
}

// verifyBinaryFn 试运行校验入口；测试可替换以避免依赖真实可执行文件。
var verifyBinaryFn = verifyBinary

// verifyBinary 试运行新二进制确认它与本机架构兼容且可正常启动。
func verifyBinary(ctx context.Context, path string) error {
	cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "version").CombinedOutput()
	txt := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("新二进制无法在本机运行（架构不匹配或文件损坏）：%v，输出：%s", err, txt)
	}
	if !strings.Contains(txt, "OneCloud Panel") {
		return fmt.Errorf("新二进制自检未通过，输出：%s", txt)
	}
	return nil
}

// swapExecutable 用新文件替换目标可执行文件。
//
// 绝不覆盖写运行中的二进制（Linux 下必然 ETXTBSY），只用目录项改名：
// 首选一次 rename（同一目录即同一文件系统，原子且对运行中的旧进程安全）；
// 万一失败，再退化为「旧文件改名让位 → 新文件就位」，任一步失败都回滚，
// 保证 target 始终指向一个可用的完整二进制。
func swapExecutable(tmpPath, target string) error {
	err := os.Rename(tmpPath, target)
	if err == nil {
		return nil
	}
	firstErr := err

	old := target + ".old"
	_ = os.Remove(old) // 清理上次残留
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("替换二进制失败：就位失败 %v；备份旧文件失败 %v", firstErr, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Rename(old, target) // 回滚
		return fmt.Errorf("替换二进制失败：%v（已回滚）", err)
	}
	_ = os.Chmod(target, 0o755)
	_ = os.Remove(old)
	return nil
}

// ---- server 上的升级状态 ----

// setUpgradeResult 记录并落盘一次升级结果。
func (s *server) setUpgradeResult(res UpgradeResult) {
	s.upgradeMu.Lock()
	s.upgradeRes = &res
	s.upgradeMu.Unlock()
	persistUpgradeResult(s.dataDir, res)
}

// markRestartFailure 在替换成功后标记重启失败（保留原目标版本与时间线）。
func (s *server) markRestartFailure(msg string) {
	s.upgradeMu.Lock()
	base := UpgradeResult{At: time.Now().Unix()}
	if s.upgradeRes != nil {
		base.TargetVersion = s.upgradeRes.TargetVersion
	}
	base.OK = false
	base.Error = msg
	base.At = time.Now().Unix()
	s.upgradeMu.Unlock()
	s.setUpgradeResult(base)
	log.Printf("自升级：%s", msg)
}

// upgradeSnapshot 返回应上报给面板的升级结果；AppliedVersion 用**当前进程**的
// 版本填充，这样「已替换但没重启成功」时面板能看出目标版本与运行版本不一致。
func (s *server) upgradeSnapshot() *UpgradeResult {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	if s.upgradeRes == nil {
		return nil
	}
	out := *s.upgradeRes
	out.AppliedVersion = version.Version
	return &out
}

// restartUnit 重启自身 systemd 单元。重启成功时本进程随即被终止；
// 失败则把原因写回升级结果并释放升级互斥，由下一次心跳带给面板。
func (s *server) restartUnit() {
	// 稍作延迟并已由调用方 Flush，确保 HTTP 应答先于进程退出送达面板。
	time.Sleep(500 * time.Millisecond)
	res, err := s.exec.Exec(context.Background(), "systemctl", "restart", s.unit)
	switch {
	case err != nil:
		s.failRestart(fmt.Sprintf("二进制已替换，但执行 systemctl restart %s 失败：%v；重启服务后新版本才会生效", s.unit, err))
	case res != nil && res.ExitCode != 0:
		s.failRestart(fmt.Sprintf("二进制已替换，但 systemctl restart %s 退出码 %d：%s；重启服务后新版本才会生效",
			s.unit, res.ExitCode, strings.TrimSpace(res.Output)))
	}
	// 走得到这里说明重启没成功、本进程还活着：必须释放互斥，否则后续升级
	// 会一直被 409「已有升级任务在进行中」挡住，节点再也升不了级。
	s.upgrading.Store(false)
}

// failRestart 记录重启失败（保留原目标版本与时间线）。
func (s *server) failRestart(msg string) {
	s.upgradeMu.Lock()
	base := UpgradeResult{At: time.Now().Unix()}
	if s.upgradeRes != nil {
		base.TargetVersion = s.upgradeRes.TargetVersion
	}
	base.OK = false
	base.Error = msg
	base.At = time.Now().Unix()
	s.upgradeMu.Unlock()
	s.setUpgradeResult(base)
	log.Printf("自升级：%s", msg)
}
