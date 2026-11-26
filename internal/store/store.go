package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrAccountNotFound  = errors.New("未找到匹配的账号")
	ErrAmbiguousAccount = errors.New("匹配到多个账号，请输入更精确的邮箱或别名")
)

// Store 管理 ~/.agy-switch 目录下的状态与多账号凭证保险箱
type Store struct {
	mu      sync.RWMutex
	baseDir string
}

// DefaultBaseDir 返回默认的 ~/.agy-switch 路径
func DefaultBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return filepath.Join(home, ".agy-switch"), nil
}

// New 创建并初始化 Store
func New(baseDir string) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		var err error
		baseDir, err = DefaultBaseDir()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "accounts"), 0o700); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}
	s := &Store{baseDir: baseDir}
	if _, err := os.Stat(s.statePath()); errors.Is(err, os.ErrNotExist) {
		initState := State{
			Version:  1,
			Config:   DefaultConfig(),
			Accounts: []Account{},
		}
		if err := s.writeStateUnlocked(&initState); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) statePath() string {
	return filepath.Join(s.baseDir, "state.json")
}

func (s *Store) bundlePath(accountID string) string {
	return filepath.Join(s.baseDir, "accounts", accountID, "bundle.json")
}

// AtomicWriteFile 使用同目录临时文件 + Rename 实现跨平台原子写入
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建父目录失败 (%s): %w", dir, err)
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmpPath := fmt.Sprintf("%s.tmp.%s", path, hex.EncodeToString(rnd[:]))

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("创建临时文件失败 (%s): %w", tmpPath, err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("写入临时文件失败: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("落盘同步失败: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("关闭临时文件失败: %w", closeErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("原子替换文件失败 (%s): %w", path, err)
	}
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) loadStateUnlocked() (*State, error) {
	raw, err := os.ReadFile(s.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			st := &State{Version: 1, Config: DefaultConfig(), Accounts: []Account{}}
			return st, nil
		}
		return nil, fmt.Errorf("读取 state.json 失败: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("解析 state.json 失败: %w", err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	if strings.TrimSpace(st.Config.ProxyURL) == "" {
		st.Config.ProxyURL = DefaultConfig().ProxyURL
	}
	if strings.TrimSpace(st.Config.PreferredTerm) == "" {
		st.Config.PreferredTerm = "auto"
	}
	if st.Accounts == nil {
		st.Accounts = []Account{}
	}
	return &st, nil
}

func (s *Store) writeStateUnlocked(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 state.json 失败: %w", err)
	}
	return AtomicWriteFile(s.statePath(), data, 0o600)
}

// LoadState 返回当前状态副本
func (s *Store) LoadState() (*State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadStateUnlocked()
}

// UpdateConfig 更新全局配置
func (s *Store) UpdateConfig(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadStateUnlocked()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.ProxyURL) == "" {
		cfg.ProxyURL = DefaultConfig().ProxyURL
	}
	if strings.TrimSpace(cfg.PreferredTerm) == "" {
		cfg.PreferredTerm = "auto"
	}
	st.Config = cfg
	return s.writeStateUnlocked(st)
}

// SaveBundle 保存单个账号的凭证快照包
func (s *Store) SaveBundle(accountID string, bundle *CredentialBundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBundleUnlocked(accountID, bundle)
}

func (s *Store) saveBundleUnlocked(accountID string, bundle *CredentialBundle) error {
	if bundle == nil {
		return errors.New("凭证快照不能为空")
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化凭证快照失败: %w", err)
	}
	return AtomicWriteFile(s.bundlePath(accountID), data, 0o600)
}

// LoadBundle 读取单个账号的凭证快照包
func (s *Store) LoadBundle(accountID string) (*CredentialBundle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := os.ReadFile(s.bundlePath(accountID))
	if err != nil {
		return nil, fmt.Errorf("读取账号凭证快照失败 (%s): %w", accountID, err)
	}
	var b CredentialBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("解析账号凭证快照失败 (%s): %w", accountID, err)
	}
	return &b, nil
}

// UpsertAccount 按邮箱新增或更新账号及凭证快照
func (s *Store) UpsertAccount(email, alias string, bundle *CredentialBundle, quota *AccountQuota, setCurrent bool) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, errors.New("账号邮箱不能为空")
	}
	st, err := s.loadStateUnlocked()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	var target *Account
	for i := range st.Accounts {
		if strings.EqualFold(st.Accounts[i].Email, email) {
			if strings.TrimSpace(alias) != "" && st.Accounts[i].Alias == "" {
				st.Accounts[i].Alias = strings.TrimSpace(alias)
			}
			if quota != nil {
				st.Accounts[i].Quota = quota
			}
			if setCurrent {
				st.Accounts[i].LastUsed = now
			}
			target = &st.Accounts[i]
			break
		}
	}

	if target == nil {
		acc := Account{
			ID:        newID(),
			Email:     email,
			Alias:     strings.TrimSpace(alias),
			CreatedAt: now,
			LastUsed:  now,
			Quota:     quota,
		}
		st.Accounts = append(st.Accounts, acc)
		target = &st.Accounts[len(st.Accounts)-1]
	}

	if bundle != nil {
		if err := s.saveBundleUnlocked(target.ID, bundle); err != nil {
			return nil, err
		}
	}
	if setCurrent || st.CurrentAccountID == "" {
		st.CurrentAccountID = target.ID
	}
	if err := s.writeStateUnlocked(st); err != nil {
		return nil, err
	}
	accCopy := *target
	return &accCopy, nil
}

// ResolveAccount 支持按 1-based 序号、完整邮箱、完整别名、或唯一邮箱/别名前缀查找账号
func (s *Store) ResolveAccount(query string) (*Account, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, err := s.loadStateUnlocked()
	if err != nil {
		return nil, -1, err
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, -1, ErrAccountNotFound
	}

	// 1. 按 ID 精确匹配
	for i, acc := range st.Accounts {
		if acc.ID == q {
			c := acc
			return &c, i, nil
		}
	}

	// 2. 按纯数字序号 (1-based)
	if idx, err := strconv.Atoi(q); err == nil {
		if idx >= 1 && idx <= len(st.Accounts) {
			c := st.Accounts[idx-1]
			return &c, idx - 1, nil
		}
		return nil, -1, fmt.Errorf("%w: 序号 #%d 超出范围 (共 %d 个账号)", ErrAccountNotFound, idx, len(st.Accounts))
	}

	// 3. 完整邮箱或完整别名精确匹配
	for i, acc := range st.Accounts {
		if strings.EqualFold(acc.Email, q) || (acc.Alias != "" && strings.EqualFold(acc.Alias, q)) {
			c := acc
			return &c, i, nil
		}
	}

	// 4. 唯一前缀匹配（别名或邮箱本地部分/全串）
	qLower := strings.ToLower(q)
	var matched []int
	for i, acc := range st.Accounts {
		emailLower := strings.ToLower(acc.Email)
		localPart := strings.SplitN(emailLower, "@", 2)[0]
		aliasLower := strings.ToLower(acc.Alias)
		if strings.HasPrefix(emailLower, qLower) || strings.HasPrefix(localPart, qLower) || (aliasLower != "" && strings.HasPrefix(aliasLower, qLower)) {
			matched = append(matched, i)
		}
	}
	if len(matched) == 1 {
		idx := matched[0]
		c := st.Accounts[idx]
		return &c, idx, nil
	}
	if len(matched) > 1 {
		return nil, -1, ErrAmbiguousAccount
	}
	return nil, -1, fmt.Errorf("%w: %s", ErrAccountNotFound, query)
}

// MarkSwitched 更新当前激活账号 ID 与最近使用时间戳
func (s *Store) MarkSwitched(accountID string) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.loadStateUnlocked()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for i := range st.Accounts {
		if st.Accounts[i].ID == accountID {
			st.Accounts[i].LastUsed = now
			st.CurrentAccountID = accountID
			if err := s.writeStateUnlocked(st); err != nil {
				return nil, err
			}
			c := st.Accounts[i]
			return &c, nil
		}
	}
	return nil, ErrAccountNotFound
}

// UpdateAlias 修改账号别名
func (s *Store) UpdateAlias(accountID, alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadStateUnlocked()
	if err != nil {
		return err
	}
	for i := range st.Accounts {
		if st.Accounts[i].ID == accountID {
			st.Accounts[i].Alias = strings.TrimSpace(alias)
			return s.writeStateUnlocked(st)
		}
	}
	return ErrAccountNotFound
}

// UpdateQuota 更新指定账号的配额缓存
func (s *Store) UpdateQuota(accountID string, quota *AccountQuota) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadStateUnlocked()
	if err != nil {
		return err
	}
	for i := range st.Accounts {
		if st.Accounts[i].ID == accountID {
			st.Accounts[i].Quota = quota
			return s.writeStateUnlocked(st)
		}
	}
	return ErrAccountNotFound
}

// MoveAccount 调整账号排序 (direction: -1 上移, +1 下移)
func (s *Store) MoveAccount(accountID string, direction int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadStateUnlocked()
	if err != nil {
		return err
	}
	idx := -1
	for i := range st.Accounts {
		if st.Accounts[i].ID == accountID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrAccountNotFound
	}
	targetIdx := idx + direction
	if targetIdx < 0 || targetIdx >= len(st.Accounts) {
		return nil
	}
	st.Accounts[idx], st.Accounts[targetIdx] = st.Accounts[targetIdx], st.Accounts[idx]
	return s.writeStateUnlocked(st)
}

// DeleteAccount 从本地保险箱移除账号（不影响系统当前已激活文件）
func (s *Store) DeleteAccount(accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadStateUnlocked()
	if err != nil {
		return err
	}
	next := make([]Account, 0, len(st.Accounts))
	found := false
	for _, acc := range st.Accounts {
		if acc.ID == accountID {
			found = true
			continue
		}
		next = append(next, acc)
	}
	if !found {
		return ErrAccountNotFound
	}
	st.Accounts = next
	if st.CurrentAccountID == accountID {
		if len(st.Accounts) > 0 {
			st.CurrentAccountID = st.Accounts[0].ID
		} else {
			st.CurrentAccountID = ""
		}
	}
	_ = os.RemoveAll(filepath.Join(s.baseDir, "accounts", accountID))
	return s.writeStateUnlocked(st)
}
