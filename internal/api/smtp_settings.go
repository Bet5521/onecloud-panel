package api

import (
	"net/http"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/mailer"
	"onecloud-panel/internal/store"
)

// SMTP 设置键
const (
	settingSMTPHost      = "smtp_host"
	settingSMTPPort      = "smtp_port"
	settingSMTPUser      = "smtp_username"
	settingSMTPPass      = "smtp_password"
	settingSMTPFrom      = "smtp_from"
	settingSMTPRecipient = "smtp_test_recipient" // 测试收件人（可选保存）
)

// smtpConfig 从面板设置读取 SMTP 配置。
func (a *API) smtpConfig() mailer.Config {
	g := func(k string) string {
		v, _, _ := a.store.GetSetting(k)
		return v
	}
	return mailer.Config{
		Host:     g(settingSMTPHost),
		Port:     g(settingSMTPPort),
		Username: g(settingSMTPUser),
		Password: a.smtpPassword(),
		From:     g(settingSMTPFrom),
	}
}

// smtpPassword 读取并解密 SMTP 密码；历史明文惰性升级为加密存储。
func (a *API) smtpPassword() string {
	v, _, _ := a.store.GetSetting(settingSMTPPass)
	if v == "" {
		return ""
	}
	if p, err := a.nodes.OpenSecret(v); err == nil {
		return p
	}
	// 兼容历史明文：本次按原值使用，并升级为加密存储
	if enc, err := a.nodes.SealSecret(v); err == nil {
		_ = a.store.SetSetting(settingSMTPPass, enc)
	}
	return v
}

// smtpRecipient 确定密码重置邮件的收件人：
// 优先用户的显式测试收件人设置，否则退回 From 地址（自发自收，常见家用做法）。
func (a *API) smtpRecipient(_ *store.User, mc mailer.Config) string {
	if v, _, _ := a.store.GetSetting(settingSMTPRecipient); v != "" {
		return v
	}
	return mc.From
}

type smtpTestReq struct {
	To string `json:"to"`
}

// POST /api/settings/smtp-test
func (a *API) testSMTP(w http.ResponseWriter, r *http.Request) {
	mc := a.smtpConfig()
	if !mc.Enabled() {
		writeError(w, http.StatusBadRequest, "SMTP 未完整配置（主机/端口/发件人必填）")
		return
	}

	to := mc.From
	var req smtpTestReq
	if err := decodeJSON(r, &req); err == nil && req.To != "" {
		to = req.To
	}

	body := "这是一封来自 OneCloud Panel 的测试邮件。\n\n收到此邮件说明 SMTP 配置正确。"
	if err := mailer.Send(mc, to, "OneCloud Panel SMTP 测试", body); err != nil {
		a.audit.Record(r, "settings", "smtp-test", "settings", "smtp", audit.ResultFailure,
			audit.DetailJSON(map[string]any{"error": err.Error()}))
		writeError(w, http.StatusBadGateway, "测试邮件发送失败: "+err.Error())
		return
	}
	a.audit.Record(r, "settings", "smtp-test", "settings", "smtp", audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"to": to}))
	writeJSON(w, map[string]string{"status": "ok", "hint": "测试邮件已发送至 " + to})
}
