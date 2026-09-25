package firewall

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ---- 校验 ----

var (
	rePort   = regexp.MustCompile(`^(\d{1,5})(:\d{1,5})?$`)
	reSource = regexp.MustCompile(`^[0-9a-fA-F.:]+(/\d{1,3})?$`)
)

func validatePort(p string) error {
	if !rePort.MatchString(p) {
		return fmt.Errorf("端口格式无效: %q（应为 53 或范围 50000:50100）", p)
	}
	parts := strings.Split(p, ":")
	nums := make([]int, len(parts))
	for i, s := range parts {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 || v > 65535 {
			return fmt.Errorf("端口超出有效范围: %q（1-65535）", s)
		}
		nums[i] = v
	}
	if len(nums) == 2 && nums[0] > nums[1] {
		return fmt.Errorf("端口范围起止颠倒: %q", p)
	}
	return nil
}

func validateProto(p string) error {
	switch p {
	case "", "tcp", "udp":
		return nil
	}
	return fmt.Errorf("协议仅支持 tcp / udp: %q", p)
}

func validateSource(s string) error {
	if s == "" {
		return nil
	}
	if !reSource.MatchString(s) {
		return fmt.Errorf("来源格式无效: %q（应为 IP 或 CIDR，如 10.0.0.5 或 10.0.0.0/8）", s)
	}
	return nil
}

func validateRule(r Rule) error {
	if err := validatePort(r.Port); err != nil {
		return err
	}
	if err := validateProto(r.Proto); err != nil {
		return err
	}
	if err := validateSource(r.Source); err != nil {
		return err
	}
	if r.Action != ActionAllow && r.Action != ActionDeny {
		return fmt.Errorf("动作仅支持 allow / deny: %q", r.Action)
	}
	return nil
}

// ---- 端口/协议转换 ----

// expandProto both 协议统一拆分为 tcp+udp 两条（跨后端增删行为一致）。
func expandProto(r Rule) []Rule {
	if r.Proto != "" {
		return []Rule{r}
	}
	return []Rule{
		{Port: r.Port, Proto: "tcp", Action: r.Action, Source: r.Source},
		{Port: r.Port, Proto: "udp", Action: r.Action, Source: r.Source},
	}
}

// hyphen 模型端口范围 a:b → firewalld/nft 语法 a-b。
func hyphen(p string) string { return strings.ReplaceAll(p, ":", "-") }

// colon 解析输出的 a-b 范围 → 模型统一 a:b。
func colon(p string) string { return strings.ReplaceAll(p, "-", ":") }

// splitUfwSpec "22/tcp" → ("22","tcp")；"80" → ("80","")。
func splitUfwSpec(spec string) (string, string) {
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

// ---- ufw ----

// ufwActions 动作词映射（LIMIT 视为 allow，REJECT/BLOCK 视为 deny）。
var ufwActions = map[string]string{
	"ALLOW":  ActionAllow,
	"DENY":   ActionDeny,
	"LIMIT":  ActionAllow,
	"REJECT": ActionDeny,
	"BLOCK":  ActionDeny,
}

func parseUfwStatus(out string) *Status {
	st := &Status{Backend: BackendUFW, Rules: []Rule{}, Detail: clampDetail(out)}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Status:") {
			// "inactive" 含 "active" 子串，须 TrimPrefix 后精确比较
			st.Active = strings.TrimSpace(strings.TrimPrefix(t, "Status:")) == "active"
			continue
		}
		if t == "" || strings.Contains(t, "(v6)") {
			continue
		}
		f := strings.Fields(t)
		if f[0] == "Anywhere" {
			continue // 接口绑定规则（如 Anywhere on eth0），无端口语义
		}
		// 定位动作词，且右邻必须为 IN（入站规则）
		ai := -1
		for i := 0; i+1 < len(f); i++ {
			if _, ok := ufwActions[f[i]]; ok && f[i+1] == "IN" {
				ai = i
				break
			}
		}
		if ai < 1 {
			continue
		}
		port, proto := splitUfwSpec(f[ai-1])
		src := ""
		if ai+2 < len(f) && f[ai+2] != "Anywhere" {
			src = f[ai+2]
		}
		st.Rules = append(st.Rules, Rule{Port: colon(port), Proto: proto, Action: ufwActions[f[ai]], Source: src})
	}
	return st
}

// ---- firewalld ----

var (
	reRichSrc    = regexp.MustCompile(`source address="([^"]*)"`)
	reRichPort   = regexp.MustCompile(`port port="([^"]*)"`)
	reRichProto  = regexp.MustCompile(`protocol="([^"]*)"`)
	reRichAction = regexp.MustCompile(`(accept|reject|drop)\s*$`)
)

func parseFirewalld(running bool, portsOut, richOut string) *Status {
	st := &Status{Backend: BackendFirewalld, Active: running, Rules: []Rule{}}
	// --list-ports 输出："80/tcp 443/udp 50000-50100/tcp"（均为放行）
	for _, spec := range strings.Fields(portsOut) {
		parts := strings.SplitN(spec, "/", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		st.Rules = append(st.Rules, Rule{Port: colon(parts[0]), Proto: parts[1], Action: ActionAllow})
	}
	// rich rules 逐行解析
	for _, line := range strings.Split(richOut, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		pm := reRichPort.FindStringSubmatch(t)
		if pm == nil || pm[1] == "" {
			continue // 仅展示端口类规则
		}
		r := Rule{Port: colon(pm[1]), Action: ActionAllow}
		if m := reRichProto.FindStringSubmatch(t); m != nil {
			r.Proto = m[1]
		}
		if m := reRichSrc.FindStringSubmatch(t); m != nil {
			r.Source = m[1]
		}
		if m := reRichAction.FindStringSubmatch(t); m != nil && m[1] != "accept" {
			r.Action = ActionDeny
		}
		st.Rules = append(st.Rules, r)
	}
	st.Detail = clampDetail(strings.TrimSpace(portsOut) + "\n" + strings.TrimSpace(richOut))
	return st
}

// ---- iptables ----

var (
	reIptSrc    = regexp.MustCompile(`(?:^|\s)-s\s+(\S+)`)
	reIptProto  = regexp.MustCompile(`(?:^|\s)-p\s+(\S+)`)
	reIptDport  = regexp.MustCompile(`(?:^|\s)--dport\s+(\S+)`)
	reIptTarget = regexp.MustCompile(`(?:^|\s)-j\s+(\S+)`)
)

func parseIptables(out string) *Status {
	st := &Status{Backend: BackendIptables, Rules: []Rule{}, Detail: clampDetail(out)}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "-P INPUT ") {
			st.Active = strings.TrimSpace(strings.TrimPrefix(t, "-P INPUT ")) != "ACCEPT"
			continue
		}
		if !strings.HasPrefix(t, "-A INPUT ") {
			continue
		}
		dm := reIptDport.FindStringSubmatch(t)
		if dm == nil {
			continue // 无端口的规则（如 state/multiport 行）不展示
		}
		proto, target := "", ""
		if m := reIptProto.FindStringSubmatch(t); m != nil {
			proto = m[1]
		}
		if m := reIptTarget.FindStringSubmatch(t); m != nil {
			target = m[1]
		}
		if proto != "tcp" && proto != "udp" {
			continue
		}
		if target != "ACCEPT" && target != "DROP" && target != "REJECT" {
			continue
		}
		src := ""
		if m := reIptSrc.FindStringSubmatch(t); m != nil {
			src = m[1]
		}
		action := ActionAllow
		if target != "ACCEPT" {
			action = ActionDeny
		}
		st.Rules = append(st.Rules, Rule{Port: colon(dm[1]), Proto: proto, Action: action, Source: src})
	}
	if len(st.Rules) > 0 {
		st.Active = true
	}
	return st
}

// ---- nftables ----

var (
	reNftRule   = regexp.MustCompile(`(?:ip6? saddr (\S+)\s+)?(tcp|udp) dport (\S+) (accept|drop)`)
	reNftHandle = regexp.MustCompile(`# handle (\d+)`)
)

func parseNftables(out string) *Status {
	st := &Status{Backend: BackendNftables, Rules: []Rule{}, Detail: clampDetail(out)}
	inInput := false
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if !inInput {
			if strings.HasPrefix(t, "chain INPUT {") {
				inInput = true
				st.Active = true
			}
			continue
		}
		if t == "}" {
			inInput = false
			continue
		}
		m := reNftRule.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		action := ActionAllow
		if m[4] == "drop" {
			action = ActionDeny
		}
		st.Rules = append(st.Rules, Rule{Port: colon(m[3]), Proto: m[2], Action: action, Source: m[1]})
	}
	return st
}

// matchNftHandle 在 `nft -a list chain` 输出中按内容（协议+端口+动作+来源精确匹配）
// 定位规则并返回其 handle 号；未匹配返回空串。
func matchNftHandle(out string, rule Rule) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "# handle") {
			continue
		}
		m := reNftRule.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[2] != rule.Proto || colon(m[3]) != rule.Port || m[4] != nftVerb(rule.Action) {
			continue
		}
		if (m[1] == "") != (rule.Source == "") {
			continue
		}
		if m[1] != "" && m[1] != rule.Source {
			continue
		}
		if h := reNftHandle.FindStringSubmatch(line); h != nil {
			return h[1]
		}
	}
	return ""
}

// ---- 公共 ----

// maxDetail Detail 原始文本上限（nft list ruleset 可能非常大，避免响应膨胀）。
const maxDetail = 8192

func clampDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxDetail {
		return s[:maxDetail] + "\n...（已截断）"
	}
	return s
}
