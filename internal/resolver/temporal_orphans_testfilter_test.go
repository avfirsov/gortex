package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PURPOSE: P0 — a Temporal dispatch whose call site is a TEST file
// (`*_test.go`, `*Test.java`, files under `tests/`, …) must not be
// counted as a broken_dispatch. Test fixtures routinely contain
// `ExecuteActivity` calls whose handlers are mocks or live in another
// repo; counting them as integrity gaps is the dominant false-positive
// source the fork's measurement complained about.
//
// RATIONALE: DetectTemporalOrphans counts an unresolved temporal.stub
// edge off the edge's OWN FilePath (the dispatcher's file). Filtering
// test-file dispatchers there is robust (a pure function of the path —
// no Node.Meta dependency, survives incremental reindex) and only ever
// touches the unresolved branch, so resolved test→activity edges keep
// marking their handler consumed.
//
// KEYWORDS: temporal, broken_dispatch, test-file, false-positive, P0

func TestDetectTemporalOrphans_TestFileCallerExcluded(t *testing.T) {
	b := newTemporalTestGraph()
	b.addStubCall("wf_test", "activity", "ChargeActivity", "pkg/workflow_test.go")
	rep := DetectTemporalOrphans(b.g)
	assert.Empty(t, rep.BrokenDispatch,
		"dispatch from a _test.go file must not count as broken_dispatch")
}

func TestDetectTemporalOrphans_ProdCallerStillCounted(t *testing.T) {
	b := newTemporalTestGraph()
	b.addStubCall("wf", "activity", "ChargeActivity", "pkg/workflow.go")
	rep := DetectTemporalOrphans(b.g)
	require.Len(t, rep.BrokenDispatch, 1,
		"unresolved dispatch from production code stays a broken_dispatch")
	assert.Equal(t, "ChargeActivity", rep.BrokenDispatch[0].Name)
}

func TestDetectTemporalOrphans_JavaTestFileCallerExcluded(t *testing.T) {
	b := newTemporalTestGraph()
	b.addStubCall("svc", "workflow", "OrderWorkflow", "src/main/java/OrderManagerTest.java")
	rep := DetectTemporalOrphans(b.g)
	assert.Empty(t, rep.BrokenDispatch,
		"dispatch from a *Test.java file must not count as broken_dispatch")
}

func TestDetectTemporalOrphans_TestDirCallerExcluded(t *testing.T) {
	b := newTemporalTestGraph()
	b.addStubCall("h", "activity", "HelperActivity", "pkg/tests/helper.go")
	rep := DetectTemporalOrphans(b.g)
	assert.Empty(t, rep.BrokenDispatch,
		"dispatch from a file under a tests/ directory must not count")
}
