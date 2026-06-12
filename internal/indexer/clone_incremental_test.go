package indexer

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/clones"
	"github.com/zzet/gortex/internal/graph"
)

// Three small Go files holding cross-file near-duplicate (Type-2) function
// pairs: every identifier is renamed but the control flow is identical, so
// MinHash + LSH flags them as clones and emits EdgeSimilarTo. The shapes
// are deliberately spread across files so the incremental path exercises
// cross-file pair emission (UpdateFuncs querying the live LSH index, not
// just within-file pairs).

const cloneIncFileA = `package main

func sumActiveItems(items []Item) int {
	total := 0
	for i := 0; i < len(items); i++ {
		if items[i].Active {
			total += items[i].Weight * factor
		} else {
			total -= items[i].Penalty
		}
	}
	if total < 0 {
		total = 0
	}
	return total
}

func parseAndValidate(input string) (string, error) {
	parts := splitOnComma(input)
	if len(parts) == 0 {
		return "", errEmpty
	}
	first := parts[0]
	if first == "" {
		return "", errBlank
	}
	return normalize(first), nil
}
`

const cloneIncFileB = `package main

func sumEnabledRecords(records []Record) int {
	sum := 0
	for idx := 0; idx < len(records); idx++ {
		if records[idx].Enabled {
			sum += records[idx].Score * multiplier
		} else {
			sum -= records[idx].Fine
		}
	}
	if sum < 0 {
		sum = 0
	}
	return sum
}

func openAndScanRows(conn *Conn, statement string) error {
	rows, err := conn.Query(statement)
	if err != nil {
		return wrap(err, "query failed")
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return scanErr
		}
	}
	return rows.Err()
}
`

const cloneIncFileC = `package main

func decodeAndCheck(payload string) (string, error) {
	segments := splitOnComma(payload)
	if len(segments) == 0 {
		return "", errEmpty
	}
	head := segments[0]
	if head == "" {
		return "", errBlank
	}
	return normalize(head), nil
}
`

// writeCloneIncFixture writes the three-file fixture into dir and returns
// the absolute paths in a stable order.
func writeCloneIncFixture(t *testing.T, dir string) []string {
	t.Helper()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	c := filepath.Join(dir, "c.go")
	writeFile(t, a, cloneIncFileA)
	writeFile(t, b, cloneIncFileB)
	writeFile(t, c, cloneIncFileC)
	return []string{a, b, c}
}

// similarEdgeSet returns the EdgeSimilarTo {From,To} directed-edge set.
func similarEdgeSet(g graph.Store) map[[2]string]struct{} {
	set := make(map[[2]string]struct{})
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeSimilarTo {
			set[[2]string{e.From, e.To}] = struct{}{}
		}
	}
	return set
}

// TestCloneIncremental_MatchesBatch is the equivalence test: the
// EdgeSimilarTo set produced by the whole-graph batch clone pass must be
// IDENTICAL to the set produced by driving the incremental maintainer
// (EvictFuncs/UpdateFuncs) over the same files one at a time. At this small
// scale the CMS is identical between the two paths (no boilerplate
// filtering kicks in below cmsMinCorpus) so there is zero drift, making
// exact set equality the correct assertion.
func TestCloneIncremental_MatchesBatch(t *testing.T) {
	dir := t.TempDir()
	files := writeCloneIncFixture(t, dir)
	require.Greater(t, len(files), 1, "fixture must be multi-file")

	// (a) Batch path: full cold index on graph A.
	gA := graph.New()
	idxA := newTestIndexer(gA)
	_, err := idxA.Index(dir)
	require.NoError(t, err)
	batch := similarEdgeSet(gA)
	require.GreaterOrEqual(t, len(batch), 1, "fixture must produce >=1 EdgeSimilarTo (non-vacuity)")

	// (b) Incremental path: fresh graph B. The full Index() seeds the
	// incremental clone index (IndexCtx calls Rebuild at the end →
	// built=true). Re-indexing each file then drives EvictFuncs +
	// UpdateFuncs through the incremental maintainer.
	gB := graph.New()
	idxB := newTestIndexer(gB)
	_, err = idxB.Index(dir)
	require.NoError(t, err)
	require.True(t, idxB.cloneIndex.built, "incremental clone index must be built after full Index()")

	for _, f := range files {
		require.NoError(t, idxB.IndexFile(f))
	}
	incremental := similarEdgeSet(gB)

	assert.Equal(t, batch, incremental,
		"incremental clone edges must exactly equal the batch clone edges")
}

// TestCloneIncremental_WarmRestart simulates a daemon warm restart: after a
// full index, the in-memory CMS/LSH state is thrown away and the index is
// rebuilt purely from the persisted clone_shingles sidecar + the graph's
// clone_sig stamps. A subsequent single-file reindex must produce the same
// EdgeSimilarTo set as before the restart.
func TestCloneIncremental_WarmRestart(t *testing.T) {
	dir := t.TempDir()
	files := writeCloneIncFixture(t, dir)
	require.Greater(t, len(files), 1, "fixture must be multi-file")

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	want := similarEdgeSet(g)
	require.GreaterOrEqual(t, len(want), 1, "fixture must produce >=1 EdgeSimilarTo (non-vacuity)")

	// Simulate restart: drop the live incremental index and rebuild a
	// fresh one from scratch. Rebuild reads clone_sig off the graph and
	// clone_shingles from the sidecar (the in-memory *Graph persisted
	// them during finaliseCloneSignatures). No re-parse happens.
	idx.cloneIndex = newIncrementalCloneIndex()
	require.False(t, idx.cloneIndex.built)
	idx.cloneIndex.Rebuild(g, idx.repoPrefix)
	require.True(t, idx.cloneIndex.built, "Rebuild must mark the index built")
	require.Greater(t, idx.cloneIndex.corpus, 1,
		"Rebuild must reseed the corpus from clone_sig nodes")

	// A single-file reindex now runs through the incremental maintainer
	// seeded only from the sidecar. The edge set must be unchanged.
	require.NoError(t, idx.IndexFile(files[0]))
	got := similarEdgeSet(g)
	assert.Equal(t, want, got,
		"clone edges after a sidecar-only rebuild + reindex must match the pre-restart set")
}

// writeCloneFilteredFixture writes a large fixture engineered to push the
// corpus over a (test-lowered) cmsMinCorpus so the CMS boilerplate filter
// (useFilter) engages on BOTH the batch and incremental paths. It contains
// three classes of body, one per file:
//
//   - filler*: ~240 structurally varied bodies that pad the corpus.
//   - boiler*: ~40 bodies sharing one identical skeleton, so every shingle
//     they own is high-frequency and gets filtered out — they survive with
//     too few discriminative shingles and are DROPPED (no clone_sig). These
//     are the bodies whose presence the survivor-only Rebuild seeding fails
//     to count.
//   - cloneA / cloneB: one genuine Type-2 clone pair whose shared structure
//     appears in exactly two bodies (frequency = 2 ≤ threshold), so it
//     survives filtering and emits EdgeSimilarTo.
//
// The fixture is split one function per file so a single-file reindex drives
// exactly one body through EvictFuncs/UpdateFuncs.
func writeCloneFilteredFixture(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	write := func(name, body string) {
		p := filepath.Join(dir, name+".go")
		writeFile(t, p, "package main\n\n"+body)
		files = append(files, p)
	}

	ops := []string{"+", "-", "*", "/", "%", "&", "|", "^"}
	cmps := []string{">", "<", ">=", "<=", "==", "!="}
	for k := 0; k < 240; k++ {
		body := fmt.Sprintf("func filler%d(in []int) int {\n\tacc := 0\n", k)
		for s := 0; s < 20; s++ {
			op := ops[(k*7+s*3)%len(ops)]
			op2 := ops[(k*5+s*11)%len(ops)]
			cmp := cmps[(k*13+s*17)%len(cmps)]
			body += fmt.Sprintf("\tif acc %s %d {\n\t\tacc = acc %s %d %s %d\n\t}\n",
				cmp, (k*3+s)%17, op, (k+s)%13, op2, (k*2+s*5)%11)
		}
		body += "\treturn acc\n}\n"
		write(fmt.Sprintf("filler%d", k), body)
	}

	for k := 0; k < 40; k++ {
		write(fmt.Sprintf("boiler%d", k), fmt.Sprintf(`func boiler%d(a int, b int) int {
	c := a + b
	d := c + a
	e := d + b
	f := e + c
	g := f + d
	return g
}
`, k))
	}

	cloneShape := func(name, p, q, r string) string {
		return fmt.Sprintf(`func %s(%s []int) int {
	%s := 0
	for %s := 0; %s < len(%s); %s++ {
		if %s[%s] > 100 {
			%s += %s[%s] * 7 - 3
		} else if %s[%s] < -50 {
			%s -= %s[%s] / 2
		} else {
			%s += %s[%s] & 255
		}
	}
	if %s > 1000 {
		%s = 1000
	}
	return %s
}
`, name, p, q, r, r, p, r, p, r, q, p, r, p, r, q, p, r, q, p, r, q, q, q)
	}
	write("clonea", cloneShape("crunchActive", "items", "acc", "i"))
	write("cloneb", cloneShape("foldEnabled", "records", "sum", "j"))
	return files
}

// cloneBodyShingles recomputes, from the persisted clone_shingles sidecar,
// the (corpus, CMS) the batch finaliseCloneSignatures would have built — its
// body set is EVERY func/method node with shingles (survivors AND
// boilerplate-dropped), which is exactly what Rebuild must mirror. Returns
// the corpus size, a CMS seeded from all those shingles, and one sample
// shingle observed in the corpus (for a Count() spot-check).
func cloneBodyShingles(t *testing.T, g graph.Store, repoPrefix string) (corpus int, cms *clones.CMS, sample uint64) {
	t.Helper()
	r, ok := g.(graph.CloneShingleReader)
	require.True(t, ok, "in-memory graph must implement CloneShingleReader")
	rows, err := r.LoadCloneShingles(repoPrefix)
	require.NoError(t, err)
	cms = clones.NewCMS(65536, 4)
	for _, sh := range rows {
		if len(sh) == 0 {
			continue
		}
		for _, s := range sh {
			cms.Add(s)
			if sample == 0 {
				sample = s
			}
		}
		corpus++
	}
	return corpus, cms, sample
}

// TestCloneIncremental_MatchesBatch_Filtered is the equivalence test with
// the CMS boilerplate filter ENGAGED. The base TestCloneIncremental_MatchesBatch
// runs below cmsMinCorpus where useFilter=false, so the survivor-only Rebuild
// seeding bug is dormant. This test lowers cmsMinCorpus so useFilter=true on
// BOTH paths over a fixture that includes boilerplate-dominated bodies that
// finaliseCloneSignatures drops (no clone_sig) but still counts into its
// CMS/corpus. The pre-fix Rebuild seeded CMS/corpus from survivors only, so:
//
//   - its corpus would be ~2 (only the clone pair) instead of the full body
//     count, and
//   - useFilter on the next incremental update would flip to false,
//
// changing the edited file's signatures vs the batch. Both assertions below
// fail against the pre-fix Rebuild and pass after.
func TestCloneIncremental_MatchesBatch_Filtered(t *testing.T) {
	// Lower the corpus floor so the filter engages on this fixture, then
	// restore it so other tests see the production default.
	prev := cmsMinCorpus
	cmsMinCorpus = 6
	t.Cleanup(func() { cmsMinCorpus = prev })

	dir := t.TempDir()
	files := writeCloneFilteredFixture(t, dir)
	require.Greater(t, len(files), cmsMinCorpus, "fixture must exceed the lowered corpus floor")

	// (a) Batch path: full cold index on graph A.
	gA := graph.New()
	idxA := newTestIndexer(gA)
	_, err := idxA.Index(dir)
	require.NoError(t, err)
	batch := similarEdgeSet(gA)
	require.GreaterOrEqual(t, len(batch), 1,
		"filtered fixture must still produce >=1 EdgeSimilarTo (non-vacuity)")

	// The batch corpus must be well above the lowered floor (so useFilter
	// was true) AND well above the survivor count (so dropped bodies exist
	// — that gap is what the bug mishandles).
	batchCorpus, batchCMS, sample := cloneBodyShingles(t, gA, idxA.repoPrefix)
	require.Greater(t, batchCorpus, cmsMinCorpus,
		"batch corpus must exceed the floor so useFilter engaged")
	require.NotZero(t, sample, "fixture must yield at least one shingle")

	// (b) Incremental path: fresh graph B. Full Index() seeds the
	// incremental clone index via Rebuild (built=true); re-indexing each
	// file then drives EvictFuncs + UpdateFuncs.
	gB := graph.New()
	idxB := newTestIndexer(gB)
	_, err = idxB.Index(dir)
	require.NoError(t, err)
	require.True(t, idxB.cloneIndex.built,
		"incremental clone index must be built after full Index()")

	// DIRECT seeding assertions: Rebuild's CMS+corpus must mirror the batch
	// finalise's all-bodies set. The survivor-only pre-fix seeding makes
	// the corpus collapse to the survivor count and undercounts the CMS —
	// these assertions are the regression tripwire.
	idxB.cloneIndex.mu.Lock()
	gotCorpus := idxB.cloneIndex.corpus
	gotCount := idxB.cloneIndex.cms.Count(sample)
	idxB.cloneIndex.mu.Unlock()
	assert.Equal(t, batchCorpus, gotCorpus,
		"Rebuild corpus must equal the batch finalise corpus (all bodies, not survivors)")
	assert.Equal(t, batchCMS.Count(sample), gotCount,
		"Rebuild CMS Count(sample) must equal the batch finalise CMS count")

	// EDGE-SET equivalence under the engaged filter: driving each file
	// through the incremental maintainer must reproduce the batch edges.
	for _, f := range files {
		require.NoError(t, idxB.IndexFile(f))
	}
	incremental := similarEdgeSet(gB)
	assert.Equal(t, batch, incremental,
		"incremental clone edges must exactly equal the batch clone edges under the CMS filter")
}
