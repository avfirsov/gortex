package graph

import "testing"

func TestFindEdgesByIdentitiesUsesCompleteLogicalKeyAndCurrentPayload(t *testing.T) {
	store := New()
	first := &Edge{
		From: "repo:a", To: "repo:b", Kind: EdgeCalls,
		FilePath: "repo/a.go", Line: 17,
		Confidence: 0.4, Origin: "parser", Meta: map[string]any{"version": "old"},
	}
	sameSite := &Edge{
		From: "repo:a", To: "repo:c", Kind: EdgeCalls,
		FilePath: "repo/a.go", Line: 17,
		Confidence: 0.8, Origin: "lsp", Meta: map[string]any{"version": "other"},
	}
	store.AddEdge(first)
	store.AddEdge(sameSite)

	firstKey := EdgeIdentityFor(first)
	sameSiteKey := EdgeIdentityFor(sameSite)
	found := store.FindEdgesByIdentities([]EdgeIdentity{sameSiteKey, sameSiteKey})
	if len(found) != 1 {
		t.Fatalf("exact lookup returned %d edges, want 1", len(found))
	}
	if found[sameSiteKey] != sameSite {
		t.Fatalf("exact lookup returned wrong same-site edge: %#v", found[sameSiteKey])
	}
	if found[firstKey] != nil {
		t.Fatalf("exact lookup leaked unrequested same-site edge: %#v", found[firstKey])
	}

	if !store.RemoveEdge(first.From, first.To, first.Kind) {
		t.Fatal("RemoveEdge did not remove old payload")
	}
	replacement := &Edge{
		From: first.From, To: first.To, Kind: first.Kind,
		FilePath: first.FilePath, Line: first.Line,
		Confidence: 0.95, ConfidenceLabel: "high", Origin: "watcher",
		Tier: "confirmed", CrossRepo: true, Meta: map[string]any{"version": "new"},
	}
	store.AddEdge(replacement)

	found = store.FindEdgesByIdentities([]EdgeIdentity{firstKey, sameSiteKey})
	if len(found) != 2 {
		t.Fatalf("exact lookup after replacement returned %d edges, want 2", len(found))
	}
	if found[firstKey] != replacement {
		t.Fatalf("exact lookup returned stale same-key payload: %#v", found[firstKey])
	}
	if got := found[firstKey].Meta["version"]; got != "new" {
		t.Fatalf("replacement metadata = %v, want new", got)
	}
}
