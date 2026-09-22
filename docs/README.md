# OneCloud Panel 项目文档

OneCloud Panel 是一个自托管的轻量级集群管理面板，面向刷入 Armbian 的玩客云
（Amlogic S805，armv7l，约 1 GB 内存）及同类低功耗 Linux 小主机，
把多台设备组成混合架构集群，并在其上一键部署常用自托管应用。

## 核心特性

- **零运行时依赖**：单文件静态二进制（CGO_ENABLED=0），面板与 Agent 同一二进制，按子命令区分
- **默认 HTTP、可选 HTTPS**：在面板设置中开启自签或自定义证书；HTTP/HTTPS 同端口共存，可强制跳转
- **应用商店**：26 个内置配方，支持原生 systemd 直装与 Docker 容器两种方式
- **集群管理**：注册令牌接入节点，Agent 心跳上报，在线状态、资源指标一目了然
- **权限体系**：13 个权限点，预置管理员 / 操作员 / 只读用户三种角色，可自定义
- **安全基线**：argon2id 密码哈希、登录与重置限流、会话管理、全量审计日志
- **密码自助重置**：登录页凭重置码（15 分钟有效）自助重置，SMTP 邮件与服务日志双通道

## 文档目录

| 文档 | 内容 |
|---|---|
| [部署文档 DEPLOYMENT.md](DEPLOYMENT.md) | 系统要求、构建、一键安装、初始化、接入节点、启用 HTTPS、卸载 |
| [运维文档 OPERATIONS.md](OPERATIONS.md) | 服务管理、日志、备份恢复、升级、Docker、安全加固、常见问题 |
| [架构设计 ARCHITECTURE.md](ARCHITECTURE.md) | 分层架构、模块职责、数据模型、关键设计决策 |
| [用户手册 USER_GUIDE.md](USER_GUIDE.md) | 各页面功能与典型操作流程（面向使用者） |
| [API 文档 API.md](API.md) | REST API 鉴权、端点清单、请求/响应示例 |
| [开发文档 DEVELOPMENT.md](DEVELOPMENT.md) | 目录结构、开发环境、测试、交叉编译、配方开发规范 |

## 快速开始

```bash
# 在目标机器上安装面板（默认 HTTP）
curl -fsSL http://<面板IP>:8080/install.sh | sudo bash -s -- panel \
  --download-base http://<面板IP>:8080/dl --listen :8080

# 浏览器打开，按向导创建管理员（面板无默认账号）
# 需要 HTTPS 时：设置 → 传输安全 → 启用 → 重启
```

更完整的「第一次系统部署」五分钟上手指南见仓库根目录
[README.md](https://github.com/Bet5521/onecloud-panel#第一次部署5-分钟快速开始)。

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
