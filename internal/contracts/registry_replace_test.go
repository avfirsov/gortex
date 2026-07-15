package contracts

import "testing"

func TestRegistryReplaceFileUpdatesEveryIndex(t *testing.T) {
	reg := NewRegistry()
	oldProvider := Contract{ID: "http::GET::/old", Role: RoleProvider, RepoPrefix: "repo", WorkspaceID: "ws", FilePath: "a.go", SymbolID: "a.go::Old"}
	oldConsumer := Contract{ID: "http::GET::/dep", Role: RoleConsumer, RepoPrefix: "repo", WorkspaceID: "ws", FilePath: "a.go", SymbolID: "a.go::Call"}
	unchanged := Contract{ID: "http::GET::/keep", Role: RoleProvider, RepoPrefix: "repo", WorkspaceID: "ws", FilePath: "b.go", SymbolID: "b.go::Keep"}
	reg.Add(oldProvider)
	reg.Add(oldConsumer)
	reg.Add(unchanged)

	fresh := Contract{ID: "http::POST::/new", Role: RoleProvider, RepoPrefix: "repo", WorkspaceID: "ws", FilePath: "a.go", SymbolID: "a.go::New"}
	reg.ReplaceFile("a.go", []Contract{fresh})

	if got := reg.ByFile("a.go"); len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("replacement file index = %#v", got)
	}
	if got := reg.ByID(oldProvider.ID); len(got) != 0 {
		t.Fatalf("old ID survived replacement: %#v", got)
	}
	if got := reg.BySymbol(oldConsumer.SymbolID); len(got) != 0 {
		t.Fatalf("old symbol survived replacement: %#v", got)
	}
	if got := reg.ByFile("b.go"); len(got) != 1 || got[0].ID != unchanged.ID {
		t.Fatalf("unchanged file was disturbed: %#v", got)
	}
	if got := reg.ByRepo("repo"); len(got) != 2 {
		t.Fatalf("repo index size = %d, want replacement + unchanged", len(got))
	}
	if got := reg.ByWorkspace("ws"); len(got) != 2 {
		t.Fatalf("workspace index size = %d, want replacement + unchanged", len(got))
	}
}
