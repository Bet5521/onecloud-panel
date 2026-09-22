# OneCloud Panel 运维文档

## 一、服务管理

### 1.1 面板服务

```bash
systemctl status onecloud-panel     # 查看状态
systemctl start onecloud-panel      # 启动
systemctl stop onecloud-panel       # 停止
systemctl restart onecloud-panel    # 重启
systemctl reload onecloud-panel     # 重载（不支持，改用 restart）
systemctl enable onecloud-panel     # 开机自启
systemctl disable onecloud-panel    # 取消开机自启
```

### 1.2 Agent 服务

```bash
systemctl status onecloud-panel-agent
systemctl restart onecloud-panel-agent
```

### 1.3 面板自身管理 API

需登录后调用：

```bash
# 面板状态（版本/路径/运行时长/节点数）
curl -b session.txt http://127.0.0.1:8080/api/panel/status

# 面板日志（末尾 N 行）
curl -b session.txt 'http://127.0.0.1:8080/api/panel/journal?lines=200'

# 异步重启面板（systemd 重新拉起，停机约 0.5 秒）
curl -b session.txt -X POST http://127.0.0.1:8080/api/panel/restart
```

> 已在设置中开启 HTTPS 时，把上面的 `http://` 换成 `https://` 并加 `-k`（自签证书）即可；两种协议在同一端口共存。

## 二、日志

### 2.1 面板日志

```bash
# 实时跟踪
journalctl -u onecloud-panel -f

# 最近 200 行
journalctl -u onecloud-panel -n 200 --no-pager

# 按时间范围
journalctl -u onecloud-panel --since "2026-09-19 00:00" --until "2026-09-19 12:00"
```

### 2.2 应用日志（面板 API）

| 类型 | 路径 |
|---|---|
| native 应用 | `GET /api/nodes/{id}/apps/{app}/journal?lines=200` |
| docker 应用 | 同上（走 docker logs） |
| 面板自身 | `GET /api/panel/journal?lines=200` |

### 2.3 审计日志

- 面板 Web → 审计页面查询
- 保留天数：面板设置 `audit_retention_days`（设置页建议值 30；未设置或 0 表示永不清理）
- 直接查库：`sqlite3 /var/lib/onecloud-panel/panel.db "SELECT * FROM audit_logs ORDER BY id DESC LIMIT 50;"`

## 三、备份与恢复

### 3.1 需要备份的文件

| 文件 | 说明 |
|---|---|
| `/var/lib/onecloud-panel/panel.db` | 主数据库（用户、节点、应用、审计、设置） |
| `/var/lib/onecloud-panel/secret.key` | 节点令牌加密密钥（丢失则所有节点需重新注册） |
| `/var/lib/onecloud-panel/tls/` | TLS 证书与私钥 |
| `/etc/default/onecloud-panel` | 环境变量配置 |

### 3.2 备份脚本

```bash
#!/bin/bash
set -euo pipefail
BACKUP_DIR="/var/backups/onecloud-panel"
TS=$(date +%Y%m%d-%H%M%S)
mkdir -p "$BACKUP_DIR"

# 安全拷贝 SQLite（避免热备份损坏）
sqlite3 /var/lib/onecloud-panel/panel.db ".backup '$BACKUP_DIR/panel-$TS.db'"

cp -a /var/lib/onecloud-panel/secret.key "$BACKUP_DIR/secret-$TS.key"
[ -d /var/lib/onecloud-panel/tls ] && cp -a /var/lib/onecloud-panel/tls "$BACKUP_DIR/tls-$TS"
cp /etc/default/onecloud-panel "$BACKUP_DIR/env-$TS"

echo "备份完成: $BACKUP_DIR (panel-$TS.db)"
```

### 3.3 恢复

```bash
systemctl stop onecloud-panel

cp /var/backups/onecloud-panel/panel-<TS>.db /var/lib/onecloud-panel/panel.db
cp /var/backups/onecloud-panel/secret-<TS>.key /var/lib/onecloud-panel/secret.key
cp /var/backups/onecloud-panel/env-<TS> /etc/default/onecloud-panel

chown root:root /var/lib/onecloud-panel/panel.db /var/lib/onecloud-panel/secret.key
chmod 0600 /var/lib/onecloud-panel/secret.key

systemctl start onecloud-panel
```

## 四、升级

### 4.1 升级面板

```bash
# 1. 替换二进制（运行中文件需用 install 覆盖，避免 ETXTBSY）
install -m 0755 /path/to/onecloud-panel-linux-armv7 /usr/local/bin/onecloud-panel

# 2. 重启
systemctl restart onecloud-panel

# 3. 验证
onecloud-panel version
curl -s http://127.0.0.1:8080/healthz
# 开启 HTTPS 后：curl -sk https://127.0.0.1:8080/healthz
```

### 4.2 升级 Agent

从面板 UI 对节点执行「升级」，或在节点上：

```bash
install -m 0755 /path/to/onecloud-panel-linux-armv7 /usr/local/bin/onecloud-panel
systemctl restart onecloud-panel-agent
```

> 面板与 Agent 二进制是同一个文件，按 `panel` / `agent` 子命令区分模式。

## 五、节点管理

### 5.1 查看节点

```bash
curl -b session.txt http://127.0.0.1:8080/api/nodes
```

节点在线判定：`local` 节点始终在线；远程节点 `LastSeen` 在 90 秒内视为在线（心跳间隔 30 秒）。

### 5.2 重新注册节点

若 `secret.key` 丢失或节点状态异常：

```bash
# 节点上
systemctl stop onecloud-panel-agent
rm /var/lib/onecloud-panel-agent/agent.json
# 在面板生成新令牌，然后
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- agent \
  --server <面板IP>:8080 --register-token <新令牌>
```

### 5.3 移除节点

面板 UI → 节点 → 删除。节点端执行：

```bash
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- uninstall agent
```

## 六、应用管理

### 6.1 状态查询

```bash
# 已安装应用列表
curl -b session.txt http://127.0.0.1:8080/api/nodes/1/apps

# 单个应用状态
curl -b session.txt http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/status

# 应用配置
curl -b session.txt http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/config
```

### 6.2 操作

```bash
# 安装
curl -b session.txt -X POST http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/install \
  -H "Content-Type: application/json" \
  -d '{"method":"native","vars":{"port":"9090"}}'

# 启动 / 停止 / 重启
curl -b session.txt -X POST http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/start
curl -b session.txt -X POST http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/stop
curl -b session.txt -X POST http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/restart

# 卸载（purge=true 清除数据）
curl -b session.txt -X POST http://127.0.0.1:8080/api/nodes/1/apps/<app_id>/uninstall \
  -H "Content-Type: application/json" -d '{"purge_data":false}'
```

### 6.3 任务跟踪

```bash
# 任务列表
curl -b session.txt 'http://127.0.0.1:8080/api/tasks?limit=10'

# 单个任务（含输出与错误）
curl -b session.txt http://127.0.0.1:8080/api/tasks/<task_id>
```

## 七、Docker 管理

面板按需安装 Docker 到节点，配置文件 `/etc/docker/daemon.json`：

- 镜像加速：`docker.1ms.run`、`docker.xuanyuan.me`、`docker.m.daocloud.io`
- 面板采用合并策略：已有镜像源保留在前，去重后追加；内容相同不覆写
- 覆盖前自动备份 `daemon.json.bak`

```bash
# 查看 daemon.json
cat /etc/docker/daemon.json

# 手动重启 Docker（面板安装流程已先写配置再启动，无二次重启）
systemctl restart docker
```

## 八、安全

### 8.1 systemd 加固项

面板与 Agent 服务已启用：

| 选项 | 作用 |
|---|---|
| `NoNewPrivileges=true` | 禁止通过 setuid 获取新权限 |
| `PrivateTmp=true` | 独立 /tmp |
| `ProtectKernelTunables=true` | 禁止修改内核参数 |
| `ProtectKernelModules=true` | 禁止加载/卸载内核模块 |
| `ProtectKernelLogs=true` | 禁止读取内核日志 |
| `ProtectControlGroups=true` | 禁止修改 cgroup |
| `LockPersonality=true` | 锁定执行域 |
| `ProtectHostname=true` | 禁止修改主机名 |
| `RestrictRealtime=true` | 禁止实时调度 |
| `SystemCallArchitectures=native` | 仅允许原生系统调用 |

> 未启用 `ProtectSystem=strict`：面板需管理 `/etc`（应用配置）、`/var`（数据目录）、systemd、docker。

> **未启用 `RestrictSUIDSGID`**：面板/Agent 要代跑 `apt`/`dpkg`，而部分 Debian 包的
> postinst 会给自己创建的目录设置 setuid/setgid 位（例如 transmission-daemon 执行
> `chmod 4750 /var/lib/transmission-daemon/.config/transmission-daemon`）。启用该
> 限制会让这类 `chmod` 直接返回 EPERM（`Operation not permitted`），导致 dpkg
> 配置阶段失败、包卡在 `iF`（half-configured）状态。此项加固与"代管包管理器"
> 这一核心职责冲突，故不启用。

### 8.2 凭据文件权限

| 文件 | 权限 | 说明 |
|---|---|---|
| `/var/lib/onecloud-panel/secret.key` | 0600 | 节点令牌加密密钥 |
| `/etc/default/onecloud-panel-agent` | 0600 | 含注册/长期令牌 |
| `/var/lib/onecloud-panel/tls/self-key.pem`（或 `custom-key.pem`） | 0600 | TLS 私钥 |
| `/var/lib/onecloud-panel/` | 0750 | 数据目录 |

## 九、资源监控

### 9.1 面板资源占用

```bash
# 内存（玩客云实测约 8–20 MB）
systemctl status onecloud-panel | grep Memory

# CPU 时间
systemctl show onecloud-panel -p CPUUsageNSec
```

### 9.2 节点资源

面板仪表盘展示本机与各节点的内存、磁盘、负载、运行时长。

### 9.3 健康检查

```bash
# 面板健康
curl -sk https://127.0.0.1:8080/healthz    # HTTPS
curl -s  http://127.0.0.1:8080/healthz      # HTTP（未启用 TLS 时）

# Agent 健康
curl -s http://<节点IP>:9000/healthz
```

## 十、常见问题

### Q1：面板启动失败，提示数据目录不可写

```bash
ls -ld /var/lib/onecloud-panel
chown root:root /var/lib/onecloud-panel
chmod 0750 /var/lib/onecloud-panel
```

### Q2：Agent 注册失败「Token 无效」

- 确认注册令牌未过期、未超过最大使用次数
- 确认面板地址可达：`curl http://<面板IP>:8080/healthz`
- 查看 Agent 日志：`journalctl -u onecloud-panel-agent -n 50`

### Q3：应用安装卡在 running

```bash
# 查看任务输出
curl -b session.txt https://127.0.0.1:8080/api/tasks/<task_id>

# 常见原因：apt 下载慢 / 镜像拉取慢 / 端口占用
# 节点上手动检查
journalctl -u onecloud-panel-agent -n 100
docker ps -a
systemctl --failed
```

### Q4：HTTPS 证书浏览器警告

自签证书需手动信任（部署文档 7.6），或在设置中替换为自定义正式证书。

### Q5：忘记管理员密码

优先使用登录页「忘记密码？」自助重置（部署文档 5.4）；重置码可在服务日志中取得。

若自助通道不可用（如无服务器访问权限且未配 SMTP 的极端情况），可直接操作数据库兜底。
先在任意机器上生成新密码的 argon2id 哈希，再写回数据库：

```bash
systemctl stop onecloud-panel
sqlite3 /var/lib/onecloud-panel/panel.db
# 用面板自身生成哈希（版本不含该子命令时可借助任意 argon2 工具）
```

也可直接删除面板数据目录后重新安装，走 `/api/setup` 重新初始化（会丢失全部配置）。

### Q6：面板日志出现 `TLS handshake error`

同一端口同时承载 HTTP/HTTPS，偶发握手错误多为扫描器或旧客户端探测；
若浏览器正常访问也报错，确认访问的 URL 协议与证书域名（SAN）是否匹配。

## 十一、端口清单

| 端口 | 协议 | 用途 |
|---|---|---|
| 8080 | TCP | 面板 Web/API（可配置） |
| 9000 | TCP | Agent HTTP（可配置） |
| 51820 | UDP | WireGuard 应用（默认） |
| 53 | TCP/UDP | AdGuard Home DNS |
| 3000 / 80 | TCP | AdGuard Home Web |
| 7890 | TCP | mihomo 代理（默认） |
| 9090 | TCP | mihomo API（默认） |
| 8384 | TCP | Syncthing GUI |
| 22000 | TCP | Syncthing 数据同步 |
