package store

import "time"

// CreateSession 写入会话。
func (s *Store) CreateSession(tokenHash string, userID int64, ip, ua string, ttl time.Duration) error {
	t := now()
	_, err := s.DB.Exec(
		`INSERT INTO sessions (token_hash, user_id, ip, user_agent, created_at, last_seen, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, ip, ua, t, t, t+int64(ttl.Seconds()))
	return err
}

// SessionUser 解析会话并返回用户 ID；过期/不存在返回 (-1, nil)。
func (s *Store) SessionUser(tokenHash string) (int64, error) {
	var uid int64
	var expires int64
	err := s.DB.QueryRow(
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`,
		tokenHash).Scan(&uid, &expires)
	if err != nil {
		return -1, err
	}
	if expires < now() {
		_, _ = s.DB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return -1, nil
	}
	return uid, nil
}

// TouchSession 更新会话最近活跃时间。
func (s *Store) TouchSession(tokenHash string) error {
	_, err := s.DB.Exec(`UPDATE sessions SET last_seen = ? WHERE token_hash = ?`, now(), tokenHash)
	return err
}

// DeleteSession 注销指定会话。
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions 吊销某用户全部会话（改密/禁用/删除用户时使用）。
func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// CleanExpiredSessions 清理过期会话。
func (s *Store) CleanExpiredSessions() (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
