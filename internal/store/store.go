package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"onecloud-panel/internal/store/migrations"
)

// Store 数据访问层。
type Store struct {
	DB *sql.DB
}

// Open 打开（必要时创建）数据库并执行迁移与种子写入。
func Open(dbPath string) (*Store, error) {
	// _txlock 保证写串行，减少 SQLITE_BUSY
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单写者；限制连接数避免锁竞争
	db.SetMaxOpenConns(1)

	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	if err := s.seed(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.DB.Close() }

// Backup 生成一致性离线备份到 dest（dest 必须事先不存在）。
// 采用 SQLite VACUUM INTO，可在服务运行中安全备份（绕过 WAL）。
func (s *Store) Backup(dest string) error {
	if strings.ContainsAny(dest, "\x00'") {
		return fmt.Errorf("非法的备份路径")
	}
	q := "VACUUM INTO '" + strings.ReplaceAll(dest, "'", "''") + "'"
	_, err := s.DB.Exec(q)
	return err
}

func (s *Store) migrate() error {
	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		var v int
		if _, err := fmt.Sscanf(name, "%d_", &v); err != nil {
			continue
		}
		if v <= version {
			continue
		}
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
