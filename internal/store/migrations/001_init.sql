-- 001_init: OneCloud Panel 初始 schema

PRAGMA foreign_keys = ON;

-- 角色
CREATE TABLE roles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT NOT NULL UNIQUE,          -- admin / operator / viewer
    name       TEXT NOT NULL,
    builtin    INTEGER NOT NULL DEFAULT 1,
    protected  INTEGER NOT NULL DEFAULT 0,    -- 受保护角色不可改权限/删除
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE role_permissions (
    role_id    INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,                 -- 如 node:read
    PRIMARY KEY (role_id, permission)
);

-- 用户
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role_id       INTEGER NOT NULL REFERENCES roles(id),
    status        TEXT NOT NULL DEFAULT 'active', -- active / disabled
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- 节点注册令牌（一次性/限期）
CREATE TABLE registration_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    expires_at  INTEGER,                      -- unix 秒；NULL 不过期
    max_uses    INTEGER NOT NULL DEFAULT 1,
    used_count  INTEGER NOT NULL DEFAULT 0,
    created_by  INTEGER,
    created_at  INTEGER NOT NULL
);

-- 集群节点
CREATE TABLE nodes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,
    mode             TEXT NOT NULL DEFAULT 'remote', -- local / remote
    status           TEXT NOT NULL DEFAULT 'pending', -- pending / active / disabled
    network_type     TEXT,                           -- lan / wireguard
    address          TEXT,                           -- 主 Agent 地址 host:port
    alt_address      TEXT,
    agent_token_hash TEXT,
    hostname         TEXT,
    os_name          TEXT,
    os_version       TEXT,
    kernel           TEXT,
    arch             TEXT,
    cpu_cores        INTEGER,
    mem_total        INTEGER,
    last_seen        INTEGER,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE INDEX idx_nodes_status ON nodes(status);

-- 面板键值设置
CREATE TABLE panel_settings (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);

-- 审计日志
CREATE TABLE audit_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER,
    username    TEXT,
    ts          INTEGER NOT NULL,
    ip          TEXT,
    module      TEXT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    result      TEXT NOT NULL,                   -- success / failure
    request_id  TEXT,
    detail      TEXT
);

CREATE INDEX idx_audit_logs_ts     ON audit_logs(ts);
CREATE INDEX idx_audit_logs_module ON audit_logs(module);

-- 应用安装记录
CREATE TABLE app_installations (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id        INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    app_id         TEXT NOT NULL,
    method         TEXT NOT NULL,                -- native / docker
    status         TEXT NOT NULL DEFAULT 'installed', -- installed / error
    params         TEXT,                         -- JSON: 安装参数
    service_name   TEXT,                         -- systemd 单元名
    container_id   TEXT,
    container_name TEXT,
    installed_at   INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE(node_id, app_id)
);

CREATE INDEX idx_app_installations_node ON app_installations(node_id);

-- 后台任务
CREATE TABLE background_tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,
    status      TEXT NOT NULL,                   -- queued / running / success / failure
    node_id     INTEGER,
    app_id      TEXT,
    payload     TEXT,                            -- JSON: 任务参数
    output      TEXT,
    error       TEXT,
    created_by  INTEGER,
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
);
