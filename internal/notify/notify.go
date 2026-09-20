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
	if len(w.uids) > 0 {
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

// Send POST {"title","body"} JSON；HTTP 2xx 视为成功。
func (w *genericWebhook) Send(msg Message) error {
	_, err := postJSON(w.url, map[string]string{"title": msg.Title, "body": msg.Body, "content": msg.Body})
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
