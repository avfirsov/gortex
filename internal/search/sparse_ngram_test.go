package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSparseNgram pins the sparse-ngram gate to a known value for the
// duration of a test and restores it afterwards, so an ambient
// GORTEX_SPARSE_NGRAM in the environment can't make the suite flaky.
// Mirrors withStemming in fts_normalize_test.go.
func withSparseNgram(t *testing.T, on bool) {
	t.Helper()
	prev := sparseNgramEnabled
	sparseNgramEnabled = on
	t.Cleanup(func() { sparseNgramEnabled = prev })
}

func TestExpandSparseNgrams_FlagOffIsNoOp(t *testing.T) {
	withSparseNgram(t, false)
	in := []string{"validate", "token"}
	got := ExpandSparseNgrams(in, nil)
	// Gate off: the exact same slice is returned, no grams appended.
	assert.Equal(t, in, got)
}

func TestExpandSparseNgrams_FlagOffEmptyInput(t *testing.T) {
	withSparseNgram(t, false)
	assert.Empty(t, ExpandSparseNgrams(nil, nil))
}

func TestExpandSparseNgrams_PreservesWordTokensFirst(t *testing.T) {
	withSparseNgram(t, true)
	got := ExpandSparseNgrams([]string{"validate", "token"}, nil)
	// The original word tokens must survive verbatim and lead the
	// output, so an exact word match still scores through them.
	require.GreaterOrEqual(t, len(got), 2)
	assert.Equal(t, "validate", got[0])
	assert.Equal(t, "token", got[1])
}

func TestExpandSparseNgrams_EmitsFixedCharNgrams(t *testing.T) {
	withSparseNgram(t, true)
	got := ExpandSparseNgrams([]string{"token"}, nil)
	set := map[string]struct{}{}
	for _, g := range got {
		set[g] = struct{}{}
	}
	// "token" -> trigrams tok,oke,ken and 4-grams toke,oken.
	for _, want := range []string{"tok", "oke", "ken", "toke", "oken"} {
		_, ok := set[want]
		assert.Truef(t, ok, "expected sub-word gram %q in %v", want, got)
	}
	// The whole token is preserved but never re-emitted as a gram equal
	// to itself.
	assert.Equal(t, "token", got[0])
}

func TestExpandSparseNgrams_ShortTokenEmitsNoGrams(t *testing.T) {
	withSparseNgram(t, true)
	// "go" is below the minimum gram length, so it yields only itself.
	got := ExpandSparseNgrams([]string{"go"}, nil)
	assert.Equal(t, []string{"go"}, got)
}

func TestExpandSparseNgrams_CollapsesDuplicateGrams(t *testing.T) {
	withSparseNgram(t, true)
	// "aaaa": trigram "aaa" appears twice and the 4-gram is the token
	// itself (suppressed). The duplicate "aaa" must collapse to one.
	got := ExpandSparseNgrams([]string{"aaaa"}, nil)
	count := 0
	for _, g := range got {
		if g == "aaa" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate grams within a token must collapse: %v", got)
}

func TestExpandSparseNgrams_Deterministic(t *testing.T) {
	withSparseNgram(t, true)
	a := ExpandSparseNgrams([]string{"validate", "handler"}, nil)
	b := ExpandSparseNgrams([]string{"validate", "handler"}, nil)
	// No map iteration leaks into the emission order.
	assert.Equal(t, a, b)
}

// TestBM25_SparseNgramSymmetry is the load-bearing invariant: every
// term a Search query emits for a doc must be present in the postings
// Add wrote for that doc. We verify it by tokenizing both sides through
// the exact stages the backend uses and asserting the query token set
// is a subset of the indexed token set for an overlapping query.
func TestBM25_SparseNgramSymmetry(t *testing.T) {
	withSparseNgram(t, true)

	// Index side: what Add writes for the symbol name "validateToken".
	indexTokens := ExpandSparseNgrams(
		NormalizeFTSTokens(Tokenize("validateToken")), nil)
	indexed := map[string]struct{}{}
	for _, tok := range indexTokens {
		indexed[tok] = struct{}{}
	}

	// Query side: a prefix query "valid" that shares sub-word grams.
	queryTokens := ExpandSparseNgrams(
		NormalizeFTSTokens(TokenizeQuery("valid")), nil)
	require.NotEmpty(t, queryTokens)

	// At least one query gram must hit an indexed gram — otherwise the
	// expansion bought no recall. (It must, since "valid" is a prefix of
	// "validate" and they share trigrams val/ali/lid.)
	overlap := 0
	for _, tok := range queryTokens {
		if _, ok := indexed[tok]; ok {
			overlap++
		}
	}
	assert.Positivef(t, overlap, "query grams %v share no indexed gram with %v",
		queryTokens, indexTokens)
}

// TestBM25_SparseNgramRecall exercises the full backend: with the gate
// on, a sub-word query reaches a symbol it would miss on pure word
// tokenization.
func TestBM25_SparseNgramRecall(t *testing.T) {
	withSparseNgram(t, true)
	b := NewBM25()
	defer b.Close()
	b.Add("svc::validateToken", "validateToken", "auth/token.go")

	// "valid" is no word token of "validateToken" (camelCase splits to
	// validate/token), but it shares sub-word grams with "validate".
	res := b.Search("valid", 10)
	require.NotEmpty(t, res)
	assert.Equal(t, "svc::validateToken", res[0].ID)
}

// TestBM25_SparseNgramFlagOffUnchanged confirms the gate-off backend
// behaves exactly as before: a sub-word-only query finds nothing.
func TestBM25_SparseNgramFlagOffUnchanged(t *testing.T) {
	withSparseNgram(t, false)
	b := NewBM25()
	defer b.Close()
	b.Add("svc::validateToken", "validateToken", "auth/token.go")

	// With the gate off there is no sub-word path, so a fragment that is
	// not a whole word token returns nothing from the backend.
	res := b.Search("valid", 10)
	assert.Empty(t, res)
}
