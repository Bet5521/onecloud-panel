package store

import (
	"database/sql"
	"errors"
)

// ErrTaskNotFound 任务不存在。
var ErrTaskNotFound = errors.New("任务不存在")

// 任务状态常量。
const (
	TaskQueued  = "queued"
	TaskRunning = "running"
	TaskSuccess = "success"
	TaskFailure = "failure"
)

// CreateTask 入队一个任务。
func (s *Store) CreateTask(t *BackgroundTask) (int64, error) {
	t.CreatedAt = now()
	if t.Status == "" {
		t.Status = TaskQueued
	}
	res, err := s.DB.Exec(
		`INSERT INTO background_tasks
		 (type, status, node_id, app_id, payload, output, error, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, '', '', ?, ?)`,
		t.Type, t.Status, t.NodeID, t.AppID, t.Payload, t.CreatedBy, t.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	t.ID = id
	return id, nil
}

// GetTask 按 ID 读取。
func (s *Store) GetTask(id int64) (*BackgroundTask, error) {
	return s.task("WHERE id=?", id)
}

// TaskFilter 列表过滤。
type TaskFilter struct {
	Type   string
	Status string
	NodeID *int64
	Limit  int
	Offset int
}

// ListTasks 分页列表（新任务在前）。
func (s *Store) ListTasks(f TaskFilter) ([]BackgroundTask, int, error) {
	where := "WHERE 1=1"
	var args []any
	if f.Type != "" {
		where += " AND type=?"
		args = append(args, f.Type)
	}
	if f.Status != "" {
		where += " AND status=?"
		args = append(args, f.Status)
	}
	if f.NodeID != nil {
		where += " AND node_id=?"
		args = append(args, *f.NodeID)
	}
	var total int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM background_tasks "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, type, status, node_id, app_id, payload, output, error,
	             created_by, created_at, started_at, finished_at
	      FROM background_tasks ` + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []BackgroundTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

// ClaimQueuedTask 领取一个排队任务并置为 running；无任务返回 ErrTaskNotFound。
func (s *Store) ClaimQueuedTask() (*BackgroundTask, error) {
	var id int64
	err := s.DB.QueryRow(
		`SELECT id FROM background_tasks WHERE status=? ORDER BY id ASC LIMIT 1`,
		TaskQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(
		`UPDATE background_tasks SET status=?, started_at=? WHERE id=? AND status=?`,
		TaskRunning, now(), id, TaskQueued); err != nil {
		return nil, err
	}
	return s.GetTask(id)
}

// MarkTaskRunning 将指定任务置为 running（测试/对账模拟用）。
func (s *Store) MarkTaskRunning(id int64) error {
	_, err := s.DB.Exec(
		`UPDATE background_tasks SET status=?, started_at=? WHERE id=?`,
		TaskRunning, now(), id)
	return err
}

// maxTaskOutput 单任务输出总长上限 1MiB，超限保留尾部（防止长任务撑大 SQLite）。
const maxTaskOutput = 1 << 20

// AppendTaskOutput 追加输出；累计超过 1MiB 时截断保留尾部。
func (s *Store) AppendTaskOutput(id int64, chunk string) error {
	if len(chunk) >= maxTaskOutput {
		chunk = "\n[输出截断，仅保留尾部]\n" + chunk[len(chunk)-maxTaskOutput+64:]
	}
	var cur string
	_ = s.DB.QueryRow(`SELECT output FROM background_tasks WHERE id=?`, id).Scan(&cur)
	merged := cur + chunk
	if len(merged) > maxTaskOutput {
		merged = "\n[输出超限截断，仅保留尾部]\n" + merged[len(merged)-maxTaskOutput+128:]
	}
	_, err := s.DB.Exec(
		`UPDATE background_tasks SET output=? WHERE id=?`, merged, id)
	return err
}

// FinishTask 写入终态。
func (s *Store) FinishTask(id int64, status, errMsg string) error {
	_, err := s.DB.Exec(
		`UPDATE background_tasks SET status=?, error=?, finished_at=? WHERE id=?`,
		status, errMsg, now(), id)
	return err
}

// UpdateTaskPayload 覆盖任务参数（用于任务终态后清空凭据等敏感内容）。
func (s *Store) UpdateTaskPayload(id int64, payload string) error {
	_, err := s.DB.Exec(
		`UPDATE background_tasks SET payload=? WHERE id=?`, payload, id)
	return err
}

// ListRunningTasks 返回所有运行中任务（启动对账用）。
func (s *Store) ListRunningTasks() ([]BackgroundTask, error) {
	rows, err := s.DB.Query(
		`SELECT id, type, status, node_id, app_id, payload, output, error,
		        created_by, created_at, started_at, finished_at
		 FROM background_tasks WHERE status=?`, TaskRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackgroundTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// RequeueTask 将任务重新排队。
func (s *Store) RequeueTask(id int64, note string) error {
	_, err := s.DB.Exec(
		`UPDATE background_tasks SET status=?, started_at=NULL, finished_at=NULL,
		    output = output || ? WHERE id=?`,
		TaskQueued, note, id)
	return err
}

func (s *Store) task(where string, args ...any) (*BackgroundTask, error) {
	q := `SELECT id, type, status, node_id, app_id, payload, output, error,
	             created_by, created_at, started_at, finished_at
	      FROM background_tasks ` + where
	row := s.DB.QueryRow(q, args...)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	return t, err
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(sc taskScanner) (*BackgroundTask, error) {
	t := &BackgroundTask{}
	err := sc.Scan(&t.ID, &t.Type, &t.Status, &t.NodeID, &t.AppID, &t.Payload,
		&t.Output, &t.Error, &t.CreatedBy, &t.CreatedAt, &t.StartedAt, &t.FinishedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}
