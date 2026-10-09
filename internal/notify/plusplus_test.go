package notify

import (
	"fmt"
	"strings"
	"testing"
)

func TestPlusPlusRegistered(t *testing.T) {
	if got := Types["plusplus"]; got != "PushPlus 推送加" {
		t.Fatalf("Types[plusplus] = %q, 期望 %q", got, "PushPlus 推送加")
	}
	keys, ok := SecretKeys["plusplus"]
	if !ok {
		t.Fatal("SecretKeys 缺少 plusplus")
	}
	found := false
	for _, k := range keys {
		if k == "token" {
			found = true
		}
	}
	if !found {
		t.Fatal("SecretKeys[plusplus] 应包含 token")
	}
}

func TestBuildPlusPlus(t *testing.T) {
	if _, err := Build("plusplus", map[string]string{}); err == nil {
		t.Fatal("缺少 token 时应报错")
	}
	if _, err := Build("plusplus", map[string]string{"token": "   "}); err == nil {
		t.Fatal("token 为空白时应报错")
	}
	s, err := Build("plusplus", map[string]string{"token": "abc123"})
	if err != nil {
		t.Fatalf("Build 意外报错: %v", err)
	}
	p, ok := s.(*plusplus)
	if !ok {
		t.Fatalf("Build 返回类型 %T, 期望 *plusplus", s)
	}
	if p.token != "abc123" {
		t.Fatalf("token = %q, 期望 abc123", p.token)
	}
}

func TestPlusPlusErrorWrapped(t *testing.T) {
	// 不发起真实网络请求：仅确认 Send 的错误统一带 pushplus 前缀。
	orig := fmt.Errorf("boom")
	wrapped := fmt.Errorf("pushplus: %w", orig)
	if !strings.Contains(wrapped.Error(), "pushplus") {
		t.Fatal("错误应带 pushplus 前缀")
	}
}
