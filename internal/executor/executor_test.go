package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalExecAndFiles(t *testing.T) {
	dir := t.TempDir()
	g := NewGuard(dir)
	l := NewLocal(g)

	// 命令执行
	r, err := l.Exec(context.Background(), "go", "version")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(r.Output, "go version") {
		t.Fatalf("unexpected output: %s", r.Output)
	}

	// 白名单文件读写
	p := filepath.Join(dir, "a.txt")
	if err := l.WriteFile(p, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := l.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("read: %q %v", string(b), err)
	}
	ok, _ := l.Exists(p)
	if !ok {
		t.Fatal("exists should be true")
	}

	// 白名单外拒绝
	out := filepath.Join(t.TempDir(), "b.txt")
	if err := l.WriteFile(out, []byte("x")); err == nil {
		t.Fatal("白名单外写入未拒绝")
	}
	if _, err := l.ReadFile(out); err == nil {
		t.Fatal("白名单外读取未拒绝")
	}
}
