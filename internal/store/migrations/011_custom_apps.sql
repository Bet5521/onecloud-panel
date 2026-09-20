-- 011: 自定义应用表
CREATE TABLE IF NOT EXISTS custom_apps (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id      TEXT NOT NULL UNIQUE,       -- 应用唯一标识（同 recipes.ID 规则）
    name        TEXT NOT NULL,              -- 显示名称
    category    TEXT NOT NULL DEFAULT '',   -- 分类
    icon        TEXT NOT NULL DEFAULT '',   -- 图标标识
    description TEXT NOT NULL DEFAULT '',   -- 描述
    homepage    TEXT NOT NULL DEFAULT '',   -- 项目主页

    -- 安装方式：native / docker
    method      TEXT NOT NULL DEFAULT 'docker',

    -- 通用
    ports       TEXT NOT NULL DEFAULT '[]', -- JSON: [{"port":8080,"proto":"tcp","desc":"Web"}]
    variables   TEXT NOT NULL DEFAULT '[]', -- JSON: [{"key":"version","name":"版本","type":"string","default":"1.0","required":false}]

    -- native 方式字段
    download_url TEXT NOT NULL DEFAULT '',  -- 下载地址（支持 GitHub release URL 模板）
    unit_name    TEXT NOT NULL DEFAULT '',  -- systemd 服务名
    unit_template TEXT NOT NULL DEFAULT '', -- systemd unit 模板
    install_script   TEXT NOT NULL DEFAULT '', -- 自定义安装脚本
    uninstall_script TEXT NOT NULL DEFAULT '', -- 自定义卸载脚本

    -- docker 方式字段
    docker_image   TEXT NOT NULL DEFAULT '', -- Docker 镜像（如 nginx:latest）
    docker_ports   TEXT NOT NULL DEFAULT '[]', -- JSON: ["8080:80","443:443/tcp"]
    docker_volumes TEXT NOT NULL DEFAULT '[]', -- JSON: ["/data:/data"]
    docker_env     TEXT NOT NULL DEFAULT '[]', -- JSON: ["KEY=value"]
    docker_network TEXT NOT NULL DEFAULT '',   -- 网络模式
    docker_restart TEXT NOT NULL DEFAULT '',   -- 重启策略
    docker_privileged INTEGER NOT NULL DEFAULT 0, -- 是否特权模式

    -- 健康检查
    healthcheck_type TEXT NOT NULL DEFAULT '', -- http/tcp/command
    healthcheck_port INTEGER NOT NULL DEFAULT 0,
    healthcheck_path TEXT NOT NULL DEFAULT '',
    healthcheck_cmd  TEXT NOT NULL DEFAULT '',

    -- 元数据
    created_by  INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_custom_apps_app_id ON custom_apps(app_id);