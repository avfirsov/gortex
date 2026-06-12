package languages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/graph"
)

// TestGoDataflow_LocalIDsAreFunctionRelative is the regression for
// the absolute-line local-ID encoding that produced O(locals-in-file)
// edge churn on every save: adding an unrelated line above a function
// shifted every local-binding ID inside it, so the per-file
// incremental update had to delete + re-insert every dataflow edge
// even when nothing inside the function changed.
//
// The function-relative encoding (<owner>#local:<name>@+<offset>)
// anchors each binding's ID to the owner's declaration line, so the
// IDs are invariant under shifts of the function as a whole — only
// edits *inside* the function above a binding shift that binding's
// ID. The test indexes the same source twice — once verbatim, once
// with a comment inserted above the function — and asserts the local
// IDs match exactly.
func TestGoDataflow_LocalIDsAreFunctionRelative(t *testing.T) {
	original := `package foo

func Handler(x int) int {
	y := x
	z := y
	return z
}
`
	// Same Handler, but with 5 unrelated lines of comments above it.
	// If local IDs used absolute lines, every #local: target in the
	// extracted edges would shift by 5 and would NOT match the
	// originals.
	shifted := `package foo

// shimmer
// shimmer
// shimmer
// shimmer
// shimmer
func Handler(x int) int {
	y := x
	z := y
	return z
}
`

	collectLocalIDs := func(t *testing.T, src string) map[string]struct{} {
		t.Helper()
		fix := runGoExtract(t, src)
		ids := map[string]struct{}{}
		for _, edges := range fix.edgesByKind {
			for _, e := range edges {
				for _, ep := range []string{e.From, e.To} {
					if strings.Contains(ep, "#local:") {
						ids[ep] = struct{}{}
					}
				}
			}
		}
		return ids
	}

	origIDs := collectLocalIDs(t, original)
	shiftedIDs := collectLocalIDs(t, shifted)

	// Sanity: the function actually has locals to compare.
	assert.NotEmpty(t, origIDs, "extractor should emit #local: edge endpoints")

	// The two sets must match. Any divergence means a local-ID shifted
	// because of the lines added *above* the function — the exact
	// churn case the offset encoding is meant to prevent.
	assert.Equal(t, origIDs, shiftedIDs,
		"local IDs must stay stable when only lines ABOVE the function move")

	// Belt + suspenders: every #local: ID must carry the offset
	// marker (`@+<n>`) rather than the legacy `@<absoluteLine>`.
	for id := range origIDs {
		at := strings.LastIndex(id, "@")
		assert.Greater(t, at, 0, "id has no @ separator: %q", id)
		assert.Equal(t, byte('+'), id[at+1], "id must encode offset (`@+<n>`), got %q", id)
	}
}

// TestGoDataflow_LocalIDsShiftOnIntraFunctionEdit confirms the
// converse: edits *inside* the function above a binding still shift
// that binding's ID. (The offset encoding only neutralises edits
// outside the function, not inside it — local-line motion within the
// function is the load-bearing disambiguator for the same name
// shadowed at different lines.)
func TestGoDataflow_LocalIDsShiftOnIntraFunctionEdit(t *testing.T) {
	base := `package foo

func Handler(x int) int {
	y := x
	return y
}
`
	withInternalShift := `package foo

func Handler(x int) int {
	_ = 1 // <-- inserted INSIDE the function, above y
	y := x
	return y
}
`
	collect := func(t *testing.T, src string) map[string]struct{} {
		t.Helper()
		ids := map[string]struct{}{}
		for _, edges := range runGoExtract(t, src).edgesByKind {
			for _, e := range edges {
				for _, ep := range []string{e.From, e.To} {
					if strings.Contains(ep, "#local:y@") {
						ids[ep] = struct{}{}
					}
				}
			}
		}
		return ids
	}

	a := collect(t, base)
	b := collect(t, withInternalShift)
	assert.NotEmpty(t, a)
	assert.NotEmpty(t, b)
	assert.NotEqual(t, a, b,
		"adding a line INSIDE the function above the binding MUST shift the local ID — this is the disambiguator for re-bound names")
}

// TestGoClosureIDsAreFunctionRelative is the closure analogue of the
// local-binding test. The closure's anchor used to be the absolute
// `#closure@<line>`; switching it to `#closure@+<offset>` gives the
// same churn-reduction benefit. The Name field still carries the
// absolute line for human readability in outlines.
func TestGoClosureIDsAreFunctionRelative(t *testing.T) {
	original := `package foo

func Outer() func() int {
	return func() int { return 42 }
}
`
	shifted := `package foo

// a
// b
// c
func Outer() func() int {
	return func() int { return 42 }
}
`
	closureNodes := func(t *testing.T, src string) map[string]*graph.Node {
		t.Helper()
		fix := runGoExtract(t, src)
		out := map[string]*graph.Node{}
		for _, n := range fix.nodesByKind[graph.KindClosure] {
			out[n.ID] = n
		}
		return out
	}

	a := closureNodes(t, original)
	b := closureNodes(t, shifted)
	assert.NotEmpty(t, a, "extractor should emit at least one closure node")

	// IDs must match across the shift.
	for id := range a {
		assert.Contains(t, b, id,
			"closure ID must stay stable when only lines ABOVE the enclosing function move")
		assert.True(t, strings.Contains(id, "#closure@+"),
			"closure ID must use the `@+<offset>` form, got %q", id)
	}
}
