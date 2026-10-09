-- 012: 节点标签与分组
-- 标签以逗号分隔的纯文本存储（如 "客厅,玩客云"）；分组为单值文本。
ALTER TABLE nodes ADD COLUMN tags TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN node_group TEXT NOT NULL DEFAULT '';
