-- 013: 仪表盘历史指标采样
CREATE TABLE IF NOT EXISTS metric_samples (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    ts         INTEGER NOT NULL,            -- 采样时间（unix 秒）
    cpu_pct    REAL NOT NULL DEFAULT 0,     -- CPU 占用百分比
    mem_pct    REAL NOT NULL DEFAULT 0,     -- 内存占用百分比
    disk_pct   REAL NOT NULL DEFAULT 0,     -- 磁盘占用百分比
    load1      REAL NOT NULL DEFAULT 0      -- 1 分钟平均负载
);

CREATE INDEX IF NOT EXISTS idx_metric_samples_node_ts ON metric_samples(node_id, ts);
