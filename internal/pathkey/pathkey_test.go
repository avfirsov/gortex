package pathkey

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

// All non-ASCII fixtures are spelled with explicit \u / \U escapes so
// this source file stays pure-ASCII and the byte form of each fixture
// is deterministic regardless of the editor encoding used to save it.
//
//   - cafeNFC / cafeNFD: "cafe.go" with an accented e. NFC precomposes
//     it to U+00E9; NFD spells it as base "e" (U+0065) + U+0301
//     COMBINING ACUTE ACCENT. Same filename, different bytes — the
//     exact macOS-NFD vs git/Linux-NFC split this package reconciles.
//   - cjkName / cyrName: CJK ideographs and Cyrillic letters have no
//     canonical decomposition, so they are identical in every normal
//     form; included to prove non-Latin scripts pass through cleanly.
//   - emoji: an astral-plane code point, to prove rune-correctness.
const (
	cafeNFC = "café.go"       // precomposed e-acute
	cafeNFD = "café.go"      // "e" + combining acute accent
	cjkName = "日本語.go"        // CJK: "Japanese language"
	cyrName = "кириллица.go"  // Cyrillic
	emoji   = "\U0001f600.go" // grinning face
)

func TestNormalize_FoldsNFDToNFC(t *testing.T) {
	// The decomposed input must come back as the precomposed form, so
	// a path observed in NFD (macOS) keys identically to the same
	// path observed in NFC (git / Linux).
	if cafeNFD == cafeNFC {
		t.Fatal("test fixture invalid: NFD and NFC forms are byte-identical")
	}
	got := Normalize(cafeNFD)
	if got != cafeNFC {
		t.Fatalf("Normalize(NFD) = %q, want NFC %q", got, cafeNFC)
	}
}

func TestNormalize_NFCIsStable(t *testing.T) {
	// An already-NFC path is returned unchanged — normalisation is
	// idempotent, so re-keying an NFC path never drifts.
	if got := Normalize(cafeNFC); got != cafeNFC {
		t.Fatalf("Normalize(NFC) = %q, want unchanged %q", got, cafeNFC)
	}
}

func TestNormalize_Idempotent(t *testing.T) {
	for _, in := range []string{cafeNFD, cafeNFC, cjkName, cyrName, emoji, "plain/ascii/path.go"} {
		once := Normalize(in)
		twice := Normalize(once)
		if once != twice {
			t.Errorf("Normalize not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

func TestNormalize_ASCIIUnchanged(t *testing.T) {
	// Pure-ASCII paths — the overwhelmingly common case — must pass
	// through byte-identical (and via the isASCII fast path).
	for _, in := range []string{
		"main.go",
		"internal/indexer/watcher.go",
		"./pkg/util.go",
		"a/b/c/d/e.go",
		"",
	} {
		if got := Normalize(in); got != in {
			t.Errorf("Normalize(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestNormalize_AlwaysReturnsNFC(t *testing.T) {
	// Whatever Normalize returns must itself be in NFC — that is the
	// contract every keying call site relies on.
	for _, in := range []string{cafeNFD, cafeNFC, cjkName, cyrName, emoji} {
		got := Normalize(in)
		if !norm.NFC.IsNormalString(got) {
			t.Errorf("Normalize(%q) = %q is not NFC-normalised", in, got)
		}
	}
}

func TestNormalize_PreservesSeparatorsAndContent(t *testing.T) {
	// Normalisation must only touch Unicode composition — it must not
	// drop, reorder, or rewrite path separators or other content.
	cjkDir := "src/" + cjkName // already NFC
	if got := Normalize(cjkDir); got != cjkDir {
		t.Fatalf("Normalize altered an already-NFC CJK path: got %q want %q", got, cjkDir)
	}
	// A decomposed component nested in a multi-segment path folds in
	// place without disturbing the surrounding segments.
	nested := "a/" + cafeNFD + "/b.go"
	want := "a/" + cafeNFC + "/b.go"
	if got := Normalize(nested); got != want {
		t.Fatalf("Normalize(%q) = %q, want %q", nested, got, want)
	}
}

func TestEqual_NFCvsNFD(t *testing.T) {
	// The whole point: the two byte forms of one filename compare
	// equal once folded.
	if !Equal(cafeNFC, cafeNFD) {
		t.Fatalf("Equal(%q, %q) = false, want true", cafeNFC, cafeNFD)
	}
}

func TestEqual_IdenticalShortCircuit(t *testing.T) {
	if !Equal(cjkName, cjkName) {
		t.Fatalf("Equal of identical strings returned false")
	}
}

func TestEqual_DistinctPathsNotEqual(t *testing.T) {
	if Equal(cjkName, cyrName) {
		t.Fatalf("Equal reported two genuinely different paths as equal")
	}
	if Equal("a.go", "b.go") {
		t.Fatalf("Equal reported a.go == b.go")
	}
}

func TestIsASCII(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"plain.go", true},
		{"internal/indexer/watcher.go", true},
		{cafeNFC, false},
		{cafeNFD, false},
		{cjkName, false},
		{cyrName, false},
		{emoji, false},
	}
	for _, c := range cases {
		if got := isASCII(c.in); got != c.want {
			t.Errorf("isASCII(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
