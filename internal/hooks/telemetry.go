package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/zzet/gortex/internal/platform"
)

// DecisionKind enumerates the outcomes the Grep-redirect probe can log.
type DecisionKind string

const (
	DecisionProbedHit        DecisionKind = "probed_hit"
	DecisionProbedMiss       DecisionKind = "probed_miss"
	DecisionSkippedNonSymbol DecisionKind = "skipped_non_symbol"
	DecisionTimedOut         DecisionKind = "timed_out"
	// DecisionNudged records that ModeAdaptiveNudge fired its
	// once-per-burst soft-deny after a streak of non-symbolic calls.
	DecisionNudged DecisionKind = "nudged"
)

type hookDecision struct {
	Timestamp  string       `json:"ts"`
	Tool       string       `json:"tool"`
	Decision   DecisionKind `json:"decision"`
	Hits       int          `json:"hits,omitempty"`
	DurationMS int64        `json:"duration_ms,omitempty"`
}

// hookEffectiveness records one hook invocation without source, prompt, path,
// symbol, or command content. Keeping it separate from hookDecision preserves
// the existing probe log while making skipped/no-output invocations visible;
// that denominator is what catches regressions such as "91% skipped".
type hookEffectiveness struct {
	Timestamp           string `json:"ts"`
	Event               string `json:"event"`
	EmittedContext      bool   `json:"emitted_context"`
	DaemonReachable     bool   `json:"daemon_reachable"`
	AlternationSegments int    `json:"alternation_segments"`
	DurationMS          int64  `json:"duration_ms"`
}

var hookEffectivenessEvents = map[string]bool{
	"PostToolUse":      true,
	"PreToolUse":       true,
	"SessionStart":     true,
	"UserPromptSubmit": true,
	"PreCompact":       true,
	"PostCompact":      true,
	"Stop":             true,
	"SubagentStart":    true,
	"SubagentStop":     true,
}

// hookDecisionsPath returns the telemetry file path. Respects GORTEX_HOOK_LOG
// so tests can redirect writes. Defaults to ~/.gortex/cache (or the
// $XDG_CACHE_HOME equivalent when that variable is set).
func hookDecisionsPath() string {
	if p := os.Getenv("GORTEX_HOOK_LOG"); p != "" {
		return p
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v == "" || !filepath.IsAbs(v) {
		if _, err := os.UserHomeDir(); err != nil {
			return ""
		}
	}
	return filepath.Join(platform.CacheDir(), "hook-decisions.jsonl")
}

// hookEffectivenessPath is a privacy-safe, denominator-complete companion to
// hook-decisions.jsonl. Tests can redirect it independently so decision-log
// assertions remain stable.
func hookEffectivenessPath() string {
	if p := os.Getenv("GORTEX_HOOK_EFFECTIVENESS_LOG"); p != "" {
		return p
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v == "" || !filepath.IsAbs(v) {
		if _, err := os.UserHomeDir(); err != nil {
			return ""
		}
	}
	return filepath.Join(platform.CacheDir(), "hook-effectiveness.jsonl")
}

// logHookDecision appends one JSONL record. Best-effort: errors are swallowed
// because telemetry must never block a hook.
func logHookDecision(tool, _ string, decision DecisionKind, hits int, dur time.Duration) {
	path := hookDecisionsPath()
	if path == "" {
		return
	}
	rec := hookDecision{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Tool:       tool,
		Decision:   decision,
		Hits:       hits,
		DurationMS: dur.Milliseconds(),
	}
	appendHookJSONL(path, rec)
}

// logHookEffectiveness appends one bounded observation. alternationSegments is
// capped to prevent an adversarial regex from becoming a high-cardinality
// metric; values above the probe ceiling share the same overflow bucket.
func logHookEffectiveness(event string, emitted, reachable bool, alternationSegments int, dur time.Duration) {
	if !hookEffectivenessEvents[event] {
		return
	}
	if alternationSegments < 0 {
		alternationSegments = 0
	}
	if alternationSegments > maxAlternationProbes+1 {
		alternationSegments = maxAlternationProbes + 1
	}
	path := hookEffectivenessPath()
	if path == "" {
		return
	}
	appendHookJSONL(path, hookEffectiveness{
		Timestamp:           time.Now().UTC().Format(time.RFC3339Nano),
		Event:               event,
		EmittedContext:      emitted,
		DaemonReachable:     reachable,
		AlternationSegments: alternationSegments,
		DurationMS:          dur.Milliseconds(),
	})
}

func appendHookJSONL(path string, record any) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
