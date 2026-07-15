package mcp

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestApplyFacadeTargetPreservesSymbolIDArrayElements(t *testing.T) {
	ids := []any{
		"crates/searcher/src/searcher/glue.rs::MultiLine<'s, M, S>.run",
		"crates/printer/src/util.rs::Replacer<M>.replace_all",
	}
	out := map[string]any{}

	applyFacadeTarget("batch_symbols", out, map[string]any{"symbols": ids})

	want := []string{
		"crates/searcher/src/searcher/glue.rs::MultiLine<'s, M, S>.run",
		"crates/printer/src/util.rs::Replacer<M>.replace_all",
	}
	if got, ok := parseBatchSymbolIDs(out["ids"]); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %#v (%T), want an encoding that preserves %#v atomically", out["ids"], out["ids"], want)
	}
}

func TestApplyFacadeTargetLeavesScalarSymbolShorthandForLegacyParsing(t *testing.T) {
	const shorthand = "first,second"
	out := map[string]any{}

	applyFacadeTarget("batch_symbols", out, map[string]any{"symbols": shorthand})

	if got := out["ids"]; got != shorthand {
		t.Fatalf("ids = %#v (%T), want scalar shorthand %q", got, got, shorthand)
	}
}

func TestApplyFacadeTargetPreservesTypedSymbolIDArray(t *testing.T) {
	ids := []string{
		"pkg/file.rs::Type<A, B>.first",
		"pkg/file.rs::Type<C, D>.second",
	}
	out := map[string]any{}

	applyFacadeTarget("batch_symbols", out, map[string]any{"symbols": ids})

	if got, ok := parseBatchSymbolIDs(out["ids"]); !ok || !reflect.DeepEqual(got, ids) {
		t.Fatalf("ids = %#v (%T), want encoding of %#v", out["ids"], out["ids"], ids)
	}
}

func TestNormalizeFacadeReadFileRangeAndOutputCap(t *testing.T) {
	out := normalizeFacadeArguments(facadeOperationSpec{Legacy: "read_file"}, map[string]any{
		"target": map[string]any{"file": "large.rs"},
		"options": map[string]any{
			"start_line": float64(135),
			"end_line":   float64(350),
		},
		"output": map[string]any{"max_chars": float64(4096)},
	})

	if got := out["path"]; got != "large.rs" {
		t.Fatalf("path = %#v, want large.rs", got)
	}
	if got := out["offset"]; got != 135 {
		t.Fatalf("offset = %#v, want 135", got)
	}
	if got := out["limit"]; got != 216 {
		t.Fatalf("limit = %#v, want 216", got)
	}
	if got := out["max_chars"]; got != float64(4096) {
		t.Fatalf("max_chars = %#v, want propagated output cap", got)
	}
	for _, obsolete := range []string{"start_line", "end_line", "start", "end"} {
		if _, ok := out[obsolete]; ok {
			t.Fatalf("legacy request retained facade-only field %q: %#v", obsolete, out)
		}
	}
}

func TestNormalizeFacadeReadFileRangeClampsBounds(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]any
		offset  int
		limit   int
	}{
		{name: "start below one", options: map[string]any{"start_line": -10, "end_line": 2}, offset: 1, limit: 2},
		{name: "end before start", options: map[string]any{"start_line": 10, "end_line": 5}, offset: 10, limit: 1},
		{name: "end only", options: map[string]any{"end_line": 3}, offset: 1, limit: 3},
		{name: "short aliases", options: map[string]any{"start": 4, "end": 6}, offset: 4, limit: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := normalizeFacadeArguments(facadeOperationSpec{Legacy: "read_file"}, map[string]any{
				"options": tt.options,
			})
			if got := out["offset"]; got != tt.offset {
				t.Fatalf("offset = %#v, want %d", got, tt.offset)
			}
			if got := out["limit"]; got != tt.limit {
				t.Fatalf("limit = %#v, want %d", got, tt.limit)
			}
		})
	}
}

func TestNormalizeFacadeRangeDoesNotAffectOtherTools(t *testing.T) {
	out := normalizeFacadeArguments(facadeOperationSpec{Legacy: "get_symbol_source"}, map[string]any{
		"options": map[string]any{"start_line": 3, "end_line": 7},
	})
	if _, ok := out["offset"]; ok {
		t.Fatalf("non-file read unexpectedly gained offset: %#v", out)
	}
	if got := out["start_line"]; got != 3 {
		t.Fatalf("start_line = %#v, want untouched non-file option", got)
	}
}

func TestCapReadFileContentHonorsBudgetAndUTF8(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		maxChars  int
		want      string
		truncated bool
	}{
		{name: "no cap", content: "abcdef", maxChars: 0, want: "abcdef"},
		{name: "under cap", content: "abcdef", maxChars: 8, want: "abcdef"},
		{name: "ascii", content: "abcdef", maxChars: 4, want: "abcd", truncated: true},
		{name: "mid rune", content: "αβγ", maxChars: 3, want: "α", truncated: true},
		{name: "rune boundary", content: "αβγ", maxChars: 4, want: "αβ", truncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := capReadFileContent([]byte(tt.content), tt.maxChars, false)
			if string(got) != tt.want || truncated != tt.truncated {
				t.Fatalf("capReadFileContent() = (%q, %v), want (%q, %v)", got, truncated, tt.want, tt.truncated)
			}
			if tt.maxChars > 0 && len(got) > tt.maxChars {
				t.Fatalf("response bytes = %d, exceeds max_chars=%d", len(got), tt.maxChars)
			}
			if !utf8.Valid(got) {
				t.Fatalf("response is not valid UTF-8: %x", got)
			}
		})
	}
}

func TestParseBatchSymbolIDsKeepsArrayElementsAtomic(t *testing.T) {
	want := []string{
		"crates/searcher/src/searcher/glue.rs::MultiLine<'s, M, S>.run",
		"crates/printer/src/util.rs::Replacer<M>.replace_all",
	}
	for name, raw := range map[string]any{
		"json array":       []any{want[0], want[1]},
		"typed array":      append([]string(nil), want...),
		"facade transport": `["crates/searcher/src/searcher/glue.rs::MultiLine<'s, M, S>.run","crates/printer/src/util.rs::Replacer<M>.replace_all"]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseBatchSymbolIDs(raw)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("parseBatchSymbolIDs(%T) = (%#v, %v), want (%#v, true)", raw, got, ok, want)
			}
		})
	}
}

func TestParseBatchSymbolIDsSplitsOnlyScalarShorthand(t *testing.T) {
	got, ok := parseBatchSymbolIDs(" first, second ")
	want := []string{"first", "second"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBatchSymbolIDs() = (%#v, %v), want (%#v, true)", got, ok, want)
	}
	if _, ok := parseBatchSymbolIDs([]any{"valid", 42}); ok {
		t.Fatal("mixed-type ID array unexpectedly accepted")
	}
}

func BenchmarkReadFileResponseCap(b *testing.B) {
	content := []byte(strings.Repeat("λ", 1<<20))
	const maxChars = 4096
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	var response []byte
	for i := 0; i < b.N; i++ {
		response, _ = capReadFileContent(content, maxChars, false)
	}
	if len(response) > maxChars || !utf8.Valid(response) {
		b.Fatalf("invalid bounded response: bytes=%d valid_utf8=%v", len(response), utf8.Valid(response))
	}
	b.ReportMetric(float64(len(response)), "response_bytes")
}
