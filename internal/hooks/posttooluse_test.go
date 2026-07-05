package hooks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// stubBridge stubs the daemon file-summary seam (fileSummaryFn) the
// PostToolUse handlers consult, translating the three legacy test maps into
// per-file hookFileSummary values:
//
//   - indexed:   path → symbol count. Padded with generic single-line nodes.
//   - enclosing: "path:line" → {ID, Name, Kind}. Materialised as a node with a
//     [line,line] span so enclosingNode resolves it exactly.
//   - callers:   path → importer count (hookFileSummary.Dependents).
//
// Returns 0 — the port is vestigial (the lookups moved off HTTP onto the
// daemon socket, #241) but callers still thread a port into runCodex.
func stubBridge(
	t *testing.T,
	indexed map[string]int, // path → symbol count
	enclosing map[string]struct{ ID, Name, Kind string }, // "path:line" → symbol
	callers map[string]int, // path → importer count
) int {
	t.Helper()

	summaries := make(map[string]*hookFileSummary)
	ensure := func(path string) *hookFileSummary {
		s := summaries[path]
		if s == nil {
			s = &hookFileSummary{}
			summaries[path] = s
		}
		return s
	}

	for key, sym := range enclosing {
		path, line := splitKeyLine(t, key)
		s := ensure(path)
		s.Symbols = append(s.Symbols, summaryNode{
			Name: sym.Name, Kind: sym.Kind, StartLine: line, EndLine: line,
		})
	}
	for path, count := range indexed {
		s := ensure(path)
		for len(s.Symbols) < count {
			ln := len(s.Symbols) + 1
			s.Symbols = append(s.Symbols, summaryNode{
				Name: fmt.Sprintf("Sym%d", ln), Kind: "symbol", StartLine: ln, EndLine: ln,
			})
		}
	}
	for path, n := range callers {
		ensure(path).Dependents = n
	}

	prev := fileSummaryFn
	t.Cleanup(func() { fileSummaryFn = prev })
	fileSummaryFn = func(_, path string) (*hookFileSummary, bool) {
		s, ok := summaries[path]
		if !ok || len(s.Symbols) == 0 {
			return nil, false
		}
		return s, true
	}
	return 0
}

func splitKeyLine(t *testing.T, key string) (string, int) {
	t.Helper()
	idx := strings.LastIndex(key, ":")
	if idx < 0 {
		t.Fatalf("bad enclosing key %q (want path:line)", key)
	}
	line, err := strconv.Atoi(key[idx+1:])
	if err != nil {
		t.Fatalf("bad line in enclosing key %q: %v", key, err)
	}
	return key[:idx], line
}

// ---------------------------------------------------------------------------
// parseGrepHits / parseGlobPaths — pure parsing
// ---------------------------------------------------------------------------

func TestParseGrepHits(t *testing.T) {
	body := `pkg/foo.go:42:func Bar() {
pkg/foo.go:42:    duplicate line  // dedup by path:line
pkg/baz.go:7:type Quux struct{}
Found 2 matches`
	hits := parseGrepHits(body)
	if len(hits) != 2 {
		t.Fatalf("expected 2 unique hits, got %d (%+v)", len(hits), hits)
	}
	if hits[0].path != "pkg/foo.go" || hits[0].line != 42 {
		t.Errorf("hits[0] = %+v", hits[0])
	}
	if hits[1].path != "pkg/baz.go" || hits[1].line != 7 {
		t.Errorf("hits[1] = %+v", hits[1])
	}
}

func TestParseGlobPaths(t *testing.T) {
	body := `Found 3 files
src/main.go
src/helper.go
(no further matches)
internal/util/x.go
`
	paths := parseGlobPaths(body)
	want := []string{"src/main.go", "src/helper.go", "internal/util/x.go"}
	if len(paths) != len(want) {
		t.Fatalf("got %d paths, want %d (%+v)", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// enclosingNode — line-range resolution (pure)
// ---------------------------------------------------------------------------

func TestEnclosingNode(t *testing.T) {
	nodes := []summaryNode{
		{Name: "File", Kind: "type", StartLine: 1, EndLine: 100}, // widest span
		{Name: "Method", Kind: "method", StartLine: 40, EndLine: 60},
		{Name: "Nested", Kind: "func", StartLine: 45, EndLine: 50}, // innermost
		{Name: "Later", Kind: "func", StartLine: 70, EndLine: 80},
	}
	cases := []struct {
		line int
		want string
	}{
		{48, "Nested"}, // innermost span wins over Method + File
		{55, "Method"}, // inside Method but not Nested
		{90, "File"},   // only the wide span covers it
		{75, "Later"},
		{200, ""}, // no node covers it
	}
	for _, c := range cases {
		got := enclosingNode(nodes, c.line)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != c.want {
			t.Errorf("enclosingNode(line=%d) = %q, want %q", c.line, name, c.want)
		}
	}

	// A node with no end line is treated as a single-line span.
	single := []summaryNode{{Name: "Const", Kind: "const", StartLine: 7}}
	if got := enclosingNode(single, 7); got == nil || got.Name != "Const" {
		t.Errorf("single-line span: got %+v, want Const", got)
	}
	if got := enclosingNode(single, 8); got != nil {
		t.Errorf("single-line span should not cover line 8: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// parseFileSummary — JSON-RPC envelope → nodes + importer count
// ---------------------------------------------------------------------------

func TestParseFileSummary(t *testing.T) {
	payload := map[string]any{
		"nodes": []map[string]any{
			{"name": "Bar", "kind": "function", "start_line": 10, "end_line": 20},
			{"name": "Baz", "kind": "type", "start_line": 22, "end_line": 30},
		},
		"dependents":  []string{"pkg/a.go", "pkg/b.go", "pkg/c.go"},
		"total_nodes": 2,
	}
	text, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rpc, err := json.Marshal(map[string]any{
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
		},
	})
	if err != nil {
		t.Fatalf("marshal rpc: %v", err)
	}

	nodes, deps, ok := parseFileSummary(rpc)
	if !ok {
		t.Fatal("expected ok parse")
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes=%d want 2", len(nodes))
	}
	if nodes[0].Name != "Bar" || nodes[0].Kind != "function" || nodes[0].StartLine != 10 || nodes[0].EndLine != 20 {
		t.Errorf("nodes[0] = %+v", nodes[0])
	}
	if deps != 3 {
		t.Errorf("dependents=%d want 3", deps)
	}
}

func TestParseFileSummaryRejectsErrorsAndEmpty(t *testing.T) {
	cases := []struct {
		name string
		resp string
	}{
		{"tool error", `{"result":{"isError":true,"content":[{"text":"not indexed"}]}}`},
		{"no content", `{"result":{"content":[]}}`},
		{"empty nodes", `{"result":{"content":[{"text":"{\"nodes\":[]}"}]}}`},
		{"malformed envelope", `not json`},
		{"non-json content text", `{"result":{"content":[{"text":"file not indexed"}]}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, ok := parseFileSummary([]byte(c.resp)); ok {
				t.Errorf("expected ok=false for %s", c.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// postGrep — graph context for ripgrep-style match output
// ---------------------------------------------------------------------------

func TestPostGrep_EnrichesWithEnclosingSymbols(t *testing.T) {
	stubBridge(t, nil,
		map[string]struct{ ID, Name, Kind string }{
			"pkg/foo.go:42": {ID: "pkg/foo.go::Bar", Name: "Bar", Kind: "function"},
			"pkg/baz.go:7":  {ID: "pkg/baz.go::Quux", Name: "Quux", Kind: "type"},
		}, nil)

	input := postHookInput{
		ToolName: "Grep",
		ToolResponse: "pkg/foo.go:42:func Bar() {\n" +
			"pkg/baz.go:7:type Quux struct{}\n",
	}
	out := postGrep(input)
	if out == "" {
		t.Fatal("expected enrichment context, got empty")
	}
	if !strings.Contains(out, "function Bar") {
		t.Errorf("missing enclosing symbol for foo.go:42 in:\n%s", out)
	}
	if !strings.Contains(out, "type Quux") {
		t.Errorf("missing enclosing symbol for baz.go:7 in:\n%s", out)
	}
}

func TestPostGrep_EmptyWhenNoEnclosingSymbol(t *testing.T) {
	stubBridge(t, nil, nil, nil) // no summary for any hit
	input := postHookInput{
		ToolName:     "Grep",
		ToolResponse: "pkg/foo.go:42:something\n",
	}
	out := postGrep(input)
	if out != "" {
		t.Errorf("expected empty context when no symbols resolve, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// postGlob — file footprint summary
// ---------------------------------------------------------------------------

func TestPostGlob_RanksByIndexedSymbolCount(t *testing.T) {
	indexed := map[string]int{
		"src/big.go":   42,
		"src/small.go": 3,
	}
	stubBridge(t, indexed, nil, nil)

	input := postHookInput{
		ToolName:     "Glob",
		ToolResponse: "src/big.go\nsrc/small.go\nsrc/empty.go\n", // empty.go not indexed
	}
	out := postGlob(input)
	if out == "" {
		t.Fatal("expected enrichment, got empty")
	}
	// big.go (42 symbols) should appear before small.go (3 symbols).
	bigPos := strings.Index(out, "src/big.go")
	smallPos := strings.Index(out, "src/small.go")
	if bigPos < 0 || smallPos < 0 {
		t.Fatalf("expected both files in output:\n%s", out)
	}
	if bigPos >= smallPos {
		t.Errorf("expected larger file (big.go) ranked first, got:\n%s", out)
	}
	// Counts visible.
	if !strings.Contains(out, "42 symbol(s)") || !strings.Contains(out, "3 symbol(s)") {
		t.Errorf("expected symbol counts in output:\n%s", out)
	}
	// Unindexed file should NOT appear individually but should be in
	// the X/Y summary.
	if strings.Contains(out, "src/empty.go") {
		t.Errorf("unindexed file leaked into output:\n%s", out)
	}
}

func TestPostGlob_AllUnindexedReturnsEmpty(t *testing.T) {
	stubBridge(t, nil, nil, nil)
	input := postHookInput{
		ToolName:     "Glob",
		ToolResponse: "vendor/some.go\n",
	}
	out := postGlob(input)
	if out != "" {
		t.Errorf("expected empty context when nothing is indexed, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// postRead — file footprint
// ---------------------------------------------------------------------------

func TestPostRead_IncludesSymbolAndDependentCounts(t *testing.T) {
	indexed := map[string]int{"pkg/handler.go": 12}
	callers := map[string]int{"pkg/handler.go": 8}
	stubBridge(t, indexed, nil, callers)

	input := postHookInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "pkg/handler.go"},
	}
	out := postRead(input)
	if out == "" {
		t.Fatal("expected enrichment for indexed file, got empty")
	}
	if !strings.Contains(out, "12 indexed symbol(s)") {
		t.Errorf("missing symbol count in:\n%s", out)
	}
	if !strings.Contains(out, "8 file(s) import this one") {
		t.Errorf("missing importer count in:\n%s", out)
	}
}

func TestPostRead_SkipsUnindexedFiles(t *testing.T) {
	stubBridge(t, nil, nil, nil)
	input := postHookInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "README.md"},
	}
	out := postRead(input)
	if out != "" {
		t.Errorf("expected empty for unindexed file, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// runPostToolUse — dispatcher integration
// ---------------------------------------------------------------------------

func TestRunPostToolUse_DispatchesByToolName(t *testing.T) {
	indexed := map[string]int{"pkg/handler.go": 5}
	stubBridge(t, indexed, nil, nil)

	payload := []byte(`{"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"pkg/handler.go"}}`)
	out := captureStdout(t, func() { runPostToolUse(payload) })
	if out == "" {
		t.Fatal("expected JSON output from dispatcher")
	}
	var dec HookOutput
	if err := json.Unmarshal([]byte(out), &dec); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if dec.HookSpecificOutput == nil {
		t.Fatal("missing hookSpecificOutput")
	}
	if dec.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Errorf("wrong event name: %q", dec.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(dec.HookSpecificOutput.AdditionalContext, "5 indexed symbol(s)") {
		t.Errorf("missing expected context:\n%s", dec.HookSpecificOutput.AdditionalContext)
	}
}

func TestRunPostToolUse_SilentForUnsupportedTool(t *testing.T) {
	stubBridge(t, nil, nil, nil)
	payload := []byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo hi"}}`)
	out := captureStdout(t, func() { runPostToolUse(payload) })
	if out != "" {
		t.Errorf("expected silent no-op for Bash, got:\n%s", out)
	}
}

func TestRunPostToolUse_SilentOnNonPostToolUseEvent(t *testing.T) {
	payload := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"x.go"}}`)
	out := captureStdout(t, func() { runPostToolUse(payload) })
	if out != "" {
		t.Errorf("expected silent no-op for non-PostToolUse event, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Mode plumbing — Run() should route PostToolUse through runPostToolUse
// ---------------------------------------------------------------------------

func TestDispatchRoutesPostToolUseEvent(t *testing.T) {
	indexed := map[string]int{"pkg/x.go": 3}
	stubBridge(t, indexed, nil, nil)

	data := []byte(`{"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"pkg/x.go"}}`)
	withStdin(t, data, func() {
		out := captureStdout(t, func() { Run(0, ModeEnrich) })
		if out == "" {
			t.Fatal("dispatcher dropped PostToolUse silently")
		}
		if !strings.Contains(out, "3 indexed symbol(s)") {
			t.Errorf("dispatcher routed to wrong handler:\n%s", out)
		}
	})
}
