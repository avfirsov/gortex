package rerank

import (
	"reflect"
	"testing"
)

func TestLooksLikeKeywordSoup(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  bool
	}{
		// --- Soup: should be flagged. ---
		{"boolean or list", "auth OR login OR signin OR credential", true},
		{"boolean or with quotes", "timeout OR 'no access' OR 'rate limit'", true},
		{"mixed and/or", "validate AND token OR refresh AND session", true},
		{"pipe operators", "auth|login|signin|credential", true},
		{"double pipe", "parse || serialize || marshal", true},
		{"three quoted fragments", "'no access' 'rate limit' 'denied here'", true},
		{"long keyword list with operator", "parse marshal serialize encode decode unmarshal deserialize OR transform", true},

		// --- Operator-free keyword lists: should be flagged. ---
		{"bare space keyword bag", "parse decode unmarshal token jwt cache", true},
		{"bare space keyword bag long", "parse decode encode marshal unmarshal serialize deserialize", true},
		{"comma separated terms", "parse, decode, unmarshal, token", true},
		{"comma separated multiword terms", "rate limit, auth token, session cache, retry backoff", true},

		// --- Not soup: legitimate queries. ---
		{"plain symbol", "validateToken", false},
		{"natural language", "how do we validate the auth token", false},
		{"multi-clause NL", "where does the session get refreshed and stored", false},
		{"prose with lowercase or", "the cache or the store holds the value", false},
		{"two word concept", "auth middleware", false},
		{"single quoted phrase", "find the 'rate limit' handler", false},
		{"path query", "internal/auth/token.go", false},
		{"signature query", "func(ctx) error", false},
		{"empty", "", false},
		{"one operator only", "auth OR login", false},
		// Operator-free NL queries that MUST stay concept: the
		// stop-word spine and the short token count keep them out of
		// the operator-free list bucket.
		{"long NL no operator", "how does the session token cache get flushed", false},
		{"NL six content words", "validate the user auth token cache", false},
		{"prose with one comma aside", "the cache, which holds the value, is flushed", false},
		{"short keyword pair", "parse decode", false},
		{"five keyword bag below threshold", "parse decode unmarshal token jwt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := LooksLikeKeywordSoup(tc.query)
			if got != tc.want {
				t.Fatalf("LooksLikeKeywordSoup(%q) = %v, want %v (reason=%q)", tc.query, got, tc.want, reason)
			}
			if got && reason == "" {
				t.Fatalf("soup query %q flagged without a reason string", tc.query)
			}
			if !got && reason != "" {
				t.Fatalf("non-soup query %q returned a reason %q", tc.query, reason)
			}
		})
	}
}

func TestSplitSoupFragments(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "or list",
			query: "auth OR login OR signin",
			want:  []string{"auth", "login", "signin"},
		},
		{
			name:  "quoted fragments unwrapped",
			query: "timeout OR 'no access' OR \"rate limit\"",
			want:  []string{"timeout", "no access", "rate limit"},
		},
		{
			name:  "pipe operators",
			query: "auth|login|signin",
			want:  []string{"auth", "login", "signin"},
		},
		{
			name:  "double pipe",
			query: "parse || serialize",
			want:  []string{"parse", "serialize"},
		},
		{
			name:  "and operator",
			query: "validate token AND refresh session",
			want:  []string{"validate token", "refresh session"},
		},
		{
			name:  "dedup case insensitive",
			query: "Auth OR auth OR AUTH",
			want:  []string{"Auth"},
		},
		{
			name:  "comma separated terms",
			query: "parse, decode, unmarshal, token",
			want:  []string{"parse", "decode", "unmarshal", "token"},
		},
		{
			name:  "comma separated multiword terms",
			query: "rate limit, auth token, session cache",
			want:  []string{"rate limit", "auth token", "session cache"},
		},
		{
			name:  "operator-free bag splits per token",
			query: "parse decode unmarshal token jwt cache",
			want:  []string{"parse", "decode", "unmarshal", "token", "jwt", "cache"},
		},
		{
			name:  "empty",
			query: "",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitSoupFragments(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitSoupFragments(%q) = %#v, want %#v", tc.query, got, tc.want)
			}
		})
	}
}

func TestKeywordSoupQueryClass(t *testing.T) {
	// A soup query classifies as keyword_soup, overriding the
	// signature / symbol heuristics its disjuncts might trip.
	if got := ClassifyQuery("auth OR login OR signin OR credential"); got != QueryClassKeywordSoup {
		t.Fatalf("ClassifyQuery(soup) = %v, want QueryClassKeywordSoup", got)
	}
	// A genuine NL query does not.
	if got := ClassifyQuery("how do we validate the auth token"); got == QueryClassKeywordSoup {
		t.Fatalf("ClassifyQuery(NL) = keyword_soup, want a non-soup class")
	}
	// Round-trip through the string form and parser.
	if got := QueryClassKeywordSoup.String(); got != "keyword_soup" {
		t.Fatalf("QueryClassKeywordSoup.String() = %q, want keyword_soup", got)
	}
	parsed, ok := ParseQueryClass("keyword_soup")
	if !ok || parsed != QueryClassKeywordSoup {
		t.Fatalf("ParseQueryClass(keyword_soup) = (%v, %v), want (QueryClassKeywordSoup, true)", parsed, ok)
	}
	// "soup" is accepted as a short alias.
	if parsed, ok := ParseQueryClass("soup"); !ok || parsed != QueryClassKeywordSoup {
		t.Fatalf("ParseQueryClass(soup) = (%v, %v), want (QueryClassKeywordSoup, true)", parsed, ok)
	}
}

func TestKeywordSoupClassWeights(t *testing.T) {
	// Soup leans BM25-heavy (LLM channel is suppressed for it).
	bm25 := ClassWeightMultiplier(QueryClassKeywordSoup, SignalBM25)
	sem := ClassWeightMultiplier(QueryClassKeywordSoup, SignalSemantic)
	if bm25 <= 1.0 {
		t.Fatalf("keyword_soup bm25 multiplier = %v, want > 1.0", bm25)
	}
	if sem >= 1.0 {
		t.Fatalf("keyword_soup semantic multiplier = %v, want < 1.0", sem)
	}
	if a := AlphaForClass(QueryClassKeywordSoup); a != AlphaSymbol {
		t.Fatalf("AlphaForClass(keyword_soup) = %v, want AlphaSymbol (%v)", a, AlphaSymbol)
	}
}
