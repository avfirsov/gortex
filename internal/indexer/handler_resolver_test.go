package indexer

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// TestLookupHandler_DisambiguatesByPackage is the regression for the
// `/v1/contracts` mis-resolution. Two methods named `handleContracts`
// existed in the gortex repo:
//
//   - server/dashboard.go::Handler.handleContracts (the HTTP handler)
//   - mcp/tools_enhancements.go::Server.handleContracts (an MCP tool)
//
// The old lookupHandler returned nil whenever ≥2 same-repo candidates
// shared a name, so the resolver fell back to the enclosing
// `registerRoutes` and the dashboard showed the wrong handler. The
// `srcDir` tie-break filters by package directory: a `recv.method`
// call in `server/handler.go` resolves to the same-package method.
func TestLookupHandler_DisambiguatesByPackage(t *testing.T) {
	g := graph.New()
	// Same-repo, same-name, different packages.
	dashboard := &graph.Node{
		ID:         "gortex/internal/server/dashboard.go::Handler.handleContracts",
		Name:       "handleContracts",
		Kind:       graph.KindMethod,
		FilePath:   "gortex/internal/server/dashboard.go",
		RepoPrefix: "gortex",
	}
	mcpTool := &graph.Node{
		ID:         "gortex/internal/mcp/tools_enhancements.go::Server.handleContracts",
		Name:       "handleContracts",
		Kind:       graph.KindMethod,
		FilePath:   "gortex/internal/mcp/tools_enhancements.go",
		RepoPrefix: "gortex",
	}
	g.AddNode(dashboard)
	g.AddNode(mcpTool)

	idx := New(g, parser.NewRegistry(), config.Default().Index, zap.NewNop())

	// HandleFunc sits in server/handler.go — same package as
	// dashboard.go. The resolver MUST pick dashboard's handler, not
	// the cross-package mcp/tools_enhancements.go one.
	srcDir := "gortex/internal/server"
	got := idx.lookupHandler("h.handleContracts", "gortex", srcDir)
	if got == nil {
		t.Fatal("lookupHandler returned nil — the package tie-break did not engage")
	}
	if got.ID != dashboard.ID {
		t.Errorf("lookupHandler picked %q, want %q (same-package method)", got.ID, dashboard.ID)
	}
}

// TestLookupHandler_AmbiguousAcrossPackagesNoSrcDir confirms the old
// behaviour is preserved when no srcDir is given: ≥2 same-repo
// candidates → nil. We don't want callers without a source-directory
// hint to pick one arbitrarily.
func TestLookupHandler_AmbiguousAcrossPackagesNoSrcDir(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "gortex/a/x.go::A.Foo", Name: "Foo", Kind: graph.KindMethod,
		FilePath: "gortex/a/x.go", RepoPrefix: "gortex",
	})
	g.AddNode(&graph.Node{
		ID: "gortex/b/y.go::B.Foo", Name: "Foo", Kind: graph.KindMethod,
		FilePath: "gortex/b/y.go", RepoPrefix: "gortex",
	})
	idx := New(g, parser.NewRegistry(), config.Default().Index, zap.NewNop())
	if got := idx.lookupHandler("r.Foo", "gortex", ""); got != nil {
		t.Errorf("expected nil for ambiguous lookup with no srcDir, got %q", got.ID)
	}
}

// TestLookupHandler_SinglePackageMatchUnchanged is a sanity check:
// the common case (one same-repo function with a unique name) still
// resolves whether srcDir is set or not.
func TestLookupHandler_SinglePackageMatchUnchanged(t *testing.T) {
	g := graph.New()
	only := &graph.Node{
		ID: "gortex/server/h.go::Handler.handleHealth", Name: "handleHealth",
		Kind: graph.KindMethod, FilePath: "gortex/server/h.go", RepoPrefix: "gortex",
	}
	g.AddNode(only)
	idx := New(g, parser.NewRegistry(), config.Default().Index, zap.NewNop())

	for _, srcDir := range []string{"", "gortex/server", "gortex/other"} {
		got := idx.lookupHandler("h.handleHealth", "gortex", srcDir)
		if got == nil || got.ID != only.ID {
			t.Errorf("srcDir=%q: got %v, want %q", srcDir, got, only.ID)
		}
	}
}
