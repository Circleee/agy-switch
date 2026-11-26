//go:build windows

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	shell32                = windows.NewLazySystemDLL("shell32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procPostQuitMessage    = user32.NewProc("PostQuitMessage")
	procLoadImageW         = user32.NewProc("LoadImageW")
	procCreatePopupMenu    = user32.NewProc("CreatePopupMenu")
	procAppendMenuW        = user32.NewProc("AppendMenuW")
	procDestroyMenu        = user32.NewProc("DestroyMenu")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu     = user32.NewProc("TrackPopupMenu")
	procRegisterWindowMsgW = user32.NewProc("RegisterWindowMessageW")
	procShellNotifyIconW   = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmNull          = 0x0000
	wmUser          = 0x0400
	wmTrayCallback  = wmUser + 0x20
	wmUpdateTip     = wmUser + 0x21
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfChecked   = 0x00000008
	mfSeparator = 0x00000800

	tpmLeftAlign   = 0x0000
	tpmBottomAlign = 0x0020
	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100
)

// notifyIconDataW 严格对齐 Win32 C shellapi.h NOTIFYICONDATAW (x64 下精确 976 字节，兼容 ComCtl32 v6 Manifest)
type notifyIconDataW struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutVersion  uint32 // C union { UINT uTimeout; UINT uVersion; } 占 4 字节
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type winPoint struct {
	X, Y int32
}

type winMsg struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
}

// Manager 原生 Win32 系统托盘管理器（无 CGO、精确结构体对齐、即时弹出菜单）
type Manager struct {
	actions          Actions
	mu               sync.Mutex
	hwnd             windows.Handle
	hIcon            windows.Handle
	wmTaskbarCreated uint32
	stopped          bool
}

func NewManager(actions Actions) *Manager {
	return &Manager{actions: actions}
}

func (m *Manager) Start() {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		m.runLoop()
	}()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	hwnd := m.hwnd
	already := m.stopped
	m.stopped = true
	m.mu.Unlock()
	if hwnd != 0 && !already {
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
	}
}

func (m *Manager) RebuildMenu() {
	m.mu.Lock()
	hwnd := m.hwnd
	m.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmUpdateTip, 0, 0)
	}
}

func (m *Manager) runLoop() {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	clsName, _ := windows.UTF16PtrFromString("AGYSwitchTrayHiddenWnd")
	wndName, _ := windows.UTF16PtrFromString("AGYSwitchTray")

	tbMsgName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	tbMsg, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(tbMsgName)))
	m.wmTaskbarCreated = uint32(tbMsg)

	wcex := wndClassExW{
		WndProc:   windows.NewCallback(m.wndProc),
		Instance:  windows.Handle(hInst),
		ClassName: clsName,
	}
	wcex.Size = uint32(unsafe.Sizeof(wcex))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(wndName)),
		0,
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return
	}

	icoPath := filepath.Join(os.TempDir(), "agy_switch_tray_logo_v4.ico")
	_ = os.WriteFile(icoPath, IconBytes(), 0o644)
	icoPathPtr, _ := windows.UTF16PtrFromString(icoPath)
	hIcon, _, _ := procLoadImageW.Call(
		0,
		uintptr(unsafe.Pointer(icoPathPtr)),
		imageIcon,
		0, 0,
		lrLoadFromFile|lrDefaultSize,
	)

	m.mu.Lock()
	m.hwnd = windows.Handle(hwnd)
	m.hIcon = windows.Handle(hIcon)
	m.mu.Unlock()

	m.addOrModifyTrayIcon(nimAdd)

	var msg winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (m *Manager) addOrModifyTrayIcon(action uintptr) {
	m.mu.Lock()
	hwnd := m.hwnd
	hIcon := m.hIcon
	m.mu.Unlock()
	if hwnd == 0 {
		return
	}

	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	nid.UFlags = nifMessage | nifTip
	nid.UCallbackMessage = wmTrayCallback
	if hIcon != 0 {
		nid.HIcon = hIcon
		nid.UFlags |= nifIcon
	}

	tip := "AGY Switch"
	if m.actions.LoadCurrentSnapshot != nil {
		tip = buildTooltipText(m.actions.LoadCurrentSnapshot())
	}
	u16Tip, _ := windows.UTF16FromString(tip)
	if len(u16Tip) > 127 {
		u16Tip = append(u16Tip[:127], 0)
	}
	copy(nid.SzTip[:], u16Tip)

	procShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&nid)))
}

func (m *Manager) deleteTrayIcon() {
	m.mu.Lock()
	hwnd := m.hwnd
	m.mu.Unlock()
	if hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (m *Manager) wndProc(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTrayCallback:
		event := uint32(lParam & 0xFFFF)
		switch event {
		case wmLButtonUp, wmLButtonDblClk:
			if m.actions.OnShowPanel != nil {
				go m.actions.OnShowPanel()
			}
		case wmRButtonUp, wmContextMenu:
			m.showPopupMenu(hwnd)
		}
		return 0

	case wmUpdateTip:
		m.addOrModifyTrayIcon(nimModify)
		return 0

	case wmClose:
		m.deleteTrayIcon()
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		m.deleteTrayIcon()
		procPostQuitMessage.Call(0)
		return 0
	}

	if m.wmTaskbarCreated != 0 && msg == m.wmTaskbarCreated {
		m.addOrModifyTrayIcon(nimAdd)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func appendMenuText(hMenu uintptr, flags uintptr, id uintptr, text string) {
	ptr, _ := windows.UTF16PtrFromString(text)
	procAppendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(ptr)))
}

func (m *Manager) showPopupMenu(hwnd windows.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	callbacks := make(map[uint32]func())
	nextID := uint32(1000)
	reg := func(flags uintptr, text string, fn func()) {
		nextID++
		id := nextID
		if fn != nil {
			callbacks[id] = fn
		}
		appendMenuText(hMenu, flags, uintptr(id), text)
	}

	var st = m.actions.LoadCurrentSnapshot()
	if st == nil {
		reg(mfString, "打开面板", m.actions.OnShowPanel)
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
		reg(mfString, "退出", m.actions.OnQuit)
	} else {
		if len(st.Accounts) == 0 {
			reg(mfString|mfGrayed, "暂无账号", nil)
		} else {
			for idx, acc := range st.Accounts {
				accID := acc.ID
				title := fmt.Sprintf("%d. %s", idx+1, acc.Email)
				if acc.Alias != "" {
					title = fmt.Sprintf("%d. %s (%s)", idx+1, acc.Email, acc.Alias)
				}
				if qShort := formatShortQuota(acc.Quota); qShort != "" {
					title += " · " + qShort
				}
				flags := uintptr(mfString)
				if accID == st.CurrentAccountID {
					flags |= mfChecked
				}
				reg(flags, title, func() {
					if m.actions.OnSwitchAccount != nil {
						m.actions.OnSwitchAccount(accID)
					}
				})
			}
		}
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

		reg(mfString, "启动 agy", func() {
			if m.actions.OnLaunchAgy != nil {
				m.actions.OnLaunchAgy(false)
			}
		})
		reg(mfString, "继续对话 (-c)", func() {
			if m.actions.OnLaunchAgy != nil {
				m.actions.OnLaunchAgy(true)
			}
		})
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

		reg(mfString, "刷新当前余量", m.actions.OnRefreshCurrent)
		reg(mfString, "刷新全部余量", m.actions.OnRefreshAll)
		reg(mfString, "添加账号...", m.actions.OnStartLogin)
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

		proxyFlags := uintptr(mfString)
		if st.Config.ProxyEnabled {
			proxyFlags |= mfChecked
		}
		reg(proxyFlags, fmt.Sprintf("代理 %s", st.Config.ProxyURL), m.actions.OnToggleProxy)
		reg(mfString, "打开面板...", m.actions.OnShowPanel)
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

		reg(mfString, "退出", m.actions.OnQuit)
	}

	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWin.Call(uintptr(hwnd))
	cmdID, _, _ := procTrackPopupMenu.Call(
		hMenu,
		tpmLeftAlign|tpmBottomAlign|tpmRightButton|tpmReturnCmd|tpmNonotify,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(hwnd),
		0,
	)
	procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)

	if cmdID != 0 {
		if fn, ok := callbacks[uint32(cmdID)]; ok && fn != nil {
			go fn()
		}
	}
}
