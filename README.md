# AGY Switch (`agy-switch` & `agyc`)

轻量、跨平台（Windows / macOS / Linux）、**100% 纯官方流量**的 Antigravity CLI 多账号切换托盘工具与代理启动器。

## 核心特性

1. **右下角系统托盘常驻 (`agy-switch`)**：
   - 右键弹出原生系统快捷菜单：**1 键切号**、查看各号余量摘要、**一键开关代理**、**拉起 `agy` / `agy -c` 终端**、**官方登录新账号**、**刷新实时余量**。
   - 左键点击打开轻量管理面板（查看双组 `5h`/`周限额` 进度条与重置倒计时、管理别名与排序、测速代理端口、诊断 5 路凭证健康度），关闭窗口自动最小化隐藏回托盘。
2. **最高账号安全架构**：
   - **零第三方流量**：绝不自建第三方 OAuth 客户端、绝不使用非官方 HTTP/TLS 栈请求后端、绝不篡改本机设备标识（`installation_id` / `jetski_state.pbtxt`）。
   - **5 路官方凭证强一致原子切换**：同时原子同步 **OS 原生钥匙串 (`gemini:antigravity`)** + `~/.gemini` 下 4 个官方凭证文件，并在切号前自动将旧账号静默续期后的最新 Token 回存保险箱。
   - **并发运行防串号护栏**：切号前自动检测运行中的 `agy` 进程并提示确认。
3. **跨平台 `agyc` 命令行启动器 (`cmd/agyc`)**：
   - 完整替代原 PowerShell Profile 函数，在 `pwsh`、`cmd`、`bash`、`zsh` 下通用，并与后台托盘实时双向同步状态。

---

## 快速编译

### Windows (PowerShell)
```powershell
cd D:\a\agy-switch
powershell -ExecutionPolicy Bypass -File .\scripts\build.ps1
```
编译产物位于 `D:\a\agy-switch\build\bin\`：
- `agy-switch.exe`：桌面托盘与管理面板主程序
- `agyc.exe`：命令行快捷切换与代理启动器

### macOS / Linux（本地或无 Mac 时使用 GitHub Actions 云端真机构建）
- **有本机环境时**：
  ```bash
  cd frontend && npm install && npm run build && cd ..
  wails build -clean
  go build -trimpath -ldflags="-s -w" -o build/bin/agyc ./cmd/agyc
  ```
- **手上没有 Mac 电脑时（推荐）**：
  仓库已内置 [build-multiplatform.yml](.github/workflows/build-multiplatform.yml)。将代码推送到 GitHub 后，Actions 会自动分配真实的 **Apple Silicon macOS Runner**：
  1. 在真机 macOS 上执行全量 `go test`；
  2. 编译 Intel + Apple Silicon 通用的 `agy-switch.app`（Universal Binary）与 `agyc`；
  3. 自动执行 `codesign --force --deep --sign -` Ad-hoc 签名，并在真机后台启动 `.app` 做 4 秒存活冒烟测试（验证 Cocoa 状态栏与线程无崩溃/死锁）；
  4. 将打包好的 `AGY-Switch-macOS-universal.zip` 上传至 Actions Artifacts 供直接下载。
  > macOS 首次下载解压后若遇系统隔离拦截，终端执行一行 `xattr -cr agy-switch.app` 即可正常打开。

---

## 安装到 PATH 与启用

```powershell
# 将 agy-switch.exe 与 agyc.exe 安装到 ~/.local/bin 并启动右下角托盘
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1 -LaunchTray
```

> **注意**：如果你之前的 PowerShell `$PROFILE` 中定义了 `function agyc`，在 PowerShell 中同名函数优先级高于 `agyc.exe`。可以附加 `-DisableProfileFunctions` 自动备份旧配置后再注释旧的 `function agyc` 与 `function atool`。

---

## `agyc` 常用命令

```text
agyc                         直接以当前账号与配置的代理启动 agy
agyc -c                      以当前账号继续上次对话
agyc <序号|邮箱> [参数...]   5路凭证原子切号后启动 (如: agyc 1, agyc 2 -c)
agyc -u <别名|前缀> [参数..] 按别名或邮箱前缀切号后启动 (如: agyc -u work -c)
agyc -all                    秒看所有账号本地缓存余量
agyc -realall                逐号调用正版 agy /usage 实时刷新全部余量 (自动切回原号)
agyc list | current          列出已保存账号 / 检查当前 5 路凭证一致性
agyc switch <目标> [--force] 仅切号不启动 agy
agyc import                  从 ~/.antigravity_tools 只读导入现有账号与缓存
agyc panel                   打开或唤醒右下角 AGY Switch 桌面托盘窗口
```
