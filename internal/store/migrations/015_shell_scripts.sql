-- 015: SH 脚本管理
-- shell_scripts 存脚本内容与元信息；shell_script_deployments 记录「脚本×节点」
-- 维度的部署状态（content_hash 用于比对节点侧脚本版本；auto_start=true 时以
-- systemd oneshot 单元 ocp-script-<id>.service 实现开机自启）。
CREATE TABLE shell_scripts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    content       TEXT NOT NULL,
    owner_user_id INTEGER,                       -- 创建者；NULL=系统级(管理员)
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_shell_scripts_owner ON shell_scripts(owner_user_id);
CREATE TABLE shell_script_deployments (
    script_id    INTEGER NOT NULL,
    node_id      INTEGER NOT NULL,
    auto_start   INTEGER NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (script_id, node_id)
);
