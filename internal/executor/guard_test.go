package executor

import (
	"path/filepath"
	"testing"
)

func TestGuard(t *testing.T) {
	root := filepath.Join(t.TempDir(), "allowed")
	g := NewGuard(root)

	// 白名单内
	if err := g.Check(filepath.Join(root, "conf", "app.yaml")); err != nil {
		t.Fatalf("白名单内路径被拒: %v", err)
	}
	if err := g.Check(root); err != nil {
		t.Fatalf("根本身应允许: %v", err)
	}

	// 白名单外
	out := filepath.Join(t.TempDir(), "other.txt")
	if err := g.Check(out); err == nil {
		t.Fatal("白名单外路径未被拒绝")
	}

	// 相对路径
	if err := g.Check("conf/app.yaml"); err == nil {
		t.Fatal("相对路径未被拒绝")
	}

	// 空守卫拒绝一切
	empty := NewGuard()
	if err := empty.Check(root); err == nil {
		t.Fatal("空守卫应拒绝访问")
	}

	// 前缀混淆：/allowed2 不应被当作 /allowed
	sibling := filepath.Join(filepath.Dir(root), "allowed2", "x")
	if err := g.Check(sibling); err == nil {
		t.Fatal("兄弟目录前缀路径未被拒绝")
	}
}
