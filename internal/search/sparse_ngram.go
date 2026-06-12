package search

import (
	"os"
	"strings"
)

// sparseNgramEnabled gates the optional sub-word n-gram emission stage
// layered over the fixed-rule word tokens that Tokenize / TokenizeQuery
// already produce. Default OFF: emitting character n-grams for every
// word token multiplies the posting set, and on identifier-heavy
// queries the extra sub-word noise can demote an exact match — a
// precision regression we are not willing to ship enabled by default
// until it proves out against the recall fixture. Opt in with
// GORTEX_SPARSE_NGRAM=1 (also true / yes / on / y).
//
// Read once at process start, exactly like the FTS-stemming and
// bigram-typo flags. The index built during a daemon's lifetime and
// every query against it share a single setting, so a mid-session
// toggle can never desynchronise the n-grammed postings from the
// n-grammed query terms: the same emission runs on the index path
// (BM25Backend.Add) and the query path (BM25Backend.Search) because
// both route their word tokens through ExpandSparseNgrams.
var sparseNgramEnabled = sparseNgramFromEnv()

func sparseNgramFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GORTEX_SPARSE_NGRAM"))) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}

// Sub-word n-gram bounds. Character n-grams in this closed range are
// emitted over each word token when no learned boundary table is
// supplied. n=3..4 is the usual sweet spot for code identifiers: short
// enough to bridge a morphological or typo gap ("valid" reaches
// "validate" via shared trigrams), long enough to stay discriminative
// (bigrams collide across nearly every token, which is why the typo
// side index keeps them separate from the BM25 postings).
const (
	sparseNgramMinN = 3
	sparseNgramMaxN = 4
	// sparseNgramMinTokenLen skips tokens no longer than the smallest
	// n-gram — a token of length <= sparseNgramMinN has at most one
	// n-gram equal to itself, so it carries no extra sub-word signal
	// and only inflates the posting set.
	sparseNgramMinTokenLen = sparseNgramMinN + 1
)

// NgramBoundaries is the data-driven split source the sparse-ngram
// stage consults when one is available. A learned boundary table built
// at index time (see BuildNgramBoundaries) satisfies it, but the
// tokenizer depends only on this abstraction so it compiles and runs
// with a nil source — degrading cleanly to fixed character n-grams.
//
// Empty reports whether the source carries any learned boundaries; an
// empty source is treated exactly like a nil one. Split cuts a token's
// runes at the source's boundaries and returns the resulting segments
// left-to-right; it must be deterministic for a given input.
type NgramBoundaries interface {
	Empty() bool
	Split(runes []rune) []string
}

// ExpandSparseNgrams returns the input word tokens unchanged when the
// sparse-ngram gate is off, and otherwise returns the input tokens
// followed by their emitted sub-word n-grams. The original word tokens
// are always preserved and always come first, so enabling the gate can
// only ADD recall paths — an exact word match still scores through the
// untouched word token, and the appended sub-word grams open additional
// fuzzy-match paths.
//
// The same function runs on both the index and query paths, so the
// postings written for a symbol and the terms a query probes them with
// are produced by identical logic and can never disagree.
//
// When a non-nil, non-empty boundary source is supplied the split
// points are data-driven: each token is cut at the source's
// high-information boundaries and the resulting segments are emitted
// alongside the original token. When the source is nil or empty the
// stage degrades to fixed character n-grams in
// [sparseNgramMinN, sparseNgramMaxN], so the tokenizer behaves
// identically whether or not a learned table has been built yet.
//
// The result is a freshly allocated slice; the input is left untouched.
// Emission is deterministic: for a given token and source the n-grams
// are produced left-to-right in a fixed order with no map iteration.
func ExpandSparseNgrams(tokens []string, table NgramBoundaries) []string {
	if !sparseNgramEnabled || len(tokens) == 0 {
		return tokens
	}
	// Preserve the original word tokens verbatim, in order, then append
	// the sub-word grams. Over-allocate modestly for the common case.
	out := make([]string, 0, len(tokens)*3)
	out = append(out, tokens...)
	for _, tok := range tokens {
		out = appendSparseNgrams(out, tok, table)
	}
	return out
}

// appendSparseNgrams appends the sub-word n-grams of one lowercase word
// token to dst and returns the grown slice. A learned boundary source,
// when present and non-empty, drives the split; otherwise the token is
// sliced into fixed-width character n-grams. Tokens too short to carry
// any sub-word signal contribute nothing. Duplicate grams within a
// single token are collapsed so a token like "aaaa" does not emit the
// same gram twice.
func appendSparseNgrams(dst []string, tok string, table NgramBoundaries) []string {
	r := []rune(tok)
	if len(r) < sparseNgramMinTokenLen {
		return dst
	}

	seen := make(map[string]struct{}, len(r))
	emit := func(g string) {
		if g == "" || g == tok {
			return
		}
		if _, dup := seen[g]; dup {
			return
		}
		seen[g] = struct{}{}
		dst = append(dst, g)
	}

	if table != nil && !table.Empty() {
		// Data-driven: split the token at the learned high-information
		// boundaries, then emit each segment. Segments shorter than the
		// minimum n are dropped — they collide across too many tokens to
		// be useful sub-word keys.
		for _, seg := range table.Split(r) {
			if len([]rune(seg)) >= sparseNgramMinN {
				emit(seg)
			}
		}
		return dst
	}

	// Fixed character n-grams in [min, max]. Cap the upper n at the
	// token length so a short token still yields its single full-length
	// gram rather than nothing.
	for n := sparseNgramMinN; n <= sparseNgramMaxN; n++ {
		if n > len(r) {
			break
		}
		for i := 0; i+n <= len(r); i++ {
			emit(string(r[i : i+n]))
		}
	}
	return dst
}
