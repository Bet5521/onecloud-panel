package apps

import (
	"sort"
	"strconv"
	"strings"

	"onecloud-panel/internal/recipes"
)

// AccessPort 应用对外可访问端口（状态接口回传给前端生成访问入口链接）。
type AccessPort struct {
	Port        int    `json:"port"`
	Proto       string `json:"proto"`       // tcp / udp
	Description string `json:"description"` // 取自配方的端口说明（可空）
}

// recipePorts 从配方声明的端口生成访问入口列表。
// 仅在容器未运行（拿不到实际映射）或非容器安装时作为回退/默认值。
func recipePorts(r *recipes.Recipe) []AccessPort {
	if r == nil {
		return []AccessPort{}
	}
	out := make([]AccessPort, 0, len(r.Ports))
	for _, p := range r.Ports {
		if p.Port <= 0 {
			continue
		}
		out = append(out, AccessPort{
			Port: p.Port, Proto: normalizeProto(p.Proto), Description: p.Description,
		})
	}
	return out
}

// accessPortsFromInspect 依据容器 inspect 结果计算「宿主可访问端口」，
// 以容器实际端口映射为准（用户安装时可能覆盖了配方默认端口）。
// host 网络模式下无映射，退回容器端口本身；描述取自配方同端口声明。
func accessPortsFromInspect(ci containerInspect, r *recipes.Recipe) []AccessPort {
	desc := map[string]string{}
	if r != nil {
		for _, p := range r.Ports {
			desc[portKey(p.Port, p.Proto)] = p.Description
		}
	}
	hostMode := ci.HostConfig.NetworkMode == "host"
	seen := map[int]bool{}
	var out []AccessPort
	add := func(port int, proto, d string) {
		if port <= 0 || seen[port] {
			return
		}
		seen[port] = true
		out = append(out, AccessPort{Port: port, Proto: proto, Description: d})
	}
	for key, binds := range ci.NetworkSettings.Ports {
		cport, proto := splitPortKey(key)
		if hostMode {
			add(cport, proto, desc[key])
			continue
		}
		for _, b := range binds {
			if b.HostPort == "" {
				continue
			}
			hp, err := strconv.Atoi(b.HostPort)
			if err != nil {
				continue
			}
			add(hp, proto, desc[key])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// portKey 归一化 "端口/协议" 键（容器 inspect 用 "80/tcp"）。
func portKey(port int, proto string) string {
	return strconv.Itoa(port) + "/" + normalizeProto(proto)
}

// splitPortKey 拆分 "80/tcp" → (80, "tcp")；无法解析时端口为 0。
func splitPortKey(key string) (int, string) {
	ps, proto := key, "tcp"
	if i := strings.IndexByte(key, '/'); i >= 0 {
		ps, proto = key[:i], key[i+1:]
	}
	p, err := strconv.Atoi(strings.TrimSpace(ps))
	if err != nil {
		return 0, normalizeProto(proto)
	}
	return p, normalizeProto(proto)
}

// normalizeProto 端口协议归一（空值按 tcp）。
func normalizeProto(proto string) string {
	p := strings.ToLower(strings.TrimSpace(proto))
	if p == "" {
		return "tcp"
	}
	return p
}
