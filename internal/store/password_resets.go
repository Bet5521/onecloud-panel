package store

import "database/sql"

// PasswordReset 密码重置记录（重置码仅以 SHA-256 哈希保存）。
type PasswordReset struct {
	ID        int64
	UserID    int64
	CodeHash  string
	ExpiresAt int64
	Used      bool
	CreatedAt int64
}

// CreatePasswordReset 写入一条重置记录。
func (s *Store) CreatePasswordReset(r *PasswordReset) error {
	_, err := s.DB.Exec(
		`INSERT INTO password_resets (user_id, code_hash, expires_at, used, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		r.UserID, r.CodeHash, r.ExpiresAt, boolInt(r.Used), r.CreatedAt)
	return err
}

// GetActivePasswordReset 取用户最近一条未使用且未过期的重置记录。
func (s *Store) GetActivePasswordReset(userID int64, nowUnix int64) (*PasswordReset, error) {
	r := &PasswordReset{}
	var used int
	err := s.DB.QueryRow(
		`SELECT id, user_id, code_hash, expires_at, used, created_at
		 FROM password_resets
		 WHERE user_id = ? AND used = 0 AND expires_at > ?
		 ORDER BY id DESC LIMIT 1`,
		userID, nowUnix).Scan(
		&r.ID, &r.UserID, &r.CodeHash, &r.ExpiresAt, &used, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Used = used == 1
	return r, nil
}

// MarkPasswordResetUsed 标记重置记录已使用。
func (s *Store) MarkPasswordResetUsed(id int64) error {
	_, err := s.DB.Exec(`UPDATE password_resets SET used = 1 WHERE id = ?`, id)
	return err
}

// DeleteUserPasswordResets 清除用户的全部重置记录（重置成功后调用）。
func (s *Store) DeleteUserPasswordResets(userID int64) error {
	_, err := s.DB.Exec(`DELETE FROM password_resets WHERE user_id = ?`, userID)
	return err
}
