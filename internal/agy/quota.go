package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"agy-switch/internal/creds"
	"agy-switch/internal/store"
)

type usageCLIOutput struct {
	Status  string `json:"status"`
	Command struct {
		Name string `json:"name"`
		Data struct {
			Description string             `json:"description"`
			Groups      []store.QuotaGroup `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// ParseUsageJSON 解析正版 `agy --print /usage --output-format json` 的标准输出
func ParseUsageJSON(rawOutput string) (*store.AccountQuota, error) {
	lines := strings.Split(rawOutput, "\n")
	var jsonLine string
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			jsonLine = trimmed
			break
		}
	}
	if jsonLine == "" {
		// 兼容多行格式化 JSON 输出
		idx := strings.Index(rawOutput, "{")
		lastIdx := strings.LastIndex(rawOutput, "}")
		if idx >= 0 && lastIdx > idx {
			jsonLine = rawOutput[idx : lastIdx+1]
		}
	}
	if jsonLine == "" {
		return nil, errors.New("未在 agy /usage 输出中找到有效的 JSON 数据")
	}

	var parsed usageCLIOutput
	if err := json.Unmarshal([]byte(jsonLine), &parsed); err != nil {
		return nil, fmt.Errorf("解析 agy /usage JSON 失败: %w", err)
	}
	if !strings.EqualFold(parsed.Status, "SUCCESS") || len(parsed.Command.Data.Groups) == 0 {
		return nil, fmt.Errorf("agy /usage 返回非成功状态 (%s)", parsed.Status)
	}

	return &store.AccountQuota{
		Groups:      parsed.Command.Data.Groups,
		LastUpdated: time.Now().Unix(),
		Source:      "live",
	}, nil
}

// QueryLiveQuota 调用本机正版 agy 二进制实时查询当前激活账号的官方配额
func QueryLiveQuota(ctx context.Context, cfg store.Config) (*store.AccountQuota, error) {
	agyBin, err := FindAgyBinary()
	if err != nil {
		return nil, err
	}
	qCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(qCtx, agyBin, "--print", "/usage", "--output-format", "json", "--print-timeout", "25s")
	cmd.Env = BuildEnvWithProxy(cfg)
	hideBackgroundWindow(cmd)

	out, err := cmd.CombinedOutput()
	parsed, parseErr := ParseUsageJSON(string(out))
	if parseErr == nil {
		return parsed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("执行 agy /usage 失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil, parseErr
}

// QuotaProgress 报告全量刷新进度
type QuotaProgress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Email   string `json:"email"`
	Stage   string `json:"stage"` // "querying" | "restoring" | "done"
	Error   string `json:"error,omitempty"`
}

// RefreshCurrentQuota 实时刷新当前激活账号的配额（无需切号，最安全快速）
func RefreshCurrentQuota(ctx context.Context, st *store.Store, syncer *creds.Syncer) (*store.AccountQuota, error) {
	state, err := st.LoadState()
	if err != nil {
		return nil, err
	}
	// 先确保当前系统激活账号已同步入库
	activeAcc, _ := syncer.SyncBackActive(st)
	targetID := state.CurrentAccountID
	if activeAcc != nil {
		targetID = activeAcc.ID
	}
	if targetID == "" {
		return nil, errors.New("当前没有已激活的账号")
	}

	q, err := QueryLiveQuota(ctx, state.Config)
	if err != nil {
		return nil, err
	}
	// 保留已有的套餐名称信息
	for _, acc := range state.Accounts {
		if acc.ID == targetID && acc.Quota != nil {
			if q.SubscriptionTier == "" {
				q.SubscriptionTier = acc.Quota.SubscriptionTier
				q.RawTier = acc.Quota.RawTier
			}
			break
		}
	}
	if err := st.UpdateQuota(targetID, q); err != nil {
		return nil, err
	}
	return q, nil
}

// RefreshAllQuotas 串行切换所有正常账号并调用正版 agy /usage 拉取配额，最后自动恢复原激活账号
func RefreshAllQuotas(ctx context.Context, st *store.Store, syncer *creds.Syncer, onProgress func(QuotaProgress)) error {
	_, _ = syncer.SyncBackActive(st)
	state, err := st.LoadState()
	if err != nil {
		return err
	}
	if len(state.Accounts) == 0 {
		return errors.New("当前没有已保存的账号")
	}

	origID := state.CurrentAccountID
	rawBackup := syncer.SnapshotRaw()

	// 把原激活账号排在最后查询，既少切一次，又能在结束时自然停留在原账号
	var ordered []store.Account
	var currentTail *store.Account
	for _, acc := range state.Accounts {
		if acc.Disabled {
			continue
		}
		if acc.ID == origID {
			c := acc
			currentTail = &c
		} else {
			ordered = append(ordered, acc)
		}
	}
	if currentTail != nil {
		ordered = append(ordered, *currentTail)
	}
	if len(ordered) == 0 {
		return errors.New("没有可查询的启用账号")
	}

	lastSwitchedID := origID
	defer func() {
		if origID != "" && lastSwitchedID != origID {
			if onProgress != nil {
				onProgress(QuotaProgress{Current: len(ordered), Total: len(ordered), Stage: "restoring"})
			}
			if _, err := syncer.SwitchTo(st, origID); err != nil {
				_ = syncer.RestoreRaw(rawBackup)
			}
		}
	}()

	total := len(ordered)
	for i, acc := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if onProgress != nil {
			onProgress(QuotaProgress{Current: i + 1, Total: total, Email: acc.Email, Stage: "querying"})
		}
		if _, err := syncer.SwitchTo(st, acc.ID); err != nil {
			if onProgress != nil {
				onProgress(QuotaProgress{Current: i + 1, Total: total, Email: acc.Email, Stage: "querying", Error: err.Error()})
			}
			continue
		}
		lastSwitchedID = acc.ID

		q, qErr := QueryLiveQuota(ctx, state.Config)
		if qErr != nil {
			if onProgress != nil {
				onProgress(QuotaProgress{Current: i + 1, Total: total, Email: acc.Email, Stage: "querying", Error: qErr.Error()})
			}
			continue
		}
		if acc.Quota != nil && q.SubscriptionTier == "" {
			q.SubscriptionTier = acc.Quota.SubscriptionTier
			q.RawTier = acc.Quota.RawTier
		}
		_ = st.UpdateQuota(acc.ID, q)
		// 每次查询后把正版 agy 可能刷新过的最新 Token 顺手回存
		_, _ = syncer.SyncBackActive(st)
	}

	if onProgress != nil {
		onProgress(QuotaProgress{Current: total, Total: total, Stage: "done"})
	}
	return nil
}

// QuotaSummary 提取用于托盘菜单展示的简短百分比摘要
type QuotaSummary struct {
	HasData     bool
	Gemini5hPct int
	GeminiWkPct int
	Claude5hPct int
	ClaudeWkPct int
}

// SummarizeQuota 提取 Gemini 与 Claude/GPT 组的 5h / 周剩余百分比（无数据时为 -1）
func SummarizeQuota(q *store.AccountQuota) QuotaSummary {
	sum := QuotaSummary{
		Gemini5hPct: -1,
		GeminiWkPct: -1,
		Claude5hPct: -1,
		ClaudeWkPct: -1,
	}
	if q == nil || len(q.Groups) == 0 {
		return sum
	}
	sum.HasData = true
	for _, g := range q.Groups {
		nameLower := strings.ToLower(g.Name)
		isGemini := strings.Contains(nameLower, "gemini")
		isClaude := strings.Contains(nameLower, "claude") || strings.Contains(nameLower, "gpt")
		for _, b := range g.Buckets {
			idLower := strings.ToLower(b.ID)
			pct := int(math.Round(b.RemainingFraction * 100))
			if isGemini || strings.HasPrefix(idLower, "gemini-") {
				if b.Window == "weekly" || strings.Contains(idLower, "weekly") {
					sum.GeminiWkPct = pct
				} else {
					sum.Gemini5hPct = pct
				}
			} else if isClaude || strings.HasPrefix(idLower, "3p-") {
				if b.Window == "weekly" || strings.Contains(idLower, "weekly") {
					sum.ClaudeWkPct = pct
				} else {
					sum.Claude5hPct = pct
				}
			}
		}
	}
	return sum
}
