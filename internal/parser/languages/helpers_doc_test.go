package languages

import (
	"strings"
	"testing"
)

func TestExtractDocAbove_SlashSlash_Go(t *testing.T) {
	src := []byte(`package x

// Foo does something useful.
// Second line of explanation.
func Foo() {}
`)
	// "func Foo()" is on line 5 → row 4 (0-based).
	got := ExtractDocAbove(src, 4, DocLangSlashSlash)
	want := "Foo does something useful. Second line of explanation."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractDocAbove_StopsAtBlankLine(t *testing.T) {
	src := []byte(`package x

// Old unrelated comment.

// Foo is the real doc.
func Foo() {}
`)
	got := ExtractDocAbove(src, 5, DocLangSlashSlash)
	if got != "Foo is the real doc." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_NoComment(t *testing.T) {
	src := []byte(`package x

func Foo() {}
`)
	if got := ExtractDocAbove(src, 2, DocLangSlashSlash); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractDocAbove_TopOfFile(t *testing.T) {
	src := []byte(`func Foo() {}
`)
	if got := ExtractDocAbove(src, 0, DocLangSlashSlash); got != "" {
		t.Fatalf("expected empty for row 0, got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_JSDoc(t *testing.T) {
	src := []byte(`import x from "y";

/**
 * Computes the answer.
 * Has more detail here.
 */
function compute() {}
`)
	got := ExtractDocAbove(src, 6, DocLangBlockStar)
	if !strings.Contains(got, "Computes the answer.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_StopsAtJSDocTag(t *testing.T) {
	src := []byte(`/**
 * Compute X.
 *
 * @param y the y
 * @returns the result
 */
function compute() {}
`)
	got := ExtractDocAbove(src, 6, DocLangBlockStar)
	if got != "Compute X." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_BlockStar_FallsBackToLineComments(t *testing.T) {
	src := []byte(`// JS-style line doc.
function f() {}
`)
	got := ExtractDocAbove(src, 1, DocLangBlockStar)
	if got != "JS-style line doc." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_Hash_Ruby(t *testing.T) {
	src := []byte(`module X
  # The greeter.
  # Welcomes the user.
  def greet
  end
end
`)
	// "def greet" is line 4 → row 3.
	got := ExtractDocAbove(src, 3, DocLangHash)
	if got != "The greeter. Welcomes the user." {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_CSharpXML_StripsTags(t *testing.T) {
	src := []byte(`using System;

/// <summary>
/// Returns the answer.
/// </summary>
public int Compute() => 42;
`)
	got := ExtractDocAbove(src, 5, DocLangCSharpXML)
	if !strings.Contains(got, "Returns the answer.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDocAbove_Truncates(t *testing.T) {
	long := strings.Repeat("a", docMaxLen+50)
	src := []byte("// " + long + "\nfunc F() {}\n")
	got := ExtractDocAbove(src, 1, DocLangSlashSlash)
	if len(got) > docMaxLen+5 {
		t.Fatalf("doc not truncated: %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-3:])
	}
}

func TestExtractPyDocstring_TripleQuoted(t *testing.T) {
	body := `
    """The docstring.

    Multi-line detail.
    """
    return 42
`
	got := ExtractPyDocstring(body)
	if !strings.HasPrefix(got, "The docstring.") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractPyDocstring_None(t *testing.T) {
	body := `
    return 42
`
	if got := ExtractPyDocstring(body); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestVisibilityByCase(t *testing.T) {
	if v := VisibilityByCase("Foo"); v != VisibilityPublic {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByCase("foo"); v != VisibilityPackage {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByCase(""); v != "" {
		t.Fatalf("got %q", v)
	}
}

func TestVisibilityByUnderscore(t *testing.T) {
	if v := VisibilityByUnderscore("_foo"); v != VisibilityPrivate {
		t.Fatalf("got %q", v)
	}
	if v := VisibilityByUnderscore("foo"); v != VisibilityPublic {
		t.Fatalf("got %q", v)
	}
}

func TestVisibilityFromModifiers(t *testing.T) {
	cases := []struct {
		mods []string
		def  string
		want string
	}{
		{[]string{"public"}, VisibilityPackage, VisibilityPublic},
		{[]string{"private"}, VisibilityPackage, VisibilityPrivate},
		{[]string{"protected"}, VisibilityPackage, VisibilityProtected},
		{[]string{"internal"}, VisibilityPackage, VisibilityInternal},
		{[]string{"pub"}, VisibilityPrivate, VisibilityPublic},
		{[]string{"pub(crate)"}, VisibilityPrivate, VisibilityInternal},
		{[]string{"open"}, VisibilityPackage, VisibilityPublic},
		{[]string{"fileprivate"}, VisibilityInternal, VisibilityPrivate},
		{[]string{"static", "final"}, VisibilityPackage, VisibilityPackage},
		{nil, VisibilityPublic, VisibilityPublic},
	}
	for _, c := range cases {
		if got := VisibilityFromModifiers(c.mods, c.def); got != c.want {
			t.Fatalf("mods=%v default=%q got=%q want=%q", c.mods, c.def, got, c.want)
		}
	}
}
