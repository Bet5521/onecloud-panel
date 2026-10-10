-- 020: 通知通道新增「系统通知」标记
--
-- 系统通知通道仅供系统级下发（如密码自助重置码），不允许在「用户管理」里
-- 被选作用户个人的通知方式；只有非系统通道才出现在用户可选列表中。
ALTER TABLE notification_channels ADD COLUMN is_system INTEGER NOT NULL DEFAULT 0;
