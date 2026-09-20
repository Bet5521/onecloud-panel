package notify

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ErrProviderNotImplemented 该短信平台类型已预留但尚未实现。
var ErrProviderNotImplemented = errors.New("暂未实现")

// 云短信 API 端点；声明为包级变量以便测试注入 httptest 服务地址。
var (
	aliyunSMSURL  = "https://dysmsapi.aliyuncs.com/"
	tencentSMSURL = "https://sms.tencentcloudapi.com/"
	// tencentSMSHost 参与 TC3 签名 canonicalHeaders，需与 tencentSMSURL 的主机名一致。
	tencentSMSHost = "sms.tencentcloudapi.com"
)

// SMSProviders 已实现的云短信平台。
var SMSProviders = map[string]bool{
	"aliyun":  true,
	"tencent": true,
	// 预留：类型与配置项已登记，发送时明确提示"暂未实现"
	"huawei": false,
	"baidu":  false,
}

// SMSProviderNames 短信平台显示名。
var SMSProviderNames = map[string]string{
	"aliyun":  "阿里云",
	"tencent": "腾讯云",
	"huawei":  "华为云",
	"baidu":   "百度云",
}

// smsSender 按 provider 分派的短信发送器。
type smsSender struct {
	provider string
	cfg      map[string]string
}

// buildSMS 依据 provider 构建短信发送器并校验必填配置。
// 未实现的平台（华为云/百度云）同样允许保存配置，仅在 Send 时明确报错。
func buildSMS(cfg map[string]string) (Sender, error) {
	provider := strings.TrimSpace(cfg["provider"])
	if provider == "" {
		return nil, errors.New("provider 必填")
	}
	if _, known := SMSProviders[provider]; !known {
		return nil, fmt.Errorf("不支持的短信平台: %s", provider)
	}
	if strings.TrimSpace(cfg["sign_name"]) == "" {
		return nil, errors.New("sign_name（签名）必填")
	}
	if strings.TrimSpace(cfg["template_code"]) == "" {
		return nil, errors.New("template_code（模板）必填")
	}
	switch provider {
	case "aliyun":
		if strings.TrimSpace(cfg["access_key_id"]) == "" {
			return nil, errors.New("access_key_id 必填")
		}
		if strings.TrimSpace(cfg["access_key_secret"]) == "" {
			return nil, errors.New("access_key_secret 必填")
		}
	case "tencent":
		if strings.TrimSpace(cfg["secret_id"]) == "" {
			return nil, errors.New("secret_id 必填")
		}
		if strings.TrimSpace(cfg["secret_key"]) == "" {
			return nil, errors.New("secret_key 必填")
		}
		if strings.TrimSpace(cfg["app_id"]) == "" {
			return nil, errors.New("app_id（短信应用 SdkAppId）必填")
		}
	}
	return &smsSender{provider: provider, cfg: cfg}, nil
}

// SMSReady 判断短信配置是否真正可用（provider 已实现且必填项齐全）。
// 未实现平台（华为云/百度云）视为不可用，用于拒绝以短信方式下发验证码。
func SMSReady(cfg map[string]string) bool {
	if !SMSProviders[strings.TrimSpace(cfg["provider"])] {
		return false
	}
	_, err := buildSMS(cfg)
	return err == nil
}

// Send 发送短信；收件号码取 msg.To，为空回退通道级 phone。
func (s *smsSender) Send(msg Message) error {
	to := normalPhone(msg.To)
	if to == "" {
		to = normalPhone(s.cfg["phone"])
	}
	if to == "" {
		return errors.New("未指定短信接收号码")
	}
	code := strings.TrimSpace(msg.Code)
	if code == "" {
		code = strings.TrimSpace(msg.Body)
	}
	if code == "" {
		return errors.New("短信模板变量为空")
	}
	switch s.provider {
	case "aliyun":
		return s.sendAliyun(to, code)
	case "tencent":
		return s.sendTencent(to, code)
	}
	return fmt.Errorf("%s 短信%s", SMSProviderNames[s.provider], ErrProviderNotImplemented.Error())
}

// normalPhone 归一化手机号：去空白与连字符。
func normalPhone(s string) string {
	s = strings.TrimSpace(s)
	return strings.NewReplacer(" ", "", "-", "").Replace(s)
}

// ---- 阿里云短信 ----

// sendAliyun RPC 风格调用 dysmsapi（HMAC-SHA1 签名）。
func (s *smsSender) sendAliyun(phone, code string) error {
	region := strings.TrimSpace(s.cfg["region"])
	if region == "" {
		region = "cn-hangzhou"
	}
	tmplParam, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return err
	}
	params := map[string]string{
		"AccessKeyId":      strings.TrimSpace(s.cfg["access_key_id"]),
		"Action":           "SendSms",
		"Format":           "JSON",
		"PhoneNumbers":     phone,
		"RegionId":         region,
		"SignName":         strings.TrimSpace(s.cfg["sign_name"]),
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   randomNonce(),
		"SignatureVersion": "1.0",
		"TemplateCode":     strings.TrimSpace(s.cfg["template_code"]),
		"TemplateParam":    string(tmplParam),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2017-05-25",
	}
	cqs := canonicalQuery(params)
	stringToSign := "POST&%2F&" + percentEncode(cqs)
	mac := hmac.New(sha1.New, []byte(strings.TrimSpace(s.cfg["access_key_secret"])+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	form := cqs + "&Signature=" + percentEncode(signature)
	req, err := http.NewRequest(http.MethodPost, aliyunSMSURL,
		strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("aliyun: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("aliyun: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aliyun: HTTP %d: %s", resp.StatusCode, truncate(string(body)))
	}
	var r struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("aliyun: 响应解析失败: %w", err)
	}
	if r.Code != "OK" {
		return fmt.Errorf("aliyun: code=%s msg=%s", r.Code, r.Message)
	}
	return nil
}

// canonicalQuery 按阿里云规则生成规范化查询串（key 升序、RFC3986 编码）。
func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	return strings.Join(parts, "&")
}

// percentEncode 阿里云要求的 RFC3986 编码（空格转 %20，~ 不转义）。
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func randomNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// ---- 腾讯云短信 ----

// sendTencent TC3-HMAC-SHA256 签名调用 sms.tencentcloudapi.com。
func (s *smsSender) sendTencent(phone, code string) error {
	region := strings.TrimSpace(s.cfg["region"])
	if region == "" {
		region = "ap-guangzhou"
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+86" + phone
	}
	payload, err := json.Marshal(map[string]any{
		"PhoneNumberSet":   []string{phone},
		"SmsSdkAppId":      strings.TrimSpace(s.cfg["app_id"]),
		"SignName":         strings.TrimSpace(s.cfg["sign_name"]),
		"TemplateId":       strings.TrimSpace(s.cfg["template_code"]),
		"TemplateParamSet": []string{code},
	})
	if err != nil {
		return err
	}
	ts := time.Now().Unix()
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")
	host := tencentSMSHost
	const action = "SendSms"
	contentType := "application/json; charset=utf-8"

	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hexSHA256(payload)

	stringToSign := "TC3-HMAC-SHA256\n" + fmt.Sprintf("%d", ts) + "\n" +
		date + "/sms/tc3_request\n" + hexSHA256([]byte(canonicalRequest))

	secretKey := strings.TrimSpace(s.cfg["secret_key"])
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, "sms")
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	req, err := http.NewRequest(http.MethodPost, tencentSMSURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2021-01-11")
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-TC-Region", region)
	req.Header.Set("Authorization", fmt.Sprintf(
		"TC3-HMAC-SHA256 Credential=%s/%s/sms/tc3_request, SignedHeaders=%s, Signature=%s",
		strings.TrimSpace(s.cfg["secret_id"]), date, signedHeaders, signature))

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tencent: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("tencent: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tencent: HTTP %d: %s", resp.StatusCode, truncate(string(body)))
	}
	var r struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
			SendStatusSet []struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"SendStatusSet"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("tencent: 响应解析失败: %w", err)
	}
	if r.Response.Error != nil {
		return fmt.Errorf("tencent: code=%s msg=%s", r.Response.Error.Code, r.Response.Error.Message)
	}
	for _, st := range r.Response.SendStatusSet {
		if st.Code != "Ok" {
			return fmt.Errorf("tencent: code=%s msg=%s", st.Code, st.Message)
		}
	}
	return nil
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}
