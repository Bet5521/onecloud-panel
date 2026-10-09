package store

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"onecloud-panel/internal/store/migrations"
)

// migrationNumber 提取 "016_node_tags.sql" -> 16。
func migrationNumber(name string) (int, bool) {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0, false
	}
	v, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, false
	}
	return v, true
}

// TestMigrationNumbersUnique 守住迁移编号唯一性。
//
// migrate() 会按文件名排序，并且只在 `编号 > PRAGMA user_version` 时执行，
// 执行后把 user_version 提升到该编号。因此两个文件若共用同一编号，
// 排在后面的那个会被静默跳过（既不报错也不落库），表现为「列/表莫名缺失」。
// 这条断言用于在多分支合并时尽早暴露该冲突。
func TestMigrationNumbersUnique(t *testing.T) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("读取内嵌迁移目录失败: %v", err)
	}
	seen := map[int]string{}
	var nums []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, ok := migrationNumber(e.Name())
		if !ok {
			t.Errorf("迁移文件名不符合 <编号>_<名称>.sql 约定: %s", e.Name())
			continue
		}
		if prev, dup := seen[v]; dup {
			t.Errorf("迁移编号 %d 重复: %s 与 %s（后者会被静默跳过）", v, prev, e.Name())
		}
		seen[v] = e.Name()
		nums = append(nums, v)
	}
	if len(nums) == 0 {
		t.Fatal("未发现任何迁移文件")
	}
	if !sort.IntsAreSorted(nums) {
		t.Error("迁移编号应可排序且唯一")
	}
}

// TestMigrationsAppliedOnFreshDB 在全新库上跑完整迁移，校验被合并进来的
// 三组结构确实落库（对应 tags/node_group、metric_samples、agent 自升级字段）。
func TestMigrationsAppliedOnFreshDB(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	cols := map[string]bool{}
	rows, err := s.DB.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		t.Fatalf("table_info(nodes): %v", err)
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	rows.Close()

	for _, want := range []string{
		"tags", "node_group", // 016_node_tags
		"agent_version", "storage_json", "auto_upgrade", // 018_node_agent_upgrade
		"owner_user_id", // 012_node_owner（main 侧）
	} {
		if !cols[want] {
			t.Errorf("nodes 表缺少列 %q，检查迁移编号是否被静默跳过", want)
		}
	}

	for _, tbl := range []string{
		"metric_samples",              // 017_metric_samples
		"channel_event_subscriptions", // 014_notification_subscriptions（main 侧）
		"user_event_subscriptions",
		"notification_schedule",
		"shell_scripts", // 015_shell_scripts（main 侧）
	} {
		var n int
		if err := s.DB.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil {
			t.Fatalf("查询表 %s 失败: %v", tbl, err)
		}
		if n == 0 {
			t.Errorf("缺少表 %q，检查迁移编号是否被静默跳过", tbl)
		}
	}

	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version < 18 {
		t.Errorf("user_version = %d, 期望 >= 18（最高迁移编号）", version)
	}
}
