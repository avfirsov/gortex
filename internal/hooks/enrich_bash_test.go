package hooks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newIndexedBridge returns a test HTTP server that answers /api/graph/file?path=…
// as if every requested file were indexed with the given symbol count, and
// returns the parsed port. Used to exercise enrichBash's ReadSource path
// without standing up a real daemon.
func newIndexedBridge(t *testing.T, symbols int) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph/file", func(w http.ResponseWriter, _ *http.Request) {
		nodes := make([]any, symbols+1) // file node + symbol nodes
		_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// httptest URLs are http://127.0.0.1:<port>; queryGortex concatenates
	// "http://localhost:<port>" so we need the numeric port.
	parts := strings.Split(strings.TrimPrefix(srv.URL, "http://"), ":")
	var port int
	if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func TestEnrichBash_GrepHit_Denies(t *testing.T) {
	redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{
		{Name: "handleFoo", Kind: "function", FilePath: "internal/a.go", Line: 42},
	}, nil)

	r := enrichBash(map[string]any{"command": `grep -rn "handleFoo" .`}, 0)
	if !r.deny {
		t.Fatalf("expected deny on grep hit, got %+v", r)
	}
	if !strings.Contains(r.reason, "handleFoo") {
		t.Error("deny reason should mention the pattern")
	}
	if !strings.Contains(r.reason, "internal/a.go:42") {
		t.Error("deny reason should list the hit")
	}
}

func TestEnrichBash_GrepMiss_SoftGuidance(t *testing.T) {
	redirectTelemetry(t)
	stubProbe(t, nil, nil) // daemon reachable, no hits

	r := enrichBash(map[string]any{"command": `grep -rn "handleFoo" .`}, 0)
	if r.deny {
		t.Fatal("miss should not deny")
	}
	if !strings.Contains(r.context, "search_symbols") {
		t.Error("miss should return soft guidance mentioning search_symbols")
	}
}

func TestEnrichBash_GrepPiped_PassesThrough(t *testing.T) {
	// grep after | is a filter on upstream output — not a codebase search.
	rec := stubProbe(t, nil, nil)
	r := enrichBash(map[string]any{"command": `go test ./... | grep FAIL`}, 0)
	if r.deny || r.context != "" {
		t.Errorf("piped grep should pass through, got %+v", r)
	}
	if len(rec.calls) != 0 {
		t.Errorf("piped grep should not probe daemon, got calls %v", rec.calls)
	}
}

func TestEnrichBash_RgBare_Denies(t *testing.T) {
	redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{
		{Name: "MyType", Kind: "type", FilePath: "a.go", Line: 5},
	}, nil)

	r := enrichBash(map[string]any{"command": `rg MyType`}, 0)
	if !r.deny {
		t.Fatalf("expected deny, got %+v", r)
	}
}

func TestEnrichBash_FindName_Denies(t *testing.T) {
	redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{
		{Name: "Handler", Kind: "type", FilePath: "x.go", Line: 10},
	}, nil)

	r := enrichBash(map[string]any{"command": `find . -name "Handler*"`}, 0)
	if !r.deny {
		t.Fatalf("expected deny for find -name with symbol-shaped root, got %+v", r)
	}
}

func TestEnrichBash_FindNameGoFiles_NoProbe(t *testing.T) {
	// `-name "*.go"` reduces to ".go" which is not symbol-shaped — no probe,
	// no deny. Returns soft guidance because the pattern is >2 chars.
	rec := stubProbe(t, nil, nil)
	r := enrichBash(map[string]any{"command": `find . -name "*.go"`}, 0)
	if r.deny {
		t.Fatal("find -name *.go should not deny")
	}
	if len(rec.calls) != 0 {
		t.Errorf("non-symbol-shaped name should not probe, got %v", rec.calls)
	}
}

func TestEnrichBash_FindTypeD_Passthrough(t *testing.T) {
	rec := stubProbe(t, nil, nil)
	r := enrichBash(map[string]any{"command": `find . -maxdepth 3 -type d`}, 0)
	if r.deny || r.context != "" {
		t.Errorf("find -type d should pass through, got %+v", r)
	}
	if len(rec.calls) != 0 {
		t.Error("find without -name should not probe")
	}
}

func TestEnrichBash_CatIndexedSource_Denies(t *testing.T) {
	port := newIndexedBridge(t, 17)
	r := enrichBash(map[string]any{"command": `cat /repo/handler.go`}, port)
	if !r.deny {
		t.Fatalf("expected deny for cat of indexed source, got %+v", r)
	}
	if !strings.Contains(r.reason, "/repo/handler.go") {
		t.Error("deny reason should mention the file path")
	}
	if !strings.Contains(r.reason, "17 symbols") {
		t.Error("deny reason should include the symbol count")
	}
	if !strings.Contains(r.reason, "get_file_summary") {
		t.Error("deny reason should point to get_file_summary")
	}
}

func TestEnrichBash_CatUnindexedSource_SoftGuidance(t *testing.T) {
	// port 0 → bridge unreachable → file treated as not indexed.
	r := enrichBash(map[string]any{"command": `head -20 /tmp/foo.go`}, 0)
	if r.deny {
		t.Fatal("unindexed source should not deny")
	}
	if !strings.Contains(r.context, "get_symbol_source") {
		t.Error("soft guidance should mention get_symbol_source")
	}
}

func TestEnrichBash_CatLogfile_Passthrough(t *testing.T) {
	r := enrichBash(map[string]any{"command": `cat /tmp/app.log`}, 0)
	if r.deny || r.context != "" {
		t.Errorf("cat of non-source file should pass through, got %+v", r)
	}
}

func TestEnrichBash_EmptyCommand(t *testing.T) {
	r := enrichBash(map[string]any{"command": ""}, 0)
	if r.deny || r.context != "" {
		t.Errorf("empty command should pass through, got %+v", r)
	}
}

func TestEnrichBash_UnrelatedCommand(t *testing.T) {
	rec := stubProbe(t, nil, nil)
	for _, cmd := range []string{
		`ls /repo`,
		`go build ./...`,
		`git status`,
		`echo hello`,
	} {
		r := enrichBash(map[string]any{"command": cmd}, 0)
		if r.deny || r.context != "" {
			t.Errorf("%q should pass through, got %+v", cmd, r)
		}
	}
	if len(rec.calls) != 0 {
		t.Errorf("unrelated commands should not probe, got %v", rec.calls)
	}
}

func TestEnrichBash_TelemetryTaggedAsBash(t *testing.T) {
	logPath := redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{
		{Name: "handleFoo", Kind: "function", FilePath: "a.go", Line: 1},
	}, nil)

	_ = enrichBash(map[string]any{"command": `grep -rn handleFoo .`}, 0)

	recs := readDecisions(t, logPath)
	if len(recs) != 1 {
		t.Fatalf("expected 1 telemetry record, got %d", len(recs))
	}
	if recs[0].Tool != "Bash" {
		t.Errorf("tool = %q, want %q", recs[0].Tool, "Bash")
	}
	if recs[0].Decision != DecisionProbedHit {
		t.Errorf("decision = %v, want probed_hit", recs[0].Decision)
	}
}
