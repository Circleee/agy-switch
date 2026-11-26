package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"agy-switch/internal/agy"
	"agy-switch/internal/creds"
	"agy-switch/internal/ipc"
	"agy-switch/internal/store"
)

const (
	cReset  = "\033[0m"
	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cGray   = "\033[90m"
	cBold   = "\033[1m"
)

var (
	reDigits = regexp.MustCompile(`^\d+$`)
	reEmail  = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	baseDir, err := store.DefaultBaseDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
		return 1
	}
	st, err := store.New(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s 初始化配置存储失败: %v\n", cRed, cReset, err)
		return 1
	}
	paths, err := creds.DefaultTargetPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
		return 1
	}
	syncer := creds.NewSyncer(paths, creds.OSKeyringProvider{})

	// 首次运行若保险箱为空，自动纳管当前系统已登录凭证
	if initState, err := st.LoadState(); err == nil && len(initState.Accounts) == 0 {
		if acc, err2 := syncer.SyncBackActive(st); err2 == nil && acc != nil {
			_, _ = st.MarkSwitched(acc.ID)
		}
	}

	if len(args) >= 1 {
		first := args[0]
		switch first {
		case "-h", "--help", "help":
			printHelp()
			return 0
		case "-all", "--all", "quota":
			live := len(args) >= 2 && (args[1] == "-real" || args[1] == "--real" || args[1] == "-realall")
			return runShowQuota(st, syncer, live)
		case "-realall", "--realall":
			return runShowQuota(st, syncer, true)
		case "list", "ls":
			if len(args) >= 2 && (args[1] == "-all" || args[1] == "--all") {
				return runShowQuota(st, syncer, false)
			}
			if len(args) >= 2 && (args[1] == "-realall" || args[1] == "--realall") {
				return runShowQuota(st, syncer, true)
			}
			return runList(st)
		case "current", "whoami":
			return runCurrent(st, syncer)
		case "import":
			res, err := creds.ImportFromAtool(st, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s[错误]%s 导入失败: %v\n", cRed, cReset, err)
				return 1
			}
			ipc.NotifyExisting(baseDir, "RELOAD")
			fmt.Printf("%s✓ 成功导入 %d 个账号%s (%s)\n", cGreen, res.ImportedCount, cReset, strings.Join(res.Emails, ", "))
			return 0
		case "panel", "gui":
			return runOpenPanel(baseDir)
		case "switch":
			return runSwitchSubcommand(baseDir, st, syncer, args[1:])
		}
	}

	// 解析切号与透传启动参数
	var targetAccount string
	forceSwitch := false
	noProxy := false
	passArgs := args

	for len(passArgs) > 0 {
		if passArgs[0] == "--force" {
			forceSwitch = true
			passArgs = passArgs[1:]
			continue
		}
		if passArgs[0] == "--no-proxy" {
			noProxy = true
			passArgs = passArgs[1:]
			continue
		}
		break
	}

	if len(passArgs) >= 1 {
		first := passArgs[0]
		if (first == "-u" || first == "--account") && len(passArgs) >= 2 {
			targetAccount = passArgs[1]
			passArgs = passArgs[2:]
		} else if reDigits.MatchString(first) || reEmail.MatchString(first) {
			targetAccount = first
			passArgs = passArgs[1:]
		}
	}

	if targetAccount != "" {
		if code := performSwitch(baseDir, st, syncer, targetAccount, forceSwitch); code != 0 {
			return code
		}
	}

	return execOfficialAgy(baseDir, st, syncer, passArgs, noProxy)
}

func performSwitch(baseDir string, st *store.Store, syncer *creds.Syncer, target string, force bool) int {
	state, err := st.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
		return 1
	}
	if !force && state.Config.WarnOnRunning {
		if n := agy.CountRunningAgyProcesses(); n > 0 {
			fmt.Fprintf(os.Stderr, "%s[安全提示]%s 检测到本机有 %d 个正在运行的 agy 进程。若需强制切换请附带 --force 参数，或先退出旧窗口以免串号。\n", cYellow, cReset, n)
			return 1
		}
	}

	acc, err := syncer.SwitchTo(st, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[切号失败]%s %v\n", cRed, cReset, err)
		return 1
	}
	ipc.NotifyExisting(baseDir, "RELOAD")
	label := acc.Email
	if acc.Alias != "" {
		label = fmt.Sprintf("%s (%s)", acc.Email, acc.Alias)
	}
	fmt.Printf("%s✓ 已切换当前 agy 账号 -> %s%s\n", cGreen, label, cReset)
	return 0
}

func runSwitchSubcommand(baseDir string, st *store.Store, syncer *creds.Syncer, subArgs []string) int {
	force := false
	filtered := make([]string, 0, len(subArgs))
	for _, a := range subArgs {
		if a == "--force" || a == "-f" {
			force = true
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) == 0 {
		fmt.Fprintf(os.Stderr, "用法: agyc switch <序号|邮箱|别名> [--force] [agyc|agy] [参数...]\n")
		return 1
	}
	if code := performSwitch(baseDir, st, syncer, filtered[0], force); code != 0 {
		return code
	}
	// 兼容旧习惯: agyc switch 1 agyc -c
	if len(filtered) >= 2 && (filtered[1] == "agyc" || filtered[1] == "agy") {
		noSkip := filtered[1] == "agy"
		rest := filtered[2:]
		if noSkip {
			return execOfficialAgyRaw(baseDir, st, syncer, rest, false, true)
		}
		return execOfficialAgy(baseDir, st, syncer, rest, false)
	}
	return 0
}

func execOfficialAgy(baseDir string, st *store.Store, syncer *creds.Syncer, passArgs []string, noProxy bool) int {
	return execOfficialAgyRaw(baseDir, st, syncer, passArgs, noProxy, false)
}

func execOfficialAgyRaw(baseDir string, st *store.Store, syncer *creds.Syncer, passArgs []string, noProxy bool, forceNoSkip bool) int {
	state, err := st.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
		return 1
	}
	cfg := state.Config
	if noProxy {
		cfg.ProxyEnabled = false
	}

	if cfg.ProxyEnabled {
		chk := agy.TestProxy(cfg.ProxyURL, 1500*time.Millisecond)
		if !chk.Reachable {
			fmt.Fprintf(os.Stderr, "%s[代理未就绪]%s %s\n提示: 请先开启代理客户端，或运行 `agyc --no-proxy` / 在 `agyc panel` 中调整代理端口。\n", cRed, cReset, chk.Message)
			return 1
		}
	}

	agyBin, err := agy.FindAgyBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
		return 1
	}

	var finalArgs []string
	if cfg.SkipPermissions && !forceNoSkip {
		finalArgs = append(finalArgs, "--dangerously-skip-permissions")
	}
	finalArgs = append(finalArgs, passArgs...)

	cmd := exec.Command(agyBin, finalArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = agy.BuildEnvWithProxy(cfg)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	err = cmd.Run()

	// 会话结束后把正版 agy 期间可能静默续期的新 Token 自动回存至保险箱
	if _, syncErr := syncer.SyncBackActive(st); syncErr == nil {
		ipc.NotifyExisting(baseDir, "RELOAD")
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s[错误]%s 启动 agy 失败: %v\n", cRed, cReset, err)
		return 1
	}
	return 0
}

func runOpenPanel(baseDir string) int {
	if ipc.NotifyExisting(baseDir, "SHOW") {
		fmt.Printf("%s✓ 已唤醒后台运行的 AGY Switch 主面板%s\n", cGreen, cReset)
		return 0
	}
	selfExe, err := os.Executable()
	if err == nil {
		guiName := "agy-switch"
		if runtime.GOOS == "windows" {
			guiName = "agy-switch.exe"
		}
		candidate := filepath.Join(filepath.Dir(selfExe), guiName)
		if _, statErr := os.Stat(candidate); statErr == nil {
			cmd := exec.Command(candidate)
			if startErr := cmd.Start(); startErr == nil {
				fmt.Printf("%s✓ 已启动 AGY Switch 桌面托盘程序%s\n", cGreen, cReset)
				return 0
			}
		}
	}
	fmt.Fprintf(os.Stderr, "%s[提示]%s 未找到同目录下的 agy-switch 桌面程序，请先运行 build.ps1 编译。\n", cYellow, cReset)
	return 1
}

func runList(st *store.Store) int {
	state, err := st.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if len(state.Accounts) == 0 {
		fmt.Println("当前没有已保存的账号。可运行 `agyc import` 导入或打开 `agyc panel` 添加。")
		return 0
	}
	for i, acc := range state.Accounts {
		cur := "  "
		if acc.ID == state.CurrentAccountID {
			cur = cGreen + "★ " + cReset
		}
		alias := ""
		if acc.Alias != "" {
			alias = fmt.Sprintf(" (%s)", acc.Alias)
		}
		sum := agy.SummarizeQuota(acc.Quota)
		qStr := ""
		if sum.HasData {
			qStr = fmt.Sprintf("  %s[Gemini 5h:%d%% 周:%d%% | Claude 5h:%d%%]%s", cGray, maxZero(sum.Gemini5hPct), maxZero(sum.GeminiWkPct), maxZero(sum.Claude5hPct), cReset)
		}
		fmt.Printf("%s[%d] %s%s%s\n", cur, i+1, acc.Email, alias, qStr)
	}
	return 0
}

func runCurrent(st *store.Store, syncer *creds.Syncer) int {
	h := syncer.CheckHealth()
	state, _ := st.LoadState()
	fmt.Printf("当前激活邮箱: %s%s%s\n", cBold, valueOrDash(h.ActiveEmail), cReset)
	if h.Consistent {
		fmt.Printf("5 路凭证状态: %s✓ 强一致 (OS Keyring + 4 个本地凭证文件)%s\n", cGreen, cReset)
	} else {
		fmt.Printf("5 路凭证状态: %s⚠ 不一致 (建议运行 agyc switch 重新对齐)%s\n", cYellow, cReset)
	}
	if state != nil {
		fmt.Printf("代理注入配置: enabled=%v, url=%s, skip_permissions=%v\n",
			state.Config.ProxyEnabled, state.Config.ProxyURL, state.Config.SkipPermissions)
	}
	return 0
}

func runShowQuota(st *store.Store, syncer *creds.Syncer, live bool) int {
	if live {
		if n := agy.CountRunningAgyProcesses(); n > 0 {
			fmt.Fprintf(os.Stderr, "%s[注意]%s 检测到 %d 个正在运行的 agy 进程，逐号实时查询可能影响运行中的窗口。\n", cYellow, cReset, n)
		}
		err := agy.RefreshAllQuotas(context.Background(), st, syncer, func(p agy.QuotaProgress) {
			switch p.Stage {
			case "querying":
				if p.Error != "" {
					fmt.Printf("%s[%d/%d] %s 查询失败: %s%s\n", cRed, p.Current, p.Total, p.Email, p.Error, cReset)
				} else {
					fmt.Printf("%s[%d/%d] 正在切换并实时查询 %s ...%s\n", cGray, p.Current, p.Total, p.Email, cReset)
				}
			case "restoring":
				fmt.Printf("%s正在恢复原激活账号 ...%s\n", cGray, cReset)
			}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s[错误]%s %v\n", cRed, cReset, err)
			return 1
		}
		ipc.NotifyExisting(st.BaseDir(), "RELOAD")
	}

	state, err := st.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if len(state.Accounts) == 0 {
		fmt.Println("当前没有已保存的账号。可运行 `agyc import` 导入现有账号。")
		return 0
	}

	modeStr := "本地缓存"
	if live {
		modeStr = "实时拉取 (正版 agy /usage)"
	}
	fmt.Printf("\n%s================================================================================%s\n", cCyan, cReset)
	fmt.Printf("%s AGY Switch 多账号余量总览 | 模式: %s | 共 %d 个账号%s\n", cCyan, modeStr, len(state.Accounts), cReset)
	fmt.Printf("%s================================================================================%s\n", cCyan, cReset)

	now := time.Now()
	for i, acc := range state.Accounts {
		curBadge := ""
		if acc.ID == state.CurrentAccountID {
			curBadge = cGreen + "  ★ 当前激活" + cReset
		}
		alias := ""
		if acc.Alias != "" {
			alias = fmt.Sprintf(" (%s)", acc.Alias)
		}
		fmt.Printf("\n %s[%d]%s %s%s%s%s\n", cYellow, i+1, cReset, cBold, acc.Email, cReset+alias, curBadge)

		if acc.Quota == nil || len(acc.Quota.Groups) == 0 {
			fmt.Printf("     %s暂无配额数据 (运行 agyc -realall 拉取)%s\n", cGray, cReset)
			continue
		}
		upd := "-"
		if acc.Quota.LastUpdated > 0 {
			upd = time.Unix(acc.Quota.LastUpdated, 0).Local().Format("01-02 15:04:05")
		}
		tier := valueOrDash(acc.Quota.SubscriptionTier)
		fmt.Printf("     %s套餐: %s  |  数据更新: %s%s\n", cGray, tier, upd, cReset)

		for _, grp := range acc.Quota.Groups {
			fmt.Printf("     %s▸ %s%s\n", cCyan, grp.Name, cReset)
			for _, b := range grp.Buckets {
				pct := int(math.Round(b.RemainingFraction * 100))
				filled := int(math.Round(float64(pct) / 10.0))
				if pct > 0 && filled == 0 {
					filled = 1
				}
				if filled > 10 {
					filled = 10
				}
				bar := strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
				col := cGreen
				if pct < 25 {
					col = cRed
				} else if pct < 60 {
					col = cYellow
				}
				winLabel := "5h限额"
				if b.Window == "weekly" || strings.Contains(b.ID, "weekly") {
					winLabel = "周限额"
				}
				resetInfo := formatCLIReset(b.ResetTime, pct, now)
				fmt.Printf("        %-6s: %s[%s] %3d%%%s%s\n", winLabel, col, bar, pct, cReset, resetInfo)
			}
		}
	}
	fmt.Printf("\n%s================================================================================%s\n\n", cCyan, cReset)
	return 0
}

func formatCLIReset(iso string, pct int, now time.Time) string {
	if iso == "" || strings.HasPrefix(iso, "1970-01-01") {
		if pct >= 100 {
			return cGray + "  |  未消耗 (满额)" + cReset
		}
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	localT := t.Local()
	diff := localT.Sub(now)
	if diff <= 0 {
		return fmt.Sprintf("%s  |  重置: %s (已到点)%s", cGray, localT.Format("01-02 15:04"), cReset)
	}
	mins := int(math.Ceil(diff.Minutes()))
	var span string
	if mins >= 1440 {
		span = fmt.Sprintf("%dd%02dh后", mins/1440, (mins%1440)/60)
	} else if mins >= 60 {
		span = fmt.Sprintf("%dh%02dm后", mins/60, mins%60)
	} else {
		span = strconv.Itoa(mins) + "m后"
	}
	return fmt.Sprintf("%s  |  重置: %s (%s)%s", cGray, localT.Format("01-02 15:04"), span, cReset)
}

func maxZero(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func printHelp() {
	fmt.Print(`agyc (AGY Switch CLI) - 安全多账号切换 + 代理启动器

用法:
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
  agyc --no-proxy [参数...]    本次临时直连启动 agy
  agyc -h | --help             显示本帮助信息
`)
}
