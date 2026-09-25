// Package firewall 节点防火墙检测与端口规则管理（ufw / firewalld / nftables / iptables 自适应）。
// 所有操作经节点执行器（本机直连 / Agent 远程统一通道），命令一律参数化传递、不拼 shell。
package firewall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/store"
)

// 后端标识（探测优先级：ufw > firewalld > nftables > iptables）。
const (
	BackendUFW       = "ufw"
	BackendFirewalld = "firewalld"
	BackendNftables  = "nftables"
	BackendIptables  = "iptables"
	BackendNone      = "none"
)

// 规则动作。
const (
	ActionAllow = "allow"
	ActionDeny  = "deny"
)

// Rule 端口规则的统一抽象。
type Rule struct {
	Port   string `json:"port"`             // "53" 或范围 "50000:50100"（冒号分隔）
	Proto  string `json:"proto"`            // tcp / udp / ""（both）
	Action string `json:"action"`           // allow / deny
	Source string `json:"source,omitempty"` // 来源 IP/CIDR，空=任意
}

// Status 节点防火墙检测结果。
type Status struct {
	Backend string `json:"backend"` // ufw / firewalld / nftables / iptables / none
	Active  bool   `json:"active"`
	Rules   []Rule `json:"rules"`
	Detail  string `json:"detail"` // 原始状态文本，供前端折叠展示
}

// Manager 防火墙管理入口。
type Manager struct {
	execFor func(*store.Node) (executor.Executor, error)
}

// New 创建管理器；execFor 提供目标节点的执行器。
func New(execFor func(*store.Node) (executor.Executor, error)) *Manager {
	return &Manager{execFor: execFor}
}

// Detect 探测节点防火墙后端、启停状态与当前规则。
func (m *Manager) Detect(ctx context.Context, n *store.Node) (*Status, error) {
	ex, err := m.execFor(n)
	if err != nil {
		return nil, err
	}
	return detect(ctx, ex), nil
}

// ValidateRule 校验规则参数（API 层预检，返回 400 用）。
func ValidateRule(r Rule) error { return validateRule(r) }

// AddRule 新增端口规则（按检测到的后端分发；both 协议自动拆为 tcp+udp）。
func (m *Manager) AddRule(ctx context.Context, n *store.Node, rule Rule) error {
	return m.mutateRule(ctx, n, rule, false)
}

// RemoveRule 删除端口规则。
func (m *Manager) RemoveRule(ctx context.Context, n *store.Node, rule Rule) error {
	return m.mutateRule(ctx, n, rule, true)
}

func (m *Manager) mutateRule(ctx context.Context, n *store.Node, rule Rule, remove bool) error {
	if err := validateRule(rule); err != nil {
		return err
	}
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	st := detect(ctx, ex)
	for _, r := range expandProto(rule) {
		var err error
		switch st.Backend {
		case BackendUFW:
			err = ufwRule(ctx, ex, r, remove)
		case BackendFirewalld:
			err = firewalldRule(ctx, ex, r, remove)
		case BackendNftables:
			err = nftRule(ctx, ex, r, remove)
		case BackendIptables:
			err = iptablesRule(ctx, ex, r, remove)
		default:
			return errors.New("节点未检测到可用的防火墙工具")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Toggle 开启/关闭防火墙（仅 ufw / firewalld 支持）。
func (m *Manager) Toggle(ctx context.Context, n *store.Node, enabled bool) error {
	ex, err := m.execFor(n)
	if err != nil {
		return err
	}
	st := detect(ctx, ex)
	switch st.Backend {
	case BackendUFW:
		// --force 跳过交互确认（enable 时若默认拒绝入站会警告）
		args := []string{"disable"}
		if enabled {
			args = []string{"--force", "enable"}
		}
		r, err := ex.Exec(ctx, "ufw", args...)
		if err != nil || r.ExitCode != 0 {
			return execFail("ufw", r, err)
		}
	case BackendFirewalld:
		action := "stop"
		if enabled {
			action = "start"
		}
		r, err := ex.Exec(ctx, "systemctl", action, "firewalld")
		if err != nil || r.ExitCode != 0 {
			return execFail("systemctl "+action+" firewalld", r, err)
		}
	default:
		return fmt.Errorf("后端 %s 不支持一键开关，请通过节点终端手动管理", st.Backend)
	}
	return nil
}

// detect 依次探测各后端并解析状态与规则。
func detect(ctx context.Context, ex executor.Executor) *Status {
	if hasCmd(ctx, ex, "ufw") {
		out, _ := run(ctx, ex, "ufw", "status")
		return parseUfwStatus(out)
	}
	if hasCmd(ctx, ex, "firewall-cmd") {
		// --permanent 查询不依赖服务运行状态（未运行也可读持久化配置）
		state, _ := run(ctx, ex, "firewall-cmd", "--state")
		ports, _ := run(ctx, ex, "firewall-cmd", "--permanent", "--list-ports")
		rich, _ := run(ctx, ex, "firewall-cmd", "--permanent", "--list-rich-rules")
		return parseFirewalld(strings.TrimSpace(state) == "running", ports, rich)
	}
	if hasCmd(ctx, ex, "nft") {
		out, _ := run(ctx, ex, "nft", "list", "ruleset")
		return parseNftables(out)
	}
	if hasCmd(ctx, ex, "iptables") {
		out, _ := run(ctx, ex, "iptables", "-S", "INPUT")
		return parseIptables(out)
	}
	return &Status{Backend: BackendNone, Rules: []Rule{}}
}

// ---- 命令执行辅助 ----

// hasCmd 以 <cmd> --version 试探命令是否存在（exit 0 即存在）。
func hasCmd(ctx context.Context, ex executor.Executor, name string) bool {
	r, err := ex.Exec(ctx, name, "--version")
	return err == nil && r.ExitCode == 0
}

// run 执行命令并返回合并输出。
func run(ctx context.Context, ex executor.Executor, name string, args ...string) (string, error) {
	r, err := ex.Exec(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return r.Output, nil
}

// execFail 统一包装执行失败（本地 err 或远端 exit code 非 0）。
func execFail(op string, r *executor.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s 执行失败: %w", op, err)
	}
	return fmt.Errorf("%s 失败: %s", op, strings.TrimSpace(r.Output))
}

// ---- 各后端规则命令 ----

// ufwRule 构造并执行 ufw 规则命令；删除时参数须与添加时完全一致。
func ufwRule(ctx context.Context, ex executor.Executor, rule Rule, remove bool) error {
	args := []string{"allow"}
	if rule.Action == ActionDeny {
		args[0] = "deny"
	}
	if remove {
		args = append([]string{"delete"}, args...)
	}
	if rule.Source != "" {
		args = append(args, "from", rule.Source, "to", "any", "port", rule.Port)
		if rule.Proto != "" {
			args = append(args, "proto", rule.Proto)
		}
	} else if rule.Proto != "" {
		args = append(args, rule.Port+"/"+rule.Proto)
	} else {
		args = append(args, rule.Port)
	}
	r, err := ex.Exec(ctx, "ufw", args...)
	if err != nil || r.ExitCode != 0 {
		return execFail("ufw", r, err)
	}
	return nil
}

// firewalldRule 写入 permanent 配置；服务运行中时最后 reload 生效。
func firewalldRule(ctx context.Context, ex executor.Executor, rule Rule, remove bool) error {
	var args []string
	if rule.Source == "" && rule.Action == ActionAllow {
		// 普通端口放行：--add-port=<port>/<proto>（范围语法 a-b）
		verb := "--add-port"
		if remove {
			verb = "--remove-port"
		}
		args = []string{"--permanent", verb + "=" + hyphen(rule.Port) + "/" + rule.Proto}
	} else {
		// 带来源或拒绝：rich rule（deny 用 reject 提示更友好）
		verb := "--add-rich-rule"
		if remove {
			verb = "--remove-rich-rule"
		}
		target := "accept"
		if rule.Action == ActionDeny {
			target = "reject"
		}
		var sb strings.Builder
		sb.WriteString("rule")
		if rule.Source != "" {
			if strings.Contains(rule.Source, ":") {
				sb.WriteString(` family="ipv6"`)
			} else {
				sb.WriteString(` family="ipv4"`)
			}
			sb.WriteString(` source address="` + rule.Source + `"`)
		}
		sb.WriteString(` port port="` + hyphen(rule.Port) + `" protocol="` + rule.Proto + `" ` + target)
		args = []string{"--permanent", verb + "=" + sb.String()}
	}
	r, err := ex.Exec(ctx, "firewall-cmd", args...)
	if err != nil || r.ExitCode != 0 {
		return execFail("firewall-cmd", r, err)
	}
	if state, _ := run(ctx, ex, "firewall-cmd", "--state"); strings.TrimSpace(state) == "running" {
		r, err := ex.Exec(ctx, "firewall-cmd", "--reload")
		if err != nil || r.ExitCode != 0 {
			return execFail("firewall-cmd --reload", r, err)
		}
	}
	return nil
}

// nftRule 追加规则到固定的 inet/filter/INPUT 链。
func nftRule(ctx context.Context, ex executor.Executor, rule Rule, remove bool) error {
	if remove {
		return nftRemove(ctx, ex, rule)
	}
	args := []string{"add", "rule", "inet", "filter", "INPUT"}
	if rule.Source != "" {
		if strings.Contains(rule.Source, ":") {
			args = append(args, "ip6", "saddr", rule.Source)
		} else {
			args = append(args, "ip", "saddr", rule.Source)
		}
	}
	args = append(args, rule.Proto, "dport", hyphen(rule.Port), nftVerb(rule.Action))
	r, err := ex.Exec(ctx, "nft", args...)
	if err != nil || r.ExitCode != 0 {
		return execFail("nft", r, err)
	}
	return nil
}

// nftRemove 列出带 handle 的 INPUT 链规则，按内容匹配后删除。
func nftRemove(ctx context.Context, ex executor.Executor, rule Rule) error {
	out, _ := run(ctx, ex, "nft", "-a", "list", "chain", "inet", "filter", "INPUT")
	handle := matchNftHandle(out, rule)
	if handle == "" {
		return errors.New("未找到匹配的 nftables 规则（仅支持删除 inet/filter/INPUT 链下的端口规则）")
	}
	r, err := ex.Exec(ctx, "nft", "delete", "rule", "inet", "filter", "INPUT", "handle", handle)
	if err != nil || r.ExitCode != 0 {
		return execFail("nft delete", r, err)
	}
	return nil
}

// iptablesRule 在 INPUT 链追加/删除规则（运行时规则，不持久化）。
func iptablesRule(ctx context.Context, ex executor.Executor, rule Rule, remove bool) error {
	op := "-A"
	if remove {
		op = "-D"
	}
	target := "ACCEPT"
	if rule.Action == ActionDeny {
		target = "DROP"
	}
	args := []string{op, "INPUT"}
	if rule.Source != "" {
		args = append(args, "-s", rule.Source)
	}
	args = append(args, "-p", rule.Proto, "--dport", rule.Port, "-j", target)
	r, err := ex.Exec(ctx, "iptables", args...)
	if err != nil || r.ExitCode != 0 {
		return execFail("iptables", r, err)
	}
	return nil
}

// nftVerb 动作映射：allow→accept / deny→drop。
func nftVerb(action string) string {
	if action == ActionDeny {
		return "drop"
	}
	return "accept"
}
