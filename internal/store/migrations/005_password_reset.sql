-- 005_password_reset: 登录页自助密码重置（一次性重置码，短期有效）
CREATE TABLE IF NOT EXISTS password_resets (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT NOT NULL,              -- 重置码 SHA-256
  expires_at INTEGER NOT NULL,           -- Unix 秒
  used       INTEGER NOT NULL DEFAULT 0, -- 0 未使用 / 1 已使用
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets(user_id);
