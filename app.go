package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"agy-switch/internal/agy"
	"agy-switch/internal/creds"
	"agy-switch/internal/ipc"
	"agy-switch/internal/store"
	"agy-switch/internal/tray"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// DashboardData 汇总主面板所需的完整视图模型
type DashboardData struct {
	Platform        string             `json:"platform"`
	State           *store.State       `json:"state"`
	Health          creds.HealthReport `json:"health"`
	AgyBinaryPath   string             `json:"agy_binary_path"`
	RunningAgyCount int                `json:"running_agy_count"`
	AutoStart       bool               `json:"auto_start"`
	AtoolAvailable  bool               `json:"atool_available"`
	LoginRunning    bool               `json:"login_running"`
	QuotaRefreshing bool               `json:"quota_refreshing"`
}

// SwitchResult 返回账号切换结果（含并发运行确认拦截信息）
type SwitchResult struct {
	Switched     bool           `json:"switched"`
	NeedConfirm  bool           `json:"need_confirm"`
	RunningCount int            `json:"running_count"`
	Message      string         `json:"message"`
	Account      *store.Account `json:"account,omitempty"`
}

// App 绑定 Wails 前端与系统托盘的核心控制器
type App struct {
	ctx             context.Context
	st              *store.Store
	syncer          *creds.Syncer
	authRunner      *agy.AuthFlowRunner
	trayMgr         *tray.Manager
	ipcSrv          *ipc.Server
	quotaRefreshing atomic.Bool
	quitting        atomic.Bool
}

func NewApp(st *store.Store, syncer *creds.Syncer, ipcSrv *ipc.Server) *App {
	return &App{
		st:         st,
		syncer:     syncer,
		authRunner: agy.NewAuthFlowRunner(),
		ipcSrv:     ipcSrv,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 首次启动若保险箱为空，自动将本机当前已登录的 agy 账号无损纳管入库
	if st, err := a.st.LoadState(); err == nil && len(st.Accounts) == 0 {
		if acc, err2 := a.syncer.SyncBackActive(a.st); err2 == nil && acc != nil {
			_, _ = a.st.MarkSwitched(acc.ID)
		}
	}

	a.trayMgr = tray.NewManager(tray.Actions{
		OnShowPanel: func() {
			a.ShowPanel()
		},
		OnSwitchAccount: func(accountID string) {
			res, err := a.SwitchAccount(accountID, false)
			if err != nil {
				a.ShowPanel()
				a.emitToast("error", "切号失败: "+err.Error())
				return
			}
			if res.NeedConfirm {
				// 检测到正在运行的 agy 会话时唤起面板让用户确认，确保安全不串号
				a.ShowPanel()
				wailsRuntime.EventsEmit(a.ctx, "switch:confirm_needed", map[string]any{
					"account_id":    accountID,
					"running_count": res.RunningCount,
					"message":       res.Message,
				})
			}
		},
		OnLaunchAgy: func(continueSession bool) {
			if err := a.LaunchAgy(continueSession); err != nil {
				a.ShowPanel()
				a.emitToast("error", err.Error())
			}
		},
		OnRefreshCurrent: func() {
			go func() {
				if _, err := a.RefreshAccountQuota(""); err != nil {
					a.emitToast("error", "刷新余量失败: "+err.Error())
				} else {
					a.emitToast("success", "当前账号余量已更新")
				}
			}()
		},
		OnRefreshAll: func() {
			_ = a.RefreshAllQuotas()
		},
		OnStartLogin: func() {
			a.ShowPanel()
			if err := a.StartOfficialLogin(); err != nil {
				a.emitToast("error", err.Error())
			}
		},
		OnToggleProxy: func() {
			st, err := a.st.LoadState()
			if err != nil {
				return
			}
			cfg := st.Config
			cfg.ProxyEnabled = !cfg.ProxyEnabled
			_ = a.SaveConfig(cfg)
		},
		OnQuit: func() {
			a.QuitApp()
		},
		LoadCurrentSnapshot: func() *store.State {
			st, _ := a.st.LoadState()
			return st
		},
	})
	if runtime.GOOS == "darwin" {
		// macOS 下稍候 200ms 待 Cocoa 主事件循环 [NSApp run] 完全就绪后再挂载状态栏图标，防止主线程死锁
		go func() {
			time.Sleep(200 * time.Millisecond)
			a.trayMgr.Start()
		}()
	} else {
		a.trayMgr.Start()
	}
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	if a.quitting.Load() {
		return false
	}
	wailsRuntime.WindowHide(ctx)
	return true
}

func (a *App) shutdown(_ context.Context) {
	a.authRunner.Cancel()
	if a.trayMgr != nil {
		a.trayMgr.Stop()
	}
	if a.ipcSrv != nil {
		a.ipcSrv.Close()
	}
}

// ShowPanel 唤醒并置顶显示主面板窗口
func (a *App) ShowPanel() {
	if a.ctx == nil {
		return
	}
	wailsRuntime.WindowUnminimise(a.ctx)
	wailsRuntime.WindowShow(a.ctx)
	wailsRuntime.EventsEmit(a.ctx, "state:updated")
}

// HideToTray 隐藏主面板到右下角系统托盘
func (a *App) HideToTray() {
	if a.ctx != nil {
		wailsRuntime.WindowHide(a.ctx)
	}
}

// QuitApp 完全退出应用并移除托盘图标
func (a *App) QuitApp() {
	a.quitting.Store(true)
	if a.trayMgr != nil {
		a.trayMgr.Stop()
	}
	if a.ipcSrv != nil {
		a.ipcSrv.Close()
	}
	if a.ctx != nil {
		wailsRuntime.Quit(a.ctx)
	}
}

func (a *App) notifyChanged() {
	if a.trayMgr != nil {
		a.trayMgr.RebuildMenu()
	}
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "state:updated")
	}
}

func (a *App) emitToast(kind, msg string) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "app:toast", map[string]string{
			"kind":    kind,
			"message": msg,
		})
	}
}

// GetDashboard 获取完整面板状态
func (a *App) GetDashboard() (*DashboardData, error) {
	// 顺手同步一次当前激活凭证的静默续期变化
	if health := a.syncer.CheckHealth(); health.ActiveEmail != "" {
		_, _ = a.syncer.SyncBackActive(a.st)
	}
	st, err := a.st.LoadState()
	if err != nil {
		return nil, err
	}
	agyPath, _ := agy.FindAgyBinary()
	atoolAvailable := false
	if atoolDir, err := creds.DefaultAtoolDir(); err == nil {
		if _, statErr := os.Stat(filepath.Join(atoolDir, "accounts.json")); statErr == nil {
			atoolAvailable = true
		}
	}

	return &DashboardData{
		Platform:        runtime.GOOS,
		State:           st,
		Health:          a.syncer.CheckHealth(),
		AgyBinaryPath:   agyPath,
		RunningAgyCount: agy.CountRunningAgyProcesses(),
		AutoStart:       agy.GetAutoStart(),
		AtoolAvailable:  atoolAvailable,
		LoginRunning:    a.authRunner.IsRunning(),
		QuotaRefreshing: a.quotaRefreshing.Load(),
	}, nil
}

// SwitchAccount 切换当前激活账号（带运行中进程串号保护）
func (a *App) SwitchAccount(idOrQuery string, force bool) (*SwitchResult, error) {
	st, err := a.st.LoadState()
	if err != nil {
		return nil, err
	}
	if !force && st.Config.WarnOnRunning {
		if running := agy.CountRunningAgyProcesses(); running > 0 {
			return &SwitchResult{
				Switched:     false,
				NeedConfirm:  true,
				RunningCount: running,
				Message:      fmt.Sprintf("检测到本机有 %d 个正在运行的 agy 会话。直接切号可能导致旧窗口在续期 Token 时发生账号串号，建议先退出旧会话或确认强制切换。", running),
			}, nil
		}
	}

	acc, err := a.syncer.SwitchTo(a.st, idOrQuery)
	if err != nil {
		return nil, err
	}
	a.notifyChanged()
	return &SwitchResult{
		Switched: true,
		Account:  acc,
		Message:  fmt.Sprintf("已切换至 %s", acc.Email),
	}, nil
}

// LaunchAgy 按当前配置拉起带代理环境变量的系统终端运行 agy
func (a *App) LaunchAgy(continueSession bool) error {
	st, err := a.st.LoadState()
	if err != nil {
		return err
	}
	if st.Config.ProxyEnabled {
		chk := agy.TestProxy(st.Config.ProxyURL, 1200*time.Millisecond)
		if !chk.Reachable {
			return fmt.Errorf("代理端口未就绪 (%s)，请先开启代理软件或在代理设置中关闭代理", chk.Address)
		}
	}
	var extra []string
	if continueSession {
		extra = append(extra, "-c")
	}
	return agy.LaunchTerminalWithAgy(st.Config, extra, false)
}

// SwitchAndLaunchAgy 切换到指定账号后立即打开终端运行 agy
func (a *App) SwitchAndLaunchAgy(accountID string, continueSession bool, force bool) (*SwitchResult, error) {
	res, err := a.SwitchAccount(accountID, force)
	if err != nil || res.NeedConfirm {
		return res, err
	}
	if err := a.LaunchAgy(continueSession); err != nil {
		return res, err
	}
	return res, nil
}

// StartOfficialLogin 启动零第三方流量的官方 agy 登录引导
func (a *App) StartOfficialLogin() error {
	if a.ctx == nil {
		return errors.New("应用尚未初始化")
	}
	return a.authRunner.StartOfficialLogin(context.Background(), a.st, a.syncer, func(status agy.LoginFlowStatus) {
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "login:status", status)
		}
		a.notifyChanged()
	})
}

// CancelOfficialLogin 取消登录并立即恢复原激活账号
func (a *App) CancelOfficialLogin() {
	a.authRunner.Cancel()
}

// CaptureCurrentSession 手动将当前本机已登录的 agy 账号捕获入库
func (a *App) CaptureCurrentSession() (*store.Account, error) {
	acc, err := a.syncer.SyncBackActive(a.st)
	if err != nil {
		return nil, err
	}
	_, _ = a.st.MarkSwitched(acc.ID)
	a.notifyChanged()
	return acc, nil
}

// ImportFromAntigravityTools 从 ~/.antigravity_tools 一键只读导入现有账号与配额缓存
func (a *App) ImportFromAntigravityTools() (*creds.ImportResult, error) {
	res, err := creds.ImportFromAtool(a.st, "")
	if err != nil {
		return nil, err
	}
	// 如果系统已有激活账号，确保当前激活 ID 对齐本机真实登录账号
	if health := a.syncer.CheckHealth(); health.ActiveEmail != "" {
		if acc, _, err2 := a.st.ResolveAccount(health.ActiveEmail); err2 == nil {
			_, _ = a.st.MarkSwitched(acc.ID)
		}
	}
	a.notifyChanged()
	return res, nil
}

// RefreshAccountQuota 实时刷新指定账号（或为空时刷新当前账号）的配额
func (a *App) RefreshAccountQuota(accountID string) (*store.AccountQuota, error) {
	if !a.quotaRefreshing.CompareAndSwap(false, true) {
		return nil, errors.New("正在刷新配额中，请稍候")
	}
	defer func() {
		a.quotaRefreshing.Store(false)
		a.notifyChanged()
	}()
	a.notifyChanged()

	st, err := a.st.LoadState()
	if err != nil {
		return nil, err
	}
	if accountID == "" || accountID == st.CurrentAccountID {
		return agy.RefreshCurrentQuota(context.Background(), a.st, a.syncer)
	}

	// 若刷新非当前账号：临时切过去查一次官方 /usage，结束后自动切回原号
	origID := st.CurrentAccountID
	rawSnap := a.syncer.SnapshotRaw()
	defer func() {
		if origID != "" {
			if _, err := a.syncer.SwitchTo(a.st, origID); err != nil {
				_ = a.syncer.RestoreRaw(rawSnap)
			}
		}
	}()

	if _, err := a.syncer.SwitchTo(a.st, accountID); err != nil {
		return nil, err
	}
	q, err := agy.QueryLiveQuota(context.Background(), st.Config)
	if err != nil {
		return nil, err
	}
	if err := a.st.UpdateQuota(accountID, q); err != nil {
		return nil, err
	}
	_, _ = a.syncer.SyncBackActive(a.st)
	return q, nil
}

// RefreshAllQuotas 异步串行刷新全部账号配额并推送实时进度
func (a *App) RefreshAllQuotas() error {
	if !a.quotaRefreshing.CompareAndSwap(false, true) {
		return errors.New("正在执行配额刷新任务，请稍候")
	}
	a.notifyChanged()

	go func() {
		defer func() {
			a.quotaRefreshing.Store(false)
			a.notifyChanged()
		}()
		err := agy.RefreshAllQuotas(context.Background(), a.st, a.syncer, func(p agy.QuotaProgress) {
			if a.ctx != nil {
				wailsRuntime.EventsEmit(a.ctx, "quota:progress", p)
				wailsRuntime.EventsEmit(a.ctx, "state:updated")
			}
		})
		if err != nil {
			a.emitToast("error", "全量刷新中断: "+err.Error())
		} else {
			a.emitToast("success", "全部账号余量已实时更新并恢复原激活账号")
		}
	}()
	return nil
}

// UpdateAlias 更新账号自定义别名
func (a *App) UpdateAlias(accountID, alias string) error {
	if err := a.st.UpdateAlias(accountID, alias); err != nil {
		return err
	}
	a.notifyChanged()
	return nil
}

// MoveAccount 调整账号顺序 (-1 上移, +1 下移)
func (a *App) MoveAccount(accountID string, direction int) error {
	if err := a.st.MoveAccount(accountID, direction); err != nil {
		return err
	}
	a.notifyChanged()
	return nil
}

// DeleteAccount 从本地保险箱移除指定账号
func (a *App) DeleteAccount(accountID string) error {
	if err := a.st.DeleteAccount(accountID); err != nil {
		return err
	}
	a.notifyChanged()
	return nil
}

// SaveConfig 保存全局配置并应用开机自启与托盘状态
func (a *App) SaveConfig(cfg store.Config) error {
	cfg.ProxyURL = agy.NormalizeProxyURL(cfg.ProxyURL)
	if err := a.st.UpdateConfig(cfg); err != nil {
		return err
	}
	_ = agy.SetAutoStart(cfg.AutoStart)
	a.notifyChanged()
	return nil
}

// TestProxyConnection 测试代理地址 TCP 连通性与毫秒延迟
func (a *App) TestProxyConnection(proxyURL string) agy.ProxyCheckResult {
	return agy.TestProxy(proxyURL, 2*time.Second)
}
