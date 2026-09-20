-- 010_network_type: 节点接入类型收敛为 局域网(lan)/公网(public)/wireguard/本机(local)。
-- 历史 unknown 与空值统一修正。
UPDATE nodes SET network_type = 'local' WHERE mode = 'local' AND (network_type = '' OR network_type = 'unknown');
UPDATE nodes SET network_type = 'lan' WHERE network_type = 'unknown' OR network_type = '';