-- 003_node_token_enc: Agent Token 可逆密文（用于面板轮换）

ALTER TABLE nodes ADD COLUMN agent_token_enc TEXT;
