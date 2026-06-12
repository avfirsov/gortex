package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/daemon"
)

// decodeHermes decodes a Hermes hook stdout payload, failing the test on
// invalid JSON. An empty string decodes to the zero decision.
func decodeHermes(t *testing.T, out string) hermesDecision {
	t.Helper()
	var d hermesDecision
	if strings.TrimSpace(out) == "" {
		return d
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("invalid Hermes decision JSON: %v\n%s", err, out)
	}
	return d
}

func TestRunHermes_IgnoresUnknownEvent(t *testing.T) {
	// runHermesPreToolCall / runHermesPreLLMCall both guard on the event
	// name, so a mismatched payload is a clean no-op.
	out := captureStdout(t, func() {
		runHermesPreToolCall([]byte(`{"hook_event_name":"post_tool_call","tool_name":"read_file"}`), 0, ModeDeny)
	})
	if out != "" {
		t.Errorf("non pre_tool_call should be a no-op, got %q", out)
	}
	out = captureStdout(t, func() {
		runHermesPreLLMCall([]byte(`{"hook_event_name":"on_session_start"}`))
	})
	if out != "" {
		t.Errorf("non pre_llm_call should be a no-op, got %q", out)
	}
}

func TestHermesPreToolCall_ReadFileIndexed_Blocks(t *testing.T) {
	port := newIndexedBridge(t, 12)
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","tool_input":{"path":"/repo/handler.go"}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, port, ModeDeny) })

	d := decodeHermes(t, out)
	if d.Action != "block" {
		t.Fatalf("expected action=block, got %+v", d)
	}
	if !strings.Contains(d.Message, "/repo/handler.go") {
		t.Errorf("message should name the file: %q", d.Message)
	}
	if !strings.Contains(d.Message, "get_symbol_source") {
		t.Errorf("message should redirect to graph tools: %q", d.Message)
	}
	// Canonical Hermes shape — never Claude's envelope.
	if strings.Contains(out, "hookSpecificOutput") || strings.Contains(out, "permissionDecision") {
		t.Errorf("output leaked the Claude shape: %s", out)
	}
}

func TestHermesPreToolCall_ReadFileFilePathKey(t *testing.T) {
	// Claude-style file_path key is also accepted, not just Hermes' path.
	port := newIndexedBridge(t, 3)
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","tool_input":{"file_path":"/repo/a.go"}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, port, ModeDeny) })
	if d := decodeHermes(t, out); d.Action != "block" {
		t.Fatalf("expected block for file_path key, got %+v", d)
	}
}

func TestHermesPreToolCall_ReadFileNotIndexed_NoBlock(t *testing.T) {
	// port 0 → bridge unreachable → not indexed → no block in deny mode.
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","tool_input":{"path":"/tmp/x.go"}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, 0, ModeDeny) })
	if out != "" {
		t.Errorf("unindexed read should not block (no soft channel on pre_tool_call), got %q", out)
	}
}

func TestHermesPreToolCall_ReadNonSource_NoBlock(t *testing.T) {
	port := newIndexedBridge(t, 99)
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","tool_input":{"path":"/repo/README.md"}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, port, ModeDeny) })
	if out != "" {
		t.Errorf("non-source read should pass through, got %q", out)
	}
}

func TestHermesPreToolCall_TerminalGrepHit_Blocks(t *testing.T) {
	redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{
		{Name: "handleFoo", Kind: "function", FilePath: "internal/a.go", Line: 42},
	}, nil)

	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"terminal","tool_input":{"command":"grep -rn handleFoo ."}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, 0, ModeDeny) })

	d := decodeHermes(t, out)
	if d.Action != "block" {
		t.Fatalf("expected block for terminal grep hit, got %+v", d)
	}
	if !strings.Contains(d.Message, "handleFoo") {
		t.Errorf("message should mention the matched symbol: %q", d.Message)
	}
}

func TestHermesPreToolCall_TerminalUnrelated_NoBlock(t *testing.T) {
	stubProbe(t, nil, nil)
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"terminal","tool_input":{"command":"go build ./..."}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, 0, ModeDeny) })
	if out != "" {
		t.Errorf("unrelated terminal command should pass through, got %q", out)
	}
}

func TestHermesPreToolCall_EnrichNeverBlocks(t *testing.T) {
	// Indexed read that would block under deny must pass through under
	// enrich — pre_tool_call has no soft-context channel.
	port := newIndexedBridge(t, 20)
	data := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","tool_input":{"path":"/repo/x.go"}}`)
	out := captureStdout(t, func() { runHermesPreToolCall(data, port, ModeEnrich) })
	if out != "" {
		t.Errorf("enrich mode must never block, got %q", out)
	}
}

func TestHermesPreToolCall_ConsultUnlock(t *testing.T) {
	t.Setenv(hookSessionDirEnvVar, t.TempDir())
	port := newIndexedBridge(t, 7)
	read := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"read_file","session_id":"s1","tool_input":{"path":"/repo/x.go"}}`)

	// Before consulting the graph: indexed read is blocked, with the
	// unlock hint appended.
	out := captureStdout(t, func() { runHermesPreToolCall(read, port, ModeConsultUnlock) })
	d := decodeHermes(t, out)
	if d.Action != "block" {
		t.Fatalf("expected block before graph consulted, got %+v", d)
	}
	if !strings.Contains(d.Message, "consult-unlock") {
		t.Errorf("block should carry the unlock hint: %q", d.Message)
	}

	// A Gortex graph tool call flips the marker (pass-through, no output).
	gortexCall := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"gortex.search_symbols","session_id":"s1","tool_input":{}}`)
	if got := captureStdout(t, func() { runHermesPreToolCall(gortexCall, port, ModeConsultUnlock) }); got != "" {
		t.Errorf("gortex tool call should be a silent pass-through, got %q", got)
	}

	// After consulting: the same read is no longer blocked.
	if got := captureStdout(t, func() { runHermesPreToolCall(read, port, ModeConsultUnlock) }); got != "" {
		t.Errorf("read should be unlocked after a graph call, got %q", got)
	}
}

func TestHermesPreToolCall_NudgeFiresOncePerBurst(t *testing.T) {
	t.Setenv(hookSessionDirEnvVar, t.TempDir())
	redirectTelemetry(t)
	stubProbe(t, []grepSymbolHit{{Name: "Foo", Kind: "type", FilePath: "a.go", Line: 1}}, nil)
	grep := []byte(`{"hook_event_name":"pre_tool_call","tool_name":"terminal","session_id":"n1","tool_input":{"command":"grep -rn Foo ."}}`)

	// Below the threshold: no block.
	for i := range nudgeThreshold - 1 {
		if got := captureStdout(t, func() { runHermesPreToolCall(grep, 0, ModeAdaptiveNudge) }); got != "" {
			t.Fatalf("call %d under nudge threshold should not block, got %q", i+1, got)
		}
	}
	// Threshold crossed: one soft-deny.
	out := captureStdout(t, func() { runHermesPreToolCall(grep, 0, ModeAdaptiveNudge) })
	d := decodeHermes(t, out)
	if d.Action != "block" {
		t.Fatalf("expected one nudge block at the threshold, got %+v", d)
	}
	// Streak reset: the next call proceeds.
	if got := captureStdout(t, func() { runHermesPreToolCall(grep, 0, ModeAdaptiveNudge) }); got != "" {
		t.Errorf("post-nudge call should proceed, got %q", got)
	}
}

func TestHermesPreLLMCall_FirstTurnInjectsOrientation(t *testing.T) {
	withFakeStatus(t, func() (*daemon.StatusResponse, error) {
		return nil, errDaemonUnreachable // briefing is emitted even when down
	})
	data := []byte(`{"hook_event_name":"pre_llm_call","is_first_turn":true,"cwd":"/tmp/x"}`)
	out := captureStdout(t, func() { runHermesPreLLMCall(data) })

	d := decodeHermes(t, out)
	if d.Context == "" {
		t.Fatal("first turn should inject an orientation context")
	}
	if !strings.Contains(d.Context, "Gortex Session Orientation") {
		t.Errorf("context should be the orientation briefing: %q", d.Context)
	}
	if d.Action != "" {
		t.Errorf("pre_llm_call must never block: %+v", d)
	}
}

func TestHermesPreLLMCall_FirstTurnInExtra(t *testing.T) {
	withFakeStatus(t, func() (*daemon.StatusResponse, error) {
		return nil, errDaemonUnreachable
	})
	// Some Hermes builds carry is_first_turn / cwd inside `extra`.
	data := []byte(`{"hook_event_name":"pre_llm_call","extra":{"is_first_turn":true,"cwd":"/tmp/x"}}`)
	out := captureStdout(t, func() { runHermesPreLLMCall(data) })
	if d := decodeHermes(t, out); d.Context == "" {
		t.Fatal("is_first_turn in extra should still inject orientation")
	}
}

func TestHermesPreLLMCall_LaterTurnTrivialPrompt_NoOp(t *testing.T) {
	// Not the first turn, and a trivial message → no probe, no output.
	for _, data := range []string{
		`{"hook_event_name":"pre_llm_call","is_first_turn":false,"user_message":"ok"}`,
		`{"hook_event_name":"pre_llm_call","user_message":"/clear"}`,
		`{"hook_event_name":"pre_llm_call","extra":{"user_message":""}}`,
	} {
		if got := captureStdout(t, func() { runHermesPreLLMCall([]byte(data)) }); got != "" {
			t.Errorf("trivial later-turn prompt should be a no-op, got %q for %s", got, data)
		}
	}
}

func TestHermesNormalizeReadInput(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"path key", map[string]any{"path": "/a.go"}, "/a.go"},
		{"file_path wins", map[string]any{"file_path": "/b.go", "path": "/a.go"}, "/b.go"},
		{"filename alias", map[string]any{"filename": "/c.go"}, "/c.go"},
		{"file alias", map[string]any{"file": "/d.go"}, "/d.go"},
		{"none", map[string]any{"offset": 10}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := hermesNormalizeReadInput(tc.in)["file_path"].(string)
			if got != tc.want {
				t.Errorf("file_path = %q, want %q", got, tc.want)
			}
		})
	}
	// The original keys must survive so enrichRead's narrow-read detection
	// still sees offset/limit.
	out := hermesNormalizeReadInput(map[string]any{"path": "/a.go", "offset": 5, "limit": 10})
	if out["offset"] != 5 || out["limit"] != 10 {
		t.Errorf("narrow-read hints dropped: %#v", out)
	}
}

func TestIsHermesGortexTool(t *testing.T) {
	cases := map[string]bool{
		"read_file":               false,
		"terminal":                false,
		"gortex.search_symbols":   true,
		"gortex__find_usages":     true,
		"mcp__gortex__get_symbol": true,
		"web_search":              false,
		"":                        false,
	}
	for name, want := range cases {
		if got := isHermesGortexTool(name); got != want {
			t.Errorf("isHermesGortexTool(%q) = %v, want %v", name, got, want)
		}
	}
}
