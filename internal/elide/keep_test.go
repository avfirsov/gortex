package elide

import (
	"strings"
	"testing"
)

// keepFixtureGo has three top-level functions on known line numbers
// (1-based): Alpha spans 4-7, Beta spans 10-13, Gamma spans 15-17.
const keepFixtureGo = `package svc

// Alpha is the first.
func Alpha(x int) int {
	y := x + 1
	return y
}

// Beta is the second.
func Beta(x int) int {
	z := x * 2
	return z
}

func Gamma() string {
	return "gamma-body"
}
`

func TestCompressWith_NilKeepMatchesCompress(t *testing.T) {
	plain, err := CompressString(keepFixtureGo, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	withZero, err := CompressStringWith(keepFixtureGo, "go", Options{})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	if plain != withZero {
		t.Errorf("Options{} must reproduce Compress.\nCompress:\n%s\nCompressWith:\n%s", plain, withZero)
	}
}

func TestCompressWith_KeepByName(t *testing.T) {
	out, err := CompressStringWith(keepFixtureGo, "go", Options{Keep: KeepNames([]string{"Beta"})})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	checkContains(t, out, []string{
		"func Alpha(x int) int",
		"func Beta(x int) int",
		"func Gamma() string",
		"z := x * 2", // Beta body kept verbatim
		"return z",
		"lines elided", // Alpha and Gamma still stubbed
	}, []string{
		"y := x + 1",   // Alpha elided
		`"gamma-body"`, // Gamma elided
	})
}

func TestCompressWith_KeepByLineRange(t *testing.T) {
	// Beta's declaration spans lines 10-13; keep exactly that range.
	out, err := CompressStringWith(keepFixtureGo, "go", Options{Keep: KeepLineRanges([][2]int{{10, 13}})})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	if !strings.Contains(out, "z := x * 2") {
		t.Errorf("expected Beta body kept by line range, got:\n%s", out)
	}
	for _, gone := range []string{"y := x + 1", `"gamma-body"`} {
		if strings.Contains(out, gone) {
			t.Errorf("expected %q elided, got:\n%s", gone, out)
		}
	}
}

func TestCompressWith_KeepByLineRange_Reversed(t *testing.T) {
	// A reversed [hi, lo] range must still match — KeepLineRanges
	// normalizes the bounds.
	out, err := CompressStringWith(keepFixtureGo, "go", Options{Keep: KeepLineRanges([][2]int{{13, 10}})})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	if !strings.Contains(out, "z := x * 2") {
		t.Errorf("expected reversed range to still keep Beta, got:\n%s", out)
	}
}

func TestCompressWith_KeepAny(t *testing.T) {
	// Keep Alpha by name and Gamma by line range; Beta is elided.
	keep := KeepAny(
		KeepNames([]string{"Alpha"}),
		KeepLineRanges([][2]int{{15, 17}}),
	)
	out, err := CompressStringWith(keepFixtureGo, "go", Options{Keep: keep})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	checkContains(t, out, []string{
		"y := x + 1",   // Alpha kept by name
		`"gamma-body"`, // Gamma kept by line range
		"lines elided", // Beta stubbed
	}, []string{
		"z := x * 2", // Beta elided
	})
}

func TestCompressWith_KeepNoMatch(t *testing.T) {
	out, err := CompressStringWith(keepFixtureGo, "go", Options{Keep: KeepNames([]string{"DoesNotExist"})})
	if err != nil {
		t.Fatalf("CompressStringWith: %v", err)
	}
	plain, err := CompressString(keepFixtureGo, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	if out != plain {
		t.Errorf("keep with no matching names should equal full compression.\ngot:\n%s\nwant:\n%s", out, plain)
	}
}

func TestKeepHelpers_EmptyInputs(t *testing.T) {
	if KeepLineRanges(nil) != nil {
		t.Error("KeepLineRanges(nil) should be nil")
	}
	if KeepLineRanges([][2]int{}) != nil {
		t.Error("KeepLineRanges(empty) should be nil")
	}
	if KeepNames(nil) != nil {
		t.Error("KeepNames(nil) should be nil")
	}
	if KeepNames([]string{"", ""}) != nil {
		t.Error("KeepNames of only-empty strings should be nil")
	}
	if KeepAny() != nil {
		t.Error("KeepAny() should be nil")
	}
	if KeepAny(nil, nil) != nil {
		t.Error("KeepAny(nil, nil) should be nil")
	}
}

func TestCompressWith_KeepByName_MultiLang(t *testing.T) {
	cases := []struct {
		lang  string
		src   string
		keep  string
		stays string // substring that must survive (kept body)
		gone  string // substring that must be elided
	}{
		{
			lang:  "python",
			src:   "def keep_me():\n    secret = 1\n    return secret\n\ndef drop_me():\n    other = 2\n    return other\n",
			keep:  "keep_me",
			stays: "secret = 1",
			gone:  "other = 2",
		},
		{
			lang:  "typescript",
			src:   "export function keepMe(): number {\n  const a = 11;\n  return a;\n}\n\nexport function dropMe(): number {\n  const b = 22;\n  return b;\n}\n",
			keep:  "keepMe",
			stays: "const a = 11",
			gone:  "const b = 22",
		},
		{
			lang:  "c",
			src:   "int keep_me(void) {\n    int a = 7;\n    return a;\n}\n\nint drop_me(void) {\n    int b = 9;\n    return b;\n}\n",
			keep:  "keep_me",
			stays: "int a = 7",
			gone:  "int b = 9",
		},
		{
			lang:  "rust",
			src:   "fn keep_me() -> i32 {\n    let a = 5;\n    a\n}\n\nfn drop_me() -> i32 {\n    let b = 6;\n    b\n}\n",
			keep:  "keep_me",
			stays: "let a = 5",
			gone:  "let b = 6",
		},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			out, err := CompressStringWith(tc.src, tc.lang, Options{Keep: KeepNames([]string{tc.keep})})
			if err != nil {
				t.Fatalf("CompressStringWith(%s): %v", tc.lang, err)
			}
			if !strings.Contains(out, tc.stays) {
				t.Errorf("%s: expected kept body %q to survive, got:\n%s", tc.lang, tc.stays, out)
			}
			if strings.Contains(out, tc.gone) {
				t.Errorf("%s: expected %q to be elided, got:\n%s", tc.lang, tc.gone, out)
			}
		})
	}
}
