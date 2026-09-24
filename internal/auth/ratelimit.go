package auth

import (
	"sync"
	"time"
)

// maxTrackedKeys 限流器同时跟踪的 key 上限。
// 超限时淘汰「最久未失败」的 key，避免攻击者用随机用户名/IP 灌爆内存
// （旧实现的 map 只增不减，属于无界内存增长）。
const maxTrackedKeys = 4096

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

// prune 清理窗口外记录（须持锁调用）；返回 key 当前有效失败次数。
func (l *LoginLimiter) prune(key string, cut int64) int {
	times := l.fails[key]
	kept := times[:0]
	for _, t := range times {
		if t > cut {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return 0
	}
	l.fails[key] = kept
	return len(kept)
}

// evictIfNeeded 新增 key 前做容量控制（须持锁调用）。
func (l *LoginLimiter) evictIfNeeded() {
	for len(l.fails) >= maxTrackedKeys {
		victim := ""
		var oldest int64
		for k, ts := range l.fails {
			last := int64(0)
			if n := len(ts); n > 0 {
				last = ts[n-1]
			}
			if victim == "" || last < oldest {
				victim, oldest = k, last
			}
		}
		if victim == "" {
			return
		}
		delete(l.fails, victim)
	}
}

// Allow 判断 key（用户名或 IP）当前是否允许尝试。
func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Unix() - int64(l.window/time.Second)
	return l.prune(key, cut) < l.maxFail
}

// FailCount 返回 key 窗口内失败次数（复用 Allow 的窗口淘汰逻辑）。
func (l *LoginLimiter) FailCount(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Unix() - int64(l.window/time.Second)
	return l.prune(key, cut)
}

// Fail 记录一次失败。
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().Unix()
	cut := now - int64(l.window/time.Second)
	if l.prune(key, cut) == 0 {
		// 全新（或已过期）key：写入前做容量控制
		l.evictIfNeeded()
	}
	l.fails[key] = append(l.fails[key], now)
}

// Reset 成功登录后清除计数。
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
