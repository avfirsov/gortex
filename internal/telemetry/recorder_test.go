package telemetry

import (
	"testing"
	"time"
)

func fixedNow(day string) func() time.Time {
	t, _ := time.Parse("2006-01-02", day)
	return func() time.Time { return t }
}

func TestRecorderRecordsWhenEnabled(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)
	r.now = fixedNow("2026-06-18")

	r.Record("mcp_tool_call", "search_symbols")
	r.Record("mcp_tool_call", "search_symbols")
	r.Record("cli_command", "review")
	r.Flush()

	got, err := store.Load("2026-06-18")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Counts["mcp_tool_call:search_symbols"] != 2 {
		t.Errorf("tool counter = %d, want 2", got.Counts["mcp_tool_call:search_symbols"])
	}
	if got.Counts["cli_command:review"] != 1 {
		t.Errorf("cli counter = %d, want 1", got.Counts["cli_command:review"])
	}
}

func TestRecorderDisabledRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	r := NewRecorder(Consent{Enabled: false}, store)
	r.now = fixedNow("2026-06-18")
	if r.Enabled() {
		t.Fatal("disabled recorder reports Enabled()")
	}

	r.Record("mcp_tool_call", "search_symbols")
	r.Flush()

	// Nothing recorded → no day files written at all (no telemetry dir churn).
	days, err := store.Days()
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(days) != 0 {
		t.Errorf("disabled recorder wrote days %v", days)
	}
}

func TestRecorderNilSafe(t *testing.T) {
	var r *Recorder
	// None of these may panic.
	r.Record("mcp_tool_call", "x")
	r.Flush()
	if r.Enabled() {
		t.Error("nil recorder reports Enabled()")
	}
}

func TestRecorderDropsDisallowedKey(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)
	r.now = fixedNow("2026-06-18")

	r.Record("file_path", "/Users/x/secret.go") // not allow-listed
	r.Flush()

	if days, _ := store.Days(); len(days) != 0 {
		t.Errorf("disallowed key produced a rollup file: %v", days)
	}
}

func TestRecorderThresholdAutoFlush(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)
	r.now = fixedNow("2026-06-18")

	for range recorderFlushThreshold {
		r.Record("index", "1k-10k")
	}
	// The threshold was reached, so a flush already happened without an
	// explicit Flush() call.
	got, _ := store.Load("2026-06-18")
	if got.Counts["index:1k-10k"] != recorderFlushThreshold {
		t.Errorf("auto-flush counter = %d, want %d", got.Counts["index:1k-10k"], recorderFlushThreshold)
	}
}

func TestRecorderDayRollover(t *testing.T) {
	store := NewStore(t.TempDir())
	r := NewRecorder(Consent{Enabled: true}, store)

	day := "2026-06-18"
	r.now = func() time.Time { tm, _ := time.Parse("2006-01-02", day); return tm }
	r.Record("cli_command", "a")

	day = "2026-06-19" // clock advances past midnight
	r.Record("cli_command", "b")
	r.Flush()

	d18, _ := store.Load("2026-06-18")
	d19, _ := store.Load("2026-06-19")
	if d18.Counts["cli_command:a"] != 1 {
		t.Errorf("day-18 counter = %d, want 1 (prior day flushed on rollover)", d18.Counts["cli_command:a"])
	}
	if d19.Counts["cli_command:b"] != 1 {
		t.Errorf("day-19 counter = %d, want 1", d19.Counts["cli_command:b"])
	}
}
