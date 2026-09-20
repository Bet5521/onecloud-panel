package store

import (
	"database/sql"
	"time"
)

func (s *Store) seed() error {
	t := now()
	for _, r := range builtinRoles {
		_, err := s.DB.Exec(
			`INSERT INTO roles (code, name, builtin, protected, created_at, updated_at)
			 VALUES (?, ?, 1, ?, ?, ?)
			 ON CONFLICT(code) DO NOTHING`,
			r.code, r.name, boolInt(r.protected), t, t)
		if err != nil {
			return err
		}
	}

	// 仅在角色尚无权限记录时写入默认值，避免覆盖管理员的调整
	for code, perms := range defaultRolePermissions {
		var roleID int64
		if err := s.DB.QueryRow(`SELECT id FROM roles WHERE code = ?`, code).Scan(&roleID); err != nil {
			return err
		}
		var count int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM role_permissions WHERE role_id = ?`, roleID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			for _, p := range perms {
				if _, err := s.DB.Exec(
					`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)
					 ON CONFLICT DO NOTHING`, roleID, p); err != nil {
					return err
				}
			}
		}
	}

	// 面板默认设置
	defaults := map[string]string{
		"panel_name":           "OneCloud Panel",
		"audit_retention_days": "180",
		"installed_at":         time.Now().Format(time.RFC3339),
	}
	for k, v := range defaults {
		if _, err := s.DB.Exec(
			`INSERT INTO panel_settings (k, v) VALUES (?, ?) ON CONFLICT(k) DO NOTHING`,
			k, v); err != nil {
			return err
		}
	}
	return nil
}

// RolePermissions 读取角色的权限点集合。
func (s *Store) RolePermissions(roleID int64) (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT permission FROM role_permissions WHERE role_id = ?`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]bool)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		set[p] = true
	}
	return set, rows.Err()
}

// UserByUsername 按用户名查询用户；不存在返回 sql.ErrNoRows。
func (s *Store) UserByUsername(username string) (*User, error) {
	return s.user(`WHERE username = ?`, username)
}

// UserByID 按 ID 查询用户。
func (s *Store) UserByID(id int64) (*User, error) {
	return s.user(`WHERE id = ?`, id)
}

func (s *Store) user(where string, args ...any) (*User, error) {
	q := `SELECT id, username, real_name, phone, password_hash, super_code, role_id, status,
	             notify_method, notify_email, notify_sms_phone, notify_channel_id,
	             created_at, updated_at
	      FROM users ` + where
	u := &User{}
	err := s.DB.QueryRow(q, args...).Scan(
		&u.ID, &u.Username, &u.RealName, &u.Phone, &u.PasswordHash, &u.SuperCode,
		&u.RoleID, &u.Status,
		&u.NotifyMethod, &u.NotifyEmail, &u.NotifySMSPhone, &u.NotifyChannelID,
		&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CountUsers 返回用户数（初始化判定用）。
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

var _ = sql.ErrNoRows

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
