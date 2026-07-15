package mcp

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestExploreAnswerDraftPrefersProductionOverMentionedTestHelperAndPromotesCrossFileCallee(t *testing.T) {
	builder := &graph.Node{
		ID: "ripgrep/crates/ignore/src/walk.rs::WalkBuilder", Name: "WalkBuilder", QualName: "walk::WalkBuilder",
		Kind: graph.KindFunction, FilePath: "crates/ignore/src/walk.rs", Language: "rust",
		StartLine: 1, EndLine: 1, WorkspaceID: "bench", ProjectID: "ripgrep", RepoPrefix: "ripgrep",
	}
	helper := &graph.Node{
		ID: "ripgrep/crates/ignore/src/walk.rs::walk_collect_parallel", Name: "walk_collect_parallel", QualName: "crates::ignore::walk::walk_collect_parallel",
		Kind: graph.KindFunction, FilePath: "crates/ignore/src/walk.rs", Language: "rust", Meta: map[string]any{"is_test": true},
		StartLine: 10, EndLine: 18, WorkspaceID: "bench", ProjectID: "ripgrep", RepoPrefix: "ripgrep",
	}
	buildParallel := &graph.Node{
		ID: "ripgrep/crates/ignore/src/walk.rs::WalkBuilder.build_parallel", Name: "build_parallel", QualName: "walk::WalkBuilder::build_parallel",
		Kind: graph.KindMethod, FilePath: "crates/ignore/src/walk.rs", Language: "rust",
		StartLine: 30, EndLine: 42, WorkspaceID: "bench", ProjectID: "ripgrep", RepoPrefix: "ripgrep",
	}
	buildWithCWD := &graph.Node{
		ID: "ripgrep/crates/ignore/src/dir.rs::IgnoreBuilder.build_with_cwd", Name: "build_with_cwd", QualName: "dir::IgnoreBuilder::build_with_cwd",
		Kind: graph.KindMethod, FilePath: "crates/ignore/src/dir.rs", Language: "rust",
		StartLine: 50, EndLine: 62, WorkspaceID: "bench", ProjectID: "ripgrep", RepoPrefix: "ripgrep",
	}

	targets := []exploreTarget{
		{node: builder, source: "pub struct WalkBuilder { /* ignore traversal configuration */ }"},
		{node: helper, source: "#[test] fn walk_collect_parallel() { WalkBuilder::new().build_parallel(); }"},
		{node: buildParallel, source: "pub fn build_parallel(&self) { IgnoreBuilder::build_with_cwd(self.cwd); }", callees: []*graph.Node{buildWithCWD}},
	}
	task := "Nondeterminism in ignore::WalkBuilder parallel multi-root walk: a scoped .gitignore rule from one root (e.g. tests/**/build/) incorrectly applies to unrelated root/added path (src/...) in build_parallel() walk, causing files to be nondeterministically excluded depending on thread scheduling/order of directory traversal."
	if !exploreQueryIsConceptTask(task) {
		t.Fatalf("natural-language issue with embedded symbols must remain concept-like")
	}
	exactID := exploreLocalizationExactTarget(task, targets)
	if exactID != buildParallel.ID {
		t.Fatalf("ripgrep-3419 must refine to production method %s, got %s", buildParallel.ID, exactID)
	}
	entries := exploreAnswerDraft(task, targets)

	productionIndex, helperIndex, structuralCount := -1, -1, 0
	var structural *exploreDraftEntry
	for i := range entries {
		entry := &entries[i]
		switch entry.node.ID {
		case buildParallel.ID:
			productionIndex = i
		case helper.ID:
			helperIndex = i
		}
		if entry.structural {
			structuralCount++
			structural = entry
		}
	}
	if productionIndex < 0 || helperIndex < 0 {
		t.Fatalf("draft missing production/helper: production=%d helper=%d", productionIndex, helperIndex)
	}
	if productionIndex >= helperIndex {
		t.Fatalf("production target must outrank mentioned test helper: production=%d helper=%d", productionIndex, helperIndex)
	}
	if structuralCount != 1 || structural == nil || structural.node.ID != buildWithCWD.ID || !structural.structuralCrossFile || !structural.structuralCallee {
		t.Fatalf("want one cross-file causal callee %s, got count=%d entry=%+v", buildWithCWD.ID, structuralCount, structural)
	}

	evidence := localizationEvidenceTargetsFromDraft(task, buildParallel.ID, targets, entries)
	foundNeighbor := false
	for _, target := range evidence {
		if target.node != nil && target.node.ID == buildWithCWD.ID {
			foundNeighbor = true
			if target.source != "" {
				t.Fatalf("graph-only neighbor must be materialized by the bounded reader, got eager source %q", target.source)
			}
		}
	}
	if !foundNeighbor {
		t.Fatalf("promoted cross-file callee absent from evidence")
	}
}

func TestExploreAnswerDraftKeepsExplicitTestHelperExact(t *testing.T) {
	production := &graph.Node{ID: "ripgrep/crates/ignore/src/walk.rs::WalkBuilder.build_parallel", Name: "build_parallel", QualName: "walk::WalkBuilder::build_parallel", Kind: graph.KindMethod, FilePath: "crates/ignore/src/walk.rs", Language: "rust"}
	helper := &graph.Node{ID: "ripgrep/crates/ignore/src/walk.rs::walk_collect_parallel", Name: "walk_collect_parallel", QualName: "crates::ignore::walk::walk_collect_parallel", Kind: graph.KindFunction, FilePath: "crates/ignore/src/walk.rs", Language: "rust", Meta: map[string]any{"is_test": true}}
	targets := []exploreTarget{{node: production}, {node: helper}}
	for _, query := range []string{
		"walk_collect_parallel",
		"crates/ignore/src/walk.rs::walk_collect_parallel",
		"fn walk_collect_parallel(prefix: &Path, builder: &WalkBuilder) -> Vec<String>",
	} {
		t.Run(query, func(t *testing.T) {
			if exploreQueryIsConceptTask(query) {
				t.Fatalf("pure identifier/path/signature request must remain explicit")
			}
			entries := exploreAnswerDraft(query, targets)
			if len(entries) == 0 || entries[0].node.ID != helper.ID || !entries[0].exact {
				t.Fatalf("explicit helper request must remain exact, got %+v", entries)
			}
		})
	}
}

func TestExploreQueryIsConceptTaskDistinguishesTrailingCallFromDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{
			name:  "natural language ending in call",
			query: "Explain why parallel multi-root traversal leaks ignore state into another root before it reaches build_parallel()",
			want:  true,
		},
		{
			name:  "custom C return type",
			query: "Result resolve_target(Context *ctx, Node node, Options opts, Resolver resolver, Diagnostics diagnostics);",
			want:  false,
		},
		{
			name:  "Java declaration",
			query: "public static CompletionStage<Result> resolveTarget(Context ctx, Node node, Options opts, Diagnostics diagnostics)",
			want:  false,
		},
		{
			name:  "TypeScript declaration",
			query: "export async function resolveTarget(ctx: Context, node: Node, options: Options): Promise<Result>",
			want:  false,
		},
		{
			name:  "Rust declaration",
			query: "pub async fn resolve_target(ctx: &Context, node: Node, options: Options) -> Result<T, E>",
			want:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exploreQueryIsConceptTask(test.query); got != test.want {
				t.Fatalf("exploreQueryIsConceptTask(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestExploreAnswerDraftKeepsProsePathSymbolAnchorsExact(t *testing.T) {
	production := &graph.Node{ID: "ripgrep/crates/ignore/src/walk.rs::WalkBuilder.build_parallel", Name: "build_parallel", QualName: "walk::WalkBuilder::build_parallel", Kind: graph.KindMethod, FilePath: "crates/ignore/src/walk.rs", Language: "rust"}
	helper := &graph.Node{ID: "ripgrep/crates/ignore/src/walk.rs::walk_collect_parallel", Name: "walk_collect_parallel", QualName: "crates::ignore::walk::walk_collect_parallel", Kind: graph.KindFunction, FilePath: "crates/ignore/src/walk.rs", Language: "rust", Meta: map[string]any{"is_test": true}}
	targets := []exploreTarget{{node: production}, {node: helper}}
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "test helper",
			query: "Inspect crates/ignore/src/walk.rs::walk_collect_parallel directly to understand this exact test helper behavior",
			want:  helper.ID,
		},
		{
			name:  "production method",
			query: "Inspect crates/ignore/src/walk.rs::WalkBuilder.build_parallel directly to understand this exact production method behavior",
			want:  production.ID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !exploreQueryIsConceptTask(test.query) {
				t.Fatalf("natural-language path anchor should remain a concept task")
			}
			entries := exploreAnswerDraft(test.query, targets)
			if len(entries) == 0 || entries[0].node.ID != test.want || !entries[0].exact {
				t.Fatalf("explicit path symbol must remain exact for %s, got %+v", test.want, entries)
			}
		})
	}
}
