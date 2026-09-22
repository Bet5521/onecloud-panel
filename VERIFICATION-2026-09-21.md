# OneCloud Panel — 需求改造 + 回归验证报告

日期：2026-09-21
范围：需求 #1（通知管理）~#5（体验改进）全量落地与统一回归
结论：**后端 `go test ./...` EXIT=0（全包通过），前端 `vite build` 成功**

---

## 一、验证结果总览

| 校验项 | 命令 | 结果 |
|---|---|---|
| 后端全量编译 | `go build ./...` | ✅ EXIT=0 |
| 后端全量测试 | `go test ./...` | ✅ **EXIT=0，无 FAIL** |
| 前端依赖安装 | node 直调 `npm-cli.js install` | ✅ EXIT=0（17s） |
| 前端生产构建 | node 直调 `npm-cli.js run build` | ✅ EXIT=0（1648 模块，13.89s） |
| 临时日志清理 | node `fs.unlinkSync` | ✅ 10 个 .log 已删 |

通过的包：`agent` `api` `apps` `audit` `auth` `executor` `node` `notify` `recipes` `runner` `self` `sshx` `store` `system` `tlssniff`

前端产物输出到 `internal/web/assets/`（Go 服务端内嵌目录），`AppsView-*.css/js` 正常产出
→ 重写的 `AppsView.vue`（自定义应用标签 + 向导 + 上传/安装流程）通过编译。

---

## 二、本轮回归发现并修复的既有 Bug

这些 bug 都由 requirement #3（用户通知联动）的实现埋下，此前从未跑过 `go test ./...`，
属于**首次全量回归才暴露的潜在问题**。按严重程度分类：

### P0 — 全链路不可用

**1. `notify_target` NULL 扫描崩溃（影响范围最大）**
- 症状：批量用户测试返回 `setup: 500 {"error":"创建用户失败"}`；
  直接报错 `sql: Scan error on column index 12, name "notify_target": converting NULL to string is unsupported`
- 根因：迁移 `011_user_notify_target.sql` 建的是**可为 NULL** 的 `TEXT` 列，
  但 `store.User.NotifyTarget` 声明为 `string`；新用户该列为 NULL 时 driver 无法赋值。
  创建用户会立即回读 → INSERT 成功但 `UserByID` 失败 → 500
- 修复：`internal/store/users.go`(`ListUsers`)、`internal/store/seed.go`(`user()`)
  两处 SELECT 改为 `COALESCE(notify_target, '')`
- 为什么不用 `*string`：改类型要连带改 `api/reset_deliver.go`、`user_handlers.go` 的 DTO 拷贝与
  TrimSpace 调用，`COALESCE` 语义等价（未设置=空串）且**零涟漪**

### P1 — 功能静默错误

**2. `createUser` 无视客户端传入的 `notify_method`**
- 症状：非法值 `"telepathy"` 返回 **200** 而非 400；`"sms"` / `"email"` 被静默篡改成 `"log"`
- 根因：`method := methodFromChannel(req.NotifyChannelID)` 永远按 channel 推导，
  客户端传的方法值既不参与校验也不落库 → SMS/邮件下发路径永远走不到
- 修复：改用 `req.NotifyMethod`，对非空值先做枚举校验；`UpdateUserNotify` 落库同步改用它

**3. `validNotifyMethods` 枚举与实际下发实现不一致**
- 症状：`email`/`sms` 被判为非法
- 根因：枚举只有 `log/channel`，但 `deliverToUserNotify` 实际 switch 四种
- 修复：补 `email` / `sms`

**4. `validateNotify` 对 `email`/`sms` 错误要求绑定通道**
- 症状：设置了 `sms` 方式但没绑通道的用户无法保存资料
- 根因：除 `log` 外一刀切要求 `channelID > 0`
- 修复：`log/email/sms` 直接放行（走 SMTP / 短信网关，不依赖用户绑定的通知通道），
  仅 `channel` 校验通道存在性与启用状态；「未选通道」提示改为含 `必须选择通知通道`

**5. webhook 通道被误判为「需要每用户接收标识」**
- 症状：绑定 webhook 通道（未提供 target）被拒 400「该通道需要填写接收标识」
- 根因：`notify/notify.go` 的 `ChannelTarget["webhook"].Needed = true` 设定错误——
  webhook 是把消息 POST 到**通道配置里的固定 URL**，不需要每用户标识
- 修复：`Needed` 改 `false`

**6. `updateUser` / `updateMyProfile` 同 #2 的覆写失效**
- 根因：同样用 `method = methodFromChannel(chID)` 忽略 `req.NotifyMethod`
- 修复：优先取客户端传入值，未传时才按 channel 推导

### P2 — 自引入，已即时修复

**7. 改完第 2 项后 `createUser` 编译失败**
- `undefined: method`（删掉 `method` 变量后 `UpdateUserNotify(u.ID, method, ...)` 未同步）
- 修复：改为 `req.NotifyMethod`

---

## 三、改动清单（file-level）

### 需求 #1 通知管理
| 文件 | 改动 |
|---|---|
| `internal/api/user_handlers.go` | `listMyNotifyChannels` 增加防御过滤：`notify.Build` 校验不过的配置未完成通道不出现在可选列表（「未配置的不可选」落地的最后一道闸） |

### 需求 #2 自定义应用（本轮最大块）
| 文件 | 改动 |
|---|---|
| `internal/apps/custom.go` | **新建**。按 type 合成 `*Recipe`（docker/binary/github）、`SetBinaryDir`、`RegisterCustomApps` / `RegisterCustomApp` / `UnregisterCustomApp`、`PushCustomBinary`（经 `ExecutorFor(n).WriteFile` 推二进制到节点并 chmod 0755）、systemd unit 生成、健康检查换算 |
| `internal/api/custom_app_handlers.go` | **新建**。6 条路由（列表/详情/创建/更新/删除/上传二进制，64MB 上限、原子 rename；管理员可建系统级应用）、`ensureCustomBinaryPushed` |
| `internal/recipes/loader.go` | `Registry` 加 `sync.RWMutex`；新增 `Add` / `Remove`，`List`/`Get` 加读锁（支持运行时注册合成配方） |
| `internal/apps/manager.go` | `Manager` 新增 `binaryDir` 字段 |
| `internal/panel/panel.go` | 注入 `<data-dir>/custom-apps` 并在启动时全量注册自定义应用配方 |
| `internal/api/api.go` | 注册 6 条 `/api/custom-apps*` 路由（均 `RequireAuth` + 权限中间件） |
| `internal/api/app_handlers.go` | `installApp` 在 `custom-` 前缀时先推送二进制，失败返回 400 |
| `web/src/views/AppsView.vue` | 新增「自定义应用」标签：类型标签卡片、新建/编辑向导（docker/github/binary 各自配置表单 + 健康检查）、二进制上传、删除、安装 |

> **设计要点**：AppID 用 `custom-<id>`，合成配方注入 Registry，
> 从而**完全复用现有 `POST /api/nodes/{id}/apps/{app}/...` 生命周期路由**，
> install/uninstall/status/journal 逻辑零改动。

### 需求 #3 用户管理 / #1 联动 + 上述 Bug 修复
| 文件 | 改动 |
|---|---|
| `internal/api/user_handlers.go` | `validNotifyMethods` 补 email/sms；`validateNotify` 改三段式校验；`createUser`/`updateUser`/`updateMyProfile` 保留客户端 method |
| `internal/notify/notify.go` | `ChannelTarget["webhook"].Needed` → `false` |
| `internal/store/users.go` | `ListUsers` 的 `notify_target` 改 `COALESCE` |
| `internal/store/seed.go` | `user()` 的 `notify_target` 改 `COALESCE` |

### 需求 #4 前端卡点
| 文件 | 改动 |
|---|---|
| `web/src/views/NotificationsView.vue` | 补上与 `notify.Build` 完全对齐的 `formConfigured` computed（模板第 80-81 行引用了未定义的它）；编辑既有通道时密钥回显 `******` 按「已填写」对待 |

---

## 四、环境注意事项（本机）

- **Go 不在 PATH**：用 `"C:/Users/betyk/tools/go/bin/go.exe"`（go1.27.1）
- **Bash 工具 coreutils 会随会话失效**（`head`/`cat`/`grep`/`ls` 逐个 `command not found`），
  带管道的命令必然失败（exit 127）
- **`npm` 命令不可用**：managed npm 是带 `#!/usr/bin/env bash` shebang 的包装脚本，本环境找不到 bash → EXIT=127。
  **用 node 直调 cli 绕过**：
  ```bash
  NODE="C:/Users/betyk/.workbuddy/binaries/node/versions/22.22.2-3/node.exe"
  NPM="C:/Users/betyk/.workbuddy/binaries/node/versions/22.22.2-3/node_modules/npm/bin/npm-cli.js"
  "$NODE" "$NPM" install --no-audit --no-fund
  "$NODE" "$NPM" run build
  ```
- 文件清理等 Shell 操作建议用 `node -e "require('fs')..."`，稳定可靠

---

## 五、后续建议

1. `notify_target` 目前靠 `COALESCE` 兜底；若要更严格地表达「可选」语义，
   可把 `store.User.NotifyTarget` 提升为 `*string`，但需同步改造 `reset_deliver.go` 与 DTO 拷贝，**建议在下一轮专门处理**
2. 自定义应用的**架构放行**目前是全通过（运行时架构不匹配以 exec format 暴露），
   若想安装前就拦住，可在 `SynthRecipe` 里做 ARCH 预检
3. 前端目前只做了生产构建校验，自定义应用的**端到端交互**（上传二进制 → 安装 → 日志）
   建议用真机/测试节点跑一遍
