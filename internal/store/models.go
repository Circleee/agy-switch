package store

// Config 保存全局代理与启动器配置
type Config struct {
	ProxyEnabled    bool   `json:"proxy_enabled"`
	ProxyURL        string `json:"proxy_url"`
	SkipPermissions bool   `json:"skip_permissions"`
	DefaultWorkDir  string `json:"default_work_dir"`
	PreferredTerm   string `json:"preferred_term"` // auto / wt / pwsh / powershell / cmd / terminal / iterm
	AutoStart       bool   `json:"auto_start"`
	WarnOnRunning   bool   `json:"warn_on_running"`
}

// DefaultConfig 返回开箱即用的默认配置（对齐原 agyc 习惯）
func DefaultConfig() Config {
	return Config{
		ProxyEnabled:    true,
		ProxyURL:        "http://127.0.0.1:7897",
		SkipPermissions: true,
		DefaultWorkDir:  "",
		PreferredTerm:   "auto",
		AutoStart:       false,
		WarnOnRunning:   true,
	}
}

// AgyTokenInner 对应 antigravity-oauth-token 内部 token 对象
type AgyTokenInner struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"`
	ExpiresIn    int64  `json:"expires_in"`
}

// AgyTokenPayload 对应 OS Keyring (gemini:antigravity)、antigravity-oauth-token 与 jetski-standalone-oauth-token
type AgyTokenPayload struct {
	Token        AgyTokenInner `json:"token"`
	AuthMethod   string        `json:"auth_method"`
	IDTokenEmail string        `json:"id_token_email"`
	IDToken      string        `json:"id_token"`
}

// GeminiOAuthCreds 对应 ~/.gemini/oauth_creds.json
type GeminiOAuthCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiryDate   int64  `json:"expiry_date"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope"`
}

// GoogleAccountsFile 对应 ~/.gemini/google_accounts.json
type GoogleAccountsFile struct {
	Active string   `json:"active"`
	Old    []string `json:"old"`
}

// CredentialBundle 封装单个账号完整的 5 路同源凭证快照
type CredentialBundle struct {
	AgyToken       AgyTokenPayload    `json:"agy_token"`
	OAuthCreds     GeminiOAuthCreds   `json:"oauth_creds"`
	GoogleAccounts GoogleAccountsFile `json:"google_accounts"`
}

// QuotaBucket 表示单个时间窗口配额桶 (5h 或 weekly)
type QuotaBucket struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       string  `json:"description,omitempty"`
	Window            string  `json:"window"`             // "5h" | "weekly"
	RemainingFraction float64 `json:"remaining_fraction"` // 0.0 ~ 1.0
	ResetTime         string  `json:"reset_time"`         // RFC3339 UTC
}

// QuotaGroup 表示一组模型共享配额 (如 Gemini Models / Claude and GPT models)
type QuotaGroup struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Buckets     []QuotaBucket `json:"buckets"`
}

// AccountQuota 缓存单个账号最近一次配额快照
type AccountQuota struct {
	Groups           []QuotaGroup `json:"groups"`
	SubscriptionTier string       `json:"subscription_tier,omitempty"`
	RawTier          string       `json:"raw_tier,omitempty"`
	IsForbidden      bool         `json:"is_forbidden,omitempty"`
	ForbiddenReason  string       `json:"forbidden_reason,omitempty"`
	LastUpdated      int64        `json:"last_updated"` // Unix 秒
	Source           string       `json:"source"`       // "live" | "cache" | "imported"
}

// Account 表示保存在本地保险箱中的账号元数据
type Account struct {
	ID        string        `json:"id"`
	Email     string        `json:"email"`
	Alias     string        `json:"alias"`
	Disabled  bool          `json:"disabled"`
	CreatedAt int64         `json:"created_at"`
	LastUsed  int64         `json:"last_used"`
	Quota     *AccountQuota `json:"quota,omitempty"`
}

// State 表示 ~/.agy-switch/state.json 顶层持久化结构
type State struct {
	Version          int       `json:"version"`
	CurrentAccountID string    `json:"current_account_id"`
	Config           Config    `json:"config"`
	Accounts         []Account `json:"accounts"`
}
