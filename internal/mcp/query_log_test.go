package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountFromResultText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"json_total", `{"todos":[{"a":1}],"total":7}`, 7},
		{"json_symbols_array", `{"symbols":[{"id":"a"},{"id":"b"},{"id":"c"}]}`, 3},
		{"json_results_zero", `{"results":[]}`, 0},
		{"json_relevant_symbols", `{"task":"x","relevant_symbols":[{"id":"a"},{"id":"b"}]}`, 2},
		{"json_top_array", `[{"id":"a"},{"id":"b"}]`, 2},
		{"not_modified", `{"not_modified":true,"etag":"abc"}`, 0},
		{"gcx_rows", "GCX1 tool=search_symbols fields=id,name\nrow1\nrow2\nrow3\n", 3},
		{"toon_text", "first line\nsecond line\n", 2},
		{"largest_array_wins", `{"a":[1],"b":[1,2,3,4]}`, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countFromResultText(c.text); got != c.want {
				t.Fatalf("countFromResultText(%q) = %d, want %d", c.text, got, c.want)
			}
		})
	}
}

func TestFirstStringArg(t *testing.T) {
	args := map[string]any{
		"id":     "",
		"symbol": "pkg/foo.go::Bar",
		"seeds":  []any{"a", "b", "c"},
		"n":      float64(5),
	}
	if got := firstStringArg(args, []string{"id", "symbol"}); got != "pkg/foo.go::Bar" {
		t.Fatalf("firstStringArg id->symbol = %q", got)
	}
	if got := firstStringArg(args, []string{"seeds"}); got != "a,b,c" {
		t.Fatalf("firstStringArg seeds = %q", got)
	}
	if got := firstStringArg(args, []string{"missing"}); got != "" {
		t.Fatalf("firstStringArg missing = %q", got)
	}
}

func TestQueryLoggerAppendAndTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "query-log.jsonl")
	t.Setenv("GORTEX_QUERY_LOG", path)
	t.Setenv("GORTEX_QUERY_LOG_DISABLE", "")

	q := newQueryLogger()
	if !q.enabled {
		t.Fatal("logger should be enabled")
	}
	if q.Path() != path {
		t.Fatalf("path = %q, want %q", q.Path(), path)
	}
	for i := 0; i < 5; i++ {
		q.append([]byte(`{"ts":"2026-06-05T00:00:0` + string(rune('0'+i)) + `Z","tool":"search_symbols","question":"q","nodes_returned":0,"zero_result":true,"ok":true}`))
	}
	q.Close()

	records, scanned, err := readQueryLogTail(path, 100)
	if err != nil {
		t.Fatalf("readQueryLogTail: %v", err)
	}
	if scanned != 5 || len(records) != 5 {
		t.Fatalf("scanned=%d records=%d, want 5/5", scanned, len(records))
	}
	zero := 0
	for _, r := range records {
		if r.Tool != "search_symbols" {
			t.Fatalf("unexpected tool %q", r.Tool)
		}
		if r.ZeroResult {
			zero++
		}
	}
	if zero != 5 {
		t.Fatalf("zero=%d, want 5", zero)
	}

	// Tail limit keeps the newest N.
	tail, _, _ := readQueryLogTail(path, 2)
	if len(tail) != 2 {
		t.Fatalf("tail len=%d, want 2", len(tail))
	}
	if !strings.Contains(tail[1].TS, "04") {
		t.Fatalf("tail should end with newest record, got %q", tail[1].TS)
	}
}

func TestQueryLoggerDisabled(t *testing.T) {
	t.Setenv("GORTEX_QUERY_LOG_DISABLE", "1")
	q := newQueryLogger()
	if q.enabled {
		t.Fatal("logger should be disabled")
	}
	if q.shouldLog("search_symbols") {
		t.Fatal("disabled logger should not log")
	}
	if q.Path() != "" {
		t.Fatalf("disabled logger path = %q, want empty", q.Path())
	}
}

func TestQueryLoggerRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "query-log.jsonl")
	t.Setenv("GORTEX_QUERY_LOG", path)
	t.Setenv("GORTEX_QUERY_LOG_DISABLE", "")

	q := newQueryLogger()
	q.maxBytes = 200 // force rotation after a couple of lines
	for i := 0; i < 20; i++ {
		q.append([]byte(`{"ts":"t","tool":"search_symbols","question":"some longer question text here"}`))
	}
	q.Close()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup at %s.1: %v", path, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty live log after rotation: %v", err)
	}
}

func TestShouldLogToolSet(t *testing.T) {
	t.Setenv("GORTEX_QUERY_LOG", filepath.Join(t.TempDir(), "q.jsonl"))
	t.Setenv("GORTEX_QUERY_LOG_DISABLE", "")
	q := newQueryLogger()
	for _, tool := range []string{"search_symbols", "smart_context", "find_usages", "search_text"} {
		if !q.shouldLog(tool) {
			t.Fatalf("%s should be logged", tool)
		}
	}
	for _, tool := range []string{"edit_file", "graph_stats", "store_memory"} {
		if q.shouldLog(tool) {
			t.Fatalf("%s should NOT be logged", tool)
		}
	}
}
