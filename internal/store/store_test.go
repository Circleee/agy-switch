package store

import (
	"path/filepath"
	"testing"
)

func makeDummyBundle(email string) *CredentialBundle {
	var b CredentialBundle
	b.AgyToken.Token.AccessToken = "acc-" + email
	b.AgyToken.Token.RefreshToken = "ref-" + email
	b.AgyToken.Token.TokenType = "Bearer"
	b.AgyToken.AuthMethod = "consumer"
	b.AgyToken.IDTokenEmail = email
	b.GoogleAccounts.Active = email
	return &b
}

func TestStoreCRUDAndResolve(t *testing.T) {
	dir := t.TempDir()
	st, err := New(filepath.Join(dir, ".agy-switch"))
	if err != nil {
		t.Fatalf("New store failed: %v", err)
	}

	a1, err := st.UpsertAccount("work.user@gmail.com", "work", makeDummyBundle("work.user@gmail.com"), nil, true)
	if err != nil {
		t.Fatalf("UpsertAccount 1 failed: %v", err)
	}
	a2, err := st.UpsertAccount("personal.dev@gmail.com", "home", makeDummyBundle("personal.dev@gmail.com"), nil, false)
	if err != nil {
		t.Fatalf("UpsertAccount 2 failed: %v", err)
	}

	// 按序号 1 / 2 查找
	got1, idx1, err := st.ResolveAccount("1")
	if err != nil || idx1 != 0 || got1.ID != a1.ID {
		t.Fatalf("Resolve by index '1' failed: got=%+v idx=%d err=%v", got1, idx1, err)
	}
	got2, idx2, err := st.ResolveAccount("2")
	if err != nil || idx2 != 1 || got2.ID != a2.ID {
		t.Fatalf("Resolve by index '2' failed: got=%+v idx=%d err=%v", got2, idx2, err)
	}

	// 按别名与邮箱前缀查找
	if got, _, err := st.ResolveAccount("home"); err != nil || got.ID != a2.ID {
		t.Fatalf("Resolve by alias 'home' failed: %v", err)
	}
	if got, _, err := st.ResolveAccount("work.u"); err != nil || got.ID != a1.ID {
		t.Fatalf("Resolve by prefix 'work.u' failed: %v", err)
	}

	// 排序移动与删除
	if err := st.MoveAccount(a2.ID, -1); err != nil {
		t.Fatalf("MoveAccount failed: %v", err)
	}
	if got, _, err := st.ResolveAccount("1"); err != nil || got.ID != a2.ID {
		t.Fatalf("Expected a2 at index 1 after move, got=%+v err=%v", got, err)
	}

	if err := st.DeleteAccount(a2.ID); err != nil {
		t.Fatalf("DeleteAccount failed: %v", err)
	}
	state, err := st.LoadState()
	if err != nil || len(state.Accounts) != 1 || state.CurrentAccountID != a1.ID {
		t.Fatalf("Unexpected state after delete: %+v err=%v", state, err)
	}
}
