package rerank

import "strings"

// SourceBiasSignal promotes a production (non-test) symbol above its
// own test when BOTH surface for the same query. It is deliberately
// batch-relative: it contributes 0 unless a test candidate for the
// same symbol is present in the candidate set, so it never shifts
// scores on the common case where no test co-occurs — it only breaks
// the tie when an implementation and its test both match (e.g. the
// query "validateToken" surfaces both auth/token.go::ValidateToken
// and auth/token_test.go::TestValidateToken).
//
// This complements PathPenaltySignal (which demotes test files
// unconditionally): the penalty pushes the test down, this boost
// pulls the matching implementation up, and together they give the
// crisp "source over test" ordering an agent wants when it asks for a
// definition. Because it fires only on a co-occurring source/test
// pair, it does not double-count the path penalty across the whole
// result set.
type SourceBiasSignal struct{}

// Name returns the canonical signal name registered in DefaultWeights.
func (SourceBiasSignal) Name() string { return SignalSourceBias }

// Contribute returns 1.0 for a production candidate whose name stem
// matches a test candidate present in the same batch, else 0. Nil and
// test candidates contribute 0.
func (SourceBiasSignal) Contribute(_ string, c *Candidate, ctx *Context) float64 {
	if c == nil || c.Node == nil || ctx == nil {
		return 0
	}
	if len(ctx.testNameStems) == 0 {
		return 0
	}
	fp := c.Node.FilePath
	if fp == "" || isTestPath(fp) {
		// The candidate is itself a test (or pathless) — no boost.
		return 0
	}
	if _, ok := ctx.testNameStems[strings.ToLower(c.Node.Name)]; ok {
		return 1.0
	}
	return 0
}

// isTestPath reports whether a file path looks like a test file. Shares
// the rubric PathPenaltySignal uses for its test tier so the two
// signals agree on what "a test" is.
func isTestPath(fp string) bool {
	return pathRETest.MatchString(strings.ReplaceAll(fp, "\\", "/"))
}

// testNameStem normalises a test symbol name to the implementation
// name it most likely exercises: lowercased, with the conventional
// test affixes stripped. TestValidateToken -> validatetoken,
// validate_token_test -> validate_token, describeAuthSpec -> describeauth.
func testNameStem(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(s, "test_"):
		s = s[len("test_"):]
	case strings.HasPrefix(s, "test"):
		s = s[len("test"):]
	}
	switch {
	case strings.HasSuffix(s, "_test"):
		s = s[:len(s)-len("_test")]
	case strings.HasSuffix(s, "test"):
		s = s[:len(s)-len("test")]
	case strings.HasSuffix(s, "_spec"):
		s = s[:len(s)-len("_spec")]
	case strings.HasSuffix(s, "spec"):
		s = s[:len(s)-len("spec")]
	}
	return strings.Trim(s, "_")
}
