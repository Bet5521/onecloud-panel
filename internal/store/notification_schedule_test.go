package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 回归：全新库上 notification_schedule 的初始行曾以 `DATETIME DEFAULT CURRENT_TIMESTAMP`
// 建列，读取时驱动返回 time.Time，而 Go 侧按 int64 扫描 → 报错，
// 「通知管理」页因此弹出「查询失败」。迁移 019 将其统一为 INTEGER。
func TestNotificationScheduleReadOnFreshDB(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	sc, err := s.GetNotificationSchedule()
	if err != nil {
		t.Fatalf("GetNotificationSchedule: %v", err)
	}
	if sc.IntervalHours != 24 || !sc.IncludeNodes || !sc.IncludeApps {
		t.Fatalf("默认值不符: %+v", sc)
	}
	if sc.Enabled {
		t.Fatalf("默认应为未启用: %+v", sc)
	}

	// 保存后读回，updated_at 应为可用的 Unix 秒
	if err := s.SaveNotificationSchedule(&NotificationSchedule{
		Enabled: true, IntervalHours: 6, IncludeNodes: true, IncludeApps: false,
		ChannelIDs: []int64{3, 5},
	}); err != nil {
		t.Fatalf("SaveNotificationSchedule: %v", err)
	}
	got, err := s.GetNotificationSchedule()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.Enabled || got.IntervalHours != 6 || got.IncludeApps {
		t.Fatalf("保存未生效: %+v", got)
	}
	if len(got.ChannelIDs) != 2 || got.ChannelIDs[0] != 3 {
		t.Fatalf("channel_ids 不符: %+v", got.ChannelIDs)
	}
	if got.UpdatedAt < time.Now().Add(-time.Hour).Unix() {
		t.Fatalf("updated_at 不像是刚写入的时间戳: %d", got.UpdatedAt)
	}
}

// unixFromAny 兼容驱动可能返回的多种时间表示。
func TestUnixFromAny(t *testing.T) {
	want := int64(1700000000)
	cases := []any{
		int64(1700000000),
		int(1700000000),
		float64(1700000000),
		"1700000000",
		[]byte("1700000000"),
		time.Unix(1700000000, 0).UTC(),
		"2023-11-14 22:13:20",
	}
	for _, v := range cases {
		if got := unixFromAny(v); got != want {
			t.Errorf("unixFromAny(%#v) = %d, want %d", v, got, want)
		}
	}
	if unixFromAny(nil) != 0 || unixFromAny("") != 0 || unixFromAny("bogus") != 0 {
		t.Fatal("非法输入应返回 0")
	}
}
