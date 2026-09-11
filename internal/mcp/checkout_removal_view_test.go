package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graphview"
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
