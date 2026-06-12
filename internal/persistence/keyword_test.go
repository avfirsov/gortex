package persistence

import (
	"path/filepath"
	"testing"
)

func TestKeywordStore_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kw")

	// A missing file loads as an empty store, not an error.
	loaded, err := LoadKeyword(dir)
	if err != nil {
		t.Fatalf("LoadKeyword on a missing dir errored: %v", err)
	}
	if loaded == nil || len(loaded.Keywords) != 0 {
		t.Fatalf("missing-file load should yield an empty store")
	}

	store := &KeywordStore{
		Version:  "1",
		RepoPath: "/repo",
		Keywords: []KeywordAssoc{
			{Keyword: "auth", Matches: []KeywordMatch{
				{SymbolID: "pkg::LoginService", HitCount: 5, LastUsed: 1700},
			}},
			{Keyword: "token", Matches: []KeywordMatch{
				{SymbolID: "pkg::ParseJWT", HitCount: 3, LastUsed: 1701},
			}},
		},
	}
	if err := SaveKeyword(dir, store); err != nil {
		t.Fatalf("SaveKeyword errored: %v", err)
	}
	back, err := LoadKeyword(dir)
	if err != nil {
		t.Fatalf("LoadKeyword after save errored: %v", err)
	}
	if len(back.Keywords) != 2 {
		t.Fatalf("round-trip lost keywords: got %d, want 2", len(back.Keywords))
	}
	if back.Keywords[0].Keyword != "auth" ||
		back.Keywords[0].Matches[0].SymbolID != "pkg::LoginService" ||
		back.Keywords[0].Matches[0].HitCount != 5 {
		t.Errorf("round-trip corrupted the first keyword: %+v", back.Keywords[0])
	}
}

func TestKeywordStore_TrimsOverCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kw")
	store := &KeywordStore{}
	for i := 0; i < maxKeywords+50; i++ {
		store.Keywords = append(store.Keywords, KeywordAssoc{Keyword: string(rune('a' + i%26))})
	}
	if err := SaveKeyword(dir, store); err != nil {
		t.Fatalf("SaveKeyword errored: %v", err)
	}
	if len(store.Keywords) != maxKeywords {
		t.Errorf("SaveKeyword should trim to %d keywords, got %d", maxKeywords, len(store.Keywords))
	}
}

func TestMaxKeywordEntries_TighterThanCombo(t *testing.T) {
	if MaxKeywordEntries() >= MaxComboEntries() {
		t.Errorf("MaxKeywordEntries (%d) should be tighter than MaxComboEntries (%d)",
			MaxKeywordEntries(), MaxComboEntries())
	}
}
