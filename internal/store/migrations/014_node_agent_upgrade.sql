-- 014_node_agent_upgrade: 节点 Agent 版本、存储信息、自动升级开关
ALTER TABLE nodes ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN storage_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN auto_upgrade INTEGER NOT NULL DEFAULT 0;
