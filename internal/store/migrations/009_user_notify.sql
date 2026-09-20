-- 009_user_notify: 用户级通知方式（验证码等定向投递）。
-- notify_method: log / email / sms / channel
ALTER TABLE users ADD COLUMN notify_method TEXT NOT NULL DEFAULT 'log';
ALTER TABLE users ADD COLUMN notify_email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN notify_sms_phone TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN notify_channel_id INTEGER;