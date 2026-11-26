package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agy-switch/internal/store"
)

type atoolIndex struct {
	Accounts            []atoolIndexEntry `json:"accounts"`
	CurrentAgyAccountID string            `json:"current_agy_account_id"`
	CurrentAccountID    string            `json:"current_account_id"`
}

type atoolIndexEntry struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
	LastUsed int64  `json:"last_used"`
}

type atoolAccountFile struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
	Banned   bool   `json:"banned"`
	Token    struct {
		AccessToken     string `json:"access_token"`
		RefreshToken    string `json:"refresh_token"`
		ExpiresIn       int64  `json:"expires_in"`
		ExpiryTimestamp int64  `json:"expiry_timestamp"`
		TokenType       string `json:"token_type"`
		Email           string `json:"email"`
		IDToken         string `json:"id_token"`
	} `json:"token"`
	Quota *struct {
		SubscriptionTier string `json:"subscription_tier"`
		RawTier          string `json:"raw_tier"`
		IsForbidden      bool   `json:"is_forbidden"`
		ForbiddenReason  string `json:"forbidden_reason"`
		LastUpdated      int64  `json:"last_updated"`
		QuotaGroups      []struct {
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			Buckets     []struct {
				BucketID          string  `json:"bucket_id"`
				ID                string  `json:"id"`
				Window            string  `json:"window"`
				RemainingFraction float64 `json:"remaining_fraction"`
				ResetTime         string  `json:"reset_time"`
				DisplayName       string  `json:"display_name"`
				Description       string  `json:"description"`
			} `json:"buckets"`
		} `json:"quota_groups"`
		Models []struct {
			Name       string  `json:"name"`
			Percentage float64 `json:"percentage"`
			ResetTime  string  `json:"reset_time"`
		} `json:"models"`
	} `json:"quota"`
}

// ImportResult 汇总导入结果
type ImportResult struct {
	ImportedCount int      `json:"imported_count"`
	Emails        []string `json:"emails"`
	SourceDir     string   `json:"source_dir"`
}

// DefaultAtoolDir 返回默认的 ~/.antigravity_tools 路径
func DefaultAtoolDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".antigravity_tools"), nil
}

// ImportFromAtool 只读扫描 ~/.antigravity_tools 并将现有账号与配额缓存无损导入 Store
func ImportFromAtool(st *store.Store, atoolDir string) (*ImportResult, error) {
	if strings.TrimSpace(atoolDir) == "" {
		var err error
		atoolDir, err = DefaultAtoolDir()
		if err != nil {
			return nil, err
		}
	}

	indexPath := filepath.Join(atoolDir, "accounts.json")
	rawIdx, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("未找到 Antigravity Tools 账号索引 (%s): %w", indexPath, err)
	}

	var idx atoolIndex
	if err := json.Unmarshal(rawIdx, &idx); err != nil {
		return nil, fmt.Errorf("解析 accounts.json 失败: %w", err)
	}

	activeID := idx.CurrentAgyAccountID
	if activeID == "" {
		activeID = idx.CurrentAccountID
	}

	res := &ImportResult{
		SourceDir: atoolDir,
		Emails:    []string{},
	}

	for _, entry := range idx.Accounts {
		accPath := filepath.Join(atoolDir, "accounts", entry.ID+".json")
		rawAcc, err := os.ReadFile(accPath)
		if err != nil {
			continue
		}
		var af atoolAccountFile
		if err := json.Unmarshal(rawAcc, &af); err != nil {
			continue
		}

		email := strings.TrimSpace(strings.ToLower(af.Email))
		if email == "" {
			email = strings.TrimSpace(strings.ToLower(entry.Email))
		}
		if email == "" {
			email = strings.TrimSpace(strings.ToLower(af.Token.Email))
		}
		if email == "" || (af.Token.RefreshToken == "" && af.Token.AccessToken == "") {
			continue
		}

		alias := strings.TrimSpace(af.Name)
		if alias == "" {
			alias = strings.TrimSpace(entry.Name)
		}

		expTime := time.Now().UTC().Add(55 * time.Minute)
		if af.Token.ExpiryTimestamp > 0 {
			// 兼容秒级或毫秒级时间戳
			if af.Token.ExpiryTimestamp > 1e12 {
				expTime = time.UnixMilli(af.Token.ExpiryTimestamp).UTC()
			} else {
				expTime = time.Unix(af.Token.ExpiryTimestamp, 0).UTC()
			}
		}
		tokType := af.Token.TokenType
		if tokType == "" {
			tokType = "Bearer"
		}
		expIn := af.Token.ExpiresIn
		if expIn <= 0 {
			expIn = 3599
		}

		payload := store.AgyTokenPayload{
			Token: store.AgyTokenInner{
				AccessToken:  af.Token.AccessToken,
				TokenType:    tokType,
				RefreshToken: af.Token.RefreshToken,
				Expiry:       expTime.Format("2006-01-02T15:04:05.000000Z"),
				ExpiresIn:    expIn,
			},
			AuthMethod:   "consumer",
			IDTokenEmail: email,
			IDToken:      af.Token.IDToken,
		}

		bundle, err := BuildBundle(payload, DefaultOAuthScopes)
		if err != nil {
			continue
		}

		quota := convertAtoolQuota(&af)
		isCurrent := entry.ID == activeID
		if _, err := st.UpsertAccount(email, alias, bundle, quota, isCurrent); err != nil {
			continue
		}
		res.ImportedCount++
		res.Emails = append(res.Emails, email)
	}

	return res, nil
}

func convertAtoolQuota(af *atoolAccountFile) *store.AccountQuota {
	if af == nil || af.Quota == nil {
		return nil
	}
	q := af.Quota
	out := &store.AccountQuota{
		SubscriptionTier: q.SubscriptionTier,
		RawTier:          q.RawTier,
		IsForbidden:      q.IsForbidden,
		ForbiddenReason:  q.ForbiddenReason,
		LastUpdated:      q.LastUpdated,
		Source:           "imported",
		Groups:           []store.QuotaGroup{},
	}

	if len(q.QuotaGroups) > 0 {
		for _, g := range q.QuotaGroups {
			grp := store.QuotaGroup{
				Name:        g.DisplayName,
				Description: g.Description,
				Buckets:     make([]store.QuotaBucket, 0, len(g.Buckets)),
			}
			for _, b := range g.Buckets {
				bid := b.BucketID
				if bid == "" {
					bid = b.ID
				}
				grp.Buckets = append(grp.Buckets, store.QuotaBucket{
					ID:                bid,
					Name:              b.DisplayName,
					Description:       b.Description,
					Window:            b.Window,
					RemainingFraction: b.RemainingFraction,
					ResetTime:         b.ResetTime,
				})
			}
			out.Groups = append(out.Groups, grp)
		}
		return out
	}

	// Fallback: 若旧缓存仅有 models 列表，按 gemini / claude 聚合
	var geminiMin, claudeMin *float64
	var geminiReset, claudeReset string
	for _, m := range q.Models {
		nameLower := strings.ToLower(m.Name)
		frac := m.Percentage / 100.0
		if strings.Contains(nameLower, "gemini") {
			if geminiMin == nil || frac < *geminiMin {
				v := frac
				geminiMin = &v
				geminiReset = m.ResetTime
			}
		} else if strings.Contains(nameLower, "claude") || strings.Contains(nameLower, "gpt") {
			if claudeMin == nil || frac < *claudeMin {
				v := frac
				claudeMin = &v
				claudeReset = m.ResetTime
			}
		}
	}
	if geminiMin != nil {
		out.Groups = append(out.Groups, store.QuotaGroup{
			Name: "Gemini Models",
			Buckets: []store.QuotaBucket{
				{ID: "gemini-5h", Name: "Quota Remaining", Window: "5h", RemainingFraction: *geminiMin, ResetTime: geminiReset},
			},
		})
	}
	if claudeMin != nil {
		out.Groups = append(out.Groups, store.QuotaGroup{
			Name: "Claude and GPT models",
			Buckets: []store.QuotaBucket{
				{ID: "3p-5h", Name: "Quota Remaining", Window: "5h", RemainingFraction: *claudeMin, ResetTime: claudeReset},
			},
		})
	}
	return out
}
