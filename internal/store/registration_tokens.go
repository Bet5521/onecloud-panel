package store

// CreateRegistrationToken 写入注册令牌（哈希）。
func (s *Store) CreateRegistrationToken(t *RegistrationToken) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO registration_tokens
		 (token_hash, description, expires_at, max_uses, used_count, created_by, created_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		t.TokenHash, t.Description, t.ExpiresAt, t.MaxUses, t.CreatedBy, t.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListRegistrationTokens 全部注册令牌。
func (s *Store) ListRegistrationTokens() ([]RegistrationToken, error) {
	rows, err := s.DB.Query(
		`SELECT id, token_hash, description, expires_at, max_uses, used_count, created_by, created_at
		 FROM registration_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RegistrationToken
	for rows.Next() {
		var t RegistrationToken
		if err := rows.Scan(
			&t.ID, &t.TokenHash, &t.Description, &t.ExpiresAt, &t.MaxUses,
			&t.UsedCount, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetRegistrationTokenByHash 按哈希查询。
func (s *Store) GetRegistrationTokenByHash(hash string) (*RegistrationToken, error) {
	t := &RegistrationToken{}
	err := s.DB.QueryRow(
		`SELECT id, token_hash, description, expires_at, max_uses, used_count, created_by, created_at
		 FROM registration_tokens WHERE token_hash=?`, hash).Scan(
		&t.ID, &t.TokenHash, &t.Description, &t.ExpiresAt, &t.MaxUses,
		&t.UsedCount, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// RegistrationTokenByID 按 ID 查询。
func (s *Store) RegistrationTokenByID(id int64) (*RegistrationToken, error) {
	t := &RegistrationToken{}
	err := s.DB.QueryRow(
		`SELECT id, token_hash, description, expires_at, max_uses, used_count, created_by, created_at
		 FROM registration_tokens WHERE id=?`, id).Scan(
		&t.ID, &t.TokenHash, &t.Description, &t.ExpiresAt, &t.MaxUses,
		&t.UsedCount, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// IncrementTokenUse 计数 +1。
func (s *Store) IncrementTokenUse(id int64) error {
	_, err := s.DB.Exec(
		`UPDATE registration_tokens SET used_count = used_count + 1 WHERE id=?`, id)
	return err
}

// DeleteRegistrationToken 删除令牌。
func (s *Store) DeleteRegistrationToken(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM registration_tokens WHERE id=?`, id)
	return err
}
