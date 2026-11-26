param(
    [switch]$DisableProfileFunctions,
    [switch]$LaunchTray
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$binSource = Join-Path $projectRoot 'build\bin'
$targetBinDir = Join-Path $env:USERPROFILE '.local\bin'

if (-not (Test-Path (Join-Path $binSource 'agy-switch.exe')) -or -not (Test-Path (Join-Path $binSource 'agyc.exe'))) {
    Write-Error "未找到编译产物，请先运行 .\scripts\build.ps1"
    exit 1
}

New-Item -ItemType Directory -Force -Path $targetBinDir | Out-Null
Copy-Item -Force (Join-Path $binSource 'agy-switch.exe') (Join-Path $targetBinDir 'agy-switch.exe')
Copy-Item -Force (Join-Path $binSource 'agyc.exe') (Join-Path $targetBinDir 'agyc.exe')
Write-Host "✓ 已安装 agy-switch.exe 与 agyc.exe 到 $targetBinDir" -ForegroundColor Green

if ($DisableProfileFunctions) {
    $sharedProfile = Join-Path $env:USERPROFILE 'OneDrive\code\backup\agy\Microsoft.PowerShell_profile.ps1'
    if (-not (Test-Path $sharedProfile)) {
        $sharedProfile = $PROFILE
    }
    if ($sharedProfile -and (Test-Path $sharedProfile)) {
        $bak = "$sharedProfile.bak.$(Get-Date -Format 'yyyyMMddHHmmss')"
        Copy-Item -Force $sharedProfile $bak
        Write-Host "✓ 已备份旧 PowerShell Profile 到: $bak" -ForegroundColor Yellow
        Write-Host "  提示: 请在新终端中移除或注释旧 function agyc，使 PATH 中的 agyc.exe 优先生效。" -ForegroundColor Gray
    }
} else {
    Write-Host "ℹ 注意: 若当前 PowerShell Profile 中仍定义了 function agyc，函数优先级高于 agyc.exe。" -ForegroundColor Yellow
    Write-Host "  可附加 -DisableProfileFunctions 先自动备份旧配置，再注释旧 agyc/atool 函数。" -ForegroundColor Gray
}

if ($LaunchTray) {
    Start-Process (Join-Path $targetBinDir 'agy-switch.exe')
    Write-Host "✓ 已启动 AGY Switch 托盘应用" -ForegroundColor Green
}
