# CI/CD：自动编译与发布

本文档描述 OneCloud Panel 的 GitHub Actions 工作流：**推送代码即自动编译，并把产物发布到
GitHub Release**，供用户下载与面板「在线更新」使用。

工作流文件：[`.github/workflows/release.yml`](../.github/workflows/release.yml)

---

## 一、触发方式

| 触发 | 行为 |
|---|---|
| **push 到 `main`** | 自动编译全部平台 → 版本号自增（默认 **patch**）→ 打新 tag → 创建/更新 Release 并上传产物 |
| **手动触发**（Actions → Build & Release → Run workflow） | 同上，但可在下拉框选择递增方式：`patch` / `minor` / `major` / `current` |
| **pull_request 到 `main`** | 仅编译 + `go vet` + 单元测试做校验，**不发布**（不创建 tag / Release） |

> **不触发的情况**：仅修改 `*.md`、`docs/**`、`.workbuddy/**`、`LICENSE` 的推送不会触发
> 流程（避免产生无意义的版本）。如需「每次 push 都发版」，删除工作流里 `on.push.paths-ignore`
> 一段即可；如需跳过某一次提交，在 commit message 中加入 `[skip ci]`。

---

## 二、版本号与 tag 规则

版本号基于**当前仓库中最大的 `v*` tag** 自动递增，tag 与 Release 使用同一个值。

| 递增方式 | 由 `v2.2.0` 得到 | 说明 |
|---|---|---|
| `patch`（默认） | `v2.2.1` | 缺陷修复、小改动 |
| `minor` | `v2.3.0` | 新增功能 |
| `major` | `v3.0.0` | 不兼容变更 |
| `current` | `v2.2.0` | 不递增（重发当前版本，如修复构建产物） |

- 取 tag 的命令：`git tag -l 'v*' --sort=-v:refname | head -n1`（按版本号排序取最大）。
- 若仓库尚无任何 `v*` tag，则基准为 `v0.0.0`。
- 工作流带 `concurrency` 串行控制，避免并发运行导致 tag 冲突。
- 该版本号通过 `-ldflags` 注入到二进制：`onecloud-panel version` 可查看
  `Version / Commit / BuildDate`。

---

## 三、工作流步骤

### Job 1：`build`（编译与校验）

1. **检出代码**（`fetch-depth: 0`，需要完整历史与 tag 才能算版本号）；
2. **安装 Go**（版本读自 `go.mod`）+ **Node.js 22**（启用依赖缓存）；
3. **构建前端**（`npm ci && npm run build`，产物写入 `internal/web/assets/`，由 `go:embed` 嵌入）；
4. **`go vet ./...`** + **`go test ./... -p 1`**（测试不通过则中止，不会发布）；
5. **计算版本号**（输出 `version` / `tag` / `commit` / `build_date`）；
6. **交叉编译全部平台**（`CGO_ENABLED=0`，`-trimpath`）；
7. **生成 `checksums.txt`**（`sha256sum`）；
8. **上传产物**（artifact `onecloud-panel-dist`）；
9. **写入任务摘要**（含产物清单与大小）。

### Job 2：`release`（发布）

1. **下载产物**；
2. **校验产物完整性**（`sha256sum -c checksums.txt`）；
3. **创建 / 更新 Release**：`tag_name = <计算出的 tag>`，标题同 tag，自动生成变更说明，
   上传全部二进制与 `checksums.txt`。

> `pull_request` 事件不会执行本 job。仅 `push` 到 `main` 与 `workflow_dispatch` 会发布。

---

## 四、产物命名约定（重要）

产物名与面板内置的**在线更新器**（`internal/update`）严格一致，否则在线更新会找不到资产：

| 文件 | 目标平台 |
|---|---|
| `onecloud-panel-linux-armv7` | Linux / armv7（玩客云等 Armbian 32 位） |
| `onecloud-panel-linux-arm64` | Linux / aarch64 |
| `onecloud-panel-linux-amd64` | Linux / x86_64 |
| `onecloud-panel-linux-386` | Linux / i386 |
| `onecloud-panel-windows-amd64.exe` | Windows x86_64 |
| `onecloud-panel-windows-arm64.exe` | Windows ARM64 |
| `onecloud-panel-darwin-amd64` | macOS Intel |
| `onecloud-panel-darwin-arm64` | macOS Apple Silicon |
| `checksums.txt` | 校验和（每行 `<sha256>  <文件名>`） |

面板在线更新（**设置 → 版本更新**）逻辑：

- 查询 `https://api.github.com/repos/Bet5521/onecloud-panel/releases/latest`；
- 按当前 `GOOS/GOARCH` 映射到上表文件名（`arm` → `armv7`）；
- 下载后校验 sha256：优先用 Release API 返回的 `digest` 字段，缺失时回退下载
  `checksums.txt`（格式必须为 `<64位hex>  <文件名>`，两个空格分隔）。

> 因此：**改产物命名或改 `checksums.txt` 格式会破坏在线更新**，务必与上表保持一致。

---

## 五、权限与安全

- 工作流声明 `permissions: contents: write`，使用内置 `GITHUB_TOKEN` 创建 tag / Release；
- 由 `GITHUB_TOKEN` 推送的 tag 不会再次触发工作流，不会形成循环；
- 发布前必过 `go vet` + 单元测试，测试失败即不发布；
- 发布前对产物做 `sha256sum -c` 自校验，避免上传损坏文件。

---

## 六、本地预览 / 手动发版

需要在本地复现 CI 的编译结果：

```bash
# Linux / macOS
./build.sh -v 2.2.1          # 构建前端 + 交叉编译产物到 dist/

# Windows PowerShell
.\build.ps1 -Version 2.2.1
```

手工发版（不走 Actions）也可以，只需保证产物命名与上表一致并附带 `checksums.txt`，
然后创建 Release 即可。

---

## 七、常见问题

| 现象 | 排查方向 |
|---|---|
| 未触发工作流 | 是否只改了文档类文件（被 `paths-ignore` 跳过）？commit 是否含 `[skip ci]`？默认分支是否为 `main`？ |
| 前端构建失败 | `web/package-lock.json` 是否与 `package.json` 一致（CI 用 `npm ci`，要求锁文件同步）？ |
| Go 版本不符 | `go.mod` 的 `go` 指令会被 `setup-go` 读取，确保该版本可在 Actions 安装 |
| Release 无产物 | 看 `release` job 的「校验产物完整性」步骤，通常为 `checksums.txt` 与实际文件不一致 |
| 在线更新提示无产物 | 核对 Release 资产名是否与上表完全一致（含 `.exe` 后缀） |
