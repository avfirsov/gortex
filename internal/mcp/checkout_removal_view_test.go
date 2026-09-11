package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/gitstate"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graphview"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/reconcile"
)

// removalCalls is the removal surface, through both doors: the legacy tool
// names and the compact facade operation that lowers onto one of them.
func removalCalls(target string) []struct {
	name string
	tool string
	args map[string]any
} {
	return []struct {
		name string
		tool string
		args map[string]any
	}{
		{"untrack_repository", "untrack_repository", map[string]any{"path": target}},
		{"forget_checkout", "forget_checkout", map[string]any{"path": target}},
		{"facade_untrack", "workspace_admin", map[string]any{"operation": "untrack", "path": target}},
	}
}

// selectToolSurface puts the fixture session on the surface that publishes
// the tool under test. The surface gate refuses a name the session's preset
// does not advertise, and it fires before view routing — a different gate to
// the one these tests are about.
func selectToolSurface(srv *Server, tool string) {
	surface := "full"
	if isFacadeToolName(tool) {
		surface = FacadeSurfaceVersion
	}
	srv.NoteSessionToolPolicy(viewTestSession, surface, "")
}

// TestCheckoutRemovalRunsThroughAnUnbindableCWD is the reported failure:
// `gortex untrack <path>` relays through an MCP session whose working
// directory IS the checkout being removed, and the middleware refused the call
// before the handler ran because that directory could not be bound to a
// checkout view.
//
// A nested directory carrying its own .git is a checkout automatic discovery
// has not registered. Its parent is deliberately not authority for it, so
// binding fails for every request — which is precisely the state a removal has
// to survive, and the one graph reads must still refuse.
func TestCheckoutRemovalRunsThroughAnUnbindableCWD(t *testing.T) {
	stack := newViewStack(t)
	nested := filepath.Join(stack.repoRoot, "vendored-theme")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, ".git"), 0o755))

	// The gate is real and stays real: a graph read through the same cwd is
	// still refused rather than answered off the parent's corpus.
	readRan := false
	res, err := stack.callWithView(t, nested, "get_symbol", nil,
		func(context.Context) (*mcplib.CallToolResult, error) {
			readRan = true
			return mcplib.NewToolResultText(`{}`), nil
		})
	require.NoError(t, err)
	require.False(t, readRan, "get_symbol reached its handler through an unbindable cwd")
	assertToolError(t, res, graphview.CodeCheckoutInaccessible)

	for _, call := range removalCalls(nested) {
		t.Run(call.name, func(t *testing.T) {
			selectToolSurface(stack.srv, call.tool)
			ran, bound := false, false
			res, err := stack.callWithView(t, nested, call.tool, call.args,
				func(ctx context.Context) (*mcplib.CallToolResult, error) {
					ran, bound = true, requestViewFromContext(ctx) != nil
					return mcplib.NewToolResultText(`{"status":"untracked"}`), nil
				})
			require.NoError(t, err)
			require.False(t, res.IsError, viewResultText(t, res))
			require.True(t, ran, "the removal never reached its handler")
			require.False(t, bound, "a removal must not claim a view it never needed")
		})
	}
}

// TestCheckoutRemovalNeverBindsTheSessionCWD pins the skip as unconditional.
//
// The reported error was one binder outcome of several — automatic discovery
// still pending, its 250ms slice spent — and a removal that skipped only that
// outcome would still be hostage to the next one. A cwd that binds perfectly
// well proves the difference: the same session gets a routed view for a graph
// read and no view at all for a removal.
func TestCheckoutRemovalNeverBindsTheSessionCWD(t *testing.T) {
	stack := newViewStack(t)

	var reader graph.Reader
	if _, err := stack.callWithView(t, stack.worktreeRoot, "get_symbol", nil,
		captureReader(stack.srv, &reader)); err != nil {
		t.Fatalf("call: %v", err)
	}
	require.True(t, hasNode(reader, "repo/added.go::Fresh"),
		"the fixture cwd binds to a routed view, so the skip below is what is being observed")

	for _, call := range removalCalls(stack.worktreeRoot) {
		t.Run(call.name, func(t *testing.T) {
			selectToolSurface(stack.srv, call.tool)
			ran, bound := false, false
			res, err := stack.callWithView(t, stack.worktreeRoot, call.tool, call.args,
				func(ctx context.Context) (*mcplib.CallToolResult, error) {
					ran, bound = true, requestViewFromContext(ctx) != nil
					return mcplib.NewToolResultText(`{"status":"untracked"}`), nil
				})
			require.NoError(t, err)
			require.False(t, res.IsError, viewResultText(t, res))
			require.True(t, ran)
			require.False(t, bound, "a removal bound the session cwd to a view anyway")
		})
	}
}

// TestCheckoutRemovalToolRecognisesBothDoors keeps the predicate honest about
// what it exempts: the two removal verbs and the facade operation that lowers
// onto one of them, and nothing that reads a graph.
func TestCheckoutRemovalToolRecognisesBothDoors(t *testing.T) {
	stack := newViewStack(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want bool
	}{
		{"untrack_repository", map[string]any{"path": "/tmp/x"}, true},
		{"forget_checkout", map[string]any{"path": "/tmp/x"}, true},
		{"workspace_admin", map[string]any{"operation": "untrack", "path": "/tmp/x"}, true},
		{"workspace_admin", map[string]any{"operation": "track", "path": "/tmp/x"}, false},
		{"track_repository", map[string]any{"path": "/tmp/x"}, false},
		{"get_symbol", map[string]any{"id": "repo/keep.go::Keeper"}, false},
	} {
		req := mcplib.CallToolRequest{}
		req.Params.Name = tc.tool
		req.Params.Arguments = tc.args
		got := stack.srv.checkoutRemovalTool(&req)
		require.Equalf(t, tc.want, got, "%s %v", tc.tool, tc.args)
	}
}

// TestUntrackSurvivesPendingCheckoutDiscovery reproduces the reported failure
// with the error it actually carried, rather than a stand-in for it.
//
// A linked worktree that automatic discovery has not finished registering is
// bound by observing it, and observation gets a 250ms slice. Blocking the HEAD
// sampler spends that slice without finishing, which is the busy outcome a slow
// Windows or network checkout produces on its own. A graph read through that
// cwd is refused with the reported `view_building` message; the removal, whose
// answer comes from catalog rows, runs.
func TestUntrackSurvivesPendingCheckoutDiscovery(t *testing.T) {
	t.Setenv("GORTEX_TOOLS", "facade-v1")
	f := newRealCheckoutMutationFixture(t)
	root := filepath.Join(filepath.Dir(f.primary), "discovery-pending")
	checkoutMutationGit(t, f.primary, "worktree", "add", "-b", "discovery-pending", root)

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	reconcile.WithHEADSampler(func(ctx context.Context, root string) (gitstate.HEADState, error) {
		select {
		case <-ctx.Done():
			return gitstate.HEADState{}, errors.New("injected Git subprocess killed")
		case <-release:
			return gitstate.SampleHEAD(ctx, root)
		}
	})(f.srv.lifecycle.Reconciler())
	t.Cleanup(unblock)

	// The binder really is wedged, and it fails the way the report did.
	read := f.facade(t, root, "search", map[string]any{"operation": "symbols", "query": "Old"})
	assertToolError(t, read, graphview.CodeViewBuilding)
	require.Contains(t, viewResultText(t, read), "checkout mutation lane is busy",
		"the control read must carry the reported cause, not some other refusal")

	// Same session, same wedged cwd: the removal reaches its handler and answers
	// from the catalog. `gortex untrack` is exactly this call.
	removal := f.facade(t, root, "workspace_admin", map[string]any{
		"operation": "untrack", "path": f.primary,
	})
	require.False(t, removal.IsError, viewResultText(t, removal))
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(viewResultText(t, removal)), &payload))
	require.Equal(t, "preview", payload["status"])
	require.Equal(t, string(indexer.UntrackPlanPrimaryClosure), payload["plan"],
		"the removal must return a real catalog-derived plan, not a refusal")
}
