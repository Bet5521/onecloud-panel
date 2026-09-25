# 实施计划：Docker 安装参数覆盖 / SH 脚本管理 / 节点 SSH 终端 / SD 卡管理

> 状态：已实施完成　|　日期：2026-09-25　|　实施顺序：功能1 → 功能2 → 功能4 → 功能3（功能3需新增依赖，放最后）
>
> 实施结果（2026-09-25）：4 项功能全部落地，`go build ./... && go vet ./... && go test ./...` 全部通过。
> 与计划的关键偏离：fstab 自启通过 Agent 的 ReadFile/WriteFile 读写 `/etc/fstab`（备份至
> `/etc/fstab.ocp.bak`），而非计划中假设的 exec 单引号写入；`updateFstab` 对同挂载点幂等（替换
> UUID/选项而非重复追加）。前端（xterm 终端、存储区块、脚本页面、docker 覆盖表单）已实现，
> 但本机无 node/npm，`web` 构建产物需在装有 Node.js 的环境执行 `npm install && npm run build`
> 后经 embed 生效。文档同步更新至 [API.md](API.md)（storage/terminal/scripts/docker 覆盖）。

---

## 功能1：Docker 应用安装时参数可修改（端口/数据卷/环境变量）

**问题**：`installReq` 仅有 `{method, vars}`，`DockerInstall` 直接使用配方渲染后的 `DockerSpec`，配方未声明为变量的端口/卷/环境变量无法调整。

### 数据模型与语义
- `installReq` / `apps.TaskPayload` 增加可选字段 `Docker *DockerOverride`：
  ```go
  type DockerOverride struct {
      Ports   []string `json:"ports,omitempty"`   // 全量替换（空=用配方默认）
      Volumes []string `json:"volumes,omitempty"` // 全量替换（空=用配方默认）
      Env     []string `json:"env,omitempty"`     // 增量合并（KEY=VALUE，后者覆盖同名 KEY）
      Restart string   `json:"restart,omitempty"` // 可选：no/always/unless-stopped/on-failure
  }
  ```
- 安装记录 `AppInstallation.Params` 改存 `{"vars":{...},"docker":{...}}` 结构；`installationVars` 兼容旧格式（无 `vars` 键时按旧扁平 map 解析），卸载/重渲染时同样应用覆盖（保证命名卷清理一致）。

### 后端改动
| 文件 | 改动 |
|---|---|
| `internal/apps/helpers.go` | `paramsJSON(vars)` → `paramsJSON(vars, ov)`；新增 `installationDocker(in)` |
| `internal/apps/docker.go` | `DockerInstall`/`DockerUninstall` 渲染后调用 `applyDockerOverride(ds, ov)`（Render 已深拷贝，可安全修改）；`buildCreateBody` 不变 |
| `internal/apps/validate.go` | 新增 `ValidateDockerOverride(appID, ov)`：端口复用 `parsePortSpec` 语义校验；env 必须为 `KEY=VALUE`（KEY 符合 `[A-Za-z_][A-Za-z0-9_]*`，值禁控制字符/换行）；卷格式 `宿主:容器[:ro]`（绝对路径或合法命名卷，禁空格/`..`）；restart 枚举校验 |
| `internal/api/app_handlers.go` | `installReq` 加字段；调用 `a.apps.ValidateDockerOverride`；payload 带入 |
| `internal/apps/tasks.go` | `paramsJSON(p.Vars, p.Docker)`（对账补建记录处同步） |

### 前端改动（`web/src/views/AppsView.vue`）
- 安装对话框：`method==='docker'` 时显示「高级设置」折叠区（端口/卷/环境变量三个可编辑行编辑器，参考自定义应用向导的 textarea+逐行模式），预填配方声明默认值，placeholder 说明「留空使用配方默认」；`submit()` 组装 `docker` 对象。

### 验证
`go build ./... && go vet ./... && go test ./internal/...`；新增覆盖校验与应用逻辑单测（含旧 Params 兼容解析）；浏览器手动安装一个 docker 应用并改端口/ENV 验证。

---

## 功能2：应用管理增加 SH 脚本管理（上传脚本 + 可选开机自启）

### 数据模型（新迁移 `internal/store/migrations/015_shell_scripts.sql`）
```sql
CREATE TABLE IF NOT EXISTS shell_scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    owner_user_id INTEGER,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS shell_script_deployments (
    script_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    auto_start INTEGER NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (script_id, node_id)
);
```
> 自启是「脚本×节点」维度，放部署表而非脚本表。

### 后端
- `internal/store/shell_scripts.go`（新）：脚本 CRUD + 部署记录 Upsert/List/Delete。
- `internal/scriptsvc/`（新包）：
  - `Deploy(ctx, ex, script, autostart)`：脚本经 exec+base64 分块写入节点 `/etc/onecloud-scripts/<id>.sh`（agent 文件接口有白名单，统一走 exec 写入，本机/远程一致）→ `chmod 750`；
  - 自启：生成 `/etc/systemd/system/ocp-script-<id>.service`（`Type=oneshot`、`After=network-online.target`、`ExecStart=/etc/onecloud-scripts/<id>.sh`）→ `daemon-reload` → `enable/disable`；关闭自启时 disable 并删单元文件；
  - 脚本内容安全校验（非空、大小上限、禁 NUL）。
- `internal/api/script_handlers.go`（新）：
  - `GET/POST /api/scripts`、`PUT/DELETE /api/scripts/{id}`（app:read / app:write）
  - `POST /api/scripts/{id}/run` `{node_id}` → 后台任务 `script_run`（ExecStream `sh /etc/onecloud-scripts/<id>.sh`，输出进任务日志/TaskProgressDialog；先 Deploy 再跑）
  - `POST /api/scripts/{id}/deploy` `{node_id, auto_start}`（同步执行，返回结果）
  - 删除脚本时清理各节点部署。审计记录。
- `internal/apps/tasks.go` 或 scriptsvc 内注册 `script_run` 任务类型。

### 前端（`web/src/views/AppsView.vue`）
- 新增「脚本」标签页：脚本列表（名称/描述/更新时间/操作）；
- 新建/编辑对话框：名称、描述、内容（textarea + 文件选择上传，FileReader 读取）；
- 「运行」对话框：选择节点 → 任务进度弹窗看输出；「部署/自启」对话框：选节点 + 自启开关。

### 验证
建表迁移（启动自动执行）、CRUD + run/deploy 单测；手动上传脚本在节点执行、设自启后 `systemctl is-enabled` 验证。

---

## 功能4：节点管理增加 SD 卡检测 / 挂载 / 自动挂载

### 后端
- `internal/storage/storage.go`（新包）：全部经 `ExecutorFor`（本机/远程统一）：
  - `List`：`lsblk -b -P -o NAME,PATH,SIZE,TYPE,FSTYPE,MOUNTPOINT,RM,HOTPLUG,MODEL` 解析 `KEY="value"` 行，过滤 `TYPE in (disk,part)`；
  - `Mount(device, mountpoint)`：校验设备 `/dev/` 前缀、挂载点绝对路径（禁 `..`/空格）→ `mkdir -p` → `mount`；
  - `Unmount(mountpoint)`：`umount`；
  - `Autostart(device, mountpoint, fstype, enabled)`：`blkid -s UUID -o value` 取 UUID → 生成 `UUID=xxx <mp> <fstype> defaults,nofail,noatime 0 2` → 修改 `/etc/fstab`（先 `cp /etc/fstab /etc/fstab.ocp.bak` 备份，按挂载点去重旧行，exec+单引号安全写入；`nofail` 保证设备缺失不阻塞启动）。
- `internal/api/storage_handlers.go`（新）：
  - `GET /api/nodes/{id}/storage/devices`（node:read）
  - `POST /api/nodes/{id}/storage/mount`、`/unmount`、`/autostart`（node:write + assertNodeOwner + 审计）

### 前端（`web/src/views/NodesView.vue`）
- 节点抽屉新增「存储」区块：设备表（设备/型号/大小/文件系统/挂载点/可移除标记）+ 刷新；
- 行操作：「挂载」（弹窗填挂载点，默认 `/mnt/sd-<设备名>`）、「卸载」、「自启」开关（enable/disable fstab 条目）。

### 验证
解析函数单测（lsblk/blkid 样例输出）；本机节点实测检测/挂载/卸载/自启，重启验证自动挂载。

---

## 功能3：节点管理增加 SSH 终端（输入密码连接，交互执行命令）

### 依赖（仅此功能新增）
- 后端：`github.com/gorilla/websocket`（go.mod）
- 前端：`@xterm/xterm` + `@xterm/addon-fit`（package.json）

### 后端
- `internal/sshx/`（扩展）：新增交互式 Shell 能力——`NewSession` + `RequestPty("xterm-256color", cols, rows)` + `Shell()`，暴露 stdin/stdout 句柄与 `WindowChange(w,h)`；复用现有密码认证、KeyboardInteractive 兼容、`HostKeyError` 指纹确认（PolicyPin）。
- `internal/api/terminal_handler.go`（新）：`GET /api/nodes/{id}/terminal` WebSocket 端点
  - 鉴权：`RequireAuth(PermNodeWrite)` + `assertNodeOwner`；CSRF 已确认兼容（同源 WS 升级 Origin 同站放行）；
  - 协议（JSON 帧，终端数据 base64）：
    - 客户端→服务端：`{action:"start", user, password, port}` 首帧；`{type:"input", data}`；`{type:"resize", cols, rows}`；`{action:"confirm_hostkey"}`；
    - 服务端→客户端：`{type:"hostkey", fingerprint}`（首连要求确认）；`{type:"started"}`；`{type:"output", data}`；`{type:"exit"|"error", ...}`；
  - **密码仅在内存中使用，不落库、不写日志**；连接关闭即丢弃；审计只记录「谁在何时对哪个节点开启/关闭终端」；
  - 目标主机取 `node.Address`（local 节点回环连接本机 sshd）。

### 前端（`web/src/views/NodesView.vue`）
- 节点抽屉操作区新增「终端」按钮 → 对话框两态：
  1. 连接表单：用户名（默认 root）、端口（默认 22）、密码、首连指纹确认提示；
  2. 终端区：xterm.js 实例，WS 连接 `ws(s)://host/api/nodes/{id}/terminal`，input/output 转发、`fit` 自适应后发送 resize；关闭对话框即断开。

### 验证
`sshx` PTY 单测（本地 sshd 或跳过标记）；浏览器手开终端执行命令、调整窗口尺寸、断开重连。

---

## 总体验证
- `go build ./... && go vet ./... && go test ./...`
- `cd web && npm run build`（构建产物经 embed 更新）
- 手动回归：4 项功能各跑一遍 + 原有应用安装/卸载回归。

## 不做的事（控制范围）
- 不新增权限点/角色（复用 node:*/app:*）
- 不改动 agent（SD 卡与脚本全部走既有 Executor/exec 能力）
- 不忘记更新 docs
