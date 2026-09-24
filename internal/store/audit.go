package store

import (
	"strings"
)

// AuditFilter 审计日志查询条件。
type AuditFilter struct {
	Username string
	Module   string
	Action   string
	Result   string
	Start    int64 // unix 秒，0 表示不限
	End      int64
	Page     int
	PageSize int
}

// InsertAuditLog 写入一条审计记录。
func (s *Store) InsertAuditLog(l *AuditLog) error {
	t := l.Ts
	if t == 0 {
		t = now()
	}
	_, err := s.DB.Exec(
		`INSERT INTO audit_logs
		 (user_id, username, ts, ip, module, action, target_type, target_id, result, request_id, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.UserID, l.Username, t, l.IP, l.Module, l.Action,
		l.TargetType, l.TargetID, l.Result, l.RequestID, l.Detail)
	return err
}

// QueryAuditLogs 分页查询审计日志，返回 (日志列表, 总数, error)。
func (s *Store) QueryAuditLogs(f AuditFilter) ([]AuditLog, int64, error) {
	var where []string
	var args []any
	if f.Username != "" {
		where = append(where, "username = ?")
		args = append(args, f.Username)
	}
	if f.Module != "" {
		where = append(where, "module = ?")
		args = append(args, f.Module)
	}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.Result != "" {
		where = append(where, "result = ?")
		args = append(args, f.Result)
	}
	if f.Start > 0 {
		where = append(where, "ts >= ?")
		args = append(args, f.Start)
	}
	if f.End > 0 {
		where = append(where, "ts <= ?")
		args = append(args, f.End)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 500 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	q := `SELECT id, user_id, username, ts, ip, module, action, target_type, target_id,
	             result, request_id, detail
	      FROM audit_logs` + cond + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	queryArgs := append(append([]any{}, args...), f.PageSize, offset)
	rows, err := s.DB.Query(q, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []AuditLog
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(
			&l.ID, &l.UserID, &l.Username, &l.Ts, &l.IP, &l.Module, &l.Action,
			&l.TargetType, &l.TargetID, &l.Result, &l.RequestID, &l.Detail); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// DeleteAuditLogsBefore 删除指定时间之前的日志，返回删除条数。
func (s *Store) DeleteAuditLogsBefore(ts int64) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM audit_logs WHERE ts < ?`, ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
