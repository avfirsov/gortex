package telemetry

import (
	"strings"
	"testing"
	"time"
)

// TestIndexAndSessionRecordSitesEmit proves the F9 record-site helpers emit the
// right allow-listed, bucketed counters: an index pass records a file-count
// BUCKET (never the exact count) plus deduped per-language counters, and the
// daemon-session / install / uninstall sites emit their bounded dimensions.
func TestIndexAndSessionRecordSitesEmit(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)
	r.now = fixedNow("2026-06-18")

	RecordIndex(r, 4200, []string{"go", "python", "go", "", "  "})
	RecordDaemonSession(r, "sqlite")
	RecordInstall(r, "install", "claude")
	RecordInstall(r, "uninstall", "cursor")
	r.Flush()

	got, err := store.Load("2026-06-18")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Counts["index:1k-10k"] != 1 {
		t.Errorf("index bucket = %d, want 1 (counts=%v)", got.Counts["index:1k-10k"], got.Counts)
	}
	if got.Counts["index:4200"] != 0 {
		t.Error("the exact file count must never be recorded")
	}
	// Per-language counters: deduped (go appears twice), empties dropped.
	if got.Counts["index_lang:go"] != 1 {
		t.Errorf("index_lang:go = %d, want 1 (deduped)", got.Counts["index_lang:go"])
	}
	if got.Counts["index_lang:python"] != 1 {
		t.Errorf("index_lang:python = %d, want 1", got.Counts["index_lang:python"])
	}
	if got.Counts["index_lang"] != 0 {
		t.Error("a blank language must not record a bare index_lang counter")
	}
	if got.Counts["daemon_session:sqlite"] != 1 {
		t.Errorf("daemon_session:sqlite = %d, want 1", got.Counts["daemon_session:sqlite"])
	}
	if got.Counts["install:claude"] != 1 || got.Counts["uninstall:cursor"] != 1 {
		t.Errorf("install/uninstall counters wrong: %v", got.Counts)
	}
}

// TestClientNameFolding proves an MCP client handshake folds to a bounded,
// version-free token, lowercased, and that empty names record nothing.
func TestClientNameFolding(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)
	r.now = fixedNow("2026-06-18")

	RecordClient(r, "claude-code 1.0.42") // version dropped
	RecordClient(r, "Cursor")             // lowercased
	RecordClient(r, "   ")                // whitespace-only → dropped
	RecordClient(r, "")                   // empty → dropped
	r.Flush()

	got, err := store.Load("2026-06-18")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Counts["client:claude-code"] != 1 {
		t.Errorf("client fold = %v, want client:claude-code", got.Counts)
	}
	if got.Counts["client:cursor"] != 1 {
		t.Errorf("client name must be lowercased: %v", got.Counts)
	}
	for k := range got.Counts {
		if strings.Contains(k, "1.0.42") {
			t.Errorf("the client version leaked into telemetry: %q", k)
		}
	}
	if got.Counts["client"] != 0 {
		t.Error("an empty client name must not record a bare client counter")
	}
	if got := NormalizeClientName("VS Code 1.2"); got != "vs" {
		t.Errorf("NormalizeClientName first-field-lowercase = %q, want vs", got)
	}
}

func TestBucketFileCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "<100"}, {99, "<100"},
		{100, "100-1k"}, {999, "100-1k"},
		{1000, "1k-10k"}, {9999, "1k-10k"},
		{10000, "10k+"}, {5_000_000, "10k+"},
	}
	for _, c := range cases {
		if got := BucketFileCount(c.n); got != c.want {
			t.Errorf("BucketFileCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestBucketDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Microsecond, "<1ms"},
		{time.Millisecond, "1-10ms"},
		{9 * time.Millisecond, "1-10ms"},
		{10 * time.Millisecond, "10-100ms"},
		{99 * time.Millisecond, "10-100ms"},
		{100 * time.Millisecond, "100ms-1s"},
		{999 * time.Millisecond, "100ms-1s"},
		{time.Second, "1-10s"},
		{10 * time.Second, "10s+"},
	}
	for _, c := range cases {
		if got := BucketDuration(c.d); got != c.want {
			t.Errorf("BucketDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRollupAddAllowList(t *testing.T) {
	r := NewRollup(time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC))
	if r.Day != "2026-06-18" {
		t.Fatalf("Day = %q, want 2026-06-18", r.Day)
	}

	// Allow-listed key counts.
	if !r.Add("mcp_tool_call", "search_symbols") {
		t.Error("allow-listed metric was dropped")
	}
	r.Add("mcp_tool_call", "search_symbols")
	if got := r.Counts["mcp_tool_call:search_symbols"]; got != 2 {
		t.Errorf("dim counter = %d, want 2", got)
	}

	// A key not on the allow-list is dropped entirely.
	if r.Add("secret_path", "/Users/x/private.go") {
		t.Error("non-allow-listed metric must be dropped")
	}
	if len(r.Counts) != 1 {
		t.Errorf("dropped metric leaked into counts: %v", r.Counts)
	}

	// A path-like dimension on an allowed key is discarded; the bare key
	// still counts (no leak of the path).
	r.Add("index", "/abs/path/with/slashes")
	if _, leaked := r.Counts["index:/abs/path/with/slashes"]; leaked {
		t.Error("path dimension leaked into a counter name")
	}
	if got := r.Counts["index"]; got != 1 {
		t.Errorf("bare key counter = %d, want 1", got)
	}

	// A bucket-label dimension passes.
	r.Add("index", BucketFileCount(5000))
	if got := r.Counts["index:1k-10k"]; got != 1 {
		t.Errorf("bucket dim counter = %d, want 1", got)
	}
}

func TestRollupMerge(t *testing.T) {
	day := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	a := NewRollup(day)
	a.Add("cli_command", "index")
	b := NewRollup(day)
	b.Add("cli_command", "index")
	b.Add("daemon_session", "sqlite")
	a.Merge(b)
	if got := a.Counts["cli_command:index"]; got != 2 {
		t.Errorf("merged counter = %d, want 2", got)
	}
	if got := a.Counts["daemon_session:sqlite"]; got != 1 {
		t.Errorf("merged new counter = %d, want 1", got)
	}
	if a.Total() != 3 {
		t.Errorf("Total = %d, want 3", a.Total())
	}

	// A mismatched-day merge is ignored (no corruption).
	other := NewRollup(day.AddDate(0, 0, 1))
	other.Add("cli_command", "x")
	a.Merge(other)
	if a.Total() != 3 {
		t.Errorf("mismatched-day merge mutated rollup: Total = %d, want 3", a.Total())
	}
}

func TestIsAllowedMetric(t *testing.T) {
	for _, k := range []string{
		"mcp_tool_call", "mcp_facade_call", "mcp_facade_status", "mcp_facade_outcome",
		"mcp_facade_invalid", "mcp_facade_latency", "cli_command", "index", "daemon_session",
	} {
		if !IsAllowedMetric(k) {
			t.Errorf("%q should be allow-listed", k)
		}
	}
	for _, k := range []string{"", "file_path", "user_query", "anything_else"} {
		if IsAllowedMetric(k) {
			t.Errorf("%q must not be allow-listed", k)
		}
	}
}

func TestFacadeMetricRollupKeepsOnlyBoundedDimensions(t *testing.T) {
	r := NewRollup(time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC))
	for key, dim := range map[string]string{
		"mcp_facade_call":    "read.file",
		"mcp_facade_status":  "read.file.ok",
		"mcp_facade_outcome": "read.file.success",
		"mcp_facade_invalid": "read.unknown.invalid_argument",
		"mcp_facade_latency": "read.file.<1ms",
	} {
		if !r.Add(key, dim) {
			t.Fatalf("%s was unexpectedly rejected", key)
		}
		if got := r.Counts[key+":"+dim]; got != 1 {
			t.Errorf("%s:%s = %d, want 1", key, dim, got)
		}
	}

	const sensitive = "/Users/alice/private/repository/operation"
	r.Add("mcp_facade_outcome", sensitive)
	if got := r.Counts["mcp_facade_outcome"]; got != 1 {
		t.Fatalf("unsafe dimension should fold to the bare key, got %v", r.Counts)
	}
	for key := range r.Counts {
		if strings.Contains(key, "alice") || strings.Contains(key, "private") {
			t.Fatalf("sensitive facade dimension leaked: %q", key)
		}
	}
}
