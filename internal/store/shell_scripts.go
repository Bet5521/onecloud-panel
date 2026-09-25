package store

import (
	"database/sql"
	"errors"
)

// ErrShellScriptNotFound 脚本不存在。
var ErrShellScriptNotFound = errors.New("脚本不存在")

// ListShellScripts 返回脚本清单。
// visibleTo 为 nil 时返回全部（管理员）；非 nil 时返回该用户创建的脚本 + 系统级脚本。
func (s *Store) ListShellScripts(visibleTo *int64) ([]ShellScript, error) {
	q := `SELECT id, name, description, content, owner_user_id, created_at, updated_at
	      FROM shell_scripts`
	var args []any
	if visibleTo != nil {
		q += " WHERE owner_user_id = ? OR owner_user_id IS NULL"
		args = append(args, *visibleTo)
	}
	q += " ORDER BY id DESC"
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShellScript
	for rows.Next() {
		var sc ShellScript
		var owner sql.NullInt64
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Content,
			&owner, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		if owner.Valid {
			v := owner.Int64
			sc.OwnerUserID = &v
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// GetShellScript 按 ID 查询脚本。
func (s *Store) GetShellScript(id int64) (*ShellScript, error) {
	var sc ShellScript
	var owner sql.NullInt64
	err := s.DB.QueryRow(
		`SELECT id, name, description, content, owner_user_id, created_at, updated_at
		 FROM shell_scripts WHERE id = ?`, id).
		Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Content,
			&owner, &sc.CreatedAt, &sc.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrShellScriptNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner.Valid {
		v := owner.Int64
		sc.OwnerUserID = &v
	}
	return &sc, nil
}

// CreateShellScript 新建脚本，返回填充后的对象。
func (s *Store) CreateShellScript(sc *ShellScript) (*ShellScript, error) {
	t := now()
	res, err := s.DB.Exec(
		`INSERT INTO shell_scripts (name, description, content, owner_user_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sc.Name, sc.Description, sc.Content, sc.OwnerUserID, t, t)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetShellScript(id)
}

// UpdateShellScript 更新脚本元信息与内容（不改动归属）。
func (s *Store) UpdateShellScript(id int64, name, description, content string) error {
	_, err := s.DB.Exec(
		`UPDATE shell_scripts SET name=?, description=?, content=?, updated_at=? WHERE id=?`,
		name, description, content, now(), id)
	return err
}

// DeleteShellScript 删除脚本及其全部部署记录。
func (s *Store) DeleteShellScript(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM shell_script_deployments WHERE script_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shell_scripts WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertShellScriptDeployment 记录/更新脚本在某节点的部署状态。
func (s *Store) UpsertShellScriptDeployment(d *ShellScriptDeployment) error {
	auto := 0
	if d.AutoStart {
		auto = 1
	}
	_, err := s.DB.Exec(
		`INSERT INTO shell_script_deployments (script_id, node_id, auto_start, content_hash)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(script_id, node_id) DO UPDATE SET
		   auto_start=excluded.auto_start, content_hash=excluded.content_hash`,
		d.ScriptID, d.NodeID, auto, d.ContentHash)
	return err
}

// GetShellScriptDeployment 查询单条部署记录（无记录返回 nil，不视为错误）。
func (s *Store) GetShellScriptDeployment(scriptID, nodeID int64) (*ShellScriptDeployment, error) {
	var d ShellScriptDeployment
	var auto int
	err := s.DB.QueryRow(
		`SELECT script_id, node_id, auto_start, content_hash
		 FROM shell_script_deployments WHERE script_id=? AND node_id=?`,
		scriptID, nodeID).Scan(&d.ScriptID, &d.NodeID, &auto, &d.ContentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.AutoStart = auto != 0
	return &d, nil
}

// ListShellScriptDeployments 脚本的全部部署记录。
func (s *Store) ListShellScriptDeployments(scriptID int64) ([]ShellScriptDeployment, error) {
	rows, err := s.DB.Query(
		`SELECT script_id, node_id, auto_start, content_hash
		 FROM shell_script_deployments WHERE script_id=? ORDER BY node_id`, scriptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShellScriptDeployment
	for rows.Next() {
		var d ShellScriptDeployment
		var auto int
		if err := rows.Scan(&d.ScriptID, &d.NodeID, &auto, &d.ContentHash); err != nil {
			return nil, err
		}
		d.AutoStart = auto != 0
		out = append(out, d)
	}
	return out, rows.Err()
}
