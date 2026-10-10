-- 019: 修正 notification_schedule.updated_at 的存储类型
--
-- 014 以 `updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP` 建列。SQLite 是
-- 动态类型，默认值写入的是文本时间戳；而驱动会按声明类型把 DATETIME 解析成
-- time.Time，Go 侧却按 int64 扫描 → 报错，接口返回 500「查询失败」，通知管理页
-- 直接打不开（全新库必现，因为初始行由 DEFAULT 生成）。
--
-- 这里重建该单行表，并把既有的文本时间统一转换为 Unix 秒（INTEGER），
-- 与 SaveNotificationSchedule 写入的 now() 保持一致。
CREATE TABLE IF NOT EXISTS notification_schedule_new (
    id             INTEGER  PRIMARY KEY CHECK (id = 1),
    enabled        INTEGER  NOT NULL DEFAULT 0,
    interval_hours INTEGER  NOT NULL DEFAULT 24,
    include_nodes  INTEGER  NOT NULL DEFAULT 1,
    include_apps   INTEGER  NOT NULL DEFAULT 1,
    channel_ids    TEXT     NOT NULL DEFAULT '[]',
    updated_at     INTEGER  NOT NULL DEFAULT 0
);

INSERT OR REPLACE INTO notification_schedule_new
    (id, enabled, interval_hours, include_nodes, include_apps, channel_ids, updated_at)
SELECT id, enabled, interval_hours, include_nodes, include_apps, channel_ids,
       CASE typeof(updated_at)
            WHEN 'text'    THEN COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
            WHEN 'integer' THEN CAST(updated_at AS INTEGER)
            WHEN 'real'    THEN CAST(updated_at AS INTEGER)
            ELSE 0
       END
FROM notification_schedule;

DROP TABLE notification_schedule;
ALTER TABLE notification_schedule_new RENAME TO notification_schedule;
INSERT OR IGNORE INTO notification_schedule (id) VALUES (1);
