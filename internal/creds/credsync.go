package creds

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agy-switch/internal/store"

	"github.com/zalando/go-keyring"
)

const (
	KeyringService = "gemini"
	KeyringUser    = "antigravity"

	DefaultOAuthScopes = "https://www.googleapis.com/auth/aicode https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile openid"
)

// KeyringProvider 抽象系统原生钥匙串，便于跨平台运行与沙盒单元测试
type KeyringProvider interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

// OSKeyringProvider 调用系统原生安全钥匙串 (Win CredMgr / macOS Keychain / Linux SecretService)
type OSKeyringProvider struct{}

func (OSKeyringProvider) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (OSKeyringProvider) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (OSKeyringProvider) Delete(service, user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MemoryKeyringProvider 纯内存钥匙串（仅供单元测试使用，绝不触碰真实系统凭据）
type MemoryKeyringProvider struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemoryKeyring() *MemoryKeyringProvider {
	return &MemoryKeyringProvider{data: make(map[string]string)}
}

func (m *MemoryKeyringProvider) Get(service, user string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[service+":"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (m *MemoryKeyringProvider) Set(service, user, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[service+":"+user] = secret
	return nil
}

func (m *MemoryKeyringProvider) Delete(service, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, service+":"+user)
	return nil
}

// TargetPaths 定义需要成组原子同步的 4 个本地凭证文件路径
type TargetPaths struct {
	GeminiRoot         string
	AgyTokenPath       string // ~/.gemini/antigravity-cli/antigravity-oauth-token
	JetskiTokenPath    string // ~/.gemini/jetski-standalone-oauth-token
	OAuthCredsPath     string // ~/.gemini/oauth_creds.json
	GoogleAccountsPath string // ~/.gemini/google_accounts.json
}

// DefaultTargetPaths 返回基于用户主目录的标准 ~/.gemini 路径集合
func DefaultTargetPaths() (TargetPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return TargetPaths{}, fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return NewTargetPaths(filepath.Join(home, ".gemini")), nil
}

// NewTargetPaths 根据指定的 .gemini 根目录构造 4 个凭证路径
func NewTargetPaths(geminiRoot string) TargetPaths {
	return TargetPaths{
		GeminiRoot:         geminiRoot,
		AgyTokenPath:       filepath.Join(geminiRoot, "antigravity-cli", "antigravity-oauth-token"),
		JetskiTokenPath:    filepath.Join(geminiRoot, "jetski-standalone-oauth-token"),
		OAuthCredsPath:     filepath.Join(geminiRoot, "oauth_creds.json"),
		GoogleAccountsPath: filepath.Join(geminiRoot, "google_accounts.json"),
	}
}

// Syncer 负责管理 5 路凭证（OS Keyring + 4 个文件）的原子读取、切换、健康检查与备份回滚
type Syncer struct {
	paths   TargetPaths
	keyring KeyringProvider
}

func NewSyncer(paths TargetPaths, kr KeyringProvider) *Syncer {
	if kr == nil {
		kr = OSKeyringProvider{}
	}
	return &Syncer{paths: paths, keyring: kr}
}

func (s *Syncer) Paths() TargetPaths {
	return s.paths
}

// ExtractEmailFromJWT 从 IDToken (JWT) 中安全解析 email 声明（无需联网）
func ExtractEmailFromJWT(jwtStr string) string {
	parts := strings.Split(strings.TrimSpace(jwtStr), ".")
	if len(parts) < 2 {
		return ""
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(claims.Email))
}

func parseExpiryTime(expiry string) time.Time {
	expiry = strings.TrimSpace(expiry)
	if expiry == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z"} {
		if t, err := time.Parse(layout, expiry); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// BuildBundle 根据核心 AgyTokenPayload 派生出完整的 5 路同源凭证包
func BuildBundle(tok store.AgyTokenPayload, scopeHint string) (*store.CredentialBundle, error) {
	if strings.TrimSpace(tok.Token.RefreshToken) == "" && strings.TrimSpace(tok.Token.AccessToken) == "" {
		return nil, errors.New("凭证缺少有效的 refresh_token 或 access_token")
	}
	if strings.TrimSpace(tok.Token.TokenType) == "" {
		tok.Token.TokenType = "Bearer"
	}
	if strings.TrimSpace(tok.AuthMethod) == "" {
		tok.AuthMethod = "consumer"
	}
	email := strings.TrimSpace(strings.ToLower(tok.IDTokenEmail))
	if email == "" {
		email = ExtractEmailFromJWT(tok.IDToken)
	}
	if email == "" {
		return nil, errors.New("无法从凭证中识别账号邮箱 (id_token_email 为空)")
	}
	tok.IDTokenEmail = email

	expTime := parseExpiryTime(tok.Token.Expiry)
	if expTime.IsZero() {
		expTime = time.Now().UTC().Add(55 * time.Minute)
		tok.Token.Expiry = expTime.Format("2006-01-02T15:04:05.000000Z")
	}
	if tok.Token.ExpiresIn <= 0 {
		tok.Token.ExpiresIn = 3599
	}

	scope := strings.TrimSpace(scopeHint)
	if scope == "" {
		scope = DefaultOAuthScopes
	}

	return &store.CredentialBundle{
		AgyToken: tok,
		OAuthCreds: store.GeminiOAuthCreds{
			AccessToken:  tok.Token.AccessToken,
			RefreshToken: tok.Token.RefreshToken,
			TokenType:    tok.Token.TokenType,
			ExpiryDate:   expTime.UnixMilli(),
			IDToken:      tok.IDToken,
			Scope:        scope,
		},
		GoogleAccounts: store.GoogleAccountsFile{
			Active: email,
			Old:    []string{},
		},
	}, nil
}

// CaptureActive 读取当前系统激活的凭证（优先取 Keyring 与主凭证文件中较新的一个）
func (s *Syncer) CaptureActive() (*store.CredentialBundle, error) {
	var krTok *store.AgyTokenPayload
	if rawKr, err := s.keyring.Get(KeyringService, KeyringUser); err == nil && strings.TrimSpace(rawKr) != "" {
		var t store.AgyTokenPayload
		if json.Unmarshal([]byte(rawKr), &t) == nil && (t.Token.RefreshToken != "" || t.Token.AccessToken != "") {
			krTok = &t
		}
	}

	var fileTok *store.AgyTokenPayload
	for _, p := range []string{s.paths.AgyTokenPath, s.paths.JetskiTokenPath} {
		if raw, err := os.ReadFile(p); err == nil && len(raw) > 0 {
			var t store.AgyTokenPayload
			if json.Unmarshal(raw, &t) == nil && (t.Token.RefreshToken != "" || t.Token.AccessToken != "") {
				fileTok = &t
				break
			}
		}
	}

	chosen := krTok
	if chosen == nil {
		chosen = fileTok
	} else if fileTok != nil {
		// 如果同一邮箱在文件里刷新出了更新的过期时间，则采用较新的 Token
		krEmail := strings.ToLower(strings.TrimSpace(krTok.IDTokenEmail))
		fEmail := strings.ToLower(strings.TrimSpace(fileTok.IDTokenEmail))
		if krEmail == fEmail && parseExpiryTime(fileTok.Token.Expiry).After(parseExpiryTime(krTok.Token.Expiry)) {
			chosen = fileTok
		}
	}

	if chosen == nil {
		return nil, errors.New("当前系统未检测到已登录的 agy 凭证")
	}

	var existingScope string
	if rawCreds, err := os.ReadFile(s.paths.OAuthCredsPath); err == nil {
		var oc store.GeminiOAuthCreds
		if json.Unmarshal(rawCreds, &oc) == nil {
			existingScope = oc.Scope
		}
	}
	// 若 google_accounts.json 中有邮箱且 token 中缺失 id_token_email，可兜底补齐
	if strings.TrimSpace(chosen.IDTokenEmail) == "" {
		if rawAcc, err := os.ReadFile(s.paths.GoogleAccountsPath); err == nil {
			var ga store.GoogleAccountsFile
			if json.Unmarshal(rawAcc, &ga) == nil && ga.Active != "" {
				chosen.IDTokenEmail = ga.Active
			}
		}
	}

	return BuildBundle(*chosen, existingScope)
}

// ApplyBundle 将单个账号的 5 路凭证成组原子写入 OS Keyring 与 4 个本地文件
func (s *Syncer) ApplyBundle(bundle *store.CredentialBundle) error {
	if bundle == nil {
		return errors.New("bundle 不能为空")
	}
	normalized, err := BuildBundle(bundle.AgyToken, bundle.OAuthCreds.Scope)
	if err != nil {
		return err
	}

	compactAgy, err := json.Marshal(normalized.AgyToken)
	if err != nil {
		return fmt.Errorf("序列化紧凑 Keyring JSON 失败: %w", err)
	}
	prettyAgy, err := json.MarshalIndent(normalized.AgyToken, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 agy token 文件失败: %w", err)
	}
	prettyOAuth, err := json.MarshalIndent(normalized.OAuthCreds, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 oauth_creds.json 失败: %w", err)
	}
	prettyAccounts, err := json.MarshalIndent(normalized.GoogleAccounts, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 google_accounts.json 失败: %w", err)
	}

	// 1. 写入 OS 原生安全钥匙串 (gemini:antigravity)
	if err := s.keyring.Set(KeyringService, KeyringUser, string(compactAgy)); err != nil {
		return fmt.Errorf("写入系统钥匙串 (gemini:antigravity) 失败: %w", err)
	}

	// 2. 原子写入 4 个本地凭证文件
	writes := []struct {
		path string
		data []byte
	}{
		{s.paths.AgyTokenPath, prettyAgy},
		{s.paths.JetskiTokenPath, prettyAgy},
		{s.paths.OAuthCredsPath, prettyOAuth},
		{s.paths.GoogleAccountsPath, prettyAccounts},
	}
	for _, w := range writes {
		if err := store.AtomicWriteFile(w.path, w.data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// SyncBackActive 将当前激活账号最新刷新过的 Token 自动回存到 Store 保险箱中
func (s *Syncer) SyncBackActive(st *store.Store) (*store.Account, error) {
	bundle, err := s.CaptureActive()
	if err != nil {
		return nil, err
	}
	return st.UpsertAccount(bundle.AgyToken.IDTokenEmail, "", bundle, nil, false)
}

// SwitchTo 执行安全切号：先回存旧激活账号最新 Token，再原子切换 5 路目标凭证
func (s *Syncer) SwitchTo(st *store.Store, queryOrID string) (*store.Account, error) {
	targetAcc, _, err := st.ResolveAccount(queryOrID)
	if err != nil {
		return nil, err
	}
	targetBundle, err := st.LoadBundle(targetAcc.ID)
	if err != nil {
		return nil, err
	}

	// 切走前自动回存当前激活凭证（忽略未登录情况）
	_, _ = s.SyncBackActive(st)

	if err := s.ApplyBundle(targetBundle); err != nil {
		return nil, err
	}
	return st.MarkSwitched(targetAcc.ID)
}

// RawStagingSnapshot 保存登录新号前的原始字节状态，支持取消时 100% 原样回滚
type RawStagingSnapshot struct {
	HasKeyring  bool
	KeyringData string
	Files       map[string][]byte
}

// SnapshotRaw 读取当前 5 路目标的原始字节副本（用于添加新号失败/取消时精确回滚）
func (s *Syncer) SnapshotRaw() *RawStagingSnapshot {
	snap := &RawStagingSnapshot{
		Files: make(map[string][]byte),
	}
	if kr, err := s.keyring.Get(KeyringService, KeyringUser); err == nil {
		snap.HasKeyring = true
		snap.KeyringData = kr
	}
	for _, p := range []string{
		s.paths.AgyTokenPath,
		s.paths.JetskiTokenPath,
		s.paths.OAuthCredsPath,
		s.paths.GoogleAccountsPath,
	} {
		if b, err := os.ReadFile(p); err == nil {
			snap.Files[p] = b
		}
	}
	return snap
}

// RestoreRaw 将 SnapshotRaw 备份的原始状态完整恢复
func (s *Syncer) RestoreRaw(snap *RawStagingSnapshot) error {
	if snap == nil {
		return nil
	}
	if snap.HasKeyring {
		if err := s.keyring.Set(KeyringService, KeyringUser, snap.KeyringData); err != nil {
			return err
		}
	} else {
		_ = s.keyring.Delete(KeyringService, KeyringUser)
	}
	for _, p := range []string{
		s.paths.AgyTokenPath,
		s.paths.JetskiTokenPath,
		s.paths.OAuthCredsPath,
		s.paths.GoogleAccountsPath,
	} {
		if data, ok := snap.Files[p]; ok {
			if err := store.AtomicWriteFile(p, data, 0o600); err != nil {
				return err
			}
		} else {
			_ = os.Remove(p)
		}
	}
	return nil
}

// ClearActive 清空当前 5 路登录凭证槽位（绝不触碰 installation_id / settings.json / 历史库）
func (s *Syncer) ClearActive() error {
	if err := s.keyring.Delete(KeyringService, KeyringUser); err != nil {
		return fmt.Errorf("清理系统钥匙串失败: %w", err)
	}
	for _, p := range []string{
		s.paths.AgyTokenPath,
		s.paths.JetskiTokenPath,
		s.paths.OAuthCredsPath,
		s.paths.GoogleAccountsPath,
	} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("移除凭证文件失败 (%s): %w", p, err)
		}
	}
	return nil
}

// TargetStatus 表示单路凭证目标的诊断状态
type TargetStatus struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Email  string `json:"email"`
}

// HealthReport 汇总全部 5 路凭证的一致性诊断报告
type HealthReport struct {
	ActiveEmail string         `json:"active_email"`
	Consistent  bool           `json:"consistent"`
	Targets     []TargetStatus `json:"targets"`
}

// CheckHealth 检查 5 路凭证是否存在且指向同一账号（不暴露任何敏感 Token）
func (s *Syncer) CheckHealth() HealthReport {
	var targets []TargetStatus
	emails := make(map[string]struct{})

	recordEmail := func(e string) string {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			emails[e] = struct{}{}
		}
		return e
	}

	// 1. OS Keyring
	krStatus := TargetStatus{Name: "OS Keyring (gemini:antigravity)", Path: "keyring://gemini:antigravity"}
	if raw, err := s.keyring.Get(KeyringService, KeyringUser); err == nil && strings.TrimSpace(raw) != "" {
		krStatus.Exists = true
		var t store.AgyTokenPayload
		if json.Unmarshal([]byte(raw), &t) == nil {
			email := t.IDTokenEmail
			if email == "" {
				email = ExtractEmailFromJWT(t.IDToken)
			}
			krStatus.Email = recordEmail(email)
		}
	}
	targets = append(targets, krStatus)

	// 2 & 3. AgyTokenPath & JetskiTokenPath
	for _, item := range []struct{ name, path string }{
		{"agy 主凭证 (antigravity-oauth-token)", s.paths.AgyTokenPath},
		{"独立版兼容凭证 (jetski-standalone-oauth-token)", s.paths.JetskiTokenPath},
	} {
		ts := TargetStatus{Name: item.name, Path: item.path}
		if raw, err := os.ReadFile(item.path); err == nil && len(raw) > 0 {
			ts.Exists = true
			var t store.AgyTokenPayload
			if json.Unmarshal(raw, &t) == nil {
				email := t.IDTokenEmail
				if email == "" {
					email = ExtractEmailFromJWT(t.IDToken)
				}
				ts.Email = recordEmail(email)
			}
		}
		targets = append(targets, ts)
	}

	// 4. oauth_creds.json
	ocStatus := TargetStatus{Name: "Gemini 通用凭证 (oauth_creds.json)", Path: s.paths.OAuthCredsPath}
	if raw, err := os.ReadFile(s.paths.OAuthCredsPath); err == nil && len(raw) > 0 {
		ocStatus.Exists = true
		var oc store.GeminiOAuthCreds
		if json.Unmarshal(raw, &oc) == nil {
			ocStatus.Email = recordEmail(ExtractEmailFromJWT(oc.IDToken))
		}
	}
	targets = append(targets, ocStatus)

	// 5. google_accounts.json
	gaStatus := TargetStatus{Name: "激活账号索引 (google_accounts.json)", Path: s.paths.GoogleAccountsPath}
	if raw, err := os.ReadFile(s.paths.GoogleAccountsPath); err == nil && len(raw) > 0 {
		gaStatus.Exists = true
		var ga store.GoogleAccountsFile
		if json.Unmarshal(raw, &ga) == nil {
			gaStatus.Email = recordEmail(ga.Active)
		}
	}
	targets = append(targets, gaStatus)

	primaryAuthExists := targets[0].Exists || targets[1].Exists
	indexExists := targets[4].Exists

	var activeEmail string
	for e := range emails {
		activeEmail = e
		break
	}

	return HealthReport{
		ActiveEmail: activeEmail,
		Consistent:  primaryAuthExists && indexExists && len(emails) == 1,
		Targets:     targets,
	}
}
