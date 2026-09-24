# 安全审计与加固报告（v2.0.0）

> 审计范围：`onecloud-panel` 全量源码（Go 后端 / Agent / Vue 前端 / 安装脚本 / 配方文件），
> 覆盖认证与会话、权限模型、输入校验、命令与单元注入、路径穿越、密码学实现、依赖与配置面。
> 本报告同时记录本轮「功能探查」发现的缺失 / 不好用之处及修补与新增内容。

---

## 1. 结论摘要

| 等级 | 数量 | 状态 |
|---|---|---|
| 高危 | 2 | 已修复 |
| 中危 | 7 | 已修复 |
| 低危 | 8 | 已修复 |
| 信息 | 3 | 设计取舍，已记录并给出后续建议 |

另修复 4 项功能缺陷，新增 3 项功能（详见第 4 节）。

---

## 2. 高危问题（已修复）

### H1. 自定义应用：systemd 单元注入 + 任意路径写入

- **位置**：`internal/apps/custom.go`（`SynthRecipe` / `buildBinaryUnit` / `buildRunUnit` / `PushCustomBinary`）
- **问题**：自定义应用配置的 `name`、`args`、`run`、`user`、`workdir`、`exec_name` 均未做字符集与路径约束，
  直接拼接进 systemd 单元文件（`Description=` / `User=` / `WorkingDirectory=` / `ExecStart=`）、
  `/bin/sh -c "…"` 字符串以及节点文件写入路径。
  1. **单元注入**：`args` / `run` / `name` 中的换行可写入新的单元指令（如 `ExecStartPre=/bin/sh -c ...`），
     `%` 可展开 systemd 说明符；
  2. **任意文件写**：`exec_name` 允许含 `/` 与 `..`，`workdir` 可为任意绝对路径，
     `PushCustomBinary` 最终以 root 身份把上传的二进制写到 `path.Join(workdir, exec_name)`，
     可覆盖 `/etc/cron.d/*`、`/etc/ld.so.preload` 等任意落点。
- **影响**：任何持有 `app:write` 权限的账号（含内置「操作员」角色）可获得面板/节点上的任意 root 写与持久化能力，
  超出「管理自定义应用」的预期边界。
- **修复**：新增 `internal/apps/custom_validate.go`，在 `SynthRecipe` 入口统一强制校验（创建 / 更新 / 安装三条链路全覆盖），
  `PushCustomBinary` 再次校验：
  - `name`：≤ 64 字符，禁控制字符 / 引号 / `%`；
  - `exec_name`：必须是纯文件名（`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`），杜绝路径穿越；
  - `workdir`：必须为绝对路径、无 `..`，且位于 `/opt` `/opt/onecloud-apps` `/srv` `/var/lib` `/data` `/home` 之下；
  - `args` / `run`：禁控制字符 / 引号 / `%`（`run` 进入 `/bin/sh -c`）；
  - `user` / `branch` / `image` / `network` / `restart`：按白名单正则约束；
  - `repo`：仅接受 `http(s)://…` 或 `owner/repo`，拒绝前导 `-`（避免被当作 git 命令行选项）。

### H2. 请求体无大小上限

- **位置**：`internal/api/*.go`（`io.ReadAll(r.Body)`、`json.Decoder`、`ParseMultipartForm`）
- **问题**：`create/updateCustomApp` 用 `io.ReadAll(r.Body)` 读全量请求体，
  其余接口的 JSON 解码与二进制上传均无总字节上限（`ParseMultipartForm(64MB)` 只是内存阈值，不是总量上限）。
  攻击者可用超大请求体耗尽面板内存/临时磁盘（面板通常以 root 长期运行）。
- **修复**：新增 `limitBody` 中间件，对全部请求套 `http.MaxBytesReader`：
  普通请求 2 MiB、`multipart/form-data` 上传 80 MiB。

---

## 3. 中危问题（已修复）

| 编号 | 问题 | 位置 | 修复 |
|---|---|---|---|
| M1 | `X-Forwarded-For` 无条件采信：可伪造来源 IP 绕过登录限流、污染审计日志 | `internal/auth/handler.go` `ClientIP` | 仅当直连对端为回环/内网（可信反代）时采信 XFF，且只取第一个合法 IP；否则用直连地址 |
| M2 | 密码重置码确认接口完全不限流，8 位重置码（15 分钟有效）可暴力枚举 | `internal/api/password_reset.go` `confirmByResetCode` | 新增 IP + 用户名双键限流器（15 分钟 10 次失败即拒绝），错误尝试计入失败 |
| M3 | 注册令牌「先查后用」存在并发竞态：`max_uses=1` 的一次性令牌可并发注册多个节点 | `internal/node/service.go` `Register` | 新增 `ClaimRegistrationToken`（原子 `UPDATE … WHERE used_count < max_uses`），领取失败给出可区分原因；节点创建失败时回滚额度 |
| M4 | 登录用户枚举时间侧信道：用户不存在时提前返回，未跑 argon2id | `internal/auth/handler.go` `Login` | 用户不存在时执行 `DummyVerify`（预置 argon2id 哈希等时校验） |
| M5 | `LoginLimiter` 的 map 只增不减：随机用户名/IP 可灌爆内存 | `internal/auth/ratelimit.go` | 窗口外记录随读写剪枝，空键删除；新增 4096 键容量上限并淘汰最久未失败的键 |
| M6 | CSRF 仅依赖 `SameSite=Strict`，缺少请求层校验 | `internal/api/security.go` | 新增 `csrfGuard`：携带会话 Cookie 的写请求校验 `Origin`/`Referer` 与 Host 同站（兼容 `X-Forwarded-Host`），非浏览器客户端不受影响 |
| M7 | 多处 nil 解引用导致 handler panic：`updateCustomApp`、`updateUser`、`readAppConfig`、`writeAppConfig` | `internal/api/*.go` | 全部改为显式错误处理（读不到记录/节点即返回 4xx/5xx） |

---

## 4. 低危问题（已修复）

| 编号 | 问题 | 修复 |
|---|---|---|
| L1 | 会话 Cookie 未设置 `Secure` | 启用 HTTPS（含强制跳转）后置位 `Secure`，明文链路不再回传会话令牌 |
| L2 | 缺少安全响应头（CSP / HSTS / nosniff / 防嵌入等） | 新增 `securityHeaders` 中间件：`Content-Security-Policy`（`script-src 'self'`，构建产物无内联脚本）、`X-Content-Type-Options`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、`Permissions-Policy`、`COOP`/`CORP`、TLS 下 `HSTS` |
| L3 | 随机码生成存在取模偏差（字母表 31 字符，256 不整除） | 超级验证码 / 重置码改用 `crypto/rand.Int` 等概率取样 |
| L4 | `executor.Guard` 可被符号链接逃逸白名单（`/data/x -> /etc/shadow`） | `Check` 对路径与白名单根做 `EvalSymlinks` 归一后比对 |
| L5 | `/api/setup` 并发竞态可创建多个管理员 | 进程内互斥 + 既有唯一约束，串行化初始化流程 |
| L6 | 用户名未做字符集约束（空白/引号/控制字符可写进审计日志与通知） | 新增 `auth.ValidateUsername`，初始化与建号统一校验 |
| L7 | 本人修改密码后其他设备会话仍然有效 | 改密后自动注销其他会话（保留当前登录），并返回注销数量 |
| L8 | `nodeAppFromPath` 用 `http.ErrMissingBoundary` 充当业务错误（前端显示误导文案）；`uninstallApp` 吞掉 JSON 解析错误 | 换成明确错误文案；解析失败返回 400 |

---

## 5. 信息项（设计取舍 / 后续建议）

- **I1 Agent 控制面即全权执行面**：`/v1/exec`、`/v1/file`、`/v1/download` 由 Bearer Token 保护，
  持有 Token 等价于节点 root。这是纳管式 Agent 的固有模型，但建议后续版本：
  ① Agent ↔ 面板链路默认 TLS（现为明文 HTTP + 可选跳过校验）；② 引入能力分级 Token（文件/执行/容器分权）；
  ③ Agent 侧对 `/v1/file` 增加可配置路径白名单（当前仅面板侧按配方收口）。
- **I2 通知通道为管理员可配外发接口**（通用 Webhook / 企业微信 / 钉钉 / SMTP），
  具备 SSRF 面与出网能力。属功能设计（内网自托管场景为主），
  建议在多租户化时增加目标地址白名单/私网拦截策略。
- **I3 Docker Engine 隧道白名单**（`internal/agent/docker_proxy.go`）已拒绝 exec/build/attach/cp 等高危端点，
  但 `containers/create` 仍可挂载宿主路径——这是应用安装所需能力，无法进一步收权；
  已在文档中标注为「节点 root 等价操作」。

---

## 6. 功能探查：缺失 / 不好用之处与处理

### 新增

| 功能 | 说明 | 位置 |
|---|---|---|
| 登录设备管理 | 「头像菜单 → 登录设备」列出本人全部有效会话（IP / 客户端 / 最近活跃 / 过期时间 / 当前设备标记），支持单个注销、一键注销其他设备 | `GET /api/auth/sessions`、`DELETE /api/auth/sessions/{id}`、`POST /api/auth/sessions/revoke-others` + `web/src/components/SessionsDialog.vue` |
| 审计日志导出 | 按当前筛选条件导出 CSV（分页流式，单次上限 10 万行），导出行为本身记入审计 | `GET /api/audit-logs/export` + 审计页「导出 CSV」按钮 |
| 面板数据备份 | 一键下载 `panel.db` 一致快照（`VACUUM INTO`）+ 恢复说明；`include_secrets=1` 时才打包 `secret.key`（默认不含，避免凭据加密密钥随备份扩散） | `GET /api/panel/backup` + 设置页「数据备份」卡片 |

### 修补

| 问题 | 处理 |
|---|---|
| 审计日志无法按「动作」筛选 | `AuditFilter` 与查询接口增加 `action` 条件，前端增加输入框 |
| 应用安装参数对端口类 `int` 不做范围校验（填 0 / 70000 能过前置校验，安装阶段才报底层错误） | `validateVariables` 对端口类参数收紧到 1–65535，并对通用 int 做 32 位范围校验 |
| 应用相关接口的错误文案误导（`http.ErrMissingBoundary`）与静默失败 | 见 L8 |
| 前端残留未使用的 `PlaceholderView.vue`（“建设中 Task 18/19”） | 删除 |

---

## 7. 验证

- `go build ./...`、`go vet ./...`、`gofmt -l` 干净
- `go test -count=1 ./...` 全量通过（16 个测试包，含本轮新增的注入/穿越/限流/CSRF 回归用例）
- 前端 `npm run build` 通过，产物回写 `internal/web/assets`
- 端到端冒烟（真实二进制 + 真实 HTTP）38 项全部通过：安全响应头 / 初始化与登录 / 请求体限额 /
  CSRF 同站校验 / 自定义应用注入与路径穿越拒绝 / 会话管理 / 审计筛选与 CSV 导出 /
  备份归档内容校验 / 重置接口限流 / 非法输入收口

> 复现与回归用例见各包 `_test.go`；本轮新增校验逻辑集中在
> `internal/apps/custom_validate.go`、`internal/api/security.go`、`internal/auth/*`。
