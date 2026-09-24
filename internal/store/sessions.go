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

// SessionInfo 会话列表视图（不含令牌与哈希，仅可公开的会话标识与环境信息）。
type SessionInfo struct {
	ID        string `json:"id"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	ExpiresAt int64  `json:"expires_at"`
}

// ListUserSessions 列出某用户未过期的会话（按最近活跃倒序）。
func (s *Store) ListUserSessions(userID int64) ([]SessionInfo, error) {
	rows, err := s.DB.Query(
		`SELECT substr(token_hash, 1, 16), ip, user_agent, created_at, last_seen, expires_at
		 FROM sessions WHERE user_id = ? AND expires_at >= ? ORDER BY last_seen DESC`,
		userID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var it SessionInfo
		if err := rows.Scan(&it.ID, &it.IP, &it.UserAgent,
			&it.CreatedAt, &it.LastSeen, &it.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteUserSessionByID 注销某用户指定会话（sid 为会话哈希前缀，避免越权注销他人会话）。
func (s *Store) DeleteUserSessionByID(userID int64, sid string) (int64, error) {
	res, err := s.DB.Exec(
		`DELETE FROM sessions WHERE user_id = ? AND substr(token_hash, 1, 16) = ?`,
		userID, sid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
