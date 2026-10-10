-- 021_node_upgrade_result: 节点 Agent 最近一次自升级结果
--
-- 存 Agent 随心跳上报的 JSON（target_version / applied_version / ok / error / at）。
-- 升级失败原因与「已替换但没重启成功」都能据此在节点详情里展示，
-- 避免出现「点了升级，节点没升级，也没有报错」这种无反馈状态。
ALTER TABLE nodes ADD COLUMN upgrade_json TEXT NOT NULL DEFAULT '';
