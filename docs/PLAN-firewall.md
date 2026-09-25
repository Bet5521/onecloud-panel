# 实施计划：节点管理增加防火墙管理（检测 / 开关 / 端口规则增删）

> 状态：已实施完成　|　日期：2026-09-26

## 需求

在节点管理中增加防火墙管理功能，可以修改节点的防火墙设置：支持检测当前防火墙设置（后端类型、启停状态、端口规则）以及新增/删除防火墙规则、开启/关闭防火墙。完成并通过验证后 push 到 GitHub。

## 总体设计

全部经既有 `ExecutorFor` 通道执行（本机直连 / Agent 远程统一，与存储管理一致），**不改动 Agent**。按节点上实际可用的防火墙后端自适应：

| 后端（探测优先级） | 探测 | 启用判定 | 新增/删除规则 | 开关 |
|---|---|---|---|---|
| ufw | `command -v ufw` → `ufw status` | 输出含 `Status: active` | `ufw allow/deny [from <src>] [to any port <p> proto <proto>]` + `ufw delete` | `ufw --force enable` / `ufw disable` |
| firewalld | `command -v firewall-cmd` → `firewall-cmd --state` | 输出为 `running` | `firewall-cmd --permanent --add/remove-port=<p>/<proto>` + `--reload`；deny/带来源用 rich rule | `systemctl start/stop firewalld` |
| nftables | `command -v nft` → `nft list ruleset` | ruleset 含 INPUT 链规则 | 追加/删除 `nft add rule inet filter INPUT tcp dport <p> accept`（表/链不存在时报错提示） | 不支持（提示手动管理服务） |
| iptables | `command -v iptables` → `iptables -S INPUT` | 策略非 ACCEPT 或存在规则 | `-A/-D INPUT [-s <src>] -p <proto> --dport <p> -j ACCEPT/DROP` | 不支持 |
| none | 以上均不存在 | — | 400：节点未检测到防火墙工具 | 400 |

### 数据模型

```go
// internal/firewall/firewall.go
type Status struct {
    Backend string `json:"backend"` // ufw / firewalld / nftables / iptables / none
    Active  bool   `json:"active"`
    Rules   []Rule `json:"rules"`
    Detail  string `json:"detail"`  // 原始状态文本（ufw status / iptables -S，供前端折叠展示）
}
type Rule struct {
    Port   string `json:"port"`            // "53" / "50000:50100"
    Proto  string `json:"proto"`           // tcp / udp / ""(both)
    Action string `json:"action"`          // allow / deny
    Source string `json:"source,omitempty"` // 来源 IP/CIDR，空=任意
}
```

### 输入校验（防注入）

- 端口：`^(\d{1,5})(:\d{1,5})?$` 且数值 1–65535、范围起≤止；
- 协议：`tcp` / `udp` / 空（both；firewalld 与 iptables 的 both 由后端拆成两条规则处理）；
- 来源：仅允许 `[0-9a-fA-F.:/]`（IPv4/IPv6/CIDR），非空时含其他字符 → 400；
- 所有命令以参数数组传递（`ex.Exec(ctx, "ufw", ...)`），不拼 shell 字符串。

## 后端改动

| 文件 | 改动 |
|---|---|
| `internal/firewall/firewall.go`（新包） | `Manager{execFor}`（构造同 `storage.New`）；`Detect(ctx, n) (*Status)`；`AddRule`/`RemoveRule`（按后端分发，firewalld/iptables 后端 both 协议拆两条）；`Toggle`（仅 ufw/firewalld） |
| `internal/firewall/parse.go`（新） | `parseUfwStatus`、`parseFirewallPorts`（`--list-ports` + rich rules）、`parseIptablesSave`（`iptables -S INPUT`）、`validatePort/validateProto/validateSource` |
| `internal/firewall/firewall_test.go`（新） | 解析与校验纯函数单测（三种后端样例输出、非法输入拒绝） |
| `internal/api/firewall_handlers.go`（新） | 4 个 handler + `firewallNode` 公共前置（同 `storageNode`：idFromPath → GetNode → assertNodeOwner）；写操作审计 `firewall_rule_add` / `firewall_rule_remove` / `firewall_toggle` |
| `internal/api/api.go` | `SetFirewallService` + 路由（storage 与 terminal 路由之后） |
| `internal/panel/panel.go` | `firewallSvc := firewall.New(appManager.ExecutorFor)` + `apiObj.SetFirewallService(firewallSvc)` |

### API（挂 `/api/nodes/{id}/firewall*`）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/nodes/{id}/firewall` | node:read | 检测后端/启停状态与规则列表（`{"items": ...}` 同 storage 风格 → `{"backend","active","rules","detail"}`） |
| POST | `/api/nodes/{id}/firewall/rules` | node:write | 新增规则 `{port, proto, action, source}`（source 可空） |
| POST | `/api/nodes/{id}/firewall/rules/remove` | node:write | 删除规则 `{port, proto, source}` |
| POST | `/api/nodes/{id}/firewall/toggle` | node:write | `{enabled}` 开启/关闭（仅 ufw/firewalld；iptables/nftables → 400 说明） |

远端 Agent 不可达时统一 502（同 storage 的 `StatusBadGateway` 惯例）。

## 前端改动（`web/src/views/NodesView.vue`）

- 节点抽屉「存储设备」区块后新增「防火墙」card-head（标题 + 刷新按钮 `:loading="fwLoading"`）；
- 状态行：后端 tag（ufw/firewalld/nftables/iptables/未检测到）+ 启用状态 tag + `can('node:write')` 时的「开启/关闭防火墙」按钮（ElMessageBox.confirm 提示关闭风险/ufw enable 可能断开连接）；
- 规则表：端口 / 协议 / 动作（allow=success、deny=danger tag）/ 来源 / 操作（删除，confirm）；iptables 后端显示 hint「运行时规则，重启后失效」；
- 「新增规则」按钮 → 对话框：端口（必填，支持 `a:b` 范围）、协议（both/tcp/udp）、动作（allow/deny）、来源（可选 IP/CIDR，placeholder 提示留空=任意地址）；
- `openDetail` 时调用 `loadFirewall()`；复用既有 `.hint/.mono` 样式，无新依赖。

## 验证

1. `go build ./... && go vet ./... && go test ./...`（新增 firewall 包单测全绿）；
   ✅ 已执行：build / vet / test 全部通过（exit 0），firewall 包 8 个测试全绿，
   api 包含新 handler 的全量测试通过（31.4s）；
2. `cd web && npm run build` —— **本机未安装 node/npm，无法执行**；前端源码改动需在装有 Node.js 的环境构建后经 embed 生效（与功能3 终端相同限制）；
3. 手动回归（可在真机）：本机节点检测 ufw/firewalld 状态 → 增删端口规则 → 开关防火墙 → 远程节点重复一遍。

> 实施备注：计划阶段设想 backend=none 时增删/开关返回 400，实际实现统一按
> 「执行失败」处理为 502，错误文本携带原因（如「节点未检测到可用的防火墙工具」/
> 「后端 iptables 不支持一键开关，请通过节点终端手动管理」）；仅请求参数校验
> 失败返回 400。API.md 按实际语义记录。

## push 到 GitHub

用户已明确要求「通过验证后 push」。但**本机未安装 git**（`where.exe git` 与常见安装路径均无），无法在本机执行 commit/push。处理方式：完成全部代码与验证后，在最终汇报中给出待执行的 git 命令清单（add/commit/push），由用户在装有 git 的环境执行；若用户能提供 git 可执行路径则由本代理直接执行。

## 不做的事（控制范围）

- 不做自定义链 / NAT 转发 / 富规则编辑器，规则抽象仅端口级（port/proto/action/source）；
- 不做 iptables/nftables 规则持久化（iptables 规则为运行时，界面明示；nftables 提示手动管理）；
- 不新增权限点/角色（复用 node:read/node:write），不改动 Agent；
- 不忘记更新 docs（API.md 四章补 4 端点、USER_GUIDE.md 补防火墙小节、本文档状态更新）。
