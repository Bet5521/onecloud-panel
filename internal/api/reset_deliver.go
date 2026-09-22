package api

import (
	"errors"
	"strings"

	"onecloud-panel/internal/mailer"
	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
)

// readySMSChannel 返回首条已启用且平台可用（已配置密钥与签名模板）的短信通道。
func (a *API) readySMSChannel() (*store.NotificationChannel, error) {
	channels, err := a.store.EnabledNotificationChannels()
	if err != nil {
		return nil, err
	}
	for i := range channels {
		if channels[i].Type == "sms" && notify.SMSReady(parseConfig(channels[i].ConfigJSON)) {
			return &channels[i], nil
		}
	}
	return nil, errors.New("短信平台未配置")
}

// smsPlatformReady 判断是否存在可用的短信平台通道。
func (a *API) smsPlatformReady() bool {
	_, err := a.readySMSChannel()
	return err == nil
}

// deliverByEmail 向用户绑定邮箱发送重置码；未绑定则回退 SMTP 收件人设置。
func (a *API) deliverByEmail(u *store.User, code string) (string, error) {
	mc := a.smtpConfig()
	if !mc.Enabled() {
		return "", errors.New("SMTP 未配置")
	}
	to := strings.TrimSpace(u.NotifyEmail)
	if to == "" {
		to = a.smtpRecipient(u, mc)
	}
	body := "您正在重置 OneCloud Panel 登录密码。\n\n" +
		"重置码：" + code + "\n" +
		"有效期：15 分钟\n\n" +
		"如非本人操作，请忽略本邮件并检查面板安全。"
	if err := mailer.Send(mc, to, "OneCloud Panel 密码重置码", body); err != nil {
		return "", err
	}
	return "已发送至邮箱 " + maskEmail(to), nil
}

// deliverBySMS 向用户绑定手机号发送短信重置码；短信平台未配置时拒绝发送。
// 优先使用用户个性化接收标识(NotifyTarget)，回退到旧手机号字段。
func (a *API) deliverBySMS(u *store.User, code string) (string, error) {
	phone := derefStr(u.NotifyTarget)
	if phone == "" {
		phone = strings.TrimSpace(u.NotifySMSPhone)
	}
	if phone == "" {
		return "", errors.New("用户未绑定手机号")
	}
	if !a.smsPlatformReady() {
		return "", errors.New("短信平台未配置")
	}
	c, err := a.smsChannelFor(u)
	if err != nil {
		return "", err
	}
	sender, err := notify.Build(c.Type, parseConfig(c.ConfigJSON))
	if err != nil {
		return "", err
	}
	msg := notify.Message{
		Title: "OneCloud Panel 密码重置码",
		Body:  "您正在重置 OneCloud Panel 登录密码，重置码：" + code + "（15 分钟内有效）。",
		To:    phone,
		Code:  code,
	}
	if err := sender.Send(msg); err != nil {
		return "", err
	}
	return "已发送短信至 " + maskPhone(phone), nil
}

// smsChannelFor 选择短信下发通道：优先用户指定的通道，否则取首条可用通道。
func (a *API) smsChannelFor(u *store.User) (*store.NotificationChannel, error) {
	if u.NotifyChannelID != nil && *u.NotifyChannelID > 0 {
		c, err := a.store.NotificationChannelByID(*u.NotifyChannelID)
		if err != nil {
			return nil, errors.New("指定的通知通道不存在")
		}
		if c.Type != "sms" || !c.Enabled || !notify.SMSReady(parseConfig(c.ConfigJSON)) {
			return nil, errors.New("指定的通知通道不可用于短信下发")
		}
		return c, nil
	}
	return a.readySMSChannel()
}

// deliverByChannel 仅通过用户指定的那一条通知通道下发。
func (a *API) deliverByChannel(u *store.User, code string) (string, error) {
	if u.NotifyChannelID == nil || *u.NotifyChannelID <= 0 {
		return "", errors.New("用户未指定通知通道")
	}
	c, err := a.store.NotificationChannelByID(*u.NotifyChannelID)
	if err != nil {
		return "", errors.New("通知通道不存在")
	}
	if !c.Enabled {
		return "", errors.New("通知通道未启用")
	}
	sender, err := notify.Build(c.Type, parseConfig(c.ConfigJSON))
	if err != nil {
		return "", err
	}
	msg := notify.Message{
		Title: "OneCloud Panel 密码重置码",
		Body:  "您正在重置 OneCloud Panel 登录密码。\n重置码：" + code + "\n有效期：15 分钟",
		Code:  code,
		To:    derefStr(u.NotifyTarget), // 个性化：按用户接收标识定向
	}
	if err := sender.Send(msg); err != nil {
		return "", err
	}
	return "已发送至通知通道 " + c.Name, nil
}

// deliverToUserNotify 按用户绑定的通知方式定向下发。
// 返回下发渠道描述（用于服务日志）；log 方式返回空串（仅落服务日志）。
func (a *API) deliverToUserNotify(u *store.User, code string) (string, error) {
	switch u.NotifyMethod {
	case "email":
		return a.deliverByEmail(u, code)
	case "sms":
		return a.deliverBySMS(u, code)
	case "channel":
		return a.deliverByChannel(u, code)
	default: // log：不额外下发，仅由服务日志承载
		return "", nil
	}
}

// deliverResetCodeByMethod 按重置方式下发重置码：
// notification 走用户绑定的通知方式，email 走邮件。
func (a *API) deliverResetCodeByMethod(u *store.User, method, code string) (string, error) {
	if method == "notification" {
		return a.deliverToUserNotify(u, code)
	}
	return a.deliverByEmail(u, code)
}

// maskEmail 邮箱脱敏（保留首字符与域名）。
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at <= 0 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

// maskPhone 手机号脱敏（保留前 3 位与后 4 位）。
func maskPhone(p string) string {
	if len(p) < 7 {
		return "***"
	}
	return p[:3] + "****" + p[len(p)-4:]
}
