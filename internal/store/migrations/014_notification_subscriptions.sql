-- 014: 通知事件订阅与定时摘要设置
-- 通道级事件订阅（第一级过滤：管理员为通道勾选订阅的事件）
CREATE TABLE IF NOT EXISTS channel_event_subscriptions (
    channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event      TEXT    NOT NULL,
    PRIMARY KEY (channel_id, event)
);

-- 用户级事件订阅（第二级过滤：用户勾选想接收的事件）
CREATE TABLE IF NOT EXISTS user_event_subscriptions (
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event    TEXT    NOT NULL,
    PRIMARY KEY (user_id, event)
);

-- 定时状态摘要设置（单行表）
CREATE TABLE IF NOT EXISTS notification_schedule (
    id             INTEGER  PRIMARY KEY CHECK (id = 1),
    enabled        INTEGER  NOT NULL DEFAULT 0,
    interval_hours INTEGER  NOT NULL DEFAULT 24,
    include_nodes  INTEGER  NOT NULL DEFAULT 1,
    include_apps   INTEGER  NOT NULL DEFAULT 1,
    channel_ids    TEXT     NOT NULL DEFAULT '[]',
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO notification_schedule (id) VALUES (1);
