package audit

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

func TestAuditRecordQueryCleanup(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	svc := New(s)

	var roleID int64
	if err := s.DB.QueryRow(`SELECT id FROM roles WHERE code='admin'`).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("x")
	u, err := s.CreateUser("admin1", hash, roleID)
	if err != nil {
		t.Fatal(err)
	}

	// 构造带身份的请求并记录 3 类操作
	identity := &auth.Identity{User: u, RoleCode: "admin", Perms: map[string]bool{}}
	for _, e := range []struct {
		module, action, result string
	}{
		{"auth", "login", ResultSuccess},
		{"node", "create", ResultSuccess},
		{"app", "install", ResultFailure},
	} {
		r := httptest.NewRequest("POST", "/api/x", nil)
		r = r.WithContext(identity.WithContext(r.Context()))
		svc.Record(r, e.module, e.action, "", "", e.result, "")
	}

	// 无条件查询：3 条
	items, total, err := svc.Query(store.AuditFilter{Page: 1, PageSize: 10})
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("query all: items=%d total=%d err=%v", len(items), total, err)
	}

	// 字段完整性（首条）
	first := items[0]
	if first.Username != "admin1" || first.IP == "" || first.Module == "" || first.Action == "" {
		t.Fatalf("字段不完整: %+v", first)
	}

	// 按模块筛选
	_, total, _ = svc.Query(store.AuditFilter{Module: "node"})
	if total != 1 {
		t.Fatalf("module=node total=%d want 1", total)
	}
	// 按结果筛选
	_, total, _ = svc.Query(store.AuditFilter{Result: ResultFailure})
	if total != 1 {
		t.Fatalf("result=failure total=%d want 1", total)
	}
	// 按操作人筛选
	_, total, _ = svc.Query(store.AuditFilter{Username: "admin1"})
	if total != 3 {
		t.Fatalf("username total=%d want 3", total)
	}
	// 时间范围筛选（未来上界 → 0）
	_, total, _ = svc.Query(store.AuditFilter{
		Start: time.Now().Add(time.Hour).Unix()})
	if total != 0 {
		t.Fatalf("future start total=%d want 0", total)
	}
	// 分页
	page1, _, _ := svc.Query(store.AuditFilter{Page: 1, PageSize: 2})
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d want 2", len(page1))
	}

	// 保留 0 天清理 → 全删
	deleted, err := svc.RetentionCleanup(0)
	if err != nil || deleted != 0 {
		t.Fatalf("cleanup(0): deleted=%d err=%v", deleted, err)
	}
	deleted, err = svc.RetentionCleanup(3650) // 3650 天前的（无）
	if err != nil || deleted != 0 {
		t.Fatalf("cleanup(3650): deleted=%d err=%v", deleted, err)
	}
}

func TestDetailJSONRedaction(t *testing.T) {
	out := DetailJSON(map[string]any{
		"username":       "bob",
		"password":       "p@ss",
		"register_token": "tok_abc",
		"port":           8080,
	})
	for _, bad := range []string{"p@ss", "tok_abc"} {
		if strings.Contains(out, bad) {
			t.Fatalf("敏感值未脱敏: %s in %s", bad, out)
		}
	}
}
