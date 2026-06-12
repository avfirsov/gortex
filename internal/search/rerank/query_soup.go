package rerank

import (
	"strings"
	"unicode"
)

// soupStopWords is a tight set of English function words used by the
// keyword-density heuristic to tell a list of keywords ("parse encode
// decode marshal ...") apart from a genuine sentence ("how does the
// parser decode the payload"). Kept deliberately small -- the density
// check is already gated behind a boolean-operator co-signal, so a
// few misses here cannot trip a false positive on prose.
var soupStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "be": {},
	"in": {}, "of": {}, "to": {}, "for": {}, "with": {}, "by": {},
	"from": {}, "on": {}, "at": {}, "as": {}, "do": {}, "does": {},
	"how": {}, "what": {}, "where": {}, "when": {}, "why": {},
	"which": {}, "and": {}, "or": {}, "not": {}, "we": {}, "it": {},
	"this": {}, "that": {}, "into": {}, "get": {}, "gets": {},
}

// LooksLikeKeywordSoup reports whether a query is a degenerate
// boolean / OR-soup string rather than a usable search query -- the
// shape an agent produces when it pastes a list of disjuncts
// (`A OR B OR 'no access' OR error`) instead of describing intent.
// Such queries defeat BM25: every disjunct competes, the rare term
// drowns, and LLM expansion just amplifies the noise.
//
// The second return value is a short human-readable reason, suitable
// for a `query_advice` nudge on the search response.
//
// The detector is deliberately conservative -- a genuine multi-clause
// natural-language query ("how do we validate the auth token and
// refresh it") must NOT be flagged. Soup is identified only when
// several independent signals agree:
//
//   - Two or more standalone boolean operators -- uppercase OR / AND,
//     or a pipe (| / ||). A single lowercase "or" in prose does not
//     count; the operator must be a separate token.
//   - Three or more quoted fragments ('no access', "rate limit"),
//     which is how an agent fences multi-word disjuncts.
//   - A long token run (8+) that is mostly identifier-shaped tokens
//     with almost no English stop-words -- list-of-keywords texture.
//
// Any single strong signal (>=2 boolean operators, or >=3 quoted
// fragments) trips the detector; the operator-co-signal keyword-
// density heuristic trips when it ALSO sees at least one boolean
// operator. A SEPARATE, stricter operator-free branch catches the
// bare space- or comma-separated keyword list ("parse decode
// unmarshal token jwt cache") that carries no operator at all -- it
// demands more tokens and a higher identifier purity than the
// operator-assisted branch precisely because it has no operator to
// lean on, so a long genuine sentence stays classified as a concept
// query.
func LooksLikeKeywordSoup(query string) (bool, string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return false, ""
	}

	boolOps := countBooleanOperators(q)
	quoted := countQuotedFragments(q)

	// Strong signal 1: an explicit boolean disjunction of several
	// clauses. One operator can be a typo or a literal; two-plus is a
	// deliberate (and retrieval-hostile) construction.
	if boolOps >= 2 {
		return true, soupReasonBoolean
	}

	// Strong signal 2: many fenced fragments. Three or more quoted
	// substrings is an agent enumerating disjuncts, not prose.
	if quoted >= 3 {
		return true, "query stacks several quoted fragments; split them into separate searches or describe the target in plain words"
	}

	tokens := strings.Fields(q)

	// Density signal: a long run of identifier-shaped tokens with a
	// near-zero stop-word ratio. On its own this can be a legitimate
	// path-ish or signature-ish query, so it only counts as soup when
	// at least one boolean operator is also present.
	if len(tokens) >= 8 && boolOps >= 1 {
		stop, identShaped := soupTokenStats(tokens)
		stopRatio := float64(stop) / float64(len(tokens))
		identRatio := float64(identShaped) / float64(len(tokens))
		if stopRatio < 0.15 && identRatio > 0.6 {
			return true, "query is a long list of keywords with little connecting language; describe the target in a short phrase, or search one keyword at a time"
		}
	}

	// Operator-free density signal: a bare space- or comma-separated
	// keyword list with no boolean operator at all. Because there is
	// no operator co-signal to lean on, the bar is higher than the
	// operator-assisted branch -- it requires more tokens, a stricter
	// near-zero stop-word ratio, and a higher identifier purity. This
	// is the shape an agent produces when it pastes a bag of search
	// terms ("parse decode unmarshal token jwt cache") instead of a
	// phrase. The comma form is checked first since a comma list is
	// an even stronger enumeration tell.
	if looksLikeCommaList(q) {
		return true, soupReasonCommaList
	}
	if len(tokens) >= soupNoOpMinTokens && boolOps == 0 && quoted == 0 {
		stop, identShaped := soupTokenStats(tokens)
		stopRatio := float64(stop) / float64(len(tokens))
		identRatio := float64(identShaped) / float64(len(tokens))
		// No coherent phrase: a stop-word "spine" (the / a / of / to /
		// how / does ...) is what makes a token run read as a
		// sentence. Demand essentially none of it, plus near-total
		// identifier purity, before declaring an operator-free list.
		if stopRatio <= soupNoOpMaxStopRatio && identRatio >= soupNoOpMinIdentRatio {
			return true, soupReasonNoOpList
		}
	}

	return false, ""
}

// Operator-free keyword-soup thresholds. Deliberately stricter than
// the operator-assisted density branch: with no boolean operator to
// corroborate the list shape, the only thing separating a keyword bag
// from a long noun phrase is token count and stop-word density, so
// both bars are raised.
const (
	// soupNoOpMinTokens is the minimum token count for the
	// operator-free branch. Six terms is past the point where a bag
	// of code words reads as a phrase; shorter lists are too easily a
	// genuine multi-word concept query ("auth token refresh cache").
	soupNoOpMinTokens = 6
	// soupNoOpMaxStopRatio caps how much connective tissue an
	// operator-free list may carry. A single stop-word in six tokens
	// (~0.16) already hints at a phrase, so the ceiling sits just
	// below that -- a true keyword bag has essentially none.
	soupNoOpMaxStopRatio = 0.15
	// soupNoOpMinIdentRatio is the identifier-purity floor. Higher
	// than the operator-assisted branch's 0.6 because the operator-
	// free branch has no other corroborating signal.
	soupNoOpMinIdentRatio = 0.8
)

// soupReasonNoOpList is the advice for an operator-free keyword bag.
const soupReasonNoOpList = "query is a bare list of keywords with no connecting language; describe the target in a short phrase, or search one keyword at a time"

// soupReasonCommaList is the advice for a comma-separated enumeration.
const soupReasonCommaList = "query is a comma-separated list of terms; search one term at a time, or describe the target in a short phrase"

// soupTokenStats counts, over a token slice, how many tokens are
// stop-words and how many are identifier-shaped (after stripping the
// common fencing punctuation). Shared by the density branches so they
// classify tokens identically.
func soupTokenStats(tokens []string) (stop, identShaped int) {
	for _, t := range tokens {
		lower := strings.ToLower(strings.Trim(t, "'\"`,;()[]{}"))
		if lower == "" {
			continue
		}
		if _, ok := soupStopWords[lower]; ok {
			stop++
		}
		if isIdentifierShapedToken(lower) {
			identShaped++
		}
	}
	return stop, identShaped
}

// looksLikeCommaList reports whether the query is a comma-separated
// enumeration of terms -- three or more comma-delimited fragments,
// each a short identifier-shaped run, with no fragment reading as a
// sentence. This is the "parse, decode, unmarshal, token" shape an
// agent emits when it commas-up a term bag. A single comma (an aside
// in prose) or a fragment with several words is NOT a list.
func looksLikeCommaList(q string) bool {
	if !strings.Contains(q, ",") {
		return false
	}
	parts := strings.Split(q, ",")
	frags := 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		words := strings.Fields(p)
		// A genuine list fragment is one or two terms ("rate limit").
		// A fragment of three-plus words is a clause, not a list item
		// -- that points back to prose with an aside, not an
		// enumeration.
		if len(words) > 2 {
			return false
		}
		// Every word in the fragment must be identifier-shaped; a
		// stop-word inside a fragment ("the cache") marks prose.
		for _, w := range words {
			lw := strings.ToLower(strings.Trim(w, "'\"`;()[]{}"))
			if _, ok := soupStopWords[lw]; ok {
				return false
			}
			if !isIdentifierShapedToken(lw) {
				return false
			}
		}
		frags++
	}
	return frags >= 3
}

// soupReasonBoolean is the default advice string for a boolean-OR
// soup query; reused by the search handler when it pins the
// keyword_soup class without re-running the detector.
const soupReasonBoolean = "query reads as a boolean OR-list; search ranks best on a single concept or symbol name -- run one query per disjunct, or describe the intent in plain words"

// countBooleanOperators counts standalone boolean-operator tokens in
// the query. Recognised: uppercase OR / AND as whole words, and a
// pipe (| or ||) as a whole token or wedged between two word
// characters (`a|b`). A lowercase "or"/"and" is treated as prose and
// does NOT count -- that is what keeps genuine sentences out of the
// soup bucket.
func countBooleanOperators(q string) int {
	n := 0
	for _, tok := range strings.Fields(q) {
		switch tok {
		case "OR", "AND", "||", "|":
			n++
			continue
		}
		// An interior pipe (`auth|login`) is a disjunction even when
		// not whitespace-separated.
		if strings.Contains(tok, "|") && tok != "|" {
			// Count each pipe -- `a|b|c` is two disjunctions.
			n += strings.Count(tok, "|")
		}
	}
	return n
}

// countQuotedFragments counts balanced single- or double-quoted
// substrings. An unbalanced trailing quote is ignored.
func countQuotedFragments(q string) int {
	n := 0
	for _, quote := range []byte{'\'', '"'} {
		count := strings.Count(q, string(quote))
		n += count / 2
	}
	return n
}

// isIdentifierShapedToken reports whether a token looks like a code
// identifier or a single content word rather than connective tissue
// -- it is non-empty and made only of letters, digits, underscores,
// and the common namespace separators. Used by the keyword-density
// heuristic; intentionally permissive because the density check is
// already gated behind a boolean-operator co-signal.
func isIdentifierShapedToken(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '_', '.', '/', '-', ':':
			continue
		}
		return false
	}
	return true
}

// SplitSoupFragments breaks a keyword-soup query into its disjunct
// fragments. It splits on the boolean operators LooksLikeKeywordSoup
// recognises (OR / AND / | / ||) AND on commas, unwraps surrounding
// quotes, trims each fragment, and drops empties and duplicates. When
// the query carries no operator and no comma at all -- the bare
// operator-free keyword-list shape -- each whitespace-separated token
// becomes its own fragment, since that list has no delimiter other
// than the spaces between terms. The result feeds the BM25 OR-merge
// path so each disjunct is retrieved on its own terms instead of
// competing inside one mangled query.
func SplitSoupFragments(query string) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	// Normalise the pipe and comma operators to a word boundary so a
	// single Fields pass handles `a OR b`, `a|b`, and `a, b` alike.
	q = strings.ReplaceAll(q, "||", " | ")
	q = strings.ReplaceAll(q, "|", " | ")
	q = strings.ReplaceAll(q, ",", " , ")

	raw := strings.Fields(q)
	hasDelimiter := false
	for _, tok := range raw {
		switch tok {
		case "OR", "AND", "|", ",":
			hasDelimiter = true
		}
	}
	// Operator-free, comma-free list: the only delimiter is the
	// whitespace between terms, so interleave a comma sentinel between
	// every token to force a per-token flush in the shared loop below
	// (which still handles quote-unwrap + dedup uniformly).
	if !hasDelimiter {
		interleaved := make([]string, 0, len(raw)*2)
		for _, tok := range raw {
			interleaved = append(interleaved, tok, ",")
		}
		raw = interleaved
	}

	var (
		out  []string
		cur  []string
		seen = map[string]struct{}{}
	)
	flush := func() {
		if len(cur) == 0 {
			return
		}
		frag := unquoteFragment(strings.Join(cur, " "))
		cur = cur[:0]
		frag = strings.TrimSpace(frag)
		if frag == "" {
			return
		}
		key := strings.ToLower(frag)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, frag)
	}
	for _, tok := range raw {
		switch tok {
		case "OR", "AND", "|", ",":
			flush()
			continue
		}
		cur = append(cur, tok)
	}
	flush()
	return out
}

// unquoteFragment strips a single matched pair of surrounding single
// or double quotes from a fragment. An unbalanced or absent quote
// leaves the string unchanged.
func unquoteFragment(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
