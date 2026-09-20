package recipes

// MethodCompat 单种安装方式的兼容性结论。
type MethodCompat struct {
	Method    string `json:"method"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// Compatibility 判定配方在指定架构上各安装方式的可用性。
func (r *Recipe) Compatibility(arch string) []MethodCompat {
	out := make([]MethodCompat, 0, len(r.Methods))
	for _, m := range r.Methods {
		c := MethodCompat{Method: m, Supported: true}
		switch m {
		case "native":
			if r.Native == nil || !contains(r.Native.Arches, arch) {
				c.Supported = false
				c.Reason = "该应用未提供 " + arch + " 架构的直装支持"
			}
		case "docker":
			if r.Docker == nil || !contains(r.Docker.Arches, arch) {
				c.Supported = false
				c.Reason = "该应用未提供 " + arch + " 架构的容器镜像"
			}
		}
		out = append(out, c)
	}
	return out
}

// Supports 快速判定。
func (r *Recipe) Supports(method, arch string) (bool, string) {
	for _, c := range r.Compatibility(arch) {
		if c.Method == method {
			return c.Supported, c.Reason
		}
	}
	return false, "未知安装方式: " + method
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
