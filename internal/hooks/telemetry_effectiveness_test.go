package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookEffectivenessRecordsSkippedDenominatorAndAlternation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "effectiveness.jsonl")
	t.Setenv("GORTEX_HOOK_EFFECTIVENESS_LOG", path)

	oldReachable := daemonReachableFn
	daemonReachableFn = func() bool { return true }
	t.Cleanup(func() { daemonReachableFn = oldReachable })
	stubProbe(t, nil, nil)

	withEffect := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rg 'Alpha|two words|Beta'"}}`)
	captureStdout(t, func() { runPreToolUse(withEffect, 0, ModeEnrich) })
	withoutEffect := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	captureStdout(t, func() { runPreToolUse(withoutEffect, 0, ModeEnrich) })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read effectiveness telemetry: %v", err)
	}
	if strings.Contains(string(data), "Alpha") || strings.Contains(string(data), "two words") {
		t.Fatalf("effectiveness telemetry leaked command/query content: %s", data)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("records=%d want 2: %s", len(lines), data)
	}
	var first, second hookEffectiveness
	if json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil {
		t.Fatalf("invalid effectiveness JSONL: %s", data)
	}
	if first.Event != "PreToolUse" || !first.EmittedContext || !first.DaemonReachable || first.AlternationSegments != 3 {
		t.Fatalf("first record=%+v", first)
	}
	if second.Event != "PreToolUse" || second.EmittedContext || !second.DaemonReachable || second.AlternationSegments != 0 {
		t.Fatalf("second record=%+v", second)
	}
}

func TestHookEffectivenessRejectsUnknownEventsAndCapsSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "effectiveness.jsonl")
	t.Setenv("GORTEX_HOOK_EFFECTIVENESS_LOG", path)
	logHookEffectiveness("arbitrary-user-value", true, true, 99, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unknown event must not be logged, stat err=%v", err)
	}
	logHookEffectiveness("PreToolUse", true, false, 99, 0)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record hookEffectiveness
	if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record) != nil {
		t.Fatalf("invalid record: %s", data)
	}
	if record.AlternationSegments != maxAlternationProbes+1 || record.DaemonReachable {
		t.Fatalf("record=%+v", record)
	}
}
