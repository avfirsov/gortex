package hooks

import (
	"strings"
	"testing"
)

func TestSplitAlternation(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"foo", []string{"foo"}},
		{"place_edges|location_edge|normalize", []string{"place_edges", "location_edge", "normalize"}},
		{"a | b | c", []string{"a", "b", "c"}},
		{"foo|", []string{"foo"}},
		{"|foo", []string{"foo"}},
		{`a\|b`, []string{`a\|b`}}, // escaped pipe stays literal → single segment
		{"||", []string{"||"}},     // degenerate: no usable segments → whole pattern
	}
	for _, c := range cases {
		got := splitAlternation(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitAlternation(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitAlternation(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestEnrichGrep_Alternation_ProbesEachSegment covers the Codex-style
// multi-keyword grep pattern: every identifier-shaped alternative is probed
// and a hit on any of them denies with the aggregated matches.
func TestEnrichGrep_Alternation_ProbesEachSegment(t *testing.T) {
	logPath := redirectTelemetry(t)
	hits := []grepSymbolHit{{Name: "placeEdges", Kind: "function", FilePath: "internal/a.go", Line: 5}}
	rec := stubProbe(t, hits, nil)

	result := enrichGrep(map[string]any{"pattern": "place_edges|location_edge|normalize"}, 0)
	if !result.deny {
		t.Fatalf("alternation with symbol hits should deny, got context=%q", result.context)
	}
	if len(rec.calls) != 3 {
		t.Errorf("expected 3 segment probes, got %v", rec.calls)
	}
	recs := readDecisions(t, logPath)
	if len(recs) != 1 || recs[0].Decision != DecisionProbedHit {
		t.Fatalf("expected one probed_hit, got %+v", recs)
	}
}

// TestEnrichGrep_Alternation_MixedProbesOnlySymbolSegments verifies that only
// the identifier-shaped alternatives are probed — hyphenated / spaced keywords
// are skipped.
func TestEnrichGrep_Alternation_MixedProbesOnlySymbolSegments(t *testing.T) {
	hits := []grepSymbolHit{{Name: "entity_links", Kind: "type", FilePath: "internal/e.go", Line: 9}}
	rec := stubProbe(t, hits, nil)

	result := enrichGrep(map[string]any{"pattern": "ai-redesign|entity_links|world-map"}, 0)
	if !result.deny {
		t.Fatalf("expected deny when one segment is a real symbol, got %q", result.context)
	}
	if len(rec.calls) != 1 || rec.calls[0] != "entity_links" {
		t.Errorf("expected only the identifier segment probed, got %v", rec.calls)
	}
}

// TestEnrichGrep_Alternation_PureTextSkips covers a multi-keyword pattern with
// no identifier-shaped alternative: it must not probe and must steer the agent
// toward search_text.
func TestEnrichGrep_Alternation_PureTextSkips(t *testing.T) {
	logPath := redirectTelemetry(t)
	rec := stubProbe(t, nil, nil)

	result := enrichGrep(map[string]any{"pattern": "Phase 5|world-map"}, 0)
	if result.deny {
		t.Fatal("pure-text alternation should not deny")
	}
	if !strings.Contains(result.context, "search_text") {
		t.Errorf("guidance should point at search_text: %q", result.context)
	}
	if len(rec.calls) != 0 {
		t.Errorf("pure-text alternation should not probe, got %v", rec.calls)
	}
	recs := readDecisions(t, logPath)
	if len(recs) != 1 || recs[0].Decision != DecisionSkippedNonSymbol {
		t.Fatalf("expected skipped_non_symbol, got %+v", recs)
	}
}

// TestEnrichGrep_Alternation_DaemonUnreachableNoTelemetry checks that a fully
// unreachable daemon during an alternation probe stays quiet rather than
// logging a false miss.
func TestEnrichGrep_Alternation_DaemonUnreachableNoTelemetry(t *testing.T) {
	logPath := redirectTelemetry(t)
	stubProbe(t, nil, errDaemonUnreachable)

	result := enrichGrep(map[string]any{"pattern": "foo_a|foo_b"}, 0)
	if result.deny {
		t.Fatal("unreachable daemon should not deny")
	}
	if recs := readDecisions(t, logPath); len(recs) != 0 {
		t.Errorf("unreachable daemon (alternation) should emit no telemetry, got %+v", recs)
	}
}
