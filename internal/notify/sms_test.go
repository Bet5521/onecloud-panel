package notify

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// aliyunEncodeTest 独立实现的 RFC3986 编码（不复用被测代码，保证复算独立）。
func aliyunEncodeTest(s string) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}
	return b.String()
}

// 阿里云短信：签名可被独立复算，且必填业务参数正确。
func TestSMSAliyunSignature(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotForm = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","Message":"OK"}`)
	}))
	defer srv.Close()

	oldURL := aliyunSMSURL
	aliyunSMSURL = srv.URL + "/"
	defer func() { aliyunSMSURL = oldURL }()

	const secret = "AlYunSecret"
	cfg := map[string]string{
		"provider": "aliyun", "access_key_id": "AlYunKey", "access_key_secret": secret,
		"sign_name": "面板签名", "template_code": "SMS_123456",
	}
	s, err := Build("sms", cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !SMSReady(cfg) {
		t.Fatal("配置齐全的阿里云通道应判定为可用")
	}
	if err := s.Send(Message{To: "13800138000", Code: "ABCD1234"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	vals, err := url.ParseQuery(gotForm)
	if err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}
	sig := vals.Get("Signature")
	if sig == "" {
		t.Fatalf("请求缺少 Signature: %s", gotForm)
	}
	if vals.Get("Action") != "SendSms" || vals.Get("Version") != "2017-05-25" {
		t.Fatalf("公共参数不符: %v", vals)
	}
	if vals.Get("PhoneNumbers") != "13800138000" || vals.Get("SignName") != "面板签名" ||
		vals.Get("TemplateCode") != "SMS_123456" {
		t.Fatalf("业务参数不符: %v", vals)
	}
	if !strings.Contains(vals.Get("TemplateParam"), "ABCD1234") {
		t.Fatalf("模板变量未携带验证码: %v", vals.Get("TemplateParam"))
	}

	// 独立复算签名
	vals.Del("Signature")
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, aliyunEncodeTest(k)+"="+aliyunEncodeTest(vals.Get(k)))
	}
	stringToSign := "POST&%2F&" + aliyunEncodeTest(strings.Join(parts, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if sig != want {
		t.Fatalf("阿里云签名不匹配\n got=%s\nwant=%s", sig, want)
	}
}

// 阿里云返回非 OK 时应报错。
func TestSMSAliyunErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"触发流控"}`)
	}))
	defer srv.Close()

	oldURL := aliyunSMSURL
	aliyunSMSURL = srv.URL + "/"
	defer func() { aliyunSMSURL = oldURL }()

	s, _ := Build("sms", map[string]string{
		"provider": "aliyun", "access_key_id": "k", "access_key_secret": "s",
		"sign_name": "签", "template_code": "T",
	})
	err := s.Send(Message{To: "13800138000", Code: "123456"})
	if err == nil || !strings.Contains(err.Error(), "BUSINESS_LIMIT_CONTROL") {
		t.Fatalf("应返回平台错误码, got %v", err)
	}
}

// 腾讯云短信：TC3 签名可被独立复算，手机号自动补 +86。
func TestSMSTencentSignature(t *testing.T) {
	var (
		gotAuth, gotTS, gotCT string
		gotBody               []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTS = r.Header.Get("X-TC-Timestamp")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"Response":{"SendStatusSet":[{"Code":"Ok","Message":"send success"}]}}`)
	}))
	defer srv.Close()

	oldURL, oldHost := tencentSMSURL, tencentSMSHost
	tencentSMSURL = srv.URL + "/"
	tencentSMSHost = strings.TrimPrefix(srv.URL, "http://")
	defer func() { tencentSMSURL, tencentSMSHost = oldURL, oldHost }()

	const secretKey = "TencentSecretKey"
	cfg := map[string]string{
		"provider": "tencent", "secret_id": "TencentSecretId", "secret_key": secretKey,
		"app_id": "1400000000", "sign_name": "面板签名", "template_code": "1234567",
	}
	s, err := Build("sms", cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := s.Send(Message{To: "13800138000", Code: "WXYZ9876"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if !strings.Contains(string(gotBody), `"+8613800138000"`) ||
		!strings.Contains(string(gotBody), `"WXYZ9876"`) {
		t.Fatalf("请求体不符: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"1400000000"`) {
		t.Fatalf("请求体缺少 SdkAppId: %s", gotBody)
	}

	// 独立复算 TC3 签名
	ts, err := strconv.ParseInt(gotTS, 10, 64)
	if err != nil {
		t.Fatalf("时间戳非法: %q", gotTS)
	}
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")
	signedHeaders := "content-type;host;x-tc-action"
	canonicalHeaders := "content-type:" + gotCT + "\n" +
		"host:" + tencentSMSHost + "\n" +
		"x-tc-action:sendsms\n"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" +
		hexSHA256(gotBody)
	stringToSign := "TC3-HMAC-SHA256\n" + gotTS + "\n" +
		date + "/sms/tc3_request\n" + hexSHA256([]byte(canonicalRequest))

	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, "sms")
	secretSigning := hmacSHA256(secretService, "tc3_request")
	sig := hexEncode(hmacSHA256(secretSigning, stringToSign))
	want := "TC3-HMAC-SHA256 Credential=TencentSecretId/" + date +
		"/sms/tc3_request, SignedHeaders=" + signedHeaders + ", Signature=" + sig
	if gotAuth != want {
		t.Fatalf("腾讯云签名不匹配\n got=%s\nwant=%s", gotAuth, want)
	}
}

func hexEncode(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexDigits[c>>4], hexDigits[c&0x0f])
	}
	return string(out)
}

// 未实现的平台：允许保存配置，但发送时明确提示"暂未实现"，且判定为不可用。
func TestSMSProviderNotImplemented(t *testing.T) {
	cfg := map[string]string{"provider": "huawei", "sign_name": "签", "template_code": "T"}
	s, err := Build("sms", cfg)
	if err != nil {
		t.Fatalf("未实现平台也应允许保存配置: %v", err)
	}
	err = s.Send(Message{To: "13800138000", Code: "1234"})
	if err == nil || !strings.Contains(err.Error(), ErrProviderNotImplemented.Error()) {
		t.Fatalf("应提示暂未实现, got %v", err)
	}
	if SMSReady(cfg) {
		t.Fatal("未实现的平台不应判定为可用")
	}
	if SMSReady(map[string]string{"provider": "baidu", "sign_name": "签", "template_code": "T"}) {
		t.Fatal("百度云未实现，不应判定为可用")
	}
}

// 必填项缺失时构建失败；未知平台被拒。
func TestSMSBuildValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]string
	}{
		{"缺 provider", map[string]string{"sign_name": "签", "template_code": "T"}},
		{"未知平台", map[string]string{"provider": "aws", "sign_name": "签", "template_code": "T"}},
		{"缺签名", map[string]string{"provider": "aliyun", "access_key_id": "k",
			"access_key_secret": "s", "template_code": "T"}},
		{"缺模板", map[string]string{"provider": "aliyun", "access_key_id": "k",
			"access_key_secret": "s", "sign_name": "签"}},
		{"阿里云缺密钥", map[string]string{"provider": "aliyun", "sign_name": "签", "template_code": "T"}},
		{"腾讯云缺 app_id", map[string]string{"provider": "tencent", "secret_id": "a",
			"secret_key": "b", "sign_name": "签", "template_code": "T"}},
	}
	for _, c := range cases {
		if _, err := Build("sms", c.cfg); err == nil {
			t.Fatalf("%s 应构建失败", c.name)
		}
		if SMSReady(c.cfg) {
			t.Fatalf("%s 不应判定为可用", c.name)
		}
	}
}

// 收件号码缺失时拒绝发送。
func TestSMSMissingRecipient(t *testing.T) {
	s, _ := Build("sms", map[string]string{
		"provider": "aliyun", "access_key_id": "k", "access_key_secret": "s",
		"sign_name": "签", "template_code": "T",
	})
	if err := s.Send(Message{Code: "1234"}); err == nil {
		t.Fatal("无收件号码应拒绝发送")
	}
}
