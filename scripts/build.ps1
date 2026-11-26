param(
    [switch]$NoRelaunch
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot\..

if (Test-Path 'C:\Program Files\Go\bin') {
    $env:PATH = "C:\Program Files\Go\bin;$env:PATH"
}

$wasRunning = $false
$runningProcs = Get-Process -Name 'agy-switch' -ErrorAction SilentlyContinue
if ($runningProcs) {
    $wasRunning = $true
    Write-Host "==> [准备] 检测到正在运行的 agy-switch.exe，先关闭以释放文件锁 ..." -ForegroundColor Yellow
    $runningProcs | Stop-Process -Force
    Start-Sleep -Milliseconds 600
}

Write-Host "==> [1/4] 生成应用图标资源 (build/appicon.png) ..." -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path "build\bin" | Out-Null
go run ./cmd/genicon

Write-Host "==> [2/4] 运行沙盒隔离单元测试 ..." -ForegroundColor Cyan
go test -v ./internal/...

Write-Host "==> [3/4] 使用 Wails 编译桌面托盘应用 (build/bin/agy-switch.exe) ..." -ForegroundColor Cyan
wails build -clean

Write-Host "==> [4/4] 编译跨平台命令行启动器 (build/bin/agyc.exe) ..." -ForegroundColor Cyan
go build -trimpath -ldflags="-s -w" -o build\bin\agyc.exe .\cmd\agyc

Write-Host "`n✓ 编译全部完成!" -ForegroundColor Green
Get-ChildItem build\bin\*.exe | Select-Object Name, @{Name='SizeMB';Expression={[math]::Round($_.Length / 1MB, 2)}}, LastWriteTime | Format-Table -AutoSize

if ($wasRunning -and -not $NoRelaunch) {
    Write-Host "==> 已为您自动重新启动新版 agy-switch.exe" -ForegroundColor Green
    Start-Process "build\bin\agy-switch.exe"
}
