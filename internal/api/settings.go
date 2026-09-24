package api

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/version"
)

// smtpPasswordMask SMTP 密码掩码回显占位：提交该值或空值时保留原密码不变。
const smtpPasswordMask = "********"

var editableSettings = map[string]bool{
	"panel_name":           true,
	"audit_retention_days": true,
	"github_proxy":         true,
	// SMTP 邮件服务
	"smtp_host":           true,
	"smtp_port":           true,
	"smtp_username":       true,
	"smtp_password":       true,
	"smtp_from":           true,
	"smtp_test_recipient": true,
	// Docker 镜像加速与第三方仓库（面板级默认值）
	"docker_registry_mirrors":    true,
	"docker_insecure_registries": true,
}

// GET /api/panel/info
func (a *API) panelInfo(w http.ResponseWriter, r *http.Request) {
	name, _, _ := a.store.GetSetting("panel_name")
	installed, _, _ := a.store.GetSetting("installed_at")
	writeJSON(w, map[string]string{
		"name":         name,
		"version":      version.Version,
		"installed_at": installed,
	})
}

// GET /api/settings
func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	all, err := a.store.AllSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "设置查询失败")
		return
	}
	// 敏感项掩码回显：SMTP 密码不以明文/密文回传
	if v := all["smtp_password"]; v != "" {
		all["smtp_password"] = smtpPasswordMask
	}
	writeJSON(w, all)
}

// PUT /api/settings
func (a *API) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	for k, v := range body {
		if !editableSettings[k] {
			writeError(w, http.StatusBadRequest, "不允许修改的设置项: "+k)
			return
		}
		if k == "panel_name" {
			if strings.TrimSpace(v) == "" || len(v) > 64 {
				writeError(w, http.StatusBadRequest, "面板名称需为 1-64 字符")
				return
			}
		}
		if k == "audit_retention_days" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 3650 {
				writeError(w, http.StatusBadRequest, "日志保留天数需为 0-3650")
				return
			}
		}
		if k == "github_proxy" {
			p := strings.TrimSpace(v)
			if p != "" {
				u, err := url.Parse(p)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
					u.Host == "" || len(p) > 200 {
					writeError(w, http.StatusBadRequest,
						"GitHub 加速地址需为合法的 http(s) URL，留空表示直连")
					return
				}
			}
		}
		if k == "smtp_port" && v != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 1 || n > 65535 {
				writeError(w, http.StatusBadRequest, "SMTP 端口需为 1-65535 的数字")
				return
			}
		}
		if k == "smtp_from" && v != "" {
			if !strings.Contains(v, "@") || len(v) > 200 {
				writeError(w, http.StatusBadRequest, "发件人地址格式不正确")
				return
			}
		}
		if k == "docker_registry_mirrors" && strings.TrimSpace(v) != "" {
			for _, m := range splitLines(v) {
				u, err := url.Parse(m)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					writeError(w, http.StatusBadRequest, "Docker 镜像加速地址需为合法的 http(s) URL，每行一个")
					return
				}
			}
		}
		if k == "docker_insecure_registries" && strings.TrimSpace(v) != "" {
			for _, r := range splitLines(v) {
				host := r
				if i := strings.LastIndex(r, ":"); i > 0 {
					host = r[:i]
				}
				if host == "" || net.ParseIP(host) == nil && !strings.Contains(host, ".") {
					writeError(w, http.StatusBadRequest, "第三方仓库地址格式不正确，每行一个（host 或 host:port）")
					return
				}
			}
		}
	}
	for k, v := range body {
		// SMTP 密码特殊处理：空值/掩码占位保留原值；新值加密落库（AES-GCM，密钥在 secret.key）
		if k == "smtp_password" {
			if v == "" || v == smtpPasswordMask {
				continue
			}
			enc, serr := a.nodes.SealSecret(v)
			if serr != nil {
				writeError(w, http.StatusInternalServerError, "密码加密失败")
				return
			}
			v = enc
		}
		if err := a.store.SetSetting(k, v); err != nil {
			writeError(w, http.StatusInternalServerError, "保存失败")
			return
		}
	}
	a.audit.Record(r, "settings", "update", "settings", "", audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"keys": mapKeys(body)}))
	writeJSON(w, map[string]string{"status": "ok"})
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// splitLines 按逗号或换行拆分，并去空白、去空项。
func splitLines(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
