#!/usr/bin/env bash
#
# OneCloud Panel 一键安装脚本（面板 / Agent）
# 除二进制与 systemd 单元文件外不安装任何系统包；可重复执行（幂等）。
#
# 用法:
#   安装 Agent : curl -fsSL http://<面板>/install.sh | sudo bash -s -- agent \
#                    --server <面板地址，可含 http(s):// 前缀> --register-token <令牌> \
#                    [--insecure 面板为自签 HTTPS 时跳过证书校验]
#   安装面板   : curl -fsSL http://<主机>/install.sh | sudo bash -s -- panel \
#                    [--download-base http://<主机>/dl] [--listen :8000]
#   卸载       : curl -fsSL http://<主机>/install.sh | sudo bash -s -- uninstall [panel|agent] [--purge]
#
# 说明: 默认以 HTTP 部署；HTTPS 请在面板「设置 → 传输安全」中开启（支持自签/自定义证书与强制跳转）。
#
# 交互式向导: bash install.sh panel --interactive   (需在 TTY 中运行)
#            bash install.sh agent --interactive
#
# 可用环境变量覆盖: DOWNLOAD_BASE（二进制下载基址）、LISTEN、DATA_DIR
#
set -euo pipefail

BIN_NAME="onecloud-panel"
BIN_PATH="/usr/local/bin/${BIN_NAME}"

PANEL_UNIT="onecloud-panel.service"
AGENT_UNIT="onecloud-panel-agent.service"
PANEL_ENV_FILE="/etc/default/onecloud-panel"
AGENT_ENV_FILE="/etc/default/onecloud-panel-agent"
PANEL_DATA_DEFAULT="/var/lib/onecloud-panel"
AGENT_DATA_DEFAULT="/var/lib/onecloud-panel-agent"

log()  { printf '\033[1;32m[install]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

# ---- 前置检查 ----
[ "$(id -u)" -eq 0 ] || die "请使用 root 或 sudo 运行"
command -v systemctl >/dev/null 2>&1 || die "未找到 systemd，本脚本仅支持 systemd 系统"
command -v curl >/dev/null 2>&1 || die "未找到 curl"

# ---- 架构识别 ----
detect_arch() {
  case "$(uname -m)" in
    armv7l|armv6l)           echo "armv7" ;;
    aarch64|arm64)           echo "arm64" ;;
    x86_64|amd64)            echo "amd64" ;;
    i386|i486|i586|i686)     echo "386" ;;
    *) die "不支持的架构: $(uname -m)" ;;
  esac
}

download_binary() {
  local base="$1" arch tmp
  arch="$(detect_arch)"
  [ -n "$base" ] || die "未提供二进制下载基址（--download-base 或 DOWNLOAD_BASE）"
  base="${base%/}"
  local url="${base}/${BIN_NAME}-linux-${arch}"
  log "下载 ${BIN_NAME} (${arch}): ${url}"
  tmp="$(mktemp)"
  if ! curl -fL --retry 3 --connect-timeout 10 -o "$tmp" "$url"; then
    rm -f "$tmp"
    die "二进制下载失败：${url}"
  fi
  install -m 0755 "$tmp" "$BIN_PATH"
  rm -f "$tmp"
  log "已安装 ${BIN_PATH}"
}

write_file() { # path content
  local path="$1" content="$2"
  mkdir -p "$(dirname "$path")"
  printf '%s\n' "$content" > "$path"
  chmod 0644 "$path"
  log "已写入 ${path}"
}

enable_now() {
  local unit="$1"
  systemctl daemon-reload
  systemctl enable "$unit" >/dev/null 2>&1
  systemctl restart "$unit"
  systemctl --no-pager --full status "$unit" >/dev/null 2>&1 || true
  log "${unit} 已启动并设置开机自启"
}

# 公共安全加固段：面板/Agent 均为 root 运行的系统管理服务，
# 需保留对 /etc、/var、systemd、docker 的管理权限，故不启用 ProtectSystem=strict。
SECURITY_HARDENING="NoNewPrivileges=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
ProtectHostname=true
RestrictRealtime=true
SystemCallArchitectures=native"

# ---- 安装面板 ----
install_panel() {
  local listen="${LISTEN:-:8000}" data_dir="${DATA_DIR:-$PANEL_DATA_DEFAULT}"
  local download_base="${DOWNLOAD_BASE:-}"

  while [ $# -gt 0 ]; do
    case "$1" in
      --listen)        listen="$2"; shift 2 ;;
      --data-dir)      data_dir="$2"; shift 2 ;;
      --download-base) download_base="$2"; shift 2 ;;
      *) die "panel: 未知参数 $1（HTTPS 请安装后在面板设置中开启）" ;;
    esac
  done

  download_binary "$download_base"
  mkdir -p "$data_dir"
  chmod 0750 "$data_dir"

  local env_content="OCP_LISTEN=${listen}
OCP_DATA_DIR=${data_dir}
OCP_UNIT_NAME=${PANEL_UNIT}"

  write_file "$PANEL_ENV_FILE" "$env_content"

  write_file "/etc/systemd/system/${PANEL_UNIT}" "[Unit]
Description=OneCloud Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-${PANEL_ENV_FILE}
ExecStart=${BIN_PATH} panel
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
${SECURITY_HARDENING}

[Install]
WantedBy=multi-user.target"

  enable_now "$PANEL_UNIT"
  log "面板安装完成，监听 ${listen}"
}

# ---- 安装 Agent ----
install_agent() {
  local server="" register_token="" token="" insecure="0"
  local listen="${LISTEN:-:9000}" data_dir="${DATA_DIR:-$AGENT_DATA_DEFAULT}"
  local download_base="${DOWNLOAD_BASE:-}"

  while [ $# -gt 0 ]; do
    case "$1" in
      --server)         server="$2"; shift 2 ;;
      --register-token) register_token="$2"; shift 2 ;;
      --token)          token="$2"; shift 2 ;;
      --listen)         listen="$2"; shift 2 ;;
      --data-dir)       data_dir="$2"; shift 2 ;;
      --download-base)  download_base="$2"; shift 2 ;;
      --insecure)       insecure="1"; shift ;;
      *) die "agent: 未知参数 $1" ;;
    esac
  done

  [ -n "$server" ] || die "agent 需要 --server <面板地址>"
  { [ -n "$register_token" ] || [ -n "$token" ]; } || \
    die "agent 需要 --register-token（首次注册）或 --token（已注册）"

  # 归一化面板地址：缺协议时按 HTTP 处理（HTTPS 部署请显式带 https:// 前缀）
  case "$server" in
    http://*|https://*) ;;
    *) server="http://${server}" ;;
  esac

  # Agent 默认从面板主机下载二进制
  if [ -z "$download_base" ]; then
    download_base="${server%/}/dl"
  fi
  download_binary "$download_base"
  mkdir -p "$data_dir"
  chmod 0750 "$data_dir"

  write_file "$AGENT_ENV_FILE" "OCP_LISTEN=${listen}
OCP_DATA_DIR=${data_dir}
OCP_SERVER=${server}
OCP_REGISTER_TOKEN=${register_token}
OCP_TOKEN=${token}
OCP_INSECURE_TLS=${insecure}"
  # 含注册/长期令牌，收紧为仅 root 可读
  chmod 0600 "$AGENT_ENV_FILE"

  write_file "/etc/systemd/system/${AGENT_UNIT}" "[Unit]
Description=OneCloud Panel Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-${AGENT_ENV_FILE}
ExecStart=${BIN_PATH} agent
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
${SECURITY_HARDENING}

[Install]
WantedBy=multi-user.target"

  enable_now "$AGENT_UNIT"
  log "Agent 安装完成，正在注册到 ${server}"
}

# ---- 卸载 ----
do_uninstall() {
  local target="all" purge="0"
  while [ $# -gt 0 ]; do
    case "$1" in
      panel|agent) target="$1"; shift ;;
      --purge)     purge="1"; shift ;;
      *) die "uninstall: 未知参数 $1" ;;
    esac
  done

  # 从 env 文件读取安装时实际使用的数据目录（兼容自定义 --data-dir）
  local data_dir_from_env
  data_dir_from_env() {
    local env_file="$1" def="$2" d
    d="$(sed -n 's/^OCP_DATA_DIR=//p' "$env_file" 2>/dev/null | tail -n1)"
    if [ -n "$d" ]; then printf '%s' "$d"; else printf '%s' "$def"; fi
  }

  local remove_one
  remove_one() {
    local unit="$1" env_file="$2" data_dir="$3"
    # 先捕获完整列表再 grep：grep -q 提前关闭管道会让 systemctl 收到 SIGPIPE，
    # 在 pipefail 下误判为单元不存在，因此不可直接管道。
    local unitfiles
    unitfiles="$(systemctl list-unit-files 2>/dev/null)"
    if printf '%s' "$unitfiles" | grep -q "^${unit}"; then
      systemctl disable --now "$unit" >/dev/null 2>&1 || true
      log "已停止并禁用 ${unit}"
    fi
    rm -f "/etc/systemd/system/${unit}" "$env_file"
    if [ "$purge" = "1" ]; then
      rm -rf "$data_dir"
      log "已清除数据目录 ${data_dir}"
    else
      log "数据目录保留：${data_dir}（加 --purge 可一并删除）"
    fi
  }

  local panel_data agent_data
  panel_data="$(data_dir_from_env "$PANEL_ENV_FILE" "$PANEL_DATA_DEFAULT")"
  agent_data="$(data_dir_from_env "$AGENT_ENV_FILE" "$AGENT_DATA_DEFAULT")"

  case "$target" in
    panel) remove_one "$PANEL_UNIT" "$PANEL_ENV_FILE" "$panel_data" ;;
    agent) remove_one "$AGENT_UNIT" "$AGENT_ENV_FILE" "$agent_data" ;;
    all)
      remove_one "$PANEL_UNIT" "$PANEL_ENV_FILE" "$panel_data"
      remove_one "$AGENT_UNIT" "$AGENT_ENV_FILE" "$agent_data" ;;
  esac
  systemctl daemon-reload || true
  # 仅当两个单元都不存在时才移除二进制。同样先捕获完整列表再 grep，
  # 避免 grep -q 提前退出造成 SIGPIPE + pipefail 误删二进制。
  local remaining_units
  remaining_units="$(systemctl list-unit-files 2>/dev/null)"
  if ! printf '%s' "$remaining_units" | grep -q "onecloud-panel"; then
    rm -f "$BIN_PATH"
    log "已移除 ${BIN_PATH}"
  fi
  log "卸载完成"
}

# ---- 交互式配置向导 ----
# 用法: bash install.sh panel --interactive
# 仅在 --interactive 且 stdin 为终端时启用；否则走默认/命令行参数，保持非交互幂等。
interactive_prompt() { # var default prompt
  local var="$1" default="$2" prompt="$3" val
  if [ -t 0 ] && [ -t 1 ]; then
    printf '%s [%s]: ' "$prompt" "$default"
    read -r val
    if [ -z "$val" ]; then val="$default"; fi
    eval "$var=\"\$val\""
  else
    eval "$var=\"\$default\""
  fi
}

run_interactive_panel() {
  local listen data_dir download_base
  log "=== OneCloud Panel 安装向导 ==="
  interactive_prompt listen ":8080" "面板监听地址"
  interactive_prompt data_dir "$PANEL_DATA_DEFAULT" "数据目录"
  interactive_prompt download_base "" "二进制下载基址（留空则必须由 --download-base 提供）"
  log "确认配置：listen=${listen} data_dir=${data_dir}"
  log "提示：HTTPS 可在安装完成后于面板设置中开启"
  install_panel --listen "$listen" --data-dir "$data_dir" ${download_base:+--download-base "$download_base"}
}

run_interactive_agent() {
  local server register_token listen data_dir
  log "=== OneCloud Agent 安装向导 ==="
  interactive_prompt server "" "面板地址 (host:port)"
  interactive_prompt register_token "" "注册令牌"
  interactive_prompt listen ":9000" "Agent 监听地址"
  interactive_prompt data_dir "$AGENT_DATA_DEFAULT" "数据目录"
  install_agent --server "$server" --register-token "$register_token" \
    --listen "$listen" --data-dir "$data_dir"
}

# ---- 入口 ----
ACTION="${1:-}"
[ -n "$ACTION" ] || die "缺少动作：panel | agent | uninstall"
shift || true

# 提取 --interactive（在其余参数前处理）
INTERACTIVE="0"
rest_args=()
for arg in "$@"; do
  case "$arg" in
    --interactive) INTERACTIVE="1" ;;
    *) rest_args+=("$arg") ;;
  esac
done

case "$ACTION" in
  panel)
    if [ "$INTERACTIVE" = "1" ]; then
      run_interactive_panel
    else
      install_panel "${rest_args[@]}"
    fi
    ;;
  agent)
    if [ "$INTERACTIVE" = "1" ]; then
      run_interactive_agent
    else
      install_agent "${rest_args[@]}"
    fi
    ;;
  uninstall) do_uninstall "${rest_args[@]}" ;;
  *) die "未知动作：${ACTION}（支持 panel / agent / uninstall）" ;;
esac
