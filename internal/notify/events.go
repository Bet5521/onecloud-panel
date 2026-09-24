package notify

// 通知事件类型：面板内置的 6 类可订阅事件。
// 通道级订阅（管理员为通道勾选）与用户级订阅（用户个人勾选）共同决定事件推送的去向。
const (
	EventNodeOnline  = "node_online"  // 节点上线
	EventNodeOffline = "node_offline" // 节点下线
	EventNodeChange  = "node_change"  // 节点变动（手动添加/编辑/移除）
	EventAppOnline   = "app_online"   // 应用上线
	EventAppOffline  = "app_offline"  // 应用下线
	EventAppChange   = "app_change"   // 应用变动（安装/卸载成功）
)

// AllEvents 全部事件及中文显示名（顺序即前端展示顺序）。
var AllEvents = []struct {
	Code string
	Name string
}{
	{EventNodeOnline, "节点上线"},
	{EventNodeOffline, "节点下线"},
	{EventNodeChange, "节点变动"},
	{EventAppOnline, "应用上线"},
	{EventAppOffline, "应用下线"},
	{EventAppChange, "应用变动"},
}

// ValidEvent 判断事件编码是否有效。
func ValidEvent(e string) bool {
	for _, ev := range AllEvents {
		if ev.Code == e {
			return true
		}
	}
	return false
}

// IsGroupChannelType 群发型通道：按通道配置直接推送（无需按用户定向）。
// 非群发型（wxpusher/sms）为个人型通道：优先按用户接收标识定向推送。
func IsGroupChannelType(typ string) bool {
	switch typ {
	case "wecom", "dingtalk", "serverchan", "webhook":
		return true
	}
	return false
}
