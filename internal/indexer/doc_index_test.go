package indexer

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestDocSummary_FirstParagraphBounded locks the doc-summary extraction:
// the leading paragraph is kept, later paragraphs (detail / examples) are
// dropped, CRLF is normalised, and a runaway single paragraph is bounded.
func TestDocSummary_FirstParagraphBounded(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single line", "Union the two sequences.", "Union the two sequences."},
		{
			"drops later paragraphs",
			"Performs matching on the ignore files.\n\n# Examples\n\nlet x = ignore();",
			"Performs matching on the ignore files.",
		},
		{
			"keeps multi-line first paragraph",
			"Executes a replacement on the given haystack\nby replacing all matches.\n\nDetail here.",
			"Executes a replacement on the given haystack\nby replacing all matches.",
		},
		{
			"crlf blank line is a paragraph break",
			"Summary line.\r\n\r\nRest of doc.",
			"Summary line.",
		},
		{"whitespace trimmed", "  spaced summary  ", "spaced summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := docSummary(tc.in); got != tc.want {
				t.Errorf("docSummary(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDocSummary_RuneBound proves a long single-paragraph doc is bounded to
// docSummaryMaxRunes so it can't dominate the token bag / dilute the name.
func TestDocSummary_RuneBound(t *testing.T) {
	long := strings.Repeat("word ", 500) // ~2500 runes, one paragraph
	got := docSummary(long)
	if n := len([]rune(got)); n > docSummaryMaxRunes {
		t.Fatalf("docSummary bound not applied: %d runes > %d", n, docSummaryMaxRunes)
	}
}

// TestSearchIndexFields_IncludesDocSummary is the load-bearing regression
// guard: a code symbol's doc comment reaches the BM25 document (the
// task-intent recall fix), while a symbol without a doc keeps an empty
// retrieval-qualifier slot and a prose (KindDoc) node stays untouched.
func TestSearchIndexFields_IncludesDocSummary(t *testing.T) {
	withDoc := &graph.Node{
		Kind: graph.KindFunction, Name: "union", FilePath: "crates/regex/src/literal.rs",
		Meta: map[string]any{
			"signature": "fn union(self, o: Seq) -> Seq",
			"doc":       "Union the two sequences if the result would be within the configured limit.\n\n# Examples\nlet u = a.union(b);",
		},
	}
	fields := searchIndexFields(withDoc, "")
	joined := strings.Join(fields, " || ")
	if !strings.Contains(joined, "Union the two sequences") {
		t.Errorf("doc summary missing from index fields: %q", joined)
	}
	// The example block after the blank line must NOT be indexed.
	if strings.Contains(joined, "let u = a.union") {
		t.Errorf("doc detail past the summary leaked into index fields: %q", joined)
	}

	noDoc := &graph.Node{
		Kind: graph.KindFunction, Name: "helper", FilePath: "x.rs",
		Meta: map[string]any{"signature": "fn helper()"},
	}
	got := searchIndexFields(noDoc, "")
	want := []string{"helper", "x.rs", "", "fn helper()", ""}
	if len(got) != len(want) {
		t.Fatalf("no-doc fields len = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("no-doc field[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	prose := &graph.Node{
		Kind: graph.KindDoc, Name: "Overview", FilePath: "README.md",
		Meta: map[string]any{"section_text": "This project searches text."},
	}
	pf := searchIndexFields(prose, "")
	if len(pf) != 3 || pf[2] != "This project searches text." {
		t.Errorf("KindDoc fields path changed: %q", pf)
	}
}
