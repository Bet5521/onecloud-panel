-- 004_node_docker: 节点 Docker Engine 版本（空字符串=未安装）
ALTER TABLE nodes ADD COLUMN docker_version TEXT NOT NULL DEFAULT '';
