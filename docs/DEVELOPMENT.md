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
.github/workflows/         GitHub Actions（自动编译 + 发布 Release）
Makefile
build.sh / build.ps1       一键构建脚本（Linux/macOS 与 Windows）
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
make build             # 交叉编译常用 3 平台到 dist/（linux-armv7 / linux-arm64 / windows-amd64.exe）
make run-panel         # 本机直接跑面板（:8000，数据目录 ./runtime-data）
make run-agent         # 本机跑 agent
make clean
```

一键构建脚本（会先构建前端再交叉编译，产物落到 `dist/`）：

```bash
./build.sh -v 2.2.1            # Linux / macOS
.\build.ps1 -Version 2.2.1     # Windows PowerShell
# 加 -s / -SkipFrontend 可跳过前端构建（需已有 internal/web/assets 产物）
```

> 完整发布（8 个平台）由 CI 完成，见下文「九、CI/CD 与发布」。

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

**发布包架构后缀**：像 lucky / ddns-go 这类把架构写进文件名或 URL 的项目，可用节点事实
`{{$.Node.ArchPkg}}` 渲染出 `x86_64` / `arm64` / `armv7` / `i386`（映射见 `render.go`
的 `NodeFactsForArch`）。示例：

```yaml
- name: 下载发布包
  download:
    url: https://github.com/gdy666/lucky/releases/download/v{{.Vars.lucky_version}}/lucky_{{.Vars.lucky_version}}_Linux_{{$.Node.ArchPkg}}.tar.gz
    dest: /tmp/lucky.tgz
```

配合 `exec` 步骤解压并把二进制 `install` 到 `/usr/local/bin`、写 `systemd` 单元即可完成直装。
新增的可直装应用应把 `methods` 写成 `[native, docker]`（native 优先，Docker 兜底）。

注意：

- 面向玩客云必须支持 `armv7l` 并提供对应镜像/二进制，否则在本机节点无法安装；
- 修改配方后跑 `go test ./internal/recipes/... ./internal/apps/...` 校验。

### 从源码构建镜像（docker.build）

上游没有适配本机架构的镜像（如玩客云 armv7l），或必须固定用自有 fork 的源码构建时，
在 `docker` 段加 `build`：安装时会在节点上获取源码并执行 `docker build`，产物打上
`image` 指定的**本地 tag**，随后按普通容器流程创建运行（**不再拉取远端镜像**）。

```yaml
docker:
  arches: [armv7l, aarch64, x86_64]
  image: myapp-local:latest      # 本地构建产物 tag
  ports: ["6060:6060"]
  build:
    type: git                    # git（默认）| archive
    source: https://github.com/<owner>/<repo>
    ref: "{{.Vars.app_ref}}"     # 分支/标签/提交；改它就等于换构建版本
    dockerfile: '{{if eq .Node.Arch "armv7l"}}Dockerfile.arm{{else}}Dockerfile{{end}}'
    context: .                   # 构建上下文相对路径（默认 .）
    target: ""                   # 多阶段构建的 --target（可选）
    args: {HTTP_PROXY: ""}       # --build-arg（可选）
    env: {DOCKER_BUILDKIT: "1"}  # 构建命令环境变量（默认 DOCKER_BUILDKIT=1）
    workdir: /var/tmp/ocp-build-myapp   # 节点上的源码目录（默认 /var/tmp/ocp-build-<id>）
```

要点：

- **每次安装都会重建**：同一提交的源码内容一致，Docker 层缓存会让重复构建很快；
  源码有更新时才会真正重跑编译，避免「静默沿用旧镜像」。
- 节点需具备 `docker` CLI（构建走 CLI，运行仍由 Engine API 管理）；`type: git` 还需 `git`。
- `type: archive` 时 `source` 直接给源码归档 URL（如 GitHub codeload 的 `tar.gz`），
  走面板的 GitHub 加速代理下载后 `tar --strip-components=1` 解包。
- 绑定挂载的目标文件（如 `/app/.env`）必须先用 `install_steps` 的 `write` 建好，
  否则 Docker 会把它当作目录创建。

### Gitea 直装的运行账号约定（踩坑记录）

Gitea 1.2x 起，`modules/setting` 的 `mustNotRunAsRoot` 会在**以 root 运行**时直接
`log.Fatal("Gitea is not supposed to be run as root...")` 退出；此外启动路径的
`mustInit(git.InitFull)` 要求系统存在 `git`。因此 `gitea.yaml` 必须以专用非 root
账号（默认 `git`）运行，并在 `install_steps` 里装 `git`——两者缺一，服务都会「装完就秒退」。

## 九、CI/CD 与发布

推送到 `main` 后由 GitHub Actions **自动编译并发布 Release**（版本号自增、tag 与 Release
同名）。完整说明见 [CI/CD 文档 CI_CD.md](CI_CD.md)。要点：

- 工作流：`.github/workflows/release.yml`；`push main` 自动发版，支持手动触发（可选递增方式）；
- 版本号：基于仓库最大 `v*` tag 自动递增（默认 patch），`-ldflags` 注入二进制；
- 产物：8 个平台二进制 + `checksums.txt`，命名必须与 `internal/update` 的在线更新约定一致
  （见 CI_CD.md「产物命名约定」）；
- 发布前必经 `go vet` + `go test`，失败不发布；
- 只改文档（`*.md` / `docs/` / `.workbuddy/`）不触发发布。

## 十、提交前自检清单

```bash
make fmt
make vet
make test
npm --prefix web run build && go build ./...
# 涉及部署产物时按目标架构交叉编译（或直接用 ./build.sh / .\build.ps1）
```

## 十一、典型开发流程示例（新增一个后端可配置项）

1. 在 `settings.go` 的 `editableSettings` 加键并写校验；
2. 业务代码通过 `store.GetSetting` 读取；
3. SettingsView 增加表单项；
4. 补/改 `*_test.go`；
5. `make test` → 前端 build → 交叉编译 → 实机部署验证 → 更新 docs。
