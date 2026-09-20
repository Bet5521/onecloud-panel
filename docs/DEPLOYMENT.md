# OneCloud Panel 部署文档

## 一、系统要求

| 项目 | 要求 |
|---|---|
| 架构 | armv7l / aarch64 / x86_64 / i386 |
| 系统 | 带 systemd 的 Linux（Armbian / Debian / Ubuntu 等） |
| 内存 | ≥ 512 MB（推荐 ≥ 1 GB） |
| 磁盘 | ≥ 200 MB 可用空间（不含应用数据） |
| 依赖 | curl（安装脚本下载二进制用） |

> 面板与 Agent 均为单文件静态二进制（CGO_ENABLED=0），不依赖 glibc 或运行时库。
>
> **默认以明文 HTTP 部署**；HTTPS 为可选项，在面板「设置 → 传输安全」中开启，
> 无需第二个端口（HTTP 与 HTTPS 共用同一监听端口，由连接首字节自动嗅探区分）。

## 二、架构总览

```
┌──────────────────────────────────────────────────────┐
│  浏览器 │ HTTP / HTTPS（同一端口自动识别）            │
└─────┬────────────────────────────────────────────────┘
      │ :8080
┌─────▼────────────────────────────────────────────────┐
│  面板节点 (onecloud-panel.service)                    │
│  ├─ 前端 SPA (embed)                                  │
│  ├─ REST API                                          │
│  ├─ 任务调度器 (runner)                               │
│  ├─ 审计 / 会话 / 用户 / 角色                         │
│  └─ /install.sh 与 /dl/<arch> 二进制分发              │
└─────┬───────────────────────┬────────────────────────┘
      │ :9000 (Agent HTTP)     │ 本机执行器 (executor)
┌─────▼─────────────┐   ┌─────▼─────────────┐
│ 远程节点 Agent     │   │ 本机应用管理       │
│ (agent 模式)       │   │ (apt/systemd/docker)│
└───────────────────┘   └───────────────────┘
```

## 三、构建二进制（可选，已有 release 可跳过）

```bash
# 全部架构
make build

# 单架构（以 armv7 为例）
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X onecloud-panel/internal/version.Version=1.0.0" \
  -o dist/onecloud-panel-linux-armv7 ./cmd/onecloud-panel
```

构建产物：
```
dist/
  onecloud-panel-linux-armv7    # 玩客云 / armv7l
  onecloud-panel-linux-arm64    # aarch64
  onecloud-panel-linux-amd64    # x86_64
  onecloud-panel-linux-386      # i386
```

## 四、部署面板

### 4.1 准备发布目录

把构建好的二进制放到面板节点的发布目录（默认 `/var/lib/onecloud-panel/releases/`）：

```bash
mkdir -p /var/lib/onecloud-panel/releases
cp dist/onecloud-panel-linux-* /var/lib/onecloud-panel/releases/
```

### 4.2 一键安装（推荐）

面板提供 `/install.sh` 端点，可从已运行的面板拉取。若首次部署，需先把 `install.sh` 和二进制传到目标机器。

```bash
# 方式 A：从已有面板拉取（默认 HTTP）
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- panel \
  --download-base http://<面板IP>:8080/dl \
  --listen :8080

# 方式 B：交互式向导（TTY 环境）
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- panel --interactive

# 方式 C：手动（已有二进制）
install -m 0755 onecloud-panel-linux-armv7 /usr/local/bin/onecloud-panel
```

安装脚本完成以下工作：
1. 识别架构并下载对应二进制到 `/usr/local/bin/onecloud-panel`
2. 创建数据目录 `/var/lib/onecloud-panel`（权限 0750）
3. 写入环境变量文件 `/etc/default/onecloud-panel`
4. 写入 systemd 单元 `/etc/systemd/system/onecloud-panel.service`
5. `systemctl enable --now onecloud-panel`

> 安装阶段不再处理 TLS。需要 HTTPS 时，先完成安装与管理员初始化，再到设置页开启（见第七节）。

### 4.3 环境变量配置

`/etc/default/onecloud-panel` 内容示例：

```ini
OCP_LISTEN=:8080
OCP_DATA_DIR=/var/lib/onecloud-panel
OCP_UNIT_NAME=onecloud-panel.service
```

修改后执行：`systemctl restart onecloud-panel`

### 4.4 安装参数

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--listen` | `OCP_LISTEN` | `:8000` | 监听地址 |
| `--data-dir` | `OCP_DATA_DIR` | `/var/lib/onecloud-panel` | 数据目录 |
| `--download-base` | `DOWNLOAD_BASE` | — | 二进制下载基址 |
| `--interactive` | — | 关 | 交互式配置向导 |
| `--unit-name` | `OCP_UNIT_NAME` | `onecloud-panel.service` | systemd 单元名 |

## 五、初始账号与密码

> **重要：面板不内置任何默认账号。** 首次访问时由初始化向导创建管理员。

### 5.1 Web 初始化（推荐）

1. 浏览器打开 `http://<面板IP>:8080`
2. 自动跳转到初始化页面
3. 设置管理员用户名（3–32 字符）与密码（≥ 8 字符）
4. 提交后自动登录

### 5.2 API 初始化（非交互 / 自动化）

面板未初始化时，`POST /api/setup` 公开可用：

```bash
curl -sS -X POST http://127.0.0.1:8080/api/setup \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"YourStrongP@ssw0rd"}'
```

返回 `{"status":"ok"}` 即创建成功，密码以 argon2id 哈希存储。

### 5.3 密码策略

- 用户名：3–32 字符
- 密码：8–128 字符，argon2id 哈希存储
- 登录与重置请求均有速率限制（按 IP 限流）

### 5.4 忘记密码（自助重置）

登录页点击「忘记密码？」即可自助重置，无需 SSH 登录服务器：

1. **申请重置码**：输入用户名提交。系统生成 8 位重置码（15 分钟内有效），
   - 已在设置页配置 SMTP 时，重置码发送至管理员邮箱；
   - **无论是否配置 SMTP，重置码始终输出到面板服务日志**：

   ```bash
   journalctl -u onecloud-panel -n 50 --no-pager
   # 日志形如：===== [密码重置] 用户 admin 的重置码：AB3XK9QM （15 分钟内有效，…） =====
   ```

2. **确认重置**：输入重置码与新密码（8–128 位）提交。重置成功后：
   - 该用户全部现有会话被注销，需用新密码重新登录；
   - 重置码立即失效，不可复用；
   - 操作写入审计日志。

> 防用户枚举：无论用户名是否存在，申请接口返回完全相同的响应。

接口等价调用：

```bash
curl -sS -X POST http://127.0.0.1:8080/api/auth/password-reset/request \
  -H "Content-Type: application/json" -d '{"username":"admin"}'

curl -sS -X POST http://127.0.0.1:8080/api/auth/password-reset/confirm \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","code":"AB3XK9QM","new_password":"NewStrongPass2"}'
```

若同时失去服务器访问权限与邮箱通道，最后的兜底方式是直接操作数据库（见运维文档）。

## 六、接入节点（Agent）

### 6.1 在面板生成注册令牌

Web UI → 节点 → 生成注册令牌（可设过期时间与最大使用次数）。

### 6.2 安装 Agent

```bash
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- agent \
  --server <面板IP>:8080 \
  --register-token <令牌>
```

Agent 完成以下工作：
1. 下载二进制到 `/usr/local/bin/onecloud-panel`
2. 创建数据目录 `/var/lib/onecloud-panel-agent`
3. 写入环境文件 `/etc/default/onecloud-panel-agent`（权限 0600，含令牌）
4. 注册到面板，获取长期 Token（落盘到数据目录）
5. 启动 `onecloud-panel-agent.service`，每 30 秒心跳上报

### 6.3 Agent 环境变量

```ini
OCP_LISTEN=:9000
OCP_DATA_DIR=/var/lib/onecloud-panel-agent
OCP_SERVER=<面板IP>:8080
OCP_REGISTER_TOKEN=<一次性令牌>   # 首次注册用
OCP_TOKEN=<长期令牌>              # 已注册后使用
```

## 七、启用 HTTPS（面板设置，可选）

### 7.1 设计说明

- HTTPS **默认关闭**，安装后无需任何 TLS 配置即可使用；
- HTTP 与 HTTPS **共用同一个监听端口**：面板对每条新连接嗅探首字节，
  TLS 握手首字节恒为 `0x16`，据此自动分流，因此开启 HTTPS 不改变端口、不影响 `install.sh`/Agent 接入；
- 配置保存后需**重启面板**生效（设置页提供「立即重启」按钮）；
- 开启「强制 HTTPS」后，明文 HTTP 请求（`/healthz` 除外）自动 301 跳转到 HTTPS。

### 7.2 自签证书（最常用）

Web UI → 设置 → 传输安全：

1. 打开「启用 HTTPS」；
2. 证书方式保持「自动生成自签证书」；
3. 「证书域名」中可填写附加域名/IP（逗号分隔）。证书 SAN **始终自动包含**
   localhost、主机名、本机全部 IPv4 与非 link-local IPv6，此处只用于补充额外名称；
4. 按需打开「强制 HTTPS」；
5. 点击「应用 HTTPS 配置」并确认重启。

生成的文件：

```
<数据目录>/tls/self-cert.pem   # 0644，ECDSA P-256，有效期 10 年
<数据目录>/tls/self-key.pem    # 0600
```

### 7.3 自定义证书

证书方式选择「上传自定义证书」，粘贴证书链 PEM 与私钥 PEM：

- 保存前校验证书与私钥是否匹配、证书是否过期，不通过则返回 400，不改动现有配置；
- 文件写入 `<数据目录>/tls/custom-cert.pem`（0644）与 `custom-key.pem`（0600）。

适合使用自有域名 + Let's Encrypt 证书的场景：把 `fullchain.pem` 与 `privkey.pem` 内容粘贴保存即可。

### 7.4 命令行方式（兼容旧部署）

保留命令行参数作为高级/兼容入口，同时提供时优先于面板设置：

```bash
onecloud-panel panel --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem
# 环境变量 OCP_TLS_CERT / OCP_TLS_KEY
```

证书加载失败时面板回退纯 HTTP 并在日志中给出警告，不会因此启动失败。

也可用子命令手动生成自签证书：

```bash
onecloud-panel selfsigned \
  --cert /tmp/cert.pem --key /tmp/key.pem \
  --host 192.168.1.10,panel.example.com
```

### 7.5 关闭 HTTPS

设置页关闭开关后保存并重启即恢复纯 HTTP；证书文件保留在磁盘上不会删除，便于再次开启。

### 7.6 信任自签证书

```bash
# Debian/Ubuntu（在客户端机器执行）
cp self-cert.pem /usr/local/share/ca-certificates/onecloud-panel.crt
update-ca-certificates
```

浏览器也可在首次警告页选择「继续访问」。

## 八、目录结构

```
/usr/local/bin/onecloud-panel          # 二进制（panel/agent 同一文件）
/etc/default/onecloud-panel            # 面板环境变量
/etc/default/onecloud-panel-agent      # Agent 环境变量 (0600)
/etc/systemd/system/onecloud-panel.service
/etc/systemd/system/onecloud-panel-agent.service
/var/lib/onecloud-panel/               # 面板数据目录
  ├─ panel.db                          # SQLite 数据库
  ├─ panel.db-wal / panel.db-shm
  ├─ secret.key                        # 节点令牌加密密钥
  ├─ tls/self-cert.pem, self-key.pem   # 自签 TLS（开启后）
  ├─ tls/custom-cert.pem, custom-key.pem # 自定义 TLS（使用时）
  └─ releases/                         # 各架构二进制分发目录
/var/lib/onecloud-panel-agent/         # Agent 数据目录
  └─ agent.json                        # 节点 ID 与长期 Token
```

## 九、卸载

```bash
# 保留数据
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- uninstall panel

# 清除数据（二进制/单元/数据目录全部删除）
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- uninstall panel --purge

# 全部卸载（面板 + Agent）
... bash -s -- uninstall --purge
```
