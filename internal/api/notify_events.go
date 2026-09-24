package api

import (
	"log"

	"onecloud-panel/internal/notify"
)

// EmitEvent 事件分发引擎：将事件推送至订阅了该事件的通道与用户。
// 分发规则（两级过滤）：
//  1. 通道级：启用中且订阅了该事件的通道（NotificationChannelsSubscribed）
//     - 群发型（wecom/dingtalk/serverchan/webhook）直接按通道配置推送；
//     - 个人型（wxpusher/sms）查绑定该通道且订阅了该事件的用户，按其接收标识定向下发。
//  2. 用户级：订阅了该事件且通知方式为 email/sms 的用户直接发送
//     （channel 方式的用户已由步骤 1 的个人型通道覆盖，不重复发送）。
//
// 单个通道/收件人失败仅记服务日志，不影响其他收件人。
func (a *API) EmitEvent(event, title, body string) {
	channels, err := a.store.NotificationChannelsSubscribed(event)
	if err != nil {
		log.Printf("[通知事件] %s 查询订阅通道失败: %v", event, err)
	}
	for i := range channels {
		c := channels[i]
		sender, berr := notify.Build(c.Type, parseConfig(c.ConfigJSON))
		if berr != nil {
			log.Printf("[通知事件] %s 通道 %s(%d) 构建发送器失败，跳过: %v", event, c.Name, c.ID, berr)
			continue
		}
		if notify.IsGroupChannelType(c.Type) {
			if serr := sender.Send(notify.Message{Title: title, Body: body}); serr != nil {
				log.Printf("[通知事件] %s 群发通道 %s(%d) 发送失败: %v", event, c.Name, c.ID, serr)
			}
			continue
		}
		// 个人型通道：按用户接收标识定向
		users, uerr := a.store.UsersBoundToChannelSubscribed(c.ID, event)
		if uerr != nil {
			log.Printf("[通知事件] %s 通道 %s(%d) 查询绑定用户失败: %v", event, c.Name, c.ID, uerr)
			continue
		}
		for j := range users {
			u := users[j]
			to := derefStr(u.NotifyTarget)
			if to == "" {
				to = u.NotifySMSPhone
			}
			if serr := sender.Send(notify.Message{Title: title, Body: body, To: to}); serr != nil {
				log.Printf("[通知事件] %s 通道 %s(%d) → 用户 %s 发送失败: %v", event, c.Name, c.ID, u.Username, serr)
			}
		}
	}

	// email/sms 方式用户直发
	users, err := a.store.UsersSubscribedToEvent(event)
	if err != nil {
		log.Printf("[通知事件] %s 查询订阅用户失败: %v", event, err)
		return
	}
	for i := range users {
		u := users[i]
		switch u.NotifyMethod {
		case "email":
			if serr := a.sendUserEmail(&u, title, body); serr != nil {
				log.Printf("[通知事件] %s 邮件 → 用户 %s 发送失败: %v", event, u.Username, serr)
			}
		case "sms":
			if serr := a.sendUserSMS(&u, title, body); serr != nil {
				log.Printf("[通知事件] %s 短信 → 用户 %s 发送失败: %v", event, u.Username, serr)
			}
		}
	}
}

// emitEventLog 以 log 方式落服务日志（无外部收件人时的兜底）。
func (a *API) emitEventLog(event, title, body string) {
	log.Printf("[通知事件] %s %s %s", event, title, body)
}
