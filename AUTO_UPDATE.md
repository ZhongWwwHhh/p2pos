# Auto Update Guide

## Overview

P2POS 支持自动更新功能，可以自动检测 GitHub Release 中的最新版本并下载更新。

## Version Format

版本号采用 `yyyymmdd-hhmm` 格式，例如 `20260209-1526`（2026年2月9日15:26）。

## How to Build with Version Injection

### Windows (PowerShell)

```powershell
# 使用 build.ps1 脚本
.\build.ps1 -Output p2pos

# 或手动指定版本
$VERSION = (Get-Date).ToString("yyyyMMdd-HHmm")
go build -ldflags "-X p2pos/internal/update.Version=$VERSION" -o p2pos.exe main.go
```

### Linux/macOS (Bash)

```bash
# 使用 build.sh 脚本
chmod +x build.sh
./build.sh p2pos

# 或手动指定版本
VERSION=$(date +%Y%m%d-%H%M)
go build -ldflags "-X p2pos/internal/update.Version=$VERSION" -o p2pos main.go
```

## How Auto Update Works

1. **启动时**：程序打印当前版本号
   ```
   [APP] P2POS version: 20260209-1525
   [APP] Starting auto-update checker...
   ```

2. **定期检查**：每 6 小时检查一次 GitHub Release
   ```
   [UPDATE] Checking for updates...
   ```

3. **发现更新**：如果有新版本
   ```
   [UPDATE] New version available: v20260209-1526 (current: 20260209-1525)
   [UPDATE] Downloading from: https://api.github.com/repos/ZhongWwwHhh/Ops-System/releases/download/v20260209-1526/p2pos-linux
   [UPDATE] Downloading new version...
   [UPDATE] Successfully updated to version v20260209-1526
   [UPDATE] Please restart the application to apply the update
   ```

## GitHub Actions Workflow

当推送到 `main` 或 `dev` 分支时，GitHub Actions 会自动：

1. **编译**：为 Windows 和 Linux 分别编译
2. **注入版本号**：使用当前时间的 `yyyymmdd-hhmm` 格式
3. **创建 Release**：自动创建 Release 并上传二进制文件

### Release Artifacts

- `p2pos-linux` - Linux x86_64
- `p2pos-win.exe` - Windows x86_64

## Update Checking Interval

当前设置为 **6 小时**检查一次。如需更改，修改 `main.go` 中的：

```go
update.StartUpdateChecker("ZhongWwwHhh", "Ops-System", 6*time.Hour)
```

## Manual Update Check

可以在 GitHub Actions workflow 中手动触发（使用 `workflow_dispatch`）：

```bash
gh workflow run build.yml
```

## Version Comparison Logic

- 版本号仅作字符串比较
- 仅当新版本 > 当前版本时才会更新
- 版本号的 `v` 前缀会自动忽略
