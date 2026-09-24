package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// captchaTTL 验证码有效期。
const captchaTTL = 5 * time.Minute

// captchaCharset 验证码字符集（去除易混淆的 0/O/1/l/I）。
const captchaCharset = "23456789ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz"

type captchaEntry struct {
	answer  string
	expires int64
}

// CaptchaStore 进程内验证码存储：一次性使用，5 分钟过期，Generate 时惰性清理。
type CaptchaStore struct {
	mu      sync.Mutex
	entries map[string]captchaEntry
}

// NewCaptchaStore 创建验证码存储。
func NewCaptchaStore() *CaptchaStore {
	return &CaptchaStore{entries: map[string]captchaEntry{}}
}

// randToken 生成随机 ID。
func randToken() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// randCode 生成 n 位随机验证码。
func randCode(n int) string {
	var b strings.Builder
	max := big.NewInt(int64(len(captchaCharset)))
	for i := 0; i < n; i++ {
		idx, _ := rand.Int(rand.Reader, max)
		b.WriteByte(captchaCharset[idx.Int64()])
	}
	return b.String()
}

// randInt 返回 [min, max] 区间随机整数。
func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return min + int(n.Int64())
}

// Generate 生成新验证码，返回 (ID, data URI 图像)。
func (c *CaptchaStore) Generate() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 惰性清理过期项
	now := time.Now().Unix()
	for id, e := range c.entries {
		if e.expires <= now {
			delete(c.entries, id)
		}
	}

	code := randCode(4)
	id := randToken()
	c.entries[id] = captchaEntry{answer: code, expires: now + int64(captchaTTL/time.Second)}
	return id, captchaSVG(code)
}

// Verify 校验验证码：忽略大小写；一次性使用（验证即删，无论成败）。
func (c *CaptchaStore) Verify(id, code string) bool {
	if id == "" || code == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		return false
	}
	delete(c.entries, id)
	if e.expires <= time.Now().Unix() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(code), e.answer)
}

// captchaColors 字符颜色池（中低饱和度，保证可读）。
var captchaColors = []string{
	"#2f54eb", "#389e0d", "#d4380d", "#08979c", "#722ed1", "#c41d7f",
}

// captchaSVG 手绘 SVG 验证码：每字符随机旋转/颜色/纵向抖动 + 干扰线 + 噪点。
func captchaSVG(code string) string {
	w, h := 130, 44
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, w, h, w, h)
	b.WriteString(`<rect width="100%" height="100%" fill="#f5f6f8"/>`)

	// 干扰线
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b,
			`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.45"/>`,
			randInt(0, w/4), randInt(0, h), randInt(w*3/4, w), randInt(0, h),
			captchaColors[randInt(0, len(captchaColors)-1)])
	}
	// 噪点
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1" fill="%s" opacity="0.35"/>`,
			randInt(0, w), randInt(0, h), captchaColors[randInt(0, len(captchaColors)-1)])
	}

	// 字符：随机旋转、纵向抖动、颜色
	x := 14
	for _, ch := range code {
		dy := randInt(-4, 4)
		rot := randInt(-22, 22)
		fontSize := randInt(24, 30)
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" font-family="Georgia, 'Times New Roman', serif" font-size="%d" font-weight="bold" fill="%s" transform="rotate(%d %d %d)">%c</text>`,
			x, h/2+10+dy, fontSize,
			captchaColors[randInt(0, len(captchaColors)-1)],
			rot, x+fontSize/2, h/2, ch)
		x += fontSize + randInt(2, 8)
	}
	b.WriteString(`</svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String()))
}
