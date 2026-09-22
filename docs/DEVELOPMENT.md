# OneCloud Panel 开发文档

## 一、技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.27，模块名 `onecloud-panel` |
| 存储 | SQLite（纯 Go 驱动 modernc.org/sqlite，支持 CGO_ENABLED=0 交叉编译） |
| 前端 | Vue 3 + Vite 6 + Element Plus（SPA，构建后由 Go go:embed） |
| 依赖 | x/crypto（argon2id）、yaml.v3；整体依赖极少 |

## 二、目录结构

```
cmd/onecloud-panel/        入口（panel/agent/selfsigned/version 子命令）
internal/
  api/                     面板 REST 端点与装配（API 组合根）
  agent/                   Agent 模式逻辑
  apps/                    应用生命周期（native/docker）
  recipes/
    files/*.yaml           内置应用配方（go:embed）
    types.go loader.go     配方结构、加载、渲染、兼容校验
  runner/                  后台任务调度
  executor/                本机命令/文件执行（含路径守卫）
  auth/                    密码/会话/中间件/限流
  notify/ sshx/            通知与云短信 / SSH 客户端
  agentclient/             Agent 回连面板的 HTTP 客户端
  config/ version/         环境变量配置 / 版本信息
  store/migrations/        SQL 迁移（001–010，按前缀顺序）
  audit/ node/ secretbox/ mailer/ self/ docker/ ops/ system/
  tlssniff/                同端口 HTTP/HTTPS 嗅探
  tlsutil/                 自签证书生成
  panel/                   面板模式组合根
  scripts/files/install.sh 一键安装脚本（go:embed）
  web/assets/              前端构建产物（go:embed）
web/                       前端源码（src/views、api、router、components）
docs/                      项目文档
dist/                      构建产物输出
Makefile
```

## 三、开发环境

```bash
# Go 工具链（≥1.27）；Node（≥18，推荐 20/24）
go version && node -v

# 安装前端依赖
npm --prefix web ci        # 或 npm --prefix web install
```

## 四、常用命令

```bash
make vet               # go vet ./...
make fmt               # gofmt -s -w
make test              # go test ./... -p 1
make build             # 交叉编译全部 4 个架构到 dist/
make run-panel         # 本机直接跑面板（:8000，数据目录 ./runtime-data）
make run-agent         # 本机跑 agent
make clean
```

单独交叉编译（armv7 玩客云，PowerShell 写法）：

```powershell
$env:CGO_ENABLED='0'; $env:GOOS='linux'; $env:GOARCH='arm'; $env:GOARM='7'
go build -trimpath -o dist/onecloud-panel-linux-armv7 ./cmd/onecloud-panel
```

版本信息可通过 ldflags 注入：`-X onecloud-panel/internal/version.Version=...`
（见 Makefile LDFLAGS）。

## 五、前端开发

```bash
npm --prefix web run dev      # Vite dev server（热更新）
npm --prefix web run build    # 产物输出到 internal/web/assets/
```

前端约定：

| 位置 | 说明 |
|---|---|
| `web/src/api/http.js` | get/post/put/del 封装，统一错误处理与凭据携带 |
| `web/src/session.js` | 当前用户/权限会话（`session.can(p)`） |
| `web/src/router/index.js` | 路由；公开页面需声明 `meta:{public:true}` |
| `web/src/views/` | 页面：Login/Setup/Dashboard/Nodes/Apps/AppDetail/Users/Roles/Audit/Settings |

**重要：前端构建产物会被 Go embed，改完前端必须重新 `go build` 才会进入二进制。**

## 六、测试

```bash
go test ./... -count=1 -p 1 -timeout 180s
```

- API 测试基础：`setup_test.go` 提供 `newTestAPI/newTestServer`（内存 SQLite +
  TempDir + 全套装配）、`do()`（构造请求并处理 Cookie）、`adminLogin()`；
- 测试可通过 `apiObj.SetDataDir()`、`SetResetCodeSink()` 注入数据目录与重置码捕获；
- 新增后端功能应同步补测试，至少覆盖成功路径、参数校验与权限拒绝。

## 七、数据库迁移规范

1. 新增文件 `internal/store/migrations/011_<name>.sql`，编号递增；
2. 迁移由 `store.Open` 在事务内按序执行，以 `PRAGMA user_version` 记录版本；
3. 迁移必须幂等（`CREATE TABLE IF NOT EXISTS` 等）；
4. 外键字段记得 `ON DELETE CASCADE` 与必要索引。

## 八、应用配方开发

配方为声明式 YAML，放 `internal/recipes/files/<id>.yaml`，启动时经 go:embed 加载并校验。

最小 Docker 配方：

```yaml
api_version: 1
id: myapp                    # 唯一，与文件名一致
name: MyApp
category: 效率
description: 一句话描述
homepage: https://example.com
methods: [docker]
ports:
  - {port: 8080, proto: tcp, description: Web}
healthcheck: {type: http, port: 8080, path: /}
docker:
  arches: [armv7l, aarch64, x86_64]
  image: example/myapp:latest
  ports: ["8080:8080"]
  volumes: [/var/lib/myapp:/data]
  # 可选：容器内运行身份（UID 或 UID:GID）。
  # 部分镜像已移除 PUID/PGID 环境变量（如 OpenList v4.1.0+ 固定以 openlist(1001)
  # 运行），必须靠它指定为 root 才能写入属主为 root 的宿主绑定目录，否则容器会因
  # “没有 ./data 目录的写权限”反复重启。
  user: "0:0"
```

原生（systemd 直装）配方关键字段（见 types.go）：

- `native.arches` 支持架构；`packages` apt 依赖；
- `download` 主程序下载（URLTemplate 支持变量，可附各架构 sha256）；
- `unit_name` / `unit_template` systemd 单元模板（`{{.Vars.x}}` 渲染变量）；
- `install_steps` / `uninstall_steps`：每步一种动作（apt/mkdir/write/exec/download/systemctl）。

变量支持 string/int/bool/select 类型；安装前做类型与字符集校验（防注入）。
`config_files` 声明面板内可编辑的配置文件白名单。

注意：

- 面向玩客云必须支持 `armv7l` 并提供对应镜像/二进制，否则在本机节点无法安装；
- 修改配方后跑 `go test ./internal/recipes/... ./internal/apps/...` 校验。

## 九、提交前自检清单

```bash
make fmt
make vet
make test
npm --prefix web run build && go build ./...
# 涉及部署产物时按目标架构交叉编译
```

## 十、典型开发流程示例（新增一个后端可配置项）

1. 在 `settings.go` 的 `editableSettings` 加键并写校验；
2. 业务代码通过 `store.GetSetting` 读取；
3. SettingsView 增加表单项；
4. 补/改 `*_test.go`；
5. `make test` → 前端 build → 交叉编译 → 实机部署验证 → 更新 docs。
