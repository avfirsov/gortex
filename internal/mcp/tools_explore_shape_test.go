package mcp

import (
	"strings"
	"testing"
)

const exploreLongIgnoreTask = `Hidden files whitelisted by an ancestor .ignore file are not searched when "." is passed as the directory argument to ripgrep (but are found when no argument is given, or "./." is given). This relates to how paths are canonicalized/parsed and how the ignore/ancestor-parent logic determines the "root" for computing ancestor .ignore whitelist rules, likely in the ignore crate's directory traversal or the path argument handling in core (hiargs/paths). Need the exact file/function responsible for treating "." specially versus other paths when building parents/ancestors for gitignore matching.`

// TestShapeExploreQuery_DistilsReport locks the report-distillation behavior:
// a pasted issue (a title, then a body of repro commands, template prompts and
// an environment table) is reduced to its retrieval signal — the lead weighted,
// the boilerplate dropped — so the defect description is no longer out-weighed
// by command-line flags and log lines. The fixture is synthetic; nothing here
// is drawn from any real project or task corpus.
func TestShapeExploreQuery_DistilsReport(t *testing.T) {
	issue := "Retry backoff never fires on throttled responses\n\n" +
		"Please tick this box to confirm you searched existing issues.\n\n" +
		"What version are you running?\n3.2.1\n" +
		"How did you install it?\npackage manager\n\n" +
		"Describe the bug.\n" +
		"The exponential backoff scheduler skips its delay when the server returns a throttle status, " +
		"so the client hammers the endpoint instead of waiting.\n\n" +
		"```\n$ mytool run --retries 5 --backoff exponential\nWARN: retry storm detected\n```\n"
	got := shapeExploreQuery(issue)

	if strings.Contains(got, "```") || strings.Contains(got, "mytool run") || strings.Contains(got, "--backoff") {
		t.Errorf("fenced repro command leaked into query: %q", got)
	}
	if strings.Contains(got, "What version are you running") {
		t.Errorf("issue-template prompt (ends in ?) leaked: %q", got)
	}
	if strings.Contains(got, "3.2.1") || strings.Contains(got, "package manager") {
		t.Errorf("short env-answer line leaked: %q", got)
	}
	// The lead is present and weighted (appears at least twice).
	if strings.Count(got, "Retry backoff never fires on throttled responses") < 2 {
		t.Errorf("lead not present/weighted: %q", got)
	}
	// The defect description survives.
	if !strings.Contains(got, "exponential backoff scheduler skips its delay") {
		t.Errorf("defect description dropped: %q", got)
	}
}

// TestShapeExploreQuery_PassThrough locks that a short focused query — the
// already-good case — is returned byte-for-byte unchanged.
func TestShapeExploreQuery_PassThrough(t *testing.T) {
	for _, q := range []string{
		"the retry backoff never triggers on a throttled response",
		"where is the websocket upgrade handled",
		"", // empty stays empty
		"a single overly long line with no newline at all that is definitely more than three hundred characters long so it crosses the size threshold but has no lead/body structure to distil because there is exactly one line and nothing resembling a report body here at all so it must pass through",
	} {
		if got := shapeExploreQuery(q); got != q {
			t.Errorf("focused query altered:\n in:  %q\n out: %q", q, got)
		}
	}
}

func TestShapeExploreQueryWeightsLongSingleLineAndRetainsLateConcepts(t *testing.T) {
	got := shapeExploreQuery(exploreLongIgnoreTask)
	lead := inlineLeadClause(exploreLongIgnoreTask)
	if lead == "" || strings.Count(got, lead) != 2 {
		t.Fatalf("long inline lead not weighted exactly once: lead=%q query=%q", lead, got)
	}
	terms := exploreConceptRecallTerms(got)
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		seen[term] = true
	}
	for _, term := range []string{"ignore", "path", "parent", "gitignore", "matching"} {
		if !seen[term] {
			t.Fatalf("late/repeated concept %q was lost from bounded terms %v", term, terms)
		}
	}
}

// TestShapeReportBody_Bounds locks the rune bound so a runaway single
// paragraph cannot re-drown the weighted lead.
func TestShapeReportBody_Bounds(t *testing.T) {
	long := strings.Repeat("alpha beta gamma delta ", 200) // one long line, >> bound
	got := shapeReportBody(long)
	if n := len([]rune(got)); n > shapeBodyMaxRunes {
		t.Fatalf("body bound not applied: %d > %d", n, shapeBodyMaxRunes)
	}
}

// TestShapeInlineQuery_DropsInertTokens locks the single-line arm: a
// report-derived one-liner (flag tokens mark it as such) loses its
// commit-SHA and version tokens — they can never name a code symbol —
// while flags and quoted literals stay verbatim. Synthetic fixture.
func TestShapeInlineQuery_DropsInertTokens(t *testing.T) {
	q := "scheduler skips delay with --retries and --backoff flags, regression from commit ab12cd3 in 3.2.1"
	got := shapeInlineQuery(q)
	if strings.Contains(got, "ab12cd3") {
		t.Errorf("commit-SHA token survived: %q", got)
	}
	if strings.Contains(got, "3.2.1") {
		t.Errorf("version token survived: %q", got)
	}
	if !strings.Contains(got, "--retries") || !strings.Contains(got, "--backoff") {
		t.Errorf("flag tokens must stay verbatim (they carry feature vocabulary): %q", got)
	}
}

// TestShapeInlineQuery_WeightsLeadClause locks the lead-clause repetition:
// a noise-marked one-liner with clause structure gets its headline clause
// appended once, so the defect statement out-weighs the trailing detail.
func TestShapeInlineQuery_WeightsLeadClause(t *testing.T) {
	q := "--verbose with --follow causes duplicate lines; watcher re-emits the same buffer region near rotation"
	got := shapeInlineQuery(q)
	lead := "--verbose with --follow causes duplicate lines"
	if strings.Count(got, lead) != 2 {
		t.Errorf("lead clause not repeated exactly once:\n got: %q", got)
	}
	// A namespaced identifier's "::" must not split the clause.
	q2 := "nondeterminism in walker::TreeBuilder parallel visit when a rule from one root leaks into another"
	if got2 := shapeInlineQuery(q2); got2 != q2 {
		t.Errorf("no-noise query with '::' was altered: %q", got2)
	}
}

// TestShapeInlineQuery_CleanPassThrough locks rule one: a clean prose /
// identifier query — including quoted plain identifiers, apostrophes, and
// dotted versions absent — passes byte-for-byte unchanged.
func TestShapeInlineQuery_CleanPassThrough(t *testing.T) {
	for _, q := range []string{
		"hidden files whitelisted by ancestor .ignore are not searched when . is passed as directory argument",
		`create() initializer type problems with mutators - Get<Mutate<StoreApi<T>, M>, "setState"> not inferring`,
		"RedirectFixedPath panics with invalid node type when path doesn't match any route",
		"HandlerRegistry: validate 'from' parameter length limit and buildContent message length limit",
		"formatDate() TypeError UTCDateTime expects integer, string given",
	} {
		if got := shapeInlineQuery(q); got != q {
			t.Errorf("clean query altered:\n in:  %q\n out: %q", q, got)
		}
	}
}

// TestShapeInlineQuery_QuotedRegexTriggers locks that a quoted regex
// literal marks the query as report-derived (triggering the inert-token
// drop) while the literal itself stays verbatim — it may carry the only
// discriminating token.
func TestShapeInlineQuery_QuotedRegexTriggers(t *testing.T) {
	q := `matcher -i "a.b|ab" fails to match "a-b" case insensitive regression from commit fe98dc7`
	got := shapeInlineQuery(q)
	if got == q {
		t.Fatalf("quoted regex literal did not trigger shaping: %q", got)
	}
	if !strings.Contains(got, `"a.b|ab"`) {
		t.Errorf("quoted literal must stay verbatim: %q", got)
	}
	if strings.Contains(got, "fe98dc7") {
		t.Errorf("commit-SHA token survived: %q", got)
	}
}

// TestIsSHAToken pins the commit-hash rubric: hex length band with at
// least one digit AND one hex letter, so issue ids and letter-only words
// never qualify.
func TestIsSHAToken(t *testing.T) {
	cases := map[string]bool{
		"ab12cd3":  true,  // abbreviated hash
		"1234567":  false, // decimal — an issue id
		"abcdefa":  false, // letters only
		"ab12cd":   false, // too short
		"fe98dc7e": true,
	}
	for in, want := range cases {
		if got := isSHAToken(in); got != want {
			t.Errorf("isSHAToken(%q) = %v, want %v", in, got, want)
		}
	}
}
