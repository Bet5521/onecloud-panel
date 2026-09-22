package store

import "time"

// CreateUser 创建用户并返回填充后的对象。
func (s *Store) CreateUser(username, passwordHash string, roleID int64) (*User, error) {
	return s.CreateUserWithProfile(username, passwordHash, roleID, "", "")
}

// CreateUserWithProfile 创建用户并写入姓名、手机号。
func (s *Store) CreateUserWithProfile(username, passwordHash string, roleID int64, realName, phone string) (*User, error) {
	t := now()
	res, err := s.DB.Exec(
		`INSERT INTO users (username, password_hash, role_id, status, real_name, phone, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', ?, ?, ?, ?)`,
		username, passwordHash, roleID, realName, phone, t, t)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.UserByID(id)
}

// UpdateUserNotify 更新用户级通知方式配置（含每用户接收标识）。
func (s *Store) UpdateUserNotify(id int64, method, email, smsPhone string, channelID *int64, target *string) error {
	_, err := s.DB.Exec(
		`UPDATE users SET notify_method = ?, notify_email = ?, notify_sms_phone = ?,
		   notify_channel_id = ?, notify_target = ?, updated_at = ?
		 WHERE id = ?`,
		method, email, smsPhone, channelID, target, now(), id)
	return err
}

// UpdateUserPassword 更新密码哈希。
func (s *Store) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := s.DB.Exec(
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, now(), id)
	return err
}

// UpdateUserStatus 启用/禁用用户。
func (s *Store) UpdateUserStatus(id int64, status string) error {
	_, err := s.DB.Exec(
		`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`,
		status, now(), id)
	return err
}

// UpdateUserProfile 更新姓名、手机号。
func (s *Store) UpdateUserProfile(id int64, realName, phone string) error {
	_, err := s.DB.Exec(
		`UPDATE users SET real_name = ?, phone = ?, updated_at = ? WHERE id = ?`,
		realName, phone, now(), id)
	return err
}

// UpdateUserSuperCode 更新超级验证码。
func (s *Store) UpdateUserSuperCode(id int64, code string) error {
	_, err := s.DB.Exec(
		`UPDATE users SET super_code = ?, updated_at = ? WHERE id = ?`,
		code, now(), id)
	return err
}

// ListUsers 返回全部用户（按创建顺序）。
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.DB.Query(
		`SELECT id, username, real_name, phone, password_hash, super_code, role_id, status,
		        notify_method, notify_email, notify_sms_phone, notify_channel_id,
		        notify_target,
		        created_at, updated_at
		 FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.RealName, &u.Phone,
			&u.PasswordHash, &u.SuperCode, &u.RoleID, &u.Status,
			&u.NotifyMethod, &u.NotifyEmail, &u.NotifySMSPhone, &u.NotifyChannelID, &u.NotifyTarget,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUserRole 更新用户角色。
func (s *Store) UpdateUserRole(id int64, roleID int64) error {
	_, err := s.DB.Exec(
		`UPDATE users SET role_id = ?, updated_at = ? WHERE id = ?`,
		roleID, now(), id)
	return err
}

// DeleteUser 删除用户。
func (s *Store) DeleteUser(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// CountActiveAdmins 统计指定角色中 active 用户数；excludeID>0 时排除该用户。
func (s *Store) CountActiveAdmins(excludeID int64) (int, error) {
	q := `SELECT COUNT(*) FROM users u JOIN roles r ON u.role_id=r.id
	      WHERE r.code='admin' AND u.status='active'`
	var args []any
	if excludeID > 0 {
		q += " AND u.id<>?"
		args = append(args, excludeID)
	}
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// now 兼容 time 包（保持与其它 store 文件一致）。
var _ = time.Now
