package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agy-switch/internal/store"
)

func sampleToken(email, refresh, access string, exp time.Time) store.AgyTokenPayload {
	return store.AgyTokenPayload{
		Token: store.AgyTokenInner{
			AccessToken:  access,
			TokenType:    "Bearer",
			RefreshToken: refresh,
			Expiry:       exp.UTC().Format("2006-01-02T15:04:05.000000Z"),
			ExpiresIn:    3599,
		},
		AuthMethod:   "consumer",
		IDTokenEmail: email,
		IDToken:      "",
	}
}

func TestFiveTargetAtomicSwitchAndSyncBack(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.New(filepath.Join(tmp, ".agy-switch"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	geminiRoot := filepath.Join(tmp, ".gemini")
	paths := NewTargetPaths(geminiRoot)
	kr := NewMemoryKeyring()
	syncer := NewSyncer(paths, kr)

	// 保护文件：模拟本机已有的 installation_id，验证切号与清空操作绝不触碰它
	instPath := filepath.Join(geminiRoot, "antigravity-cli", "installation_id")
	if err := store.AtomicWriteFile(instPath, []byte("fixed-device-uuid-1234"), 0o600); err != nil {
		t.Fatalf("write installation_id: %v", err)
	}

	now := time.Now().UTC()
	b1, err := BuildBundle(sampleToken("alice@gmail.com", "ref-alice", "acc-alice-v1", now.Add(10*time.Minute)), "")
	if err != nil {
		t.Fatalf("BuildBundle 1: %v", err)
	}
	b2, err := BuildBundle(sampleToken("bob@gmail.com", "ref-bob", "acc-bob-v1", now.Add(20*time.Minute)), "")
	if err != nil {
		t.Fatalf("BuildBundle 2: %v", err)
	}

	acc1, err := st.UpsertAccount("alice@gmail.com", "alice", b1, nil, true)
	if err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if _, err := st.UpsertAccount("bob@gmail.com", "bob", b2, nil, false); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	// 1. 切换到账号 #1 (alice)
	if _, err := syncer.SwitchTo(st, "1"); err != nil {
		t.Fatalf("SwitchTo 1: %v", err)
	}
	health := syncer.CheckHealth()
	if !health.Consistent || health.ActiveEmail != "alice@gmail.com" {
		t.Fatalf("Expected consistent health for alice, got: %+v", health)
	}

	// 2. 模拟正版 agy 在运行期间自动将 alice 的 access_token 续期到了更新的时间
	refreshedTok := sampleToken("alice@gmail.com", "ref-alice", "acc-alice-v2-refreshed", now.Add(59*time.Minute))
	rawRefreshed, _ := json.MarshalIndent(refreshedTok, "", "  ")
	if err := store.AtomicWriteFile(paths.AgyTokenPath, rawRefreshed, 0o600); err != nil {
		t.Fatalf("simulate refresh write: %v", err)
	}

	// 3. 切换到账号 #2 (bob)，验证 alice 刷新后的 v2 token 已自动回存到保险箱
	if _, err := syncer.SwitchTo(st, "bob"); err != nil {
		t.Fatalf("SwitchTo bob: %v", err)
	}
	health2 := syncer.CheckHealth()
	if !health2.Consistent || health2.ActiveEmail != "bob@gmail.com" {
		t.Fatalf("Expected consistent health for bob, got: %+v", health2)
	}

	savedAlice, err := st.LoadBundle(acc1.ID)
	if err != nil {
		t.Fatalf("LoadBundle alice: %v", err)
	}
	if savedAlice.AgyToken.Token.AccessToken != "acc-alice-v2-refreshed" {
		t.Fatalf("Expected synced-back token 'acc-alice-v2-refreshed', got %q", savedAlice.AgyToken.Token.AccessToken)
	}

	// 4. 验证登录前暂存清空与取消回滚，且 installation_id 纹丝不动
	snap := syncer.SnapshotRaw()
	if err := syncer.ClearActive(); err != nil {
		t.Fatalf("ClearActive: %v", err)
	}
	if instBytes, err := os.ReadFile(instPath); err != nil || string(instBytes) != "fixed-device-uuid-1234" {
		t.Fatalf("installation_id was unexpectedly modified or deleted!")
	}
	if err := syncer.RestoreRaw(snap); err != nil {
		t.Fatalf("RestoreRaw: %v", err)
	}
	healthRestored := syncer.CheckHealth()
	if !healthRestored.Consistent || healthRestored.ActiveEmail != "bob@gmail.com" {
		t.Fatalf("Expected restored health for bob, got: %+v", healthRestored)
	}
}

func TestImportFromAtool(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.New(filepath.Join(tmp, ".agy-switch"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	atoolDir := filepath.Join(tmp, ".antigravity_tools")
	if err := os.MkdirAll(filepath.Join(atoolDir, "accounts"), 0o700); err != nil {
		t.Fatal(err)
	}

	idxJSON := `{
	  "accounts": [
	    {"id": "id-1", "email": "dev1@example.com", "name": "work"}
	  ],
	  "current_agy_account_id": "id-1"
	}`
	accJSON := `{
	  "id": "id-1",
	  "email": "dev1@example.com",
	  "name": "work",
	  "token": {
	    "access_token": "ya29.test",
	    "refresh_token": "1//0gtest",
	    "expires_in": 3599,
	    "expiry_timestamp": 1795698959,
	    "token_type": "Bearer",
	    "email": "dev1@example.com"
	  },
	  "quota": {
	    "subscription_tier": "Pro",
	    "last_updated": 1795690000,
	    "quota_groups": [
	      {
	        "display_name": "Gemini Models",
	        "buckets": [
	          {"bucket_id": "gemini-5h", "window": "5h", "remaining_fraction": 0.85, "reset_time": "2026-11-26T17:00:00Z"}
	        ]
	      }
	    ]
	  }
	}`
	_ = os.WriteFile(filepath.Join(atoolDir, "accounts.json"), []byte(idxJSON), 0o600)
	_ = os.WriteFile(filepath.Join(atoolDir, "accounts", "id-1.json"), []byte(accJSON), 0o600)

	res, err := ImportFromAtool(st, atoolDir)
	if err != nil || res.ImportedCount != 1 {
		t.Fatalf("ImportFromAtool failed: res=%+v err=%v", res, err)
	}
	acc, _, err := st.ResolveAccount("work")
	if err != nil || acc.Email != "dev1@example.com" || acc.Quota == nil || len(acc.Quota.Groups) != 1 {
		t.Fatalf("Unexpected imported account: %+v err=%v", acc, err)
	}
}
