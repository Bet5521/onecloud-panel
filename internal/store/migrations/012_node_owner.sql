-- 012: 节点归属用户
-- 手动添加的节点记录添加者；Agent 自动注册的节点归属注册令牌创建者。
-- 本机(local)节点 owner_user_id 为 NULL，视为系统节点，仅管理员可见。
-- 非管理员用户在列表/详情中只能看到 owner_user_id = 自己的节点。
ALTER TABLE nodes ADD COLUMN owner_user_id INTEGER;
CREATE INDEX idx_nodes_owner ON nodes(owner_user_id);
