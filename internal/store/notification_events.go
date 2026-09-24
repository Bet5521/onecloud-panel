package store

import (
	"database/sql"
	"encoding/json"
)

// NotificationSchedule 定时状态摘要设置（单行）。
type NotificationSchedule struct {
	Enabled       bool
	IntervalHours int    // 仅允许 1/6/12/24
	IncludeNodes  bool
	IncludeApps   bool
	ChannelIDs    []int64
	UpdatedAt     int64
}

// ListChannelEvents 返回通道订阅的事件编码列表。
func (s *Store) ListChannelEvents(channelID int64) ([]string, error) {
	rows, err := s.DB.Query(`SELECT event FROM channel_event_subscriptions WHERE channel_id = ? ORDER BY event`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetChannelEvents 全量替换通道的事件订阅（空列表清空订阅）。
func (s *Store) SetChannelEvents(channelID int64, events []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM channel_event_subscriptions WHERE channel_id = ?`, channelID); err != nil {
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO channel_event_subscriptions (channel_id, event) VALUES (?, ?)`, channelID, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NotificationChannelsSubscribed 返回启用中且订阅了指定事件的通道。
func (s *Store) NotificationChannelsSubscribed(event string) ([]NotificationChannel, error) {
	rows, err := s.DB.Query(
		`SELECT c.id, c.type, c.name, c.config_json, c.enabled, COALESCE(c.tested_at, 0), c.created_at, c.updated_at
		 FROM notification_channels c
		 JOIN channel_event_subscriptions s ON s.channel_id = c.id
		 WHERE c.enabled = 1 AND s.event = ?
		 ORDER BY c.id`, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		var enabled int
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.ConfigJSON, &enabled, &c.TestedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// usersSubscribedBase 查询订阅指定事件且状态为 active 的用户字段片段。
const usersSubscribedBase = `SELECT u.id, u.username, u.real_name, u.phone, u.password_hash, u.super_code,
	      u.role_id, u.status, u.notify_method, u.notify_email, u.notify_sms_phone,
	      u.notify_channel_id, u.notify_target, u.created_at, u.updated_at
 FROM users u
 JOIN user_event_subscriptions s ON s.user_id = u.id
 WHERE s.event = ? AND u.status = 'active'`

func scanSubscribedUsers(rows *sql.Rows) ([]User, error) {
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.RealName, &u.Phone, &u.PasswordHash, &u.SuperCode,
			&u.RoleID, &u.Status, &u.NotifyMethod, &u.NotifyEmail, &u.NotifySMSPhone,
			&u.NotifyChannelID, &u.NotifyTarget, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsersBoundToChannelSubscribed 返回绑定指定通道且订阅了指定事件的活跃用户（个人型通道定向分发）。
func (s *Store) UsersBoundToChannelSubscribed(channelID int64, event string) ([]User, error) {
	rows, err := s.DB.Query(usersSubscribedBase+` AND u.notify_method = 'channel' AND u.notify_channel_id = ?`, event, channelID)
	if err != nil {
		return nil, err
	}
	return scanSubscribedUsers(rows)
}

// UsersSubscribedToEvent 返回订阅了指定事件的活跃用户（email/sms 方式直发）。
func (s *Store) UsersSubscribedToEvent(event string) ([]User, error) {
	rows, err := s.DB.Query(usersSubscribedBase, event)
	if err != nil {
		return nil, err
	}
	return scanSubscribedUsers(rows)
}

// ListUserEvents 返回用户订阅的事件编码列表。
func (s *Store) ListUserEvents(userID int64) ([]string, error) {
	rows, err := s.DB.Query(`SELECT event FROM user_event_subscriptions WHERE user_id = ? ORDER BY event`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetUserEvents 全量替换用户的事件订阅（空列表清空订阅）。
func (s *Store) SetUserEvents(userID int64, events []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_event_subscriptions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_event_subscriptions (user_id, event) VALUES (?, ?)`, userID, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetNotificationSchedule 读取定时摘要设置；无记录返回默认值（未启用、24h）。
func (s *Store) GetNotificationSchedule() (*NotificationSchedule, error) {
	var sc NotificationSchedule
	var enabled, includeNodes, includeApps int
	var channelIDs string
	var interval int
	err := s.DB.QueryRow(
		`SELECT enabled, interval_hours, include_nodes, include_apps, channel_ids, COALESCE(updated_at, 0)
		 FROM notification_schedule WHERE id = 1`).
		Scan(&enabled, &interval, &includeNodes, &includeApps, &channelIDs, &sc.UpdatedAt)
	if err == sql.ErrNoRows {
		return &NotificationSchedule{IntervalHours: 24, IncludeNodes: true, IncludeApps: true}, nil
	}
	if err != nil {
		return nil, err
	}
	sc.Enabled = enabled != 0
	sc.IntervalHours = interval
	sc.IncludeNodes = includeNodes != 0
	sc.IncludeApps = includeApps != 0
	if err := json.Unmarshal([]byte(channelIDs), &sc.ChannelIDs); err != nil {
		sc.ChannelIDs = nil
	}
	return &sc, nil
}

// SaveNotificationSchedule 保存定时摘要设置。
func (s *Store) SaveNotificationSchedule(sc *NotificationSchedule) error {
	buf, err := json.Marshal(sc.ChannelIDs)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(
		`UPDATE notification_schedule SET enabled = ?, interval_hours = ?, include_nodes = ?,
		   include_apps = ?, channel_ids = ?, updated_at = ?
		 WHERE id = 1`,
		boolInt(sc.Enabled), sc.IntervalHours, boolInt(sc.IncludeNodes), boolInt(sc.IncludeApps),
		string(buf), now())
	return err
}
