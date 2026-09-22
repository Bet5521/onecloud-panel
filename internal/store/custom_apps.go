package store

import (
	"database/sql"
	"errors"
)

// ErrCustomAppNotFound 自定义应用不存在。
var ErrCustomAppNotFound = errors.New("自定义应用不存在")

// ListCustomApps 返回自定义应用。
// visibleTo 为 nil 时返回全部（管理员）；非 nil 时返回该用户创建的应用 + 系统级应用。
func (s *Store) ListCustomApps(visibleTo *int64) ([]CustomApp, error) {
	q := `SELECT id, type, name, category, icon, description, homepage, config_json,
	             owner_user_id, created_by, created_at, updated_at
	      FROM custom_apps`
	var args []any
	if visibleTo != nil {
		q += " WHERE owner_user_id = ? OR owner_user_id IS NULL"
		args = append(args, *visibleTo)
	}
	q += " ORDER BY id"
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomApp
	for rows.Next() {
		var c CustomApp
		var owner, createdBy sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.Category, &c.Icon, &c.Description,
			&c.Homepage, &c.ConfigJSON, &owner, &createdBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if owner.Valid {
			v := owner.Int64
			c.OwnerUserID = &v
		}
		if createdBy.Valid {
			v := createdBy.Int64
			c.CreatedBy = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCustomApp 按 ID 查询。
func (s *Store) GetCustomApp(id int64) (*CustomApp, error) {
	var c CustomApp
	var owner, createdBy sql.NullInt64
	err := s.DB.QueryRow(
		`SELECT id, type, name, category, icon, description, homepage, config_json,
		        owner_user_id, created_by, created_at, updated_at
		 FROM custom_apps WHERE id = ?`, id).
		Scan(&c.ID, &c.Type, &c.Name, &c.Category, &c.Icon, &c.Description,
			&c.Homepage, &c.ConfigJSON, &owner, &createdBy, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCustomAppNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner.Valid {
		v := owner.Int64
		c.OwnerUserID = &v
	}
	if createdBy.Valid {
		v := createdBy.Int64
		c.CreatedBy = &v
	}
	return &c, nil
}

// CreateCustomApp 新建自定义应用，返回填充后的对象。
func (s *Store) CreateCustomApp(c *CustomApp) (*CustomApp, error) {
	t := now()
	res, err := s.DB.Exec(
		`INSERT INTO custom_apps
		 (type, name, category, icon, description, homepage, config_json,
		  owner_user_id, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Type, c.Name, c.Category, c.Icon, c.Description, c.Homepage, c.ConfigJSON,
		c.OwnerUserID, c.CreatedBy, t, t)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCustomApp(id)
}

// UpdateCustomApp 更新元信息与配置（不改动归属/创建者）。
func (s *Store) UpdateCustomApp(id int64, name, category, icon, description, homepage, configJSON string) error {
	_, err := s.DB.Exec(
		`UPDATE custom_apps SET name=?, category=?, icon=?, description=?, homepage=?,
		   config_json=?, updated_at=? WHERE id=?`,
		name, category, icon, description, homepage, configJSON, now(), id)
	return err
}

// DeleteCustomApp 删除自定义应用。
func (s *Store) DeleteCustomApp(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM custom_apps WHERE id=?`, id)
	return err
}
