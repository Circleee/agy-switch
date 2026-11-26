//go:build darwin

package tray

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <dispatch/dispatch.h>

extern void goAgyTrayMainThreadStart(void);

static void agyDispatchTrayStartOnMain(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		goAgyTrayMainThreadStart();
	});
}
*/
import "C"

import (
	"fmt"
	"sync"

	"fyne.io/systray"
)

var (
	darwinStartMu sync.Mutex
	darwinStartFn func()
)

//export goAgyTrayMainThreadStart
func goAgyTrayMainThreadStart() {
	darwinStartMu.Lock()
	fn := darwinStartFn
	darwinStartMu.Unlock()
	if fn != nil {
		fn()
	}
}

// Manager 封装 macOS 顶部状态栏托盘生命周期与动态菜单重绘
type Manager struct {
	actions    Actions
	mu         sync.Mutex
	ready      bool
	stopMenuCh chan struct{}
	macStart   func()
	macEnd     func()
}

func NewManager(actions Actions) *Manager {
	return &Manager{actions: actions}
}

func (m *Manager) Start() {
	onReady := func() {
		systray.SetIcon(IconBytes())
		systray.SetTitle("")
		systray.SetTooltip("AGY Switch")
		if m.actions.OnShowPanel != nil {
			systray.SetOnTapped(m.actions.OnShowPanel)
		}
		m.mu.Lock()
		m.ready = true
		m.mu.Unlock()
		m.RebuildMenu()
	}

	onExit := func() {
		m.mu.Lock()
		if m.stopMenuCh != nil {
			close(m.stopMenuCh)
			m.stopMenuCh = nil
		}
		m.ready = false
		m.mu.Unlock()
	}

	m.macStart, m.macEnd = systray.RunWithExternalLoop(onReady, onExit)
	if m.macStart != nil {
		// AppKit NSStatusBar / NSView 必须在 Cocoa 主线程执行
		darwinStartMu.Lock()
		darwinStartFn = m.macStart
		darwinStartMu.Unlock()
		C.agyDispatchTrayStartOnMain()
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopMenuCh != nil {
		close(m.stopMenuCh)
		m.stopMenuCh = nil
	}
	m.ready = false
	m.mu.Unlock()
}

func (m *Manager) RebuildMenu() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.actions.LoadCurrentSnapshot == nil {
		return
	}

	if m.stopMenuCh != nil {
		close(m.stopMenuCh)
	}
	stopCh := make(chan struct{})
	m.stopMenuCh = stopCh

	st := m.actions.LoadCurrentSnapshot()
	systray.ResetMenu()

	if st == nil {
		itemOpen := systray.AddMenuItem("打开面板", "")
		m.bindClick(stopCh, itemOpen, m.actions.OnShowPanel)
		systray.AddSeparator()
		itemQuit := systray.AddMenuItem("退出", "")
		m.bindClick(stopCh, itemQuit, m.actions.OnQuit)
		return
	}

	systray.SetTooltip(buildTooltipText(st))

	if len(st.Accounts) == 0 {
		emptyItem := systray.AddMenuItem("暂无账号", "")
		emptyItem.Disable()
	} else {
		for idx, acc := range st.Accounts {
			accCopy := acc
			isCur := accCopy.ID == st.CurrentAccountID
			title := fmt.Sprintf("%d. %s", idx+1, accCopy.Email)
			if accCopy.Alias != "" {
				title = fmt.Sprintf("%d. %s (%s)", idx+1, accCopy.Email, accCopy.Alias)
			}
			if qShort := formatShortQuota(accCopy.Quota); qShort != "" {
				title += " · " + qShort
			}
			item := systray.AddMenuItemCheckbox(title, "", isCur)
			m.bindClick(stopCh, item, func() {
				if m.actions.OnSwitchAccount != nil {
					m.actions.OnSwitchAccount(accCopy.ID)
				}
			})
		}
	}
	systray.AddSeparator()

	itemLaunch := systray.AddMenuItem("启动 agy", "")
	m.bindClick(stopCh, itemLaunch, func() {
		if m.actions.OnLaunchAgy != nil {
			m.actions.OnLaunchAgy(false)
		}
	})
	itemContinue := systray.AddMenuItem("继续对话 (-c)", "")
	m.bindClick(stopCh, itemContinue, func() {
		if m.actions.OnLaunchAgy != nil {
			m.actions.OnLaunchAgy(true)
		}
	})
	systray.AddSeparator()

	itemRefCur := systray.AddMenuItem("刷新当前余量", "")
	m.bindClick(stopCh, itemRefCur, m.actions.OnRefreshCurrent)

	itemRefAll := systray.AddMenuItem("刷新全部余量", "")
	m.bindClick(stopCh, itemRefAll, m.actions.OnRefreshAll)

	itemLogin := systray.AddMenuItem("添加账号...", "")
	m.bindClick(stopCh, itemLogin, m.actions.OnStartLogin)
	systray.AddSeparator()

	proxyTitle := fmt.Sprintf("代理 %s", st.Config.ProxyURL)
	itemProxy := systray.AddMenuItemCheckbox(proxyTitle, "", st.Config.ProxyEnabled)
	m.bindClick(stopCh, itemProxy, m.actions.OnToggleProxy)

	itemPanel := systray.AddMenuItem("打开面板...", "")
	m.bindClick(stopCh, itemPanel, m.actions.OnShowPanel)
	systray.AddSeparator()

	itemQuit := systray.AddMenuItem("退出", "")
	m.bindClick(stopCh, itemQuit, m.actions.OnQuit)
}

func (m *Manager) bindClick(stopCh <-chan struct{}, item *systray.MenuItem, fn func()) {
	if fn == nil {
		return
	}
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case _, ok := <-item.ClickedCh:
				if !ok {
					return
				}
				fn()
			}
		}
	}()
}
