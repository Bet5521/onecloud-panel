# 一键编译脚本（Windows）
# 用法：
#   .\build.ps1                          # 构建前端 + 三平台产物
#   .\build.ps1 -Version v2.1.0          # 指定版本号（默认 dev）
#   .\build.ps1 -SkipFrontend            # 跳过前端构建，直接编译 Go（需已有前端产物）
# 产物输出到 dist/：
#   onecloud-panel-linux-armv7
#   onecloud-panel-linux-arm64
#   onecloud-panel-windows-amd64.exe
param(
    [string]$Version = "dev",
    [switch]$SkipFrontend
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

# ---- 前置检查 ----
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "[错误] 未找到 go，请先安装 Go 并加入 PATH。" -ForegroundColor Red
    exit 1
}

# ---- 前端构建（产物输出到 internal/web/assets，由 go:embed 嵌入） ----
if (-not $SkipFrontend) {
    if (Get-Command npm -ErrorAction SilentlyContinue) {
        Write-Host "==> 构建前端 (web/) ..." -ForegroundColor Cyan
        Push-Location web
        try {
            npm install --no-audit --no-fund
            if ($LASTEXITCODE -ne 0) { throw "npm install 失败" }
            npm run build
            if ($LASTEXITCODE -ne 0) { throw "npm run build 失败" }
        } finally { Pop-Location }
    } elseif (Test-Path "internal\web\assets\index.html") {
        Write-Host "[警告] 未找到 npm，沿用 internal/web/assets 现有前端产物。" -ForegroundColor Yellow
    } else {
        Write-Host "[错误] 未找到 npm，且 internal/web/assets 无前端产物，无法编译。" -ForegroundColor Red
        exit 1
    }
} else {
    if (-not (Test-Path "internal\web\assets\index.html")) {
        Write-Host "[错误] internal/web/assets 无前端产物，不能 -SkipFrontend。" -ForegroundColor Red
        exit 1
    }
}

# ---- 版本信息注入 ----
$gitPath = (Get-Command git -ErrorAction SilentlyContinue).Source
if (-not $gitPath) {
    # 探测 GitHub Desktop 自带的 git
    $ghGit = Get-ChildItem "$env:LOCALAPPDATA\GitHubDesktop\app-*\resources\app\git\cmd\git.exe" -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1
    if ($ghGit) { $gitPath = $ghGit.FullName }
}
$commit = "unknown"
if ($gitPath) {
    $commit = (& $gitPath rev-parse --short HEAD 2>$null)
    if (-not $commit) { $commit = "unknown" }
}
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w " +
    "-X onecloud-panel/internal/version.Version=$Version " +
    "-X onecloud-panel/internal/version.Commit=$commit " +
    "-X onecloud-panel/internal/version.BuildDate=$buildDate"
Write-Host "==> 版本：$Version (commit: $commit, built: $buildDate)" -ForegroundColor Cyan

# ---- 编译三平台（与 Makefile build 目标一致） ----
New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:CGO_ENABLED = "0"

Write-Host "==> 编译 linux-armv7 ..." -ForegroundColor Cyan
$env:GOOS = "linux"; $env:GOARCH = "arm"; $env:GOARM = "7"
go build -trimpath -ldflags $ldflags -o dist\onecloud-panel-linux-armv7 .\cmd\onecloud-panel
if ($LASTEXITCODE -ne 0) { Write-Host "[错误] 编译 linux-armv7 失败。" -ForegroundColor Red; exit 1 }
Remove-Item Env:\GOARM -ErrorAction SilentlyContinue

Write-Host "==> 编译 linux-arm64 ..." -ForegroundColor Cyan
$env:GOOS = "linux"; $env:GOARCH = "arm64"
go build -trimpath -ldflags $ldflags -o dist\onecloud-panel-linux-arm64 .\cmd\onecloud-panel
if ($LASTEXITCODE -ne 0) { Write-Host "[错误] 编译 linux-arm64 失败。" -ForegroundColor Red; exit 1 }

Write-Host "==> 编译 windows-amd64 ..." -ForegroundColor Cyan
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -trimpath -ldflags $ldflags -o dist\onecloud-panel-windows-amd64.exe .\cmd\onecloud-panel
if ($LASTEXITCODE -ne 0) { Write-Host "[错误] 编译 windows-amd64 失败。" -ForegroundColor Red; exit 1 }

Remove-Item Env:\GOOS, Env:\GOARCH, Env:\CGO_ENABLED -ErrorAction SilentlyContinue

Write-Host "==> 完成，产物：" -ForegroundColor Green
Get-ChildItem dist | Format-Table Name, @{L = "大小(MB)"; E = { "{0:N1}" -f ($_.Length / 1MB) }} -AutoSize
