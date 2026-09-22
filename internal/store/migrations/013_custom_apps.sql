-- 013: 自定义应用
-- 支持三类自定义应用：
--   github  —— 连接 GitHub 仓库（提供镜像/compose，按 Docker 方式部署）
--   docker  —— 直接提供镜像、端口、环境变量、挂载
--   binary  —— 上传二进制文件，按 systemd 原生服务部署到节点
-- config_json 存储各类型的专属配置；安装记录复用 app_installations 表，
-- AppID 形如 custom:<id>。
CREATE TABLE custom_apps (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    type          TEXT NOT NULL,                 -- github / docker / binary
    name          TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT '自定义',
    icon          TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    homepage      TEXT NOT NULL DEFAULT '',
    config_json   TEXT NOT NULL DEFAULT '{}',
    owner_user_id INTEGER,                       -- 创建者；NULL=系统级(管理员)
    created_by    INTEGER,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_custom_apps_owner ON custom_apps(owner_user_id);
