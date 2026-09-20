package store

// GetRolePermissions 返回角色权限点（字典序）。
func (s *Store) GetRolePermissions(roleID int64) ([]string, error) {
	rows, err := s.DB.Query(
		`SELECT permission FROM role_permissions WHERE role_id=? ORDER BY permission`,
		roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ReplaceRolePermissions 事务内整体替换角色权限点。
func (s *Store) ReplaceRolePermissions(roleID int64, perms []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM role_permissions WHERE role_id=?`, roleID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range perms {
		if _, err := stmt.Exec(roleID, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateRoleName 更新角色显示名。
func (s *Store) UpdateRoleName(id int64, name string) error {
	_, err := s.DB.Exec(
		`UPDATE roles SET name=?, updated_at=? WHERE id=?`, name, now(), id)
	return err
}

// RoleByID 按 ID 查询角色。
func (s *Store) RoleByID(id int64) (*Role, error) {
	r := &Role{}
	err := s.DB.QueryRow(
		`SELECT id, code, name, builtin, protected, created_at, updated_at
		 FROM roles WHERE id = ?`, id).Scan(
		&r.ID, &r.Code, &r.Name, &r.Builtin, &r.Protected, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRoles 返回全部角色。
func (s *Store) ListRoles() ([]Role, error) {
	rows, err := s.DB.Query(
		`SELECT id, code, name, builtin, protected, created_at, updated_at FROM roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(
			&r.ID, &r.Code, &r.Name, &r.Builtin, &r.Protected, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
