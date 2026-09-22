-- 011: 用户通知个性化标识
-- 用户选择「已启用的通知通道」后，部分通道需要每用户接收标识
-- （如 WxPusher UID、短信手机号、Webhook Key 等）做定向个性化下发。
ALTER TABLE users ADD COLUMN notify_target TEXT;
