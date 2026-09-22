package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
)

type notificationChannelDTO struct {
	ID         int64          `json:"id"`
	Type       string         `json:"type"`
	TypeName   string         `json:"type_name"`
	Name       string         `json:"name"`
	Config     map[string]any `json:"config"`
	Enabled    bool           `json:"enabled"`
	Configured bool           `json:"configured"` // 配置是否完整（可成功构建发送器）
	TestedAt   int64          `json:"tested_at"`
}

// parseConfig 将配置 JSON 解析为扁平字符串映射。
func parseConfig(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		switch vv := v.(type) {
		case string:
			out[k] = vv
		case float64:
			out[k] = strconv.FormatFloat(vv, 'f', -1, 64)
		case bool:
			if vv {
				out[k] = "1"
			} else {
				out[k] = "0"
			}
		}
	}
	return out
}

// maskConfig 回显前对密钥字段脱敏。
func maskConfig(typ string, cfg map[string]string) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	for _, k := range notify.SecretKeys[typ] {
		if v, ok := out[k].(string); ok && v != "" {
			out[k] = notify.Mask
		}
	}
	return out
}

// encodeConfig 构建待入库配置 JSON；掩码字段保留原值。
func encodeConfig(typ, oldJSON string, cfg map[string]string) (string, error) {
	merged := parseConfig(oldJSON)
	for _, k := range notify.SecretKeys[typ] {
		if cfg[k] == notify.Mask && merged[k] != "" {
			cfg[k] = merged[k]
		}
	}
	// 校验：能构建出合法发送器
	if _, err := notify.Build(typ, cfg); err != nil {
		return "", err
	}
	buf, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func (a *API) channelDTO(c store.NotificationChannel) notificationChannelDTO {
	_, configured := notify.Build(c.Type, parseConfig(c.ConfigJSON))
	return notificationChannelDTO{
		ID:         c.ID,
		Type:       c.Type,
		TypeName:   notify.Types[c.Type],
		Name:       c.Name,
		Config:     maskConfig(c.Type, parseConfig(c.ConfigJSON)),
		Enabled:    c.Enabled,
		Configured: configured == nil,
		TestedAt:   c.TestedAt,
	}
}

type channelReq struct {
	Type    string            `json:"type"`
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Enabled *bool             `json:"enabled"`
}

// GET /api/notifications/channels
func (a *API) listNotificationChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := a.store.ListNotificationChannels()
	if err != nil {
		writeError(w, 500, "通知通道查询失败")
		return
	}
	out := make([]notificationChannelDTO, 0, len(channels))
	for _, c := range channels {
		out = append(out, a.channelDTO(c))
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// POST /api/notifications/channels
func (a *API) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var req channelReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, 400, "通道名称必填")
		return
	}
	if notify.Types[req.Type] == "" {
		writeError(w, 400, "不支持的通知类型")
		return
	}
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	cfgJSON, err := encodeConfig(req.Type, "", req.Config)
	if err != nil {
		writeError(w, 400, "配置无效: "+err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// 配置未完成的通道不允许启用
	if enabled {
		if _, berr := notify.Build(req.Type, parseConfig(cfgJSON)); berr != nil {
			writeError(w, 400, "通道未配置完成，无法启用："+berr.Error())
			return
		}
	}
	c, err := a.store.CreateNotificationChannel(&store.NotificationChannel{
		Type: req.Type, Name: req.Name, ConfigJSON: cfgJSON, Enabled: enabled,
	})
	if err != nil {
		writeError(w, 500, "创建通知通道失败")
		return
	}
	a.audit.Record(r, "settings", "notify_channel_create", "settings",
		strconv.FormatInt(c.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"type": c.Type, "name": c.Name}))
	writeJSON(w, a.channelDTO(*c))
}

// PUT /api/notifications/channels/{id}
func (a *API) updateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.NotificationChannelByID(id)
	if err != nil {
		writeError(w, 404, "通知通道不存在")
		return
	}
	var req channelReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	name := c.Name
	if s := strings.TrimSpace(req.Name); s != "" {
		name = s
	}
	cfg := req.Config
	if cfg == nil {
		cfg = map[string]string{}
	}
	cfgJSON, err := encodeConfig(c.Type, c.ConfigJSON, cfg)
	if err != nil {
		writeError(w, 400, "配置无效: "+err.Error())
		return
	}
	enabled := c.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// 配置未完成的通道不允许启用
	if enabled {
		if _, berr := notify.Build(c.Type, parseConfig(cfgJSON)); berr != nil {
			writeError(w, 400, "通道未配置完成，无法启用："+berr.Error())
			return
		}
	}
	if err := a.store.UpdateNotificationChannel(id, name, cfgJSON, enabled); err != nil {
		writeError(w, 500, "更新通知通道失败")
		return
	}
	a.audit.Record(r, "settings", "notify_channel_update", "settings",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	updated, _ := a.store.NotificationChannelByID(id)
	writeJSON(w, a.channelDTO(*updated))
}

// POST /api/notifications/channels/{id}/test — 测试发送。
func (a *API) testNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	c, err := a.store.NotificationChannelByID(id)
	if err != nil {
		writeError(w, 404, "通知通道不存在")
		return
	}
	sender, err := notify.Build(c.Type, parseConfig(c.ConfigJSON))
	if err != nil {
		writeError(w, 400, "配置无效: "+err.Error())
		return
	}
	msg := notify.Message{
		Title: "OneCloud Panel 测试通知",
		Body: "这是一条来自 OneCloud Panel 的测试消息。\n通道: " + c.Name + "\n时间: " +
			time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := sender.Send(msg); err != nil {
		a.audit.Record(r, "settings", "notify_channel_test", "settings",
			strconv.FormatInt(id, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"error": err.Error()}))
		writeError(w, http.StatusBadGateway, "测试发送失败: "+err.Error())
		return
	}
	_ = a.store.MarkNotificationChannelTested(id, time.Now().Unix())
	a.audit.Record(r, "settings", "notify_channel_test", "settings",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	updated, _ := a.store.NotificationChannelByID(id)
	writeJSON(w, a.channelDTO(*updated))
}

// DELETE /api/notifications/channels/{id}
func (a *API) deleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	if _, err := a.store.NotificationChannelByID(id); err != nil {
		writeError(w, 404, "通知通道不存在")
		return
	}
	if err := a.store.DeleteNotificationChannel(id); err != nil {
		writeError(w, 500, "删除失败")
		return
	}
	a.audit.Record(r, "settings", "notify_channel_delete", "settings",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// notifyAll 向全部启用通道发送通知，返回成功通道数与首条错误。
func (a *API) notifyAll(title, body string) (int, error) {
	channels, err := a.store.EnabledNotificationChannels()
	if err != nil {
		return 0, err
	}
	sent := 0
	var firstErr error
	for _, c := range channels {
		sender, err := notify.Build(c.Type, parseConfig(c.ConfigJSON))
		if err != nil {
			continue
		}
		if err := sender.Send(notify.Message{Title: title, Body: body}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	return sent, firstErr
}
