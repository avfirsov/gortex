package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRouter_LocalFastPath: when the resolved server slug equals the
// router's localSlug, RouteToolCall must invoke the local executor
// instead of dialing out. This is the common case for a single-host
// developer setup with one declared default server.
func TestRouter_LocalFastPath(t *testing.T) {
	cfg := &ServersConfig{
		Server: []ServerEntry{
			{Slug: "local", URL: "unix:///tmp/local.sock", Default: true},
		},
	}
	calledLocal := false
	r := NewRouter(RouterConfig{
		Servers:   cfg,
		Rosters:   NewWorkspaceRosterCache(time.Minute),
		LocalSlug: "local",
		LocalExecute: func(_ context.Context, name string, _ []byte) ([]byte, int, error) {
			calledLocal = true
			if name != "search_symbols" {
				t.Fatalf("expected search_symbols, got %q", name)
			}
			return []byte(`{"ok":true}`), 200, nil
		},
		Logger: zap.NewNop(),
	})
	out, status, err := r.RouteToolCall(context.Background(), "search_symbols", []byte(`{}`), RouteContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !calledLocal {
		t.Fatal("local executor was not called")
	}
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("body = %q", out)
	}
}

// TestRouter_LocalFastPath_CtxPropagates: callLocal must forward the
// caller's ctx verbatim. A previous regression replaced ctx with
// context.Background() inside callLocal, which silently dropped every
// per-session value attached upstream — most visibly the
// `mcp.WithSessionID` value the daemon dispatcher uses to route a
// tool call to the right per-session state. With ctx lost, the
// session-default wire-format negotiation (claude-code → gcx) saw an
// empty session ID and fell through to JSON. This test pins the
// invariant: whatever ctx the caller passes, the local executor sees.
func TestRouter_LocalFastPath_CtxPropagates(t *testing.T) {
	type ctxKey struct{}
	cfg := &ServersConfig{
		Server: []ServerEntry{
			{Slug: "local", URL: "unix:///tmp/local.sock", Default: true},
		},
	}
	var seen string
	r := NewRouter(RouterConfig{
		Servers:   cfg,
		Rosters:   NewWorkspaceRosterCache(time.Minute),
		LocalSlug: "local",
		LocalExecute: func(ctx context.Context, _ string, _ []byte) ([]byte, int, error) {
			if v, ok := ctx.Value(ctxKey{}).(string); ok {
				seen = v
			}
			return []byte(`{}`), 200, nil
		},
		Logger: zap.NewNop(),
	})
	want := "session-marker"
	ctx := context.WithValue(context.Background(), ctxKey{}, want)
	if _, _, err := r.RouteToolCall(ctx, "search_symbols", []byte(`{}`), RouteContext{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != want {
		t.Fatalf("local executor saw ctx value %q, want %q — ctx was discarded", seen, want)
	}
}

// TestRouter_ProxyToRemote: when the scope override targets a slug
// that is NOT the localSlug, the router must proxy to the upstream
// server's POST /v1/tools/<name>. The upstream is a httptest server
// returning a synthetic body; the router's response must contain it
// verbatim.
func TestRouter_ProxyToRemote(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/tools/search_symbols") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"ok":true,"server":"remote"}`)
	}))
	defer upstream.Close()

	cfg := &ServersConfig{
		Server: []ServerEntry{
			{Slug: "remote", URL: upstream.URL, Workspaces: []string{"tuck"}},
			{Slug: "local", URL: "unix:///tmp/local.sock", Default: true},
		},
	}
	r := NewRouter(RouterConfig{
		Servers:   cfg,
		Rosters:   NewWorkspaceRosterCache(time.Minute),
		LocalSlug: "local",
		LocalExecute: func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
			t.Fatal("local executor should not have been called for remote scope")
			return nil, 0, nil
		},
		Logger: zap.NewNop(),
	})
	out, status, err := r.RouteToolCall(context.Background(), "search_symbols", []byte(`{}`), RouteContext{
		ScopeOverride: "tuck",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["server"] != "remote" {
		t.Fatalf("body did not come from upstream: %v", body)
	}
}

// TestRouter_CwdResolution: a session whose cwd's `.gortex.yaml`
// declares `workspace: tuck` must route to the server whose
// `workspaces` list claims tuck.
func TestRouter_CwdResolution(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".gortex.yaml"), []byte("workspace: tuck\n"), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg := &ServersConfig{
		Server: []ServerEntry{
			{Slug: "remote", URL: upstream.URL, Workspaces: []string{"tuck"}},
			{Slug: "local", URL: "unix:///tmp/local.sock", Default: true},
		},
	}
	r := NewRouter(RouterConfig{
		Servers:   cfg,
		Rosters:   NewWorkspaceRosterCache(time.Minute),
		LocalSlug: "local",
		LocalExecute: func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
			t.Fatal("local executor should not have been called for cwd-resolved scope")
			return nil, 0, nil
		},
		Logger: zap.NewNop(),
	})
	_, status, err := r.RouteToolCall(context.Background(), "search_symbols", []byte(`{}`), RouteContext{Cwd: cwd})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
}

// TestRouter_NoConfig_LocalOnly: without a ServersConfig the router
// must always run locally. Validates the "single-server daemon"
// fallback path.
func TestRouter_NoConfig_LocalOnly(t *testing.T) {
	called := false
	r := NewRouter(RouterConfig{
		LocalExecute: func(_ context.Context, _ string, _ []byte) ([]byte, int, error) {
			called = true
			return []byte(`{"ok":true}`), 200, nil
		},
	})
	_, _, err := r.RouteToolCall(context.Background(), "x", []byte(`{}`), RouteContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("local executor was not called")
	}
}
