package lsp

import "testing"

func TestIdentifierColumn(t *testing.T) {
	src := []byte("package main\n\n// blank doc\nfunc (f *Foo) Bar() error {\n\treturn nil\n}\n")
	cases := []struct {
		name string
		line int
		want int
	}{
		// "func (f *Foo) Bar() error {" — Bar starts at col 14.
		{"Bar", 4, 14},
		// "package main" — main at col 8.
		{"main", 1, 8},
		// Identifier not on the requested line — fall back to 0.
		{"Bar", 1, 0},
		// Empty name is the fallback path.
		{"", 4, 0},
		// Past EOF — fallback.
		{"Bar", 99, 0},
	}
	for _, c := range cases {
		got := identifierColumn(src, c.line, c.name)
		if got != c.want {
			t.Errorf("identifierColumn(_, line=%d, name=%q) = %d, want %d", c.line, c.name, got, c.want)
		}
	}
}

func TestIdentifierColumn_HandlesIndentedDefs(t *testing.T) {
	// Indented method declaration — col=0 was the bug (would
	// resolve to whitespace), col=index_of_name is the fix.
	src := []byte("class Foo {\n  def hello() {\n  }\n}\n")
	got := identifierColumn(src, 2, "hello")
	if got != 6 {
		t.Errorf("expected col 6 for indented method, got %d", got)
	}
}

func TestIdentifierColumn_FirstOccurrenceWins(t *testing.T) {
	// When the name appears multiple times on the same line we
	// pick the first — matches what tree-sitter and most LSP
	// servers expect for hover/refs at the declaration site.
	src := []byte("func foo() { foo() }\n")
	got := identifierColumn(src, 1, "foo")
	if got != 5 {
		t.Errorf("expected col 5 (declaration site), got %d", got)
	}
}
