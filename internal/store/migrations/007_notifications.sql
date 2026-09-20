-- 007_notifications: 通知通道配置表（wxpusher/Server酱/企微/钉钉/webhook）。
CREATE TABLE notification_channels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,                 -- wxpusher / serverchan / wecom / dingtalk / webhook
    name        TEXT NOT NULL,
    config_json TEXT NOT NULL DEFAULT '{}',    -- 各类型配置（含密钥，回显时脱敏）
    enabled     INTEGER NOT NULL DEFAULT 1,
    tested_at   INTEGER,                       -- 最近一次测试发送成功时间；NULL 未测
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
