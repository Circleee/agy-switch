package tray

import (
	"fmt"

	"agy-switch/internal/agy"
	"agy-switch/internal/store"
)

// Actions 定义系统托盘菜单点击时触发的回调集合
type Actions struct {
	OnShowPanel         func()
	OnSwitchAccount     func(accountID string)
	OnLaunchAgy         func(continueSession bool)
	OnRefreshCurrent    func()
	OnRefreshAll        func()
	OnStartLogin        func()
	OnToggleProxy       func()
	OnQuit              func()
	LoadCurrentSnapshot func() *store.State
}

func formatShortQuota(q *store.AccountQuota) string {
	sum := agy.SummarizeQuota(q)
	if !sum.HasData {
		return ""
	}
	parts := []string{}
	if sum.Gemini5hPct >= 0 {
		parts = append(parts, fmt.Sprintf("G:%d%%", sum.Gemini5hPct))
	}
	if sum.Claude5hPct >= 0 {
		parts = append(parts, fmt.Sprintf("C:%d%%", sum.Claude5hPct))
	}
	if len(parts) == 0 {
		return ""
	}
	res := parts[0]
	for i := 1; i < len(parts); i++ {
		res += " " + parts[i]
	}
	return res
}

func buildTooltipText(st *store.State) string {
	if st == nil {
		return "AGY Switch - Antigravity 账号切换与代理启动器"
	}
	var activeAcc *store.Account
	for i := range st.Accounts {
		if st.Accounts[i].ID == st.CurrentAccountID {
			activeAcc = &st.Accounts[i]
			break
		}
	}
	tooltip := "AGY Switch"
	if activeAcc != nil {
		label := activeAcc.Email
		if activeAcc.Alias != "" {
			label = fmt.Sprintf("%s (%s)", activeAcc.Email, activeAcc.Alias)
		}
		tooltip += "\n当前: " + label
		if qTag := formatShortQuota(activeAcc.Quota); qTag != "" {
			tooltip += " (" + qTag + ")"
		}
	} else {
		tooltip += "\n未激活账号"
	}
	if st.Config.ProxyEnabled {
		tooltip += "\n代理: " + st.Config.ProxyURL
	} else {
		tooltip += "\n代理: 直连"
	}
	return tooltip
}
