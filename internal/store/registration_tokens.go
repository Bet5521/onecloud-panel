package store

import "errors"

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

// ClaimRegistrationToken 原子领取一次注册令牌使用额度。
// 判定与计数在同一条 UPDATE 内完成，消除「校验通过后并发注册」导致的
// 超额使用（旧实现先查后加，并发下同一枚一次性令牌可注册多个节点）。
func (s *Store) ClaimRegistrationToken(hash string, now int64) error {
	res, err := s.DB.Exec(
		`UPDATE registration_tokens SET used_count = used_count + 1
		 WHERE token_hash=? AND used_count < max_uses AND (expires_at IS NULL OR expires_at >= ?)`,
		hash, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	// 未命中：区分令牌不存在 / 已过期 / 已用尽，给出可操作错误
	t, err := s.GetRegistrationTokenByHash(hash)
	if err != nil {
		return errors.New("注册令牌无效")
	}
	if t.ExpiresAt != nil && *t.ExpiresAt < now {
		return errors.New("注册令牌已过期")
	}
	return errors.New("注册令牌已使用")
}

// ReleaseRegistrationToken 回滚一次使用额度（节点创建失败时的补偿）。
func (s *Store) ReleaseRegistrationToken(hash string) error {
	_, err := s.DB.Exec(
		`UPDATE registration_tokens SET used_count = max(used_count - 1, 0) WHERE token_hash=?`,
		hash)
	return err
}
