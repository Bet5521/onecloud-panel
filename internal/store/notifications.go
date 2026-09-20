package store

import (
	"database/sql"
)

// NotificationChannel 通知通道配置。
type NotificationChannel struct {
	ID         int64
	Type       string // wxpusher / serverchan / wecom / dingtalk / webhook
	Name       string
	ConfigJSON string
	Enabled    bool
	TestedAt   int64 // 0 = 从未测试成功
	CreatedAt  int64
	UpdatedAt  int64
}

// ListNotificationChannels 返回全部通知通道。
func (s *Store) ListNotificationChannels() ([]NotificationChannel, error) {
	rows, err := s.DB.Query(`SELECT id, type, name, config_json, enabled, COALESCE(tested_at, 0), created_at, updated_at
	                         FROM notification_channels ORDER BY id`)
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

// NotificationChannelByID 按 ID 查询通道。
func (s *Store) NotificationChannelByID(id int64) (*NotificationChannel, error) {
	var c NotificationChannel
	var enabled int
	err := s.DB.QueryRow(`SELECT id, type, name, config_json, enabled, COALESCE(tested_at, 0), created_at, updated_at
	                      FROM notification_channels WHERE id = ?`, id).
		Scan(&c.ID, &c.Type, &c.Name, &c.ConfigJSON, &enabled, &c.TestedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	return &c, nil
}

// EnabledNotificationChannels 返回全部启用中的通道。
func (s *Store) EnabledNotificationChannels() ([]NotificationChannel, error) {
	all, err := s.ListNotificationChannels()
	if err != nil {
		return nil, err
	}
	out := make([]NotificationChannel, 0, len(all))
	for _, c := range all {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// CreateNotificationChannel 新建通知通道。
func (s *Store) CreateNotificationChannel(c *NotificationChannel) (*NotificationChannel, error) {
	t := now()
	res, err := s.DB.Exec(
		`INSERT INTO notification_channels (type, name, config_json, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.Type, c.Name, c.ConfigJSON, boolInt(c.Enabled), t, t)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.NotificationChannelByID(id)
}

// UpdateNotificationChannel 更新名称/配置/启用状态。
func (s *Store) UpdateNotificationChannel(id int64, name, configJSON string, enabled bool) error {
	_, err := s.DB.Exec(
		`UPDATE notification_channels SET name = ?, config_json = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		name, configJSON, boolInt(enabled), now(), id)
	return err
}

// MarkNotificationChannelTested 记录测试发送成功时间。
func (s *Store) MarkNotificationChannelTested(id int64, ts int64) error {
	_, err := s.DB.Exec(
		`UPDATE notification_channels SET tested_at = ?, updated_at = ? WHERE id = ?`,
		ts, now(), id)
	return err
}

// DeleteNotificationChannel 删除通知通道。
func (s *Store) DeleteNotificationChannel(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
	return err
}

var _ = sql.ErrNoRows
