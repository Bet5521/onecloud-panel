package store

import "database/sql"

// GetSetting 读取设置值；不存在返回 ("", false, nil)。
func (s *Store) GetSetting(k string) (string, bool, error) {
	var v string
	err := s.DB.QueryRow(`SELECT v FROM panel_settings WHERE k = ?`, k).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting 写入设置。
func (s *Store) SetSetting(k, v string) error {
	_, err := s.DB.Exec(
		`INSERT INTO panel_settings (k, v) VALUES (?, ?)
		 ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// AllSettings 返回全部键值。
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT k, v FROM panel_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
