# OneCloud Panel

> 自托管的轻量级集群管理面板，把刷入 Armbian 的玩客云（Amlogic S805，armv7l，约 1 GB 内存）及同类低功耗 Linux 小主机，组成混合架构集群，并在其上一键部署常用自托管应用。

---

## 核心特性

- **零运行时依赖**：单文件静态二进制（`CGO_ENABLED=0`），面板与 Agent 共用同一二进制，按子命令区分，不依赖 glibc 或运行时库。
- **默认 HTTP、可选 HTTPS**：在「设置 → 传输安全」中开启自签或自定义证书；HTTP / HTTPS 共用同一端口，由连接首字节自动嗅探，开启 HTTPS 不改变端口、不影响节点接入。
- **应用商店**：**26 个内置配方**，支持原生 `systemd` 直装与 Docker 容器两种方式。
- **集群管理**：注册令牌接入节点，Agent 心跳上报，在线状态、资源指标一目了然。
- **权限体系**：13 个权限点，预置管理员 / 操作员 / 只读用户三种角色，可自定义。
- **安全基线**：argon2id 密码哈希、登录与重置限流、会话管理、全量审计日志。
- **密码自助重置**：登录页凭重置码（15 分钟有效）自助重置，SMTP 邮件与服务日志双通道。

---

## 第一次部署（5 分钟快速开始）

> 目标：在一台 Linux 机器上跑起面板、创建管理员、装好第一个应用。下面以一台 `x86_64` 机器、内网地址 `192.168.1.10` 为例，其他架构把二进制名替换即可（见文末「下载」）。

### 前置条件

- 带 `systemd` 的 Linux（Armbian / Debian / Ubuntu 等），架构 `armv7l` / `aarch64` / `x86_64` / `i386` 均可
- 内存 ≥ 512 MB（推荐 ≥ 1 GB），磁盘 ≥ 200 MB 可用空间（不含应用数据）
- 已安装 `curl`
- 以 `root` 执行以下命令

### 第 1 步：获取二进制

从 [GitHub Releases](https://github.com/Bet5521/onecloud-panel/releases) 下载对应架构的二进制：

```bash
# x86_64
curl -fsSL -o /usr/local/bin/onecloud-panel \
  https://github.com/Bet5521/onecloud-panel/releases/download/v1.99.9/onecloud-panel-linux-amd64

# 玩客云 / armv7l
# curl -fsSL -o /usr/local/bin/onecloud-panel \
#   https://github.com/Bet5521/onecloud-panel/releases/download/v1.99.9/onecloud-panel-linux-armv7

chmod +x /usr/local/bin/onecloud-panel
```

<details>
<summary>想自己编译？（需 Go 1.27+）</summary>

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X onecloud-panel/internal/version.Version=1.99.9" \
  -o dist/onecloud-panel-linux-amd64 ./cmd/onecloud-panel
```

更多架构见 `docs/DEPLOYMENT.md` 与 `Makefile`（`make build` 一次产出 4 个 Linux 架构）。
</details>

### 第 2 步：启动面板

最快验证方式——直接前台启动（日志直接可见）：

```bash
onecloud-panel panel --listen :8080 --data-dir /var/lib/onecloud-panel
```

生产环境建议用 `systemd` 托管（开机自启、崩溃重启）：

```bash
# 把二进制放进发布目录，再用面板内置安装脚本注册为系统服务
mkdir -p /var/lib/onecloud-panel/releases
cp /usr/local/bin/onecloud-panel /var/lib/onecloud-panel/releases/onecloud-panel-linux-amd64

# 在【已运行】的面板机器上执行（首次部署也可手动写 unit，见 DEPLOYMENT.md 4.2 方式 C）
curl -fsSL http://192.168.1.10:8080/install.sh | sudo bash -s -- panel \
  --download-base http://192.168.1.10:8080/dl --listen :8080
```

### 第 3 步：初始化管理员

> **面板不内置任何默认账号**，首次访问必须由初始化向导创建管理员。

- **方式 A · Web（推荐）**：浏览器打开 `http://192.168.1.10:8080`，自动跳转到初始化页，设置用户名（3–32 字符）与密码（≥ 8 字符），提交后自动登录。
- **方式 B · API（自动化 / 无界面）**：

```bash
curl -sS -X POST http://192.168.1.10:8080/api/setup \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"YourStrongP@ssw0rd"}'
# 返回 {"status":"ok"} 即创建成功，密码以 argon2id 哈希存储
```

### 第 4 步（可选）：接入第一个节点

把其他小主机纳管为集群节点，统一监控与下发应用：

1. 面板 Web UI → **节点** → 生成注册令牌（可设过期时间与最大使用次数）。
2. 在节点机器上执行：

```bash
curl -fsSL http://192.168.1.10:8080/install.sh | sudo bash -s -- agent \
  --server 192.168.1.10:8080 \
  --register-token <令牌>
```

Agent 每 30 秒心跳上报，注册成功后即可在面板看到节点在线状态与资源指标。

### 第 5 步：部署第一个应用

面板 Web UI → **应用商店** → 选择应用（如 `Vaultwarden` / `qBittorrent` / `Alist`）→ 按向导填写端口 / 数据目录 → 安装。安装进度与日志实时可见，完成后给出访问地址。

### 第 6 步（可选）：开启 HTTPS

纯 HTTP 已可直接使用。需要加密时：**设置 → 传输安全 → 启用 HTTPS → 应用并重启**。默认生成自签证书（SAN 自动包含本机所有 IP / 主机名），也可粘贴自有证书（如 Let's Encrypt）。开启后 HTTP 与 HTTPS 共用 `:8080`，不影响节点接入。

> 忘记管理员密码？登录页「忘记密码？」凭重置码自助重置；重置码始终同时输出到面板日志（`journalctl -u onecloud-panel -n 50`）。

---

## 下载

GitHub Releases（`v1.99.9`）提供以下二进制：

| 文件 | 适用架构 / 系统 |
|---|---|
| `onecloud-panel-linux-armv7` | 玩客云 / armv7l（Armbian 32 位） |
| `onecloud-panel-linux-arm64` | aarch64 |
| `onecloud-panel-linux-amd64` | x86_64 |
| `onecloud-panel-linux-386` | i386 |
| `onecloud-panel-windows-amd64.exe` | Windows x86_64（本地调试 / 开发） |
| `onecloud-panel-windows-arm64.exe` | Windows ARM64 |
| `onecloud-panel-darwin-amd64` | macOS Intel |
| `onecloud-panel-darwin-arm64` | macOS Apple Silicon |

面板与 Agent 是同一二进制，靠 `panel` / `agent` 子命令区分。

---

## 文档

| 文档 | 内容 |
|---|---|
| [部署文档 DEPLOYMENT.md](docs/DEPLOYMENT.md) | 系统要求、构建、一键安装、初始化、接入节点、启用 HTTPS、卸载 |
| [运维文档 OPERATIONS.md](docs/OPERATIONS.md) | 服务管理、日志、备份恢复、升级、Docker、安全加固、常见问题 |
| [架构设计 ARCHITECTURE.md](docs/ARCHITECTURE.md) | 分层架构、模块职责、数据模型、关键设计决策 |
| [用户手册 USER_GUIDE.md](docs/USER_GUIDE.md) | 各页面功能与典型操作流程（面向使用者） |
| [API 文档 API.md](docs/API.md) | REST API 鉴权、端点清单、请求 / 响应示例 |
| [开发文档 DEVELOPMENT.md](docs/DEVELOPMENT.md) | 目录结构、开发环境、测试、交叉编译、配方开发规范 |
| [项目文档索引 docs/README.md](docs/README.md) | 文档总览与内置应用清单 |

---

## 内置应用（26 款）

| 应用 | 分类 | 部署方式 |
|---|---|---|
| AdGuard Home | 网络 | 原生 / Docker |
| mihomo (Clash Meta) | 网络 | 原生 / Docker |
| WireGuard | 网络 | 原生 |
| Cloudflared | 网络 | 原生 / Docker |
| MiGPT | 网络 | Docker |
| Nginx Proxy Manager（NPM） | 网络 | Docker |
| Nginx | 网络 | Docker |
| Syncthing | 文件 | 原生 / Docker |
| 微力同步 (verysync) | 文件 | 原生 / Docker |
| Alist | 文件 | Docker |
| OpenList | 文件 | Docker |
| Gitea | 开发 | 原生 / Docker |
| aria2 (+AriaNg) | 下载 | Docker |
| qBittorrent | 下载 | Docker |
| Transmission | 下载 | 原生 |
| Memos | 效率 | Docker |
| 青龙面板 | 效率 | Docker |
| Typecho | 效率 | Docker |
| Piwigo | 多媒体 | Docker |
| Jellyfin | 多媒体 | Docker |
| XiaoMusic | 多媒体 | Docker |
| Home Assistant | 智能家居 | Docker |
| CUPS 打印服务 | 外设 | 原生 / Docker |
| CUPS Web 打印 | 外设 | Docker |
| One-KVM | 运维 | Docker |
| Vaultwarden | 安全 | Docker |

---

## 许可

本项目以 MIT 许可证开源，详见仓库 `LICENSE` 文件。
