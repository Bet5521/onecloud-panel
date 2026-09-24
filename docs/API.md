# OneCloud Panel REST API 文档

## 一、通用约定

- Base URL：`http(s)://<面板地址>`（默认 HTTP；开启 HTTPS 后同一端口两种协议均可用）
- 请求/响应：`Content-Type: application/json`；时间字段均为 Unix 秒
- 会话鉴权：登录成功后通过 **HttpOnly Cookie** 下发会话，后续请求自动携带；
  也可用 Cookie 头 `session=<值>`。Agent 接口使用注册/长期 Token，不走会话
- 成功统一返回 JSON；错误格式：

```json
{ "error": "错误描述" }
```

- 常见状态码：400 参数错误、401 未登录/会话失效、403 无权限、404 不存在、
  409 状态冲突、429 限流、500 服务端错误
- 每个响应带 `X-Request-ID`，便于与审计/日志关联

## 二、公开接口（无需鉴权）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/system/status` | 初始化状态 `{initialized: bool}` |
| GET | `/api/version` | 面板版本 |
| POST | `/api/setup` | 首次初始化（创建管理员并自动登录） |
| POST | `/api/auth/login` | 登录 |
| POST | `/api/auth/password-reset/request` | 申请密码重置码 |
| POST | `/api/auth/password-reset/confirm` | 凭重置码设置新密码 |
| POST | `/api/agent/register` | Agent 首次注册 |
| POST | `/api/agent/heartbeat` | Agent 心跳/任务拉取 |
| GET | `/install.sh` | 一键安装脚本 |
| GET | `/dl/{name}` | 发布二进制下载 |
| GET | `/healthz` | 健康检查，返回 `ok` |

### 2.1 初始化

```bash
POST /api/setup
{"username":"admin","password":"StrongPass1"}
→ 200 {"status":"ok"}   # 同时下发会话 Cookie；重复初始化返回 409
```

### 2.2 登录 / 登出

```bash
POST /api/auth/login
{"username":"admin","password":"StrongPass1"}
→ 200（Set-Cookie: session=...）
# 失败 401；连续失败 429

POST /api/auth/logout        # 需登录
GET  /api/auth/me            # 当前用户、角色与权限
```

### 2.3 密码重置

```bash
# 步骤 1：申请（防枚举，用户存在与否响应一致；按 IP 限流 5 次/10 分钟）
# method 可选：email（默认，需配置 SMTP）/ notification（按该用户绑定的通知方式定向下发）
#             / super_code（无需申请，直接进入步骤 2）
POST /api/auth/password-reset/request
{"username":"admin","method":"email"}
→ 200 {"status":"ok","hint":"若用户名有效，重置码已发送…同时输出到面板服务日志"}

# 步骤 2：确认（code 大小写不敏感；method=super_code 时 code 填 10 位超级验证码）
POST /api/auth/password-reset/confirm
{"username":"admin","code":"AB3XK9QM","new_password":"NewStrongPass2","method":"email"}
→ 200 {"status":"ok","hint":"密码已重置，请使用新密码登录","super_code":"新超级验证码"}
# 错误/过期码 → 400；成功后旧会话全部失效，超级验证码自动轮换（仅此响应返回一次）
```

## 三、仪表盘

需要 `dashboard:read`。

```bash
GET /api/dashboard/summary
→ {
    "stats": {"nodes_total":1,"nodes_online":1,"apps_running":2,...},
    "nodes": [{"id":1,"name":"...","online":true,"arch":"armv7l",...}]
  }
```

## 四、节点与注册令牌

需要 `node:read`；写操作需要 `node:write`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/nodes` | 节点列表 |
| GET | `/api/nodes/{id}` | 节点详情 |
| GET | `/api/nodes/{id}/info` | 实时主机信息 |
| POST | `/api/nodes/manual` | 手动添加节点 |
| POST | `/api/nodes/ssh-install` | SSH 通道添加节点（入队任务） |
| GET | `/api/nodes/ssh-install?id={id}` | 上述任务进度（查询参数式，不回传 payload） |
| PUT | `/api/nodes/{id}` | 编辑节点 |
| DELETE | `/api/nodes/{id}` | 删除节点 |
| POST | `/api/nodes/{id}/rotate-token` | 轮换 Agent Token |
| GET | `/api/nodes/{id}/docker/status` | Docker 状态 |
| POST | `/api/nodes/{id}/docker/install` | 安装 Docker |
| PUT | `/api/nodes/{id}/docker-config` | 保存节点镜像加速/第三方仓库配置 |
| POST | `/api/nodes/{id}/docker/apply-config` | 下发配置到节点 daemon.json（入队任务） |
| GET | `/api/network-suggest?address=host:port` | 按地址建议节点接入类型 |
| GET | `/api/registration-tokens` | 注册令牌列表 |
| POST | `/api/registration-tokens` | 创建注册令牌 |
| DELETE | `/api/registration-tokens/{id}` | 删除令牌 |

```bash
POST /api/registration-tokens
{"description":"客厅玩客云","expires_at":1799999999,"max_uses":1}
→ {"token":"<明文仅此次返回>", ...}

GET /api/network-suggest?address=192.168.1.20:9000
→ {"network_type":"lan"}
# 取值：lan（内网/回环）/ public（公网）/ wireguard（命中 WG 网段）/ local（面板本机）
# 添加/编辑节点时默认自动识别，可手动修改；节点级 docker_mirrors、
# docker_insecure_registries 字段随节点保存，覆盖面板级默认

# SSH 通道添加：面板用 SSH 登录目标机执行 Agent 安装命令，安装完成后等待 Agent 自注册，
# 再把该节点写入并置为 active。凭据（密码/私钥/安装命令）加密后随任务 payload 流转，
# 任务终态后 payload 立即清空；进度接口不回传 payload。
POST /api/nodes/ssh-install
{"host":"192.168.1.30","port":22,"user":"root","auth_mode":"password",
 "password":"<SSH 密码>","host_key_policy":"pin","host_key_fingerprint":"",
 "name":"客厅玩客云","network_type":""}
→ 200 {"task_id":31,"token_id":7}
# auth_mode：password（password 必填）/ key（private_key 必填，可另填 passphrase）
# host_key_policy：pin（默认，首次连接回显指纹并要求后续一致，首次可留空指纹）
#                 / strict（严格校验 known_hosts）
# network_type 留空则按 host 自动识别；host/user/port/auth_mode/policy 非法均 400

GET /api/nodes/ssh-install?id=31
→ 200 {"id":31,"type":"node-ssh-install","status":"running",
       "output":"…SSH 正在连接…\n[远端] 执行 Agent 安装命令：…","error":"",
       "created_at":…,"started_at":…,"finished_at":null}
# 任务不存在或非本类型 → 404；缺 id 或 id 非法 → 400
```

## 五、应用与任务

读需要 `app:read`；写操作需要 `app:write`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/recipes` | 配方列表（名称/分类/方式/架构） |
| GET | `/api/recipes/{id}` | 配方详情（变量/端口/健康检查） |
| GET | `/api/nodes/{id}/apps` | 节点已装应用 |
| POST | `/api/nodes/{id}/apps/{app}/install` | 安装（入队任务） |
| POST | `/api/nodes/{id}/apps/{app}/uninstall` | 卸载 |
| POST | `/api/nodes/{id}/apps/{app}/{action}` | start/stop/restart |
| GET | `/api/nodes/{id}/apps/{app}/status` | 运行状态/健康 |
| GET | `/api/nodes/{id}/apps/{app}/journal` | 应用日志 |
| GET | `/api/nodes/{id}/apps/{app}/config` | 读取应用配置 |
| PUT | `/api/nodes/{id}/apps/{app}/config` | 写入应用配置 |
| GET | `/api/tasks` | 任务列表（支持 limit/type/status 查询参数） |
| GET | `/api/tasks/{id}` | 任务详情（含 output/error） |

```bash
POST /api/nodes/1/apps/adguard-home/install
{"method":"native","vars":{}}
→ 200 {"task_id":12}
# 架构/配方不兼容或变量非法 → 400；不支持的 method → 400

POST /api/nodes/1/apps/adguard-home/uninstall
{"purge_data":false}
```

## 六、用户与角色

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/users` | `user:read` | `{items:[...],total:n}` |
| POST | `/api/users` | `user:write` | 创建用户 |
| PUT | `/api/users/{id}` | `user:write` | 编辑（角色/状态/姓名/手机号/通知方式） |
| DELETE | `/api/users/{id}` | `user:write` | 删除用户 |
| POST | `/api/users/{id}/password` | `user:write` | 管理员重置密码（超码轮换+注销会话） |
| POST | `/api/users/{id}/super-code` | `user:write` | 重新生成超级验证码（明文仅返回一次） |
| POST | `/api/auth/change-password` | 登录即可 | 修改本人密码 |
| PUT | `/api/auth/profile` | 登录即可 | 更新本人姓名/手机号/通知方式 |
| GET | `/api/auth/notify-channels` | 登录即可 | 本人可选通知通道（启用中，不含密钥） |
| GET | `/api/roles` | `role:read` | 角色与权限列表 |
| PUT | `/api/roles/{id}` | `role:write` | 修改角色权限 |

```bash
POST /api/users
{"username":"alice","password":"AlicePass1","role_id":2,"real_name":"张三","phone":"13800138000",
 "notify_method":"sms","notify_email":"alice@example.com","notify_sms_phone":"13800138000",
 "notify_channel_id":0}
→ 200 {"id":3,"username":"alice","real_name":"张三","phone":"13800138000",
        "notify_method":"sms","notify_sms_phone":"13800138000","role_code":"operator",
        "status":"active","super_code":"ABCD234567"}
# 用户名 3–32、密码 8–128、角色须存在，否则 400
# notify_method：log（默认，仅面板服务日志）/ email / sms / channel
#   email  → notify_email 为收件地址（留空回退 SMTP 收件人设置）
#   sms    → notify_sms_phone 为接收号码，需面板已配置可用短信通道，否则下发时报「短信平台未配置」
#   channel→ notify_channel_id 必填且该通道须存在并处于启用状态，否则 400
# super_code 为 10 位超级验证码，明文仅在创建/重置时返回一次，需提示用户保存

PUT /api/users/3
{"real_name":"李四","phone":"13900139000","status":"active",
 "notify_method":"channel","notify_channel_id":1}

PUT /api/auth/profile
{"real_name":"李四","phone":"13900139000","notify_method":"email",
 "notify_email":"lisi@example.com"}
→ 200 {"status":"ok"}
# 本人自助维护；通知方式字段为可选（*），传入任一 notify_* 字段即整体校验并更新
# log/email/sms 无需 settings:write；channel 只能选择 GET /api/auth/notify-channels 返回的启用通道

PUT /api/roles/3
{"permissions":["dashboard:read","node:read","app:read","auditlog:read"]}
```

### 6.1 登录设备与会话管理

任何登录用户可管理**自己的**会话（他人会话不可见、不可操作）。
会话标识 `id` 为令牌哈希前缀，无法反推令牌，仅用于展示与定向注销。

```bash
GET /api/auth/sessions
→ {"items":[{"id":"3f9c…","ip":"192.168.1.10","user_agent":"Mozilla/5.0 …",
             "created_at":1727000000,"last_seen":1727003600,"expires_at":1727046800,
             "current":true}],"total":1}

DELETE /api/auth/sessions/{id}
→ 200 {"status":"ok"}
# 注销的是当前会话时同步清除 Cookie；不存在/已过期 → 404

POST /api/auth/sessions/revoke-others
→ 200 {"status":"ok","revoked":2}
# 注销除当前登录外的全部会话

POST /api/auth/change-password
{"old_password":"…","new_password":"…"}
→ 200 {"status":"ok","revoked_sessions":2}
# 改密后自动注销其他设备会话（保留当前登录）并返回注销数量
```

## 七、设置与面板自管理

读需要 `settings:read`；写需要 `settings:write`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/settings` | 全部设置 |
| PUT | `/api/settings` | 更新基本/SMTP 设置（白名单键） |
| POST | `/api/settings/tls` | 更新 HTTPS 配置 |
| POST | `/api/settings/smtp-test` | 发送 SMTP 测试邮件 |
| GET | `/api/panel/info` | 面板名称/版本/安装时间 |
| GET | `/api/panel/status` | 运行状态（systemd/监听/时长/路径） |
| GET | `/api/panel/journal` | 面板日志（`?lines=200`） |
| POST | `/api/panel/restart` | 异步重启面板 |
| GET | `/api/panel/backup` | 下载数据备份 tar.gz（`?include_secrets=1` 才含 secret.key） |

```bash
PUT /api/settings
{"panel_name":"我的集群","audit_retention_days":"30","github_proxy":"",
 "smtp_host":"smtp.qq.com","smtp_port":"465","smtp_username":"me",
 "smtp_password":"授权码","smtp_from":"me@qq.com","smtp_test_recipient":"",
 "docker_registry_mirrors":"https://docker.1ms.run","docker_insecure_registries":"harbor.example.com"}
# 非白名单键 → 400；端口/URL 等字段有格式校验
# docker_registry_mirrors / docker_insecure_registries 为面板级 Docker 默认配置（换行分隔多个），
# 节点级配置优先，应用安装拉取镜像时生效

POST /api/settings/tls
{"enabled":true,"mode":"selfsigned","hosts":"panel.local",
 "cert_pem":"","key_pem":"","force_https":true}
→ 200 {"status":"ok","restart_required":true}

POST /api/settings/smtp-test
{"to":""}                     # 留空发送给发件地址自身
→ 200 {"status":"ok","hint":"测试邮件已发送至 …"}
```

设置键说明（TLS）：`tls_enabled`、`tls_mode`（selfsigned/custom）、
`tls_hosts`、`tls_cert_file`、`tls_key_file`、`tls_force_https`。
证书 PEM 不通过任何 GET 接口回显。

### 7.1 通知通道

读需要 `settings:read`；写需要 `settings:write`。
支持类型：`wxpusher`、`serverchan`（Server酱）、`wecom`（企业微信机器人）、
`dingtalk`（钉钉机器人）、`webhook`（通用，POST `{title,body,content}` JSON）、
`sms`（云短信，见下）。
密钥字段（`app_token`/`sendkey`/`secret`/`access_key_secret`/`secret_key`）GET 回显为
`******`，PUT 提交掩码值时保留原值。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/notifications/channels` | 通道列表 |
| POST | `/api/notifications/channels` | 创建通道 |
| PUT | `/api/notifications/channels/{id}` | 更新（名称/配置/启用） |
| POST | `/api/notifications/channels/{id}/test` | 发送测试消息 |
| DELETE | `/api/notifications/channels/{id}` | 删除通道 |

```bash
POST /api/notifications/channels
{"type":"dingtalk","name":"运维群机器人","enabled":true,
 "config":{"webhook":"https://oapi.dingtalk.com/robot/send?access_token=xxx","secret":"SEC..."}}
→ 200 {"id":1,"type":"dingtalk","type_name":"钉钉机器人","name":"运维群机器人",
        "config":{"webhook":"...","secret":"******"},"enabled":true,"tested_at":0}
# 配置缺必填项 → 400（如 wxpusher 缺 app_token、wecom webhook 非 https）

POST /api/notifications/channels/1/test
→ 200 {"status":"ok",...,"tested_at":1726732800}   # 发送失败 → 502，附错误详情
```

### 7.2 短信通道

通道类型 `sms`，`config.provider` 选择云平台；通用必填 `sign_name`（短信签名）与
`template_code`（模板 ID），模板中的验证码变量名需为 `code`。

| provider | 必填配置 | 默认 region | 说明 |
|---|---|---|---|
| `aliyun` | `access_key_id`、`access_key_secret` | `cn-hangzhou` | 阿里云短信，RPC 风格 HMAC-SHA1 签名 |
| `tencent` | `secret_id`、`secret_key`、`app_id` | `ap-guangzhou` | 腾讯云短信，TC3-HMAC-SHA256 签名；`app_id` 即 SmsSdkAppId |
| `huawei` | 预留（配置可保存） | — | 发送时返回「华为云短信暂未实现」 |
| `baidu` | 预留（配置可保存） | — | 发送时返回「百度云短信暂未实现」 |

```bash
POST /api/notifications/channels
{"type":"sms","name":"阿里云短信","enabled":true,
 "config":{"provider":"aliyun","sign_name":"OneCloud","template_code":"SMS_123456789",
           "access_key_id":"LTAI...","access_key_secret":"...","region":"cn-hangzhou"}}
→ 200 {"id":2,"type":"sms","type_name":"短信","name":"阿里云短信",
        "config":{"provider":"aliyun",...,"access_key_secret":"******"},"enabled":true}
# provider 缺失/未知、sign_name 或 template_code 缺失、对应平台密钥缺失 → 400
# 华为云/百度云可保存配置，但 Send 时报「暂未实现」
```

**未配置短信平台时不能通过短信收发验证码**：用户 `notify_method=sms` 时，
密码重置申请会以「短信平台未配置」（或「指定通道不可用于短信下发」）失败，
重置码仍写入面板服务日志兜底；`POST /api/notifications/channels/{id}/test`
对未实现平台同样返回 502。

用户级通知方式（`log`/`email`/`sms`/`channel`）在
`PUT /api/auth/profile`、`POST /api/users`、`PUT /api/users/{id}` 中维护；
登录页「通知」方式重置**仅向该用户绑定的那一条通道/邮箱/手机号定向下发**，
不发往其余通道。

## 八、审计

需要 `auditlog:read`。

```bash
GET /api/audit-logs?username=admin&module=auth&action=login&result=success&start=…&end=…&page=1&page_size=20
→ {"items":[{ "id":1,"username":"admin","ip":"...","module":"auth",
              "action":"login","result":"success","request_id":"...","detail":{}}],
   "total":1}
# 支持筛选：username / module / action / result / start / end（unix 秒）；分页 page / page_size（上限 500）

GET /api/audit-logs/export?action=login
→ 200 text/csv（附件下载）
# 按同一套筛选条件导出 CSV，分页流式写出，单次上限 10 万行（超出在文件尾注明）
# 导出行为本身会记入审计（module=audit, action=export_csv）
```

## 九、调用示例（curl 完整会话）

```bash
JAR=/tmp/cookies.txt
# 登录
curl -sS -c $JAR -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"StrongPass1"}'
# 读节点
curl -sS -b $JAR http://127.0.0.1:8080/api/nodes
# 查任务
curl -sS -b $JAR http://127.0.0.1:8080/api/tasks/12
```
