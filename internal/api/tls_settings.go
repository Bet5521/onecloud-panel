package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/tlsutil"
)

// TLS 设置键（值存 panel_settings；证书/私钥 PEM 只落文件，不入库）
const (
	settingTLSEnabled  = "tls_enabled"     // "0"/"1"
	settingTLSMode     = "tls_mode"        // "selfsigned"/"custom"
	settingTLSHosts    = "tls_hosts"       // 自签附加域名/IP（逗号分隔）
	settingTLSCertFile = "tls_cert_file"   // 证书文件绝对路径
	settingTLSKeyFile  = "tls_key_file"    // 私钥文件绝对路径
	settingTLSForce    = "tls_force_https" // "0"/"1"
)

type tlsSettingsReq struct {
	Enabled    bool   `json:"enabled"`
	Mode       string `json:"mode"`     // selfsigned / custom
	Hosts      string `json:"hosts"`    // 自签附加 SAN
	CertPEM    string `json:"cert_pem"` // 自定义证书
	KeyPEM     string `json:"key_pem"`  // 自定义私钥
	ForceHTTPS bool   `json:"force_https"`
}

// POST /api/settings/tls
func (a *API) updateTLSSettings(w http.ResponseWriter, r *http.Request) {
	var req tlsSettingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Mode == "" {
		req.Mode = "selfsigned"
	}

	// 关闭 HTTPS：仅更新开关，保留证书文件
	if !req.Enabled {
		if err := a.store.SetSetting(settingTLSEnabled, "0"); err != nil {
			writeError(w, http.StatusInternalServerError, "保存失败")
			return
		}
		if err := a.store.SetSetting(settingTLSForce, boolStr(req.ForceHTTPS)); err != nil {
			writeError(w, http.StatusInternalServerError, "保存失败")
			return
		}
		a.audit.Record(r, "settings", "update", "settings", "tls", audit.ResultSuccess,
			audit.DetailJSON(map[string]any{"tls_enabled": false}))
		writeJSON(w, map[string]any{"status": "ok", "restart_required": true})
		return
	}

	if req.Mode != "selfsigned" && req.Mode != "custom" {
		writeError(w, http.StatusBadRequest, "tls_mode 仅支持 selfsigned 或 custom")
		return
	}
	if len(req.Hosts) > 500 {
		writeError(w, http.StatusBadRequest, "域名列表过长")
		return
	}

	tlsDir := filepath.Join(a.dataDir, "tls")
	if err := os.MkdirAll(tlsDir, 0o750); err != nil {
		writeError(w, http.StatusInternalServerError, "TLS 目录创建失败")
		return
	}

	var certFile, keyFile string
	switch req.Mode {
	case "selfsigned":
		extra := splitHosts(req.Hosts)
		hosts := tlsutil.LocalHosts(extra)
		certPEM, keyPEM, err := tlsutil.Generate(hosts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "自签证书生成失败: "+err.Error())
			return
		}
		certFile = filepath.Join(tlsDir, "self-cert.pem")
		keyFile = filepath.Join(tlsDir, "self-key.pem")
		if err := writeFile0644(certFile, certPEM); err != nil {
			writeError(w, http.StatusInternalServerError, "证书写入失败")
			return
		}
		if err := writeFile0600(keyFile, keyPEM); err != nil {
			writeError(w, http.StatusInternalServerError, "私钥写入失败")
			return
		}

	case "custom":
		certPEM := []byte(strings.TrimSpace(req.CertPEM))
		keyPEM := []byte(strings.TrimSpace(req.KeyPEM))
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			writeError(w, http.StatusBadRequest, "自定义模式需提供证书与私钥 PEM 内容")
			return
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			writeError(w, http.StatusBadRequest, "证书与私钥不匹配或格式错误: "+err.Error())
			return
		}
		// 解析叶子证书：过期/域名校验提示
		leaf, err := leafCert(&pair)
		if err != nil {
			writeError(w, http.StatusBadRequest, "证书解析失败: "+err.Error())
			return
		}
		if time.Now().After(leaf.NotAfter) {
			writeError(w, http.StatusBadRequest, "证书已过期")
			return
		}

		certFile = filepath.Join(tlsDir, "custom-cert.pem")
		keyFile = filepath.Join(tlsDir, "custom-key.pem")
		if err := writeFile0644(certFile, certPEM); err != nil {
			writeError(w, http.StatusInternalServerError, "证书写入失败")
			return
		}
		if err := writeFile0600(keyFile, keyPEM); err != nil {
			writeError(w, http.StatusInternalServerError, "私钥写入失败")
			return
		}
	}

	settings := map[string]string{
		settingTLSEnabled:  "1",
		settingTLSMode:     req.Mode,
		settingTLSHosts:    strings.Join(splitHosts(req.Hosts), ","),
		settingTLSCertFile: certFile,
		settingTLSKeyFile:  keyFile,
		settingTLSForce:    boolStr(req.ForceHTTPS),
	}
	for k, v := range settings {
		if err := a.store.SetSetting(k, v); err != nil {
			writeError(w, http.StatusInternalServerError, "设置保存失败")
			return
		}
	}

	a.audit.Record(r, "settings", "update", "settings", "tls", audit.ResultSuccess,
		audit.DetailJSON(map[string]any{
			"tls_enabled": true, "mode": req.Mode, "force_https": req.ForceHTTPS}))

	writeJSON(w, map[string]any{
		"status": "ok", "restart_required": true,
		"hint": "TLS 配置已保存，重启面板后生效",
	})
}

func leafCert(pair *tls.Certificate) (*x509.Certificate, error) {
	if pair.Leaf != nil {
		return pair.Leaf, nil
	}
	block, _ := pem.Decode(pair.Certificate[0])
	if block == nil {
		return nil, fmt.Errorf("无法解析证书 PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func splitHosts(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func writeFile0644(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func writeFile0600(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
