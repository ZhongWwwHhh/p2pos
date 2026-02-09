#!/usr/bin/env pwsh

<#
.SYNOPSIS
Build script with version injection for P2POS
.EXAMPLE
.\build.ps1 -Output p2pos
#>

param(
    [string]$Output = "p2pos"
)

$ErrorActionPreference = "Stop"

# Get version from current date/time in yyyymmdd-hhmm format
$Version = (Get-Date).ToString("yyyyMMdd-HHmm")

# Get OS and Architecture
$OS = $env:GOOS
if (-not $OS) {
    $OS = if ($PSVersionTable.Platform -eq "Win32NT" -or -not $PSVersionTable.Platform) { "windows" } else { "linux" }
}

$ARCH = $env:GOARCH
if (-not $ARCH) {
    $ARCH = "amd64"
}

# Add .exe extension for Windows
if ($OS -eq "windows") {
    $Output = "$Output.exe"
}

Write-Host "Building P2POS..." -ForegroundColor Yellow
Write-Host "Version: $Version" -ForegroundColor Green
Write-Host "OS: $OS" -ForegroundColor Green
Write-Host "Arch: $ARCH" -ForegroundColor Green
Write-Host "Output: $Output" -ForegroundColor Green

# Build with version injection via ldflags
go build `
    -ldflags "-X p2pos/internal/update.Version=$Version" `
    -o "$Output" `
    main.go

if ($LASTEXITCODE -eq 0) {
    Write-Host "Build complete: $Output" -ForegroundColor Green
} else {
    Write-Host "Build failed!" -ForegroundColor Red
    exit 1
}
