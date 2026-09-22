// Package notify 通知通道抽象与主流推送渠道实现。
// 支持 wxpusher / serverchan（Server酱）/ wecom（企业微信机器人）/
// dingtalk（钉钉机器人）/ webhook（通用）/ sms（阿里云、腾讯云短信）。
package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message 通知消息。
type Message struct {
	Title string
	Body  string
	// To 定向收件人（短信为手机号）；为空时回退通道级默认收件人。
	To string
	// Code 短信模板变量（验证码）；为空时回退 Body。
	Code string
}

// Sender 通知发送接口。
type Sender interface {
	Send(msg Message) error
}

// httpClient 通知发送统一客户端（短超时，避免面板请求被拖死）。
var httpClient = &http.Client{Timeout: 10 * time.Second}

// Mask 掩码常量：GET 回显时密钥字段以该值代替。
const Mask = "******"

// Types 支持的通道类型及显示名。
var Types = map[string]string{
	"wxpusher":   "WxPusher",
	"serverchan": "Server酱",
	"wecom":      "企业微信机器人",
	"dingtalk":   "钉钉机器人",
	"webhook":    "通用 Webhook",
	"sms":        "短信",
}

// SecretKeys 各类型需要脱敏回显的配置键。
var SecretKeys = map[string][]string{
	"wxpusher":   {"app_token"},
	"serverchan": {"sendkey"},
	"dingtalk":   {"secret"},
	"sms":        {"access_key_secret", "secret_key"},
}

// TargetMeta 各通道类型对「每用户接收标识」的需求描述，用于用户维护个人信息时提示。
type TargetMeta struct {
	Needed bool   // 是否必须填写每用户接收标识
	Label  string // 表单标签
	Hint   string // 提示文案
	Secret bool   // 是否敏感（密码框回显）
}

// ChannelTarget 通道类型 → 接收标识需求。仅这几类需要按用户个性化下发。
var ChannelTarget = map[string]TargetMeta{
	"wxpusher": {Needed: true, Label: "接收者 UID", Hint: "WxPusher 接收者 UID，逗号分隔；留空则用通道默认主题", Secret: false},
	"sms":      {Needed: true, Label: "接收手机号", Hint: "留空则使用通道默认接收号码", Secret: false},
	"webhook":  {Needed: false, Label: "接收标识/Key", Hint: "随消息下发的用户标识，由接收端解析使用", Secret: true},
}

// TargetFor 返回通道类型的接收标识需求（未知类型返回零值）。
func TargetFor(typ string) TargetMeta { return ChannelTarget[typ] }

// Build 依据类型与配置构建发送器，并校验必填配置项。
func Build(typ string, cfg map[string]string) (Sender, error) {
	switch typ {
	case "wxpusher":
		tok := strings.TrimSpace(cfg["app_token"])
		if tok == "" {
			return nil, errors.New("app_token 必填")
		}
		return &wxpusher{appToken: tok, uids: splitCSV(cfg["uids"]), topic: strings.TrimSpace(cfg["topic"])}, nil
	case "serverchan":
		key := strings.TrimSpace(cfg["sendkey"])
		if key == "" {
			return nil, errors.New("sendkey 必填")
		}
		return &serverchan{sendKey: key}, nil
	case "wecom":
		wh := strings.TrimSpace(cfg["webhook"])
		if !strings.HasPrefix(wh, "https://") {
			return nil, errors.New("webhook 必填且需为 https 地址")
		}
		return &wecom{webhook: wh}, nil
	case "dingtalk":
		wh := strings.TrimSpace(cfg["webhook"])
		if !strings.HasPrefix(wh, "https://") {
			return nil, errors.New("webhook 必填且需为 https 地址")
		}
		return &dingtalk{webhook: wh, secret: strings.TrimSpace(cfg["secret"])}, nil
	case "webhook":
		u := strings.TrimSpace(cfg["url"])
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil, errors.New("url 必填")
		}
		return &genericWebhook{url: u}, nil
	case "sms":
		return buildSMS(cfg)
	default:
		return nil, fmt.Errorf("不支持的通知类型: %s", typ)
	}
}

// postJSON 发送 JSON POST 并返回响应体（失败时带错误信息）。
func postJSON(endpoint string, payload any) ([]byte, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Post(endpoint, "application/json; charset=utf-8", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}

// ---- WxPusher ----

type wxpusher struct {
	appToken string
	uids     []string
	topic    string
}

type wxpusherResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (w *wxpusher) Send(msg Message) error {
	if len(w.uids) == 0 && w.topic == "" {
		return errors.New("uids 与 topic 至少配置一项")
	}
	payload := map[string]any{
		"appToken":    w.appToken,
		"content":     msg.Title + "\n" + msg.Body,
		"summary":     truncate(msg.Title),
		"contentType": 1,
	}
	// 个性化：消息携带每用户接收标识时，优先定向给该 UID。
	if msg.To != "" {
		payload["uids"] = []string{strings.TrimSpace(msg.To)}
	} else if len(w.uids) > 0 {
		payload["uids"] = w.uids
	}
	if w.topic != "" {
		var topicIDs []int
		for _, part := range strings.Split(w.topic, ",") {
			var id int
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &id); err == nil {
				topicIDs = append(topicIDs, id)
			}
		}
		if len(topicIDs) > 0 {
			payload["topicIds"] = topicIDs
		}
	}
	body, err := postJSON("https://wxpusher.zjiecode.com/api/send/message", payload)
	if err != nil {
		return fmt.Errorf("wxpusher: %w", err)
	}
	var r wxpusherResp
	if err := json.Unmarshal(body, &r); err == nil && r.Code != 1000 {
		return fmt.Errorf("wxpusher: code=%d msg=%s", r.Code, r.Msg)
	}
	return nil
}

// ---- Server酱 ----

type serverchan struct {
	sendKey string
}

func (s *serverchan) Send(msg Message) error {
	form := url.Values{}
	form.Set("title", msg.Title)
	form.Set("desp", msg.Body)
	resp, err := httpClient.PostForm(
		"https://sctapi.ftqq.com/"+url.PathEscape(s.sendKey)+".send", form)
	if err != nil {
		return fmt.Errorf("serverchan: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
	}
	if err := json.Unmarshal(body, &r); err == nil && r.Code != 0 {
		return fmt.Errorf("serverchan: code=%d msg=%s", r.Code, r.Msg)
	}
	return nil
}

// ---- 企业微信机器人 ----

type wecom struct {
	webhook string
}

func (w *wecom) Send(msg Message) error {
	_, err := postJSON(w.webhook, map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": "**" + msg.Title + "**\n" + msg.Body,
		},
	})
	if err != nil {
		return fmt.Errorf("wecom: %w", err)
	}
	return nil
}

// ---- 钉钉机器人 ----

type dingtalk struct {
	webhook string
	secret  string
}

// sign 计算钉钉加签参数（timestamp+HMAC-SHA256）。
func (d *dingtalk) sign() string {
	if d.secret == "" {
		return d.webhook
	}
	ts := time.Now().UnixMilli()
	stringToSign := fmt.Sprintf("%d\n%s", ts, d.secret)
	mac := hmac.New(sha256.New, []byte(d.secret))
	mac.Write([]byte(stringToSign))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	sep := "&"
	if !strings.Contains(d.webhook, "?") {
		sep = "?"
	}
	return fmt.Sprintf("%s%stimestamp=%d&sign=%s", d.webhook, sep, ts, url.QueryEscape(sign))
}

func (d *dingtalk) Send(msg Message) error {
	_, err := postJSON(d.sign(), map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": msg.Title,
			"text":  "**" + msg.Title + "**\n\n" + msg.Body,
		},
	})
	if err != nil {
		return fmt.Errorf("dingtalk: %w", err)
	}
	return nil
}

// ---- 通用 Webhook ----

type genericWebhook struct {
	url string
}

// Send POST {"title","body"} JSON；HTTP 2xx 视为成功。个性化标识随 body 一并下发。
func (w *genericWebhook) Send(msg Message) error {
	payload := map[string]string{"title": msg.Title, "body": msg.Body, "content": msg.Body}
	if msg.To != "" {
		payload["to"] = msg.To
	}
	_, err := postJSON(w.url, payload)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	return nil
}

// ---- 工具 ----

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 20 {
		r = append(r[:20], '…')
	}
	return string(r)
}
