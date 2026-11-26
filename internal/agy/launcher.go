package agy

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agy-switch/internal/store"
)

// FindAgyBinary 自动探测本机正版 agy 可执行程序路径
func FindAgyBinary() (string, error) {
	if p, err := exec.LookPath("agy"); err == nil && p != "" {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	exeName := "agy"
	if runtime.GOOS == "windows" {
		exeName = "agy.exe"
	}
	candidates := []string{
		filepath.Join(home, ".local", "bin", exeName),
		filepath.Join(home, ".gemini", "antigravity-cli", "bin", exeName),
	}
	if runtime.GOOS == "windows" {
		if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
			candidates = append(candidates, filepath.Join(localApp, "Programs", "Antigravity", "bin", "agy.exe"))
		}
	} else {
		candidates = append(candidates, "/usr/local/bin/agy", "/opt/homebrew/bin/agy")
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("未找到正版 agy 命令，请确认已安装 Antigravity CLI 并加入 PATH")
}

// BuildEnvWithProxy 基于当前进程环境变量构造注入（或清理）代理后的环境切片
func BuildEnvWithProxy(cfg store.Config) []string {
	base := os.Environ()
	filtered := make([]string, 0, len(base)+4)
	for _, kv := range base {
		k := strings.SplitN(kv, "=", 2)[0]
		if strings.EqualFold(k, "HTTP_PROXY") || strings.EqualFold(k, "HTTPS_PROXY") ||
			strings.EqualFold(k, "http_proxy") || strings.EqualFold(k, "https_proxy") {
			continue
		}
		filtered = append(filtered, kv)
	}
	if cfg.ProxyEnabled && strings.TrimSpace(cfg.ProxyURL) != "" {
		p := NormalizeProxyURL(cfg.ProxyURL)
		filtered = append(filtered,
			"HTTP_PROXY="+p,
			"HTTPS_PROXY="+p,
			"http_proxy="+p,
			"https_proxy="+p,
		)
	}
	return filtered
}

// NormalizeProxyURL 规范化用户输入的代理地址（例如只填 127.0.0.1:7897 或 7897 时自动补全）
func NormalizeProxyURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		if !strings.Contains(s, ":") {
			s = "127.0.0.1:" + s
		}
		s = "http://" + s
	}
	return s
}

// ProxyCheckResult 表示代理端口连通性检测结果
type ProxyCheckResult struct {
	Reachable bool   `json:"reachable"`
	Address   string `json:"address"`
	LatencyMs int64  `json:"latency_ms"`
	Message   string `json:"message"`
}

// TestProxy 检测代理端口 TCP 握手连通性与毫秒级延迟
func TestProxy(rawURL string, timeout time.Duration) ProxyCheckResult {
	normalized := NormalizeProxyURL(rawURL)
	if normalized == "" {
		return ProxyCheckResult{Reachable: false, Message: "代理地址为空"}
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" {
		return ProxyCheckResult{Reachable: false, Address: normalized, Message: fmt.Sprintf("代理地址格式无效: %v", err)}
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		return ProxyCheckResult{
			Reachable: false,
			Address:   u.Host,
			Message:   fmt.Sprintf("无法连接代理端口 %s: %v", u.Host, err),
		}
	}
	_ = conn.Close()
	ms := time.Since(start).Milliseconds()
	if ms == 0 {
		ms = 1
	}
	return ProxyCheckResult{
		Reachable: true,
		Address:   u.Host,
		LatencyMs: ms,
		Message:   fmt.Sprintf("代理端口 %s 连通正常 (%d ms)", u.Host, ms),
	}
}

// SetAutoStart 配置或取消系统开机自启
func SetAutoStart(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	return setOSAutoStart(enabled, exe)
}

// GetAutoStart 查询当前是否已注册开机自启
func GetAutoStart() bool {
	return getOSAutoStart()
}

// LaunchTerminalWithAgy 在独立系统终端窗口中拉起带代理环境变量的交互式 agy 会话
func LaunchTerminalWithAgy(cfg store.Config, extraArgs []string, forceNoSkipPerm bool) error {
	agyBin, err := FindAgyBinary()
	if err != nil {
		return err
	}
	workDir := strings.TrimSpace(cfg.DefaultWorkDir)
	if workDir == "" {
		workDir, _ = os.UserHomeDir()
	}
	if st, err := os.Stat(workDir); err != nil || !st.IsDir() {
		workDir, _ = os.UserHomeDir()
	}

	var agyArgs []string
	if cfg.SkipPermissions && !forceNoSkipPerm {
		agyArgs = append(agyArgs, "--dangerously-skip-permissions")
	}
	agyArgs = append(agyArgs, extraArgs...)

	env := BuildEnvWithProxy(cfg)

	switch runtime.GOOS {
	case "windows":
		return launchWindowsTerminal(agyBin, agyArgs, workDir, cfg.PreferredTerm, env)
	case "darwin":
		return launchDarwinTerminal(agyBin, agyArgs, workDir, cfg)
	default:
		return launchLinuxTerminal(agyBin, agyArgs, workDir, env)
	}
}

func quotePSArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func launchWindowsTerminal(agyBin string, agyArgs []string, workDir, preferred string, env []string) error {
	quotedParts := []string{"& " + quotePSArg(agyBin)}
	for _, a := range agyArgs {
		quotedParts = append(quotedParts, quotePSArg(a))
	}
	psCommand := strings.Join(quotedParts, " ")

	shellExe := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil && p != "" {
		shellExe = p
	}

	pref := strings.ToLower(strings.TrimSpace(preferred))
	if pref == "" {
		pref = "auto"
	}

	// 优先使用 Windows Terminal (wt.exe)
	if pref == "auto" || pref == "wt" {
		if wtPath, err := exec.LookPath("wt.exe"); err == nil && wtPath != "" {
			cmd := exec.Command(wtPath, "-d", workDir, shellExe, "-NoExit", "-Command", psCommand)
			cmd.Dir = workDir
			cmd.Env = env
			return cmd.Start()
		}
	}

	if pref == "cmd" {
		cmdLine := `"` + agyBin + `"`
		for _, a := range agyArgs {
			cmdLine += ` "` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		cmd := exec.Command("cmd.exe", "/c", "start", "AGY CLI", "/D", workDir, "cmd.exe", "/k", cmdLine)
		cmd.Dir = workDir
		cmd.Env = env
		hideBackgroundWindow(cmd)
		return cmd.Start()
	}

	// 使用 Start-Process 打开独立 PowerShell 窗口并继承当前代理环境变量
	cmd := exec.Command("cmd.exe", "/c", "start", "AGY CLI", "/D", workDir, shellExe, "-NoExit", "-Command", psCommand)
	cmd.Dir = workDir
	cmd.Env = env
	hideBackgroundWindow(cmd)
	return cmd.Start()
}

func launchDarwinTerminal(agyBin string, agyArgs []string, workDir string, cfg store.Config) error {
	appleScript := BuildDarwinAppleScript(agyBin, agyArgs, workDir, cfg)
	return exec.Command("osascript", "-e", appleScript).Start()
}

// BuildDarwinAppleScript 生成 macOS Terminal.app 或 iTerm2 的启动脚本（方便跨平台单元测试验证）
func BuildDarwinAppleScript(agyBin string, agyArgs []string, workDir string, cfg store.Config) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("cd %q", workDir))
	if cfg.ProxyEnabled && strings.TrimSpace(cfg.ProxyURL) != "" {
		p := NormalizeProxyURL(cfg.ProxyURL)
		parts = append(parts, fmt.Sprintf("export HTTP_PROXY=%q HTTPS_PROXY=%q", p, p))
	}
	cmdCall := fmt.Sprintf("%q", agyBin)
	for _, a := range agyArgs {
		cmdCall += fmt.Sprintf(" %q", a)
	}
	parts = append(parts, cmdCall)
	shellLine := strings.Join(parts, " && ")
	escaped := strings.ReplaceAll(shellLine, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)

	if strings.EqualFold(strings.TrimSpace(cfg.PreferredTerm), "iterm") {
		return fmt.Sprintf(`tell application "iTerm"
    activate
    set newWin to (create window with default profile)
    tell current session of newWin
        write text "%s"
    end tell
end tell`, escaped)
	}

	return fmt.Sprintf(`tell application "Terminal"
    activate
    do script "%s"
end tell`, escaped)
}

func launchLinuxTerminal(agyBin string, agyArgs []string, workDir string, env []string) error {
	terms := []struct {
		bin  string
		flag string
	}{
		{"x-terminal-emulator", "-e"},
		{"gnome-terminal", "--"},
		{"konsole", "-e"},
		{"xfce4-terminal", "-x"},
		{"kitty", ""},
		{"alacritty", "-e"},
	}
	for _, t := range terms {
		if p, err := exec.LookPath(t.bin); err == nil && p != "" {
			var args []string
			if t.flag != "" {
				args = append(args, t.flag)
			}
			args = append(args, agyBin)
			args = append(args, agyArgs...)
			cmd := exec.Command(p, args...)
			cmd.Dir = workDir
			cmd.Env = env
			return cmd.Start()
		}
	}
	return fmt.Errorf("未找到可用的图形终端模拟器")
}
