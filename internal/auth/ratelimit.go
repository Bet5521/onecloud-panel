package auth

import (
	"sync"
	"time"
)

// LoginLimiter 登录失败限流（进程内；面板重启即重置）。
type LoginLimiter struct {
	mu      sync.Mutex
	fails   map[string][]int64 // key -> 失败时间戳序列
	maxFail int
	window  time.Duration
}

// NewLoginLimiter 创建限流器：窗口内失败达到上限后拒绝。
func NewLoginLimiter(maxFail int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{fails: map[string][]int64{}, maxFail: maxFail, window: window}
}

// Allow 判断 key（用户名或 IP）当前是否允许尝试。
func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-l.window).Unix()
	times := l.fails[key]
	kept := times[:0]
	for _, t := range times {
		if t > cut {
			kept = append(kept, t)
		}
	}
	l.fails[key] = kept
	return len(kept) < l.maxFail
}

// Fail 记录一次失败。
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.fails[key], time.Now().Unix())
}

// Reset 成功登录后清除计数。
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
