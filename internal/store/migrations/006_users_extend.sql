-- 006_users_extend: 用户增加姓名、手机号、超级验证码。
ALTER TABLE users ADD COLUMN real_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN phone TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN super_code TEXT NOT NULL DEFAULT '';
