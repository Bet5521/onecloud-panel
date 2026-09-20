package store

import (
	"path/filepath"
	"testing"
)

func TestOpenMigrateSeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// 三个预置角色与默认权限点
	for _, code := range []string{"admin", "operator", "viewer"} {
		var id int64
		var name string
		err := s.DB.QueryRow(`SELECT id, name FROM roles WHERE code = ?`, code).Scan(&id, &name)
		if err != nil {
			t.Fatalf("role %s: %v", code, err)
		}
		perms, err := s.RolePermissions(id)
		if err != nil {
			t.Fatalf("RolePermissions %s: %v", code, err)
		}
		if len(perms) == 0 {
			t.Fatalf("role %s 无权限点", code)
		}
		if code == "admin" && len(perms) != len(AllPermissions) {
			t.Fatalf("admin 权限点数 = %d, want %d", len(perms), len(AllPermissions))
		}
	}

	// 默认设置存在
	var name string
	if err := s.DB.QueryRow(`SELECT v FROM panel_settings WHERE k = 'panel_name'`).Scan(&name); err != nil {
		t.Fatalf("panel_name: %v", err)
	}
	if name != "OneCloud Panel" {
		t.Fatalf("panel_name = %q", name)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新打开：迁移幂等、数据保留
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	n, err := s2.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Fatalf("fresh db users = %d, want 0", n)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close2: %v", err)
	}
}
