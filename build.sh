#!/usr/bin/env bash
# 一键编译脚本（Linux / macOS）
# 用法：
#   ./build.sh                     # 构建前端 + 三平台产物
#   ./build.sh -v v2.1.0           # 指定版本号（默认 dev）
#   ./build.sh -s                  # 跳过前端构建，直接编译 Go（需已有前端产物）
# 产物输出到 dist/：
#   onecloud-panel-linux-armv7
#   onecloud-panel-linux-arm64
#   onecloud-panel-windows-amd64.exe
set -euo pipefail
cd "$(dirname "$0")"

VERSION="dev"
SKIP_FRONTEND=0

usage() { sed -n '2,11p' "$0"; exit 0; }

while getopts "v:sh" opt; do
    case "$opt" in
        v) VERSION="$OPTARG" ;;
        s) SKIP_FRONTEND=1 ;;
        h) usage ;;
        *) usage ;;
    esac
done

# ---- 前置检查 ----
if ! command -v go >/dev/null 2>&1; then
    echo "[错误] 未找到 go，请先安装 Go 并加入 PATH。" >&2
    exit 1
fi

# ---- 前端构建（产物输出到 internal/web/assets，由 go:embed 嵌入） ----
if [ "$SKIP_FRONTEND" -eq 0 ]; then
    if command -v npm >/dev/null 2>&1; then
        echo "==> 构建前端 (web/) ..."
        (
            cd web
            npm install --no-audit --no-fund
            npm run build
        )
    elif [ -f "internal/web/assets/index.html" ]; then
        echo "[警告] 未找到 npm，沿用 internal/web/assets 现有前端产物。"
    else
        echo "[错误] 未找到 npm，且 internal/web/assets 无前端产物，无法编译。" >&2
        exit 1
    fi
else
    if [ ! -f "internal/web/assets/index.html" ]; then
        echo "[错误] internal/web/assets 无前端产物，不能跳过前端构建。" >&2
        exit 1
    fi
fi

# ---- 版本信息注入 ----
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
-X onecloud-panel/internal/version.Version=$VERSION \
-X onecloud-panel/internal/version.Commit=$COMMIT \
-X onecloud-panel/internal/version.BuildDate=$BUILD_DATE"
echo "==> 版本：$VERSION (commit: $COMMIT, built: $BUILD_DATE)"

# ---- 编译三平台（与 Makefile build 目标一致） ----
mkdir -p dist
export CGO_ENABLED=0

echo "==> 编译 linux-armv7 ..."
GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$LDFLAGS" \
    -o dist/onecloud-panel-linux-armv7 ./cmd/onecloud-panel

echo "==> 编译 linux-arm64 ..."
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" \
    -o dist/onecloud-panel-linux-arm64 ./cmd/onecloud-panel

echo "==> 编译 windows-amd64 ..."
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" \
    -o dist/onecloud-panel-windows-amd64.exe ./cmd/onecloud-panel

echo "==> 完成，产物："
ls -lh dist/ | awk 'NR > 1 { printf "  %-45s %s\n", $NF, $5 }'
