package mcp

import (
	"testing"

	"github.com/zzet/gortex/internal/telemetry"
)

// TestTelemetryRecorderWiredIntoDispatch verifies that a tool call routed
// through wrapToolHandler records a consent-gated mcp_tool_call counter named
// by the tool, and nothing else.
func TestTelemetryRecorderWiredIntoDispatch(t *testing.T) {
	s := fastPathTestServer(t)
	store := telemetry.NewStore(t.TempDir())
	s.SetTelemetryRecorder(telemetry.NewRecorder(telemetry.Consent{Enabled: true}, store))

	callWrapped(t, s, echoHandler, "search_symbols")
	callWrapped(t, s, echoHandler, "search_symbols")
	callWrapped(t, s, echoHandler, "find_usages")
	s.FlushTelemetry()

	days, err := store.Days()
	if err != nil || len(days) != 1 {
		t.Fatalf("expected one day of telemetry, got days=%v err=%v", days, err)
	}
	roll, err := store.Load(days[0])
	if err != nil {
		t.Fatal(err)
	}
	if roll.Counts["mcp_tool_call:search_symbols"] != 2 {
		t.Errorf("search_symbols count = %d, want 2", roll.Counts["mcp_tool_call:search_symbols"])
	}
	if roll.Counts["mcp_tool_call:find_usages"] != 1 {
		t.Errorf("find_usages count = %d, want 1", roll.Counts["mcp_tool_call:find_usages"])
	}
}

// TestTelemetryDispatchDisabledRecordsNothing verifies that with consent off,
// dispatch records nothing and never creates the telemetry directory.
func TestTelemetryDispatchDisabledRecordsNothing(t *testing.T) {
	s := fastPathTestServer(t)
	store := telemetry.NewStore(t.TempDir())
	s.SetTelemetryRecorder(telemetry.NewRecorder(telemetry.Consent{Enabled: false}, store))

	callWrapped(t, s, echoHandler, "search_symbols")
	s.FlushTelemetry()

	if days, _ := store.Days(); len(days) != 0 {
		t.Errorf("disabled telemetry wrote days %v", days)
	}
}

// TestTelemetryDispatchNoRecorder verifies dispatch is unaffected when no
// recorder is installed (the default).
func TestTelemetryDispatchNoRecorder(t *testing.T) {
	s := fastPathTestServer(t)
	// No SetTelemetryRecorder — s.recorder is nil.
	res := callWrapped(t, s, echoHandler, "search_symbols")
	if res == nil {
		t.Fatal("dispatch with nil recorder returned no result")
	}
}
