-- 008_nodes_docker: 节点级 Docker 镜像加速与第三方仓库配置。
ALTER TABLE nodes ADD COLUMN docker_mirrors TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN docker_insecure_registries TEXT NOT NULL DEFAULT '';
