package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterWindowAndReset(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	if !l.Allow("u") {
		t.Fatal("新 key 应允许尝试")
	}
	l.Fail("u")
	l.Fail("u")
	if l.Allow("u") {
		t.Fatal("达到上限后应拒绝")
	}
	l.Reset("u")
	if !l.Allow("u") {
		t.Fatal("Reset 后应恢复")
	}
}

// 旧实现的 map 只增不减，随机 key 可灌爆内存；这里校验容量上限生效。
func TestLoginLimiterBounded(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	for i := 0; i < maxTrackedKeys*2; i++ {
		l.Fail(string(rune('a'+i%26)) + string(rune('A'+i/26%26)) + string(rune('0'+i/676%10)))
	}
	l.mu.Lock()
	n := len(l.fails)
	l.mu.Unlock()
	if n > maxTrackedKeys {
		t.Fatalf("限流器 key 数应受上限约束，实际 %d > %d", n, maxTrackedKeys)
	}
}

func TestValidateUsername(t *testing.T) {
	if err := ValidateUsername("admin"); err != nil {
		t.Fatalf("正常用户名不应报错: %v", err)
	}
	for _, bad := range []string{"a b", "a\nb", "a'b", `a"b`, "a/b", "a$b", "a;b"} {
		if err := ValidateUsername(bad); err == nil {
			t.Fatalf("非法用户名应被拒绝: %q", bad)
		}
	}
}

// 取模偏差回归：等概率取样应覆盖整个字母表。
func TestRandomCodeCoversAlphabet(t *testing.T) {
	seen := map[byte]bool{}
	for i := 0; i < 5000 && len(seen) < len(superCodeAlphabet); i++ {
		code, err := GenerateSuperCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != superCodeLen {
			t.Fatalf("长度异常: %q", code)
		}
		for j := 0; j < len(code); j++ {
			seen[code[j]] = true
		}
	}
	if len(seen) != len(superCodeAlphabet) {
		t.Fatalf("字母表覆盖不全: %d/%d", len(seen), len(superCodeAlphabet))
	}
}
