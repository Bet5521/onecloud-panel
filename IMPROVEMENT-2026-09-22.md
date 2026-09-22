# 改进报告 — 2026-09-22（落实 BUG-SCAN-2026-09-22 建议）

依据上一轮全模块潜伏 Bug 扫描结论，落地两条 P2 建议中的可行动项。本轮为代码改进，**未引入新功能**，仅提升类型诚实度与验证覆盖。

## 改动一：`store.User.NotifyTarget` 升 `*string`，移除 COALESCE 兜底

**动机**：`notify_target` 列（`011_user_notify_target.sql`）本可 NULL，但 `store.User.NotifyTarget` 原声明为 `string`，SELECT 必须靠 `COALESCE(notify_target, '')` 兜底，类型不诚实（隐藏了 nullable 语义）。与同结构已用指针的 `NotifyChannelID *int64` 不一致。

**修复方式**：字段升 `*string`，扫描侧去掉两处 COALESCE（modernc 对 nil 指针参数→NULL、扫描 NULL→nil 指针均安全，与 `NotifyChannelID` 同模式），写侧 `UpdateUserNotify` / `validateNotify` 同步升 `*string` 让 NULL 真正落库，消费侧安全解引用。

**file-level change tracking**：

| 文件 | 改动 |
|---|---|
| `internal/store/models.go` | `NotifyTarget string` → `NotifyTarget *string` |
| `internal/store/users.go` | `ListUsers` 去掉 `COALESCE(...)`；`UpdateUserNotify` 参数 `target string` → `target *string`（nil→NULL） |
| `internal/store/seed.go` | `user()` 去掉 `COALESCE(...)` |
| `internal/api/user_handlers.go` | 新增 `derefStr` 安全解引用 helper；`toUserDTO` 解引用输出；`validateNotify` 参数升 `*string`；`createUserReq.NotifyTarget` 升 `*string`（JSON 仍兼容）；三处 handler 拼 `tgt` 改为指针赋值 |
| `internal/api/reset_deliver.go` | `deliverBySMS` / `deliverByChannel` 中 `u.NotifyTarget` 消费改为 `derefStr(...)` |

**前端影响**：`userDTO.NotifyTarget` 仍输出 `string`（nil→空串），前端行为零变化。

## 改动二：`internal/apps/custom.go` 纯逻辑单测

**动机**：上一轮只做了前端生产构建校验，自定义应用（需求 #2 最大块）核心合成逻辑未实跑。本环境无运行中的三节点集群，无法做真机 E2E，故优先补纯逻辑单测填补验证缺口。

**新增文件**：`internal/apps/custom_test.go` — 6 个测试函数，覆盖：

- `TestCustomRecipeID`：合成配方 ID 格式 `custom-<id>`
- `TestBuildBinaryUnit`：systemd 单元模板（默认用户 root、参数拼接、WorkingDirectory/ExecStart、Restart）
- `TestBuildRunUnit`：`/bin/sh -c` 包装、双引号剔除
- `TestSynthHealth`：http/tcp/command 三种生成 + 端口为 0 / 无命令 / 未知类型返回 nil
- `TestWithProxyJoin`：GitHub 加速代理仅对 github 系域名生效
- `TestSynthRecipe`：docker/binary/github 三类合成结构 + 必备字段校验（缺 image/exec_name/repo 报错、默认 workdir/branch/run 兜底、clone 命令与卸载 rm 步骤）

路径断言用 `filepath.Join` / 字面正斜杠，跨平台（Windows 下 `filepath.Join` 出反斜杠已规避）。

## 验证结果

| 校验 | 命令 | 结果 |
|---|---|---|
| 编译 | `go build ./...` | ✅ EXIT=0 |
| 全量测试 | `go test ./...` | ✅ EXIT=0（internal/apps 含新用例 2.5s，internal/api 18.4s 等全绿） |
| 前端构建 | `vite build` | ✅ EXIT=0（1648 模块，10.94s） |
| 临时日志 | — | ✅ 已清理 |

## 未闭环项（环境限制，非代码问题）

- **自定义应用真机 E2E**：上传二进制→推送到节点→安装→日志链路需运行中的三节点集群，本环境无，未能实跑。纯逻辑单测已覆盖合成配方正确性，但 `PushCustomBinary`（节点 WriteFile + chmod）、`installApp` 前二进制推送钩子等运行时行为仍待真机验证。
- **扫描建议 #2 的另一半**：仅补了纯逻辑单测，未做集成级 mock 测试（如用 fakeExec 跑通二进制应用 install/uninstall 全流程）。如需更高覆盖可后续补 `TestInstallCustomBinary` 等。
