package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestResolveRustScopeCallsGenericTraitBound(t *testing.T) {
	tests := []struct {
		name       string
		typeParams any
		traits     []string
		want       string
	}{
		{
			name:       "inline bound",
			typeParams: []map[string]string{{"name": "M", "bound": "Matcher"}},
			traits:     []string{"Matcher"},
			want:       "repo::matcher.rs::Matcher.is_match",
		},
		{
			name: "decoded metadata",
			typeParams: []any{
				map[string]any{"name": "M", "bound": "Send + crate::Matcher<Item = u8>"},
			},
			traits: []string{"Matcher"},
			want:   "repo::matcher.rs::Matcher.is_match",
		},
		{
			name:       "ambiguous trait bounds",
			typeParams: []map[string]string{{"name": "M", "bound": "Matcher + OtherMatcher"}},
			traits:     []string{"Matcher", "OtherMatcher"},
			want:       "",
		},
		{
			name:       "unconstrained generic",
			typeParams: []map[string]string{{"name": "M"}},
			traits:     []string{"Matcher"},
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, call := rustGenericBoundFixture(tt.typeParams, tt.traits)
			resolved := ResolveRustScopeCalls(g)
			if tt.want == "" {
				if resolved != 0 {
					t.Fatalf("resolved = %d, want 0", resolved)
				}
				if !graph.IsUnresolvedTarget(call.To) {
					t.Fatalf("ambiguous/unbounded call resolved to %q", call.To)
				}
				return
			}
			if resolved != 1 {
				t.Fatalf("resolved = %d, want 1", resolved)
			}
			if call.To != tt.want {
				t.Fatalf("call target = %q, want %q", call.To, tt.want)
			}
			if got, _ := call.Meta["rust_resolution"].(string); got != "generic_trait_bound" {
				t.Fatalf("resolution reason = %q, want generic_trait_bound", got)
			}
		})
	}
}

func TestResolveRustScopeCallsGenericTraitBoundDuplicateDeclaration(t *testing.T) {
	g, call := rustGenericBoundFixture(
		[]map[string]string{{"name": "M", "bound": "Matcher"}},
		[]string{"Matcher"},
	)
	g.AddNode(&graph.Node{
		ID: "repo::other.rs::Matcher.is_match", Kind: graph.KindMethod, Name: "is_match",
		RepoPrefix: "repo", FilePath: "src/other.rs", Language: "rust",
		Meta: map[string]any{"receiver": "Matcher", "trait_decl": "true"},
	})
	if resolved := ResolveRustScopeCalls(g); resolved != 0 {
		t.Fatalf("resolved duplicate declaration count = %d, want 0", resolved)
	}
	if !graph.IsUnresolvedTarget(call.To) {
		t.Fatalf("duplicate trait declarations resolved to %q", call.To)
	}
}

func rustGenericBoundFixture(typeParams any, traits []string) (*graph.Graph, *graph.Edge) {
	g := graph.New()
	for _, trait := range traits {
		g.AddNode(&graph.Node{
			ID:   "repo::" + traitFileName(trait) + "::" + trait + ".is_match",
			Kind: graph.KindMethod, Name: "is_match", RepoPrefix: "repo",
			FilePath: "src/" + traitFileName(trait), Language: "rust",
			Meta: map[string]any{"receiver": trait, "trait_decl": "true"},
		})
	}
	caller := &graph.Node{
		ID: "repo::src/run.rs::run", Kind: graph.KindFunction, Name: "run",
		RepoPrefix: "repo", FilePath: "src/run.rs", Language: "rust",
		Meta: map[string]any{"type_params": typeParams},
	}
	g.AddNode(caller)
	call := &graph.Edge{
		From: caller.ID, To: "unresolved::*.is_match", Kind: graph.EdgeCalls,
		FilePath: caller.FilePath, Line: 3,
		Meta: map[string]any{"receiver_type": "M"},
	}
	g.AddEdge(call)
	return g, call
}

func traitFileName(trait string) string {
	if trait == "Matcher" {
		return "matcher.rs"
	}
	return "other_matcher.rs"
}
