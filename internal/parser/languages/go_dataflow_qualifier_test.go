package languages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/graph"
)

// TestGoDataflow_SelectorCallPreservesPackageQualifier is the
// regression for the dataflow walker dropping the package qualifier
// on selector calls (`fmt.Sprintf`, `strings.Join`, `assert.True`)
// and leaking `unresolved::*.<method>` instead of the proper
// `unresolved::extern::<importPath>::<method>` shape the call
// extractor uses. The resolver's resolveExtern pass then lands
// these on stdlib::/dep::/external::, so without preserving the
// qualifier here every package-qualified call inside a dataflow
// context (argument source, return target, value flow) stays as
// an unresolved phantom.
func TestGoDataflow_SelectorCallPreservesPackageQualifier(t *testing.T) {
	src := `package foo

import (
	"fmt"
	"strings"
)

func Handler(input string) string {
	cleaned := strings.TrimSpace(input)
	return fmt.Sprintf("got: %s", cleaned)
}
`
	fix := runGoExtract(t, src)

	// Every `unresolved::extern::<path>::<method>` target the
	// dataflow walker emits must use the canonical import path,
	// not the `*.method` collapsed form.
	var hasStringsTrimSpace, hasFmtSprintf bool
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			switch e.To {
			case "unresolved::extern::strings::TrimSpace":
				hasStringsTrimSpace = true
			case "unresolved::extern::fmt::Sprintf":
				hasFmtSprintf = true
			}
		}
	}

	assert.True(t, hasStringsTrimSpace,
		"dataflow walker must preserve the `strings` qualifier on TrimSpace(...) calls — got: %s",
		dumpDataflowSelectorTargets(fix))
	assert.True(t, hasFmtSprintf,
		"dataflow walker must preserve the `fmt` qualifier on Sprintf(...) calls — got: %s",
		dumpDataflowSelectorTargets(fix))

	// And the collapsed `*.TrimSpace`/`*.Sprintf` shape must NOT
	// appear for these calls.
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			assert.NotEqual(t, "unresolved::*.TrimSpace", e.To,
				"package-qualified Trim should never land as `unresolved::*.TrimSpace`")
			assert.NotEqual(t, "unresolved::*.Sprintf", e.To,
				"package-qualified Sprintf should never land as `unresolved::*.Sprintf`")
		}
	}
}

// TestGoDataflow_NonImportedReceiverFallsBack ensures the pass
// doesn't false-positive: when the receiver is NOT a package alias
// (a local variable, a struct field), it must keep emitting the
// `unresolved::*.<method>` form so other passes can apply their
// own heuristics.
func TestGoDataflow_NonImportedReceiverFallsBack(t *testing.T) {
	src := `package foo

type Buffer struct{}

func (b *Buffer) Write(p []byte) {}

func Run(buf *Buffer, data []byte) {
	buf.Write(data)
}
`
	fix := runGoExtract(t, src)

	// `buf.Write(data)` — buf is a parameter, NOT an import; the
	// walker's fallback path must keep `*.` (the call extractor's
	// own path already records receiver_type on the call edge).
	var seen bool
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			if e.To == "unresolved::*.Write" {
				seen = true
			}
			assert.NotEqual(t, "unresolved::extern::buf::Write", e.To,
				"`buf` is a parameter — must not be classified as a package alias")
		}
	}
	assert.True(t, seen, "the walker must still emit `unresolved::*.Write` for non-import receivers; "+
		"got: %s", dumpDataflowSelectorTargets(fix))
}

func dumpDataflowSelectorTargets(fix *extractedFixture) string {
	var b strings.Builder
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			if strings.Contains(e.To, "Sprintf") || strings.Contains(e.To, "TrimSpace") || strings.Contains(e.To, "Write") {
				b.WriteString("\n  [" + string(e.Kind) + "] " + e.From + " -> " + e.To)
			}
		}
	}
	return b.String()
}

// guard: also verifies the same fix applies in exprSources (not just
// calleeRef) — a selector accessed as a value (not invoked) should
// also preserve its qualifier. Uses a real stdlib import so the
// extractor's emitImport handler matches its production code path.
func TestGoDataflow_SelectorValuePreservesQualifier(t *testing.T) {
	src := `package foo

import "os"

func DefaultPerm() any {
	return os.ModePerm
}
`
	fix := runGoExtract(t, src)
	_ = graph.KindFunction

	var foundProperShape bool
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			// handleReturn emits `From: src, To: owner` — flow goes
			// FROM the value source TO the function's owner. So the
			// qualified target lives on e.From, not e.To.
			if strings.HasPrefix(e.From, "unresolved::extern::os::") ||
				strings.HasPrefix(e.To, "unresolved::extern::os::") {
				foundProperShape = true
			}
		}
	}
	assert.True(t, foundProperShape,
		"selector-value access (os.ModePerm) must emit the extern:: shape; got:\n%s",
		dumpAllSelectorish(fix))
}

func dumpAllSelectorish(fix *extractedFixture) string {
	var b strings.Builder
	for _, edges := range fix.edgesByKind {
		for _, e := range edges {
			if strings.Contains(e.To, "ModePerm") || strings.Contains(e.To, "::os::") || strings.HasPrefix(e.To, "unresolved::*.") {
				b.WriteString("  [" + string(e.Kind) + "] " + e.From + " -> " + e.To + "\n")
			}
		}
	}
	return b.String()
}
