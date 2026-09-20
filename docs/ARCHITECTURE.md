# OneCloud Panel 架构设计

## 一、设计目标与约束

| 约束 | 设计对策 |
|---|---|
| 玩客云 armv7l、约 1 GB 内存 | Go 单静态二进制（CGO_ENABLED=0），常驻内存约 8–20 MB；前端按需懒加载 |
| 用户多为家用内网环境 | 默认 HTTP 零配置可用；HTTPS 可选且同端口承载，不制造端口/证书门槛 |
| 设备能力参差不齐 | 配方声明支持架构，安装前做兼容性校验；原生/容器双部署方式 |
| 自托管安全要求 | argon2id 密码、令牌哈希存储、节点令牌加密盒、限流、RBAC、全量审计 |
| 弱运维能力 | install.sh 幂等、systemd 托管加固单元、面板内日志/重启/状态自服务 |

## 二、分层架构

```
┌─────────────────────────────────────────────────────────────┐
│ 表现层  web/  Vue 3 SPA（Vite 构建，go:embed 进二进制）      │
├─────────────────────────────────────────────────────────────┤
│ 接口层  internal/api   REST 路由组合、鉴权装配、请求校验      │
│         internal/agent Agent HTTP 端（注册/心跳/命令/日志）   │
├─────────────────────────────────────────────────────────────┤
│ 领域层  apps 应用生命周期   node 节点服务   auth 认证会话     │
│         recipes 配方加载/渲染/兼容   audit 审计   runner 任务 │
│         secretbox 密钥盒     mailer 邮件    self 面板自管理   │
├─────────────────────────────────────────────────────────────┤
│ 执行层  executor 本机命令/文件操作（带路径守卫）              │
│         docker Docker 引擎封装    ops 下载与健康检查          │
├─────────────────────────────────────────────────────────────┤
│ 存储层  store  SQLite（迁移版本化）+ 面板键值设置             │
│ 系统层  system 主机信息采集（Linux/非 Linux 分文件编译）      │
│ 传输层  tlssniff 同端口 HTTP/HTTPS 嗅探  tlsutil 自签证书    │
└─────────────────────────────────────────────────────────────┘
```

## 三、模块职责

| 模块 | 职责 |
|---|---|
| `cmd/onecloud-panel` | 唯一入口，子命令 `panel` / `agent` / `selfsigned` / `version` |
| `panel` | 面板模式组合根：装配全部模块、加载 TLS、启动监听与后台循环 |
| `api` | 全部面板 REST 端点；`API` 结构体是组合根，以 SetXxx 方法注入可选依赖 |
| `agent` | Agent 模式：向面板注册、周期心跳、接收并执行任务、采集主机/Docker 信息 |
| `agentclient` | Agent 调用面板的 HTTP 客户端（注册/心跳） |
| `apps` | 应用生命周期核心：native（systemd 直装）与 docker 两条链路、任务注册、状态汇总 |
| `recipes` | 配方 YAML 加载、变量模板渲染、架构/端口兼容性校验；内置配方 go:embed |
| `runner` | 后台任务调度器：queued → running → success/failure，输出落库 |
| `executor` | 本机命令执行与文件操作；`guard.go` 限制危险路径 |
| `docker` | Docker 引擎探测与操作封装 |
| `node` | 节点 CRUD、本机节点（local）自动创建与信息更新 |
| `notify` | 通知抽象与云短信（阿里云 / 腾讯云；华为云 / 百度云预留） |
| `sshx` | SSH 客户端：密码/密钥认证、known_hosts 与指纹（pin/strict）策略 |
| `auth` | argon2id 密码、会话管理器、登录处理器、鉴权中间件、IP 限流器 |
| `secretbox` | 任务 payload 与节点长期令牌的对称加密（密钥首次生成，0600） |
| `audit` | 审计写入（登录/任务/设置等）、保留期清理、HTTP 查询 Handler |
| `mailer` | SMTP 发信：465 隐式 TLS，其余 STARTTLS；UTF-8 主题编码 |
| `self` | 面板自身状态、日志读取、经 systemd 的异步重启及重启收口 |
| `config` | 环境变量与默认配置 |
| `version` | 编译期注入的版本/提交信息 |
| `store` | SQLite 访问与版本化迁移（001–010） |
| `system` | 主机信息采集（hostname/OS/内核/架构/CPU/内存/IP） |
| `tlssniff` | 同端口 HTTP/HTTPS Listener（首字节嗅探） |
| `tlsutil` | ECDSA P-256 自签证书生成、本机名称自动收集 |
| `scripts` | install.sh 的 go:embed 载体 |
| `web` | 前端构建产物的 go:embed 载体 |

## 四、数据模型（SQLite）

```
roles ──< role_permissions
  ▲
  └── users ──< password_resets          005：重置码记录
users ──< sessions（store 层管理）
users (006 扩展 real_name/phone；009 通知方式字段)

registration_tokens                     一次性/限期注册令牌
nodes ──< app_installations              节点与应用安装记录
      ── (agent_token 经 secretbox 加密)
      ── 008 Docker 镜像加速/第三方仓库；010 network_type

notification_channels                   007：通知/短信通道
panel_settings                          键值设置（TLS/SMTP/基本设置）
audit_logs                              全量审计
background_tasks                        异步任务与输出（敏感 payload 经 secretbox 加密）
```

迁移机制：`store.Open` 按 `NNN_` 前缀顺序在事务内执行，以 `PRAGMA user_version`
记录版本。当前版本 10。

关键表 `password_resets`（005）：

| 字段 | 说明 |
|---|---|
| code_hash | 重置码的 SHA-256，明文不落库 |
| expires_at | 申请时间 + 15 分钟 |
| used | 一次性；重置成功后随该用户全部记录一并删除 |
| user_id | 外键 ON DELETE CASCADE，带索引 |

## 五、关键设计决策

### 5.1 同端口 HTTP/HTTPS（tlssniff）

TLS 记录第一个字节恒为 `0x16`（Handshake）。`tlssniff.Listener.Accept()`
对新连接 `bufio.Peek(1)`：命中则 `tls.Server()` 包装，否则返回带缓冲的普通连接。

- 启用 HTTPS 不改变端口，install.sh、Agent、健康检查全部不受影响；
- 无需第二监听端口做 301 跳转，资源与防火墙规则最小化；
- TLSConfig 为 nil 时零开销直通，等价普通 Listener。

### 5.2 TLS 设置化而非安装时决定

- 安装阶段完全不涉及证书，避免首次部署被证书问题阻断（历史上曾多次踩 PEM/私钥坑）；
- 证书 PEM 只写文件（数据目录 `tls/`），不入库、不通过 GET 接口回显；
- 自签模式 SAN 自动收集 localhost/主机名/本机 IP，用户只需补充额外域名；
- 自定义模式保存前做密钥对匹配与过期校验，失败不改动现有配置；
- 命令行 `--tls-cert/--tls-key` 保留为兼容入口，优先级高于设置。

### 5.3 密码重置双通道

- 重置码 8 位、字母表去除易混字符（I/L/O/0/1）、15 分钟有效、仅哈希落库；
- SMTP 为可选增强；**重置码始终写服务日志**，保证内网无邮箱场景仍可由服务器管理员取用；
- 防用户枚举：申请接口对存在/不存在用户返回字节一致的响应；
- 重置成功：更新 argon2id 哈希 → 删除全部重置记录 → 删除该用户全部会话强制重登 → 写审计；
- 限流：按 IP 5 次 / 10 分钟，复用登录限流器实现。

### 5.4 RBAC

13 个权限点（`模块:动作`），角色—权限多对多。预置：

| 角色 | 权限范围 |
|---|---|
| admin（受保护） | 全部权限，不可改/删 |
| operator | 仪表盘、节点读写、应用读写、审计读 |
| viewer | 仪表盘、节点读、应用读、审计读 |

### 5.5 任务模型

应用安装/卸载/服务操作统一入 `background_tasks` 队列，由 `runner` 串行调度，
执行过程流式写 output；前端凭任务 ID 轮询并展示进度。重启面板不丢失任务记录。

## 六、部署形态

```
单机形态（玩客云常见）          多机形态
┌──────────────┐               ┌──────────────┐    ┌──────────────┐
│ panel        │               │ panel (local)│◄──►│ agent node 2 │
│ + local node │               │ local node   │    └──────────────┘
│ + apps       │               └──────┬───────┘    ┌──────────────┐
└──────────────┘                      └──────────►│ agent node 3 │
                                                  └──────────────┘
```

面板节点自动拥有一个 `mode=local` 的节点，应用可直接装在面板本机；
远程节点通过注册令牌经 Agent 接入。

## 七、安全边界与已知限制

- 面板服务以 root 运行（需管理 systemd/apt/Docker），通过 systemd 加固单元收窄能力，
  执行器对文件操作路径做守卫；这是家用小主机上功能性与安全性的折中。
- 明文 HTTP 模式下流量不加密，仅建议在可信内网使用；公网暴露应开启 HTTPS（最好用自定义正式证书）。
- SMTP 未配置时重置码出现在服务日志中，拥有服务器 root 的人员可看到，属预期信任模型。
