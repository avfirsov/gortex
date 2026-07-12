// Package reach precomputes per-depth incoming-reachability sets on
// every impact-seed node so blast-radius queries (AnalyzeImpact,
// explain_change_impact, simulate_chain step-impact, prompt
// SafeToChange / PreCommit, diff_context) answer in O(seeds × reach)
// map lookups instead of a live BFS.
//
// The package depends only on internal/graph; it is imported by the
// indexer (build site) and the analysis package (consumer) so the
// import graph stays acyclic — analysis already imports indexer in
// its bench tests.
package reach

import (
	"context"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/progress"
)

// Reachability index keys. Each value is a []string of node IDs that
// can reach the carrier node via incoming edges within the named
// number of hops. Tiers are per-depth (not cumulative) so they map
// 1:1 onto AnalyzeImpact's ByDepth tiers.
//
// Parallel `*_conf` and `*_label` keys carry the representative
// in-edge's Confidence and ConfidenceLabel for each ID, indexed by
// position. They turn the fast path into a pure lookup — no
// GetInEdges calls at query time — so a precomputed AnalyzeImpact
// stays sub-ms even on graphs with high fan-in.
//
// Stored on Node.Meta — gob-serialized into the daemon snapshot so
// warm starts keep O(1) impact lookups without paying the build cost.
const (
	MetaReachD1      = "reach_d1"
	MetaReachD2      = "reach_d2"
	MetaReachD3      = "reach_d3"
	MetaReachD1Conf  = "reach_d1_conf"
	MetaReachD2Conf  = "reach_d2_conf"
	MetaReachD3Conf  = "reach_d3_conf"
	MetaReachD1Label = "reach_d1_label"
	MetaReachD2Label = "reach_d2_label"
	MetaReachD3Label = "reach_d3_label"
	// MetaReachComplete is the publication marker for a reach record.
	// A matching generation is not sufficient on its own: older code
	// stamped MetaReachBuild before it finished writing the tier keys, so
	// a concurrent lookup could observe a matching stamp with empty tiers
	// and return a false-safe zero-impact result. New records are assembled
	// on a copy of the node and published only after this marker is set.
	MetaReachComplete = "reach_complete"
	// MetaReachTruncated marks a complete-but-bounded record. Its tiers are
	// valid lower-bound evidence, but callers must not interpret their end as
	// proof that no more dependents exist.
	MetaReachTruncated = "reach_truncated"

	// MetaReachBuild is a monotonic build-generation counter stamped
	// on every node the indexer touched in the most recent reach pass.
	// Consumers require both a counter match and MetaReachComplete before
	// trusting the precomputed sets. The Meta value is a uint64.
	MetaReachBuild = "reach_build"

	// reachPublishBatchSize bounds the temporary copy-on-write node set
	// retained by eager builds and clears. A few thousand rows keeps SQLite
	// transaction amortisation while avoiding a full-graph duplicate of every
	// Node and Meta map at peak.
	reachPublishBatchSize = 256

	// Lazy impact lookup is an interactive safety gate. A pathological hub
	// must return a conservative lower bound promptly, not hold the resolver
	// mutex for minutes while expanding an unbounded breadth-first frontier.
	maxLookupEdges = 5000
	lookupTimeout  = 3 * time.Second
	lockPoll       = 2 * time.Millisecond
)

var reachMetaKeys = [...]string{
	MetaReachD1, MetaReachD2, MetaReachD3,
	MetaReachD1Conf, MetaReachD2Conf, MetaReachD3Conf,
	MetaReachD1Label, MetaReachD2Label, MetaReachD3Label,
	MetaReachBuild, MetaReachComplete, MetaReachTruncated,
}

// ReachableEdge returns true when an edge participates in the impact
// graph. Mirrors AnalyzeImpact's filter exactly so the precomputed
// sets and the live walk agree on membership. Exported so the
// AnalyzeImpact live-walk path can share the same filter and tests
// can assert filter parity across the two code paths.
func ReachableEdge(k graph.EdgeKind) bool {
	return k != graph.EdgeDefines && k != graph.EdgeMemberOf
}

// ImpactSeedKind returns true for node kinds that are sensible impact
// seeds — the symbols a developer actually changes. Files, imports,
// parameters, and similar wiring kinds carry no useful blast radius,
// so we skip them to keep the index lean.
func ImpactSeedKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindFunction, graph.KindMethod,
		graph.KindType, graph.KindInterface,
		graph.KindField, graph.KindEnumMember,
		graph.KindConstant, graph.KindVariable:
		return true
	}
	return false
}

// Stats reports the work BuildIndex did.
type Stats struct {
	NodesIndexed int    // nodes that received reach_d* entries
	EntriesD1    int    // total reach_d1 IDs across all indexed nodes
	EntriesD2    int    // total reach_d2 IDs
	EntriesD3    int    // total reach_d3 IDs
	Build        uint64 // generation tag stamped on every indexed node
}

// buildCounter is a process-wide monotonic generation counter used to
// invalidate cached reach sets across snapshot reloads and
// incremental rebuilds. Bumped on every BuildIndex / ClearIndex call.
var buildCounter uint64

// topologyGate serialises impact readers/eager reach maintenance with a
// watcher topology transaction. It is separate from Store.ResolveMutex:
// watcher indexing calls resolver methods that acquire ResolveMutex
// internally, so holding that non-reentrant mutex around the whole patch would
// deadlock. Readers always acquire this gate before ResolveMutex; a watcher
// holds the writer side while its nested resolver calls take ResolveMutex.
type topologyGate struct {
	mu             sync.Mutex
	cond           *sync.Cond
	readers        int
	writer         bool
	writersWaiting int
}

// buildCounter is already process-global, so topology publication uses one
// process-global gate as well. This avoids retaining one registry entry per
// short-lived Store forever (notably across tests and workspace reloads).
// Production multi-repo indexers share one Store; independent-store watcher
// transactions are rare enough that the conservative cross-store serialization
// is preferable to an unbounded coordination registry.
var globalTopologyGate = func() *topologyGate {
	gate := &topologyGate{}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}()

func beginTopologyRead(_ graph.Store) func() {
	gate := globalTopologyGate
	gate.mu.Lock()
	for gate.writer || gate.writersWaiting > 0 {
		gate.cond.Wait()
	}
	gate.readers++
	gate.mu.Unlock()

	return func() {
		gate.mu.Lock()
		gate.readers--
		if gate.readers == 0 {
			gate.cond.Broadcast()
		}
		gate.mu.Unlock()
	}
}

// beginTopologyReadContext is the cancellable counterpart used by lazy
// impact lookups. It never waits on sync.Cond (which has no cancellation
// channel); a short poll keeps the writer-preference semantics while allowing
// an expired tool request to stop waiting.
func beginTopologyReadContext(ctx context.Context) (func(), bool) {
	gate := globalTopologyGate
	for {
		gate.mu.Lock()
		if !gate.writer && gate.writersWaiting == 0 {
			gate.readers++
			gate.mu.Unlock()
			return func() {
				gate.mu.Lock()
				gate.readers--
				if gate.readers == 0 {
					gate.cond.Broadcast()
				}
				gate.mu.Unlock()
			}, true
		}
		gate.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(lockPoll):
		}
	}
}

func lockContext(ctx context.Context, mu *sync.Mutex) bool {
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(lockPoll):
		}
	}
	return true
}

// BeginTopologyMutation prevents reach lookups/builds from observing a
// watcher's parse-then-swap and resolver work half-applied. The returned
// function must be called exactly once; changed=true invalidates every cached
// generation before waiting readers are released. A separate gate is required
// because the mutation body itself invokes resolver methods that acquire the
// store's non-reentrant ResolveMutex.
func BeginTopologyMutation(g graph.Store) func(changed bool) {
	if g == nil {
		return func(bool) {}
	}
	gate := globalTopologyGate
	gate.mu.Lock()
	gate.writersWaiting++
	for gate.writer || gate.readers > 0 {
		gate.cond.Wait()
	}
	gate.writersWaiting--
	gate.writer = true
	gate.mu.Unlock()

	var once sync.Once
	return func(changed bool) {
		once.Do(func() {
			if changed {
				InvalidateIndex()
			}
			gate.mu.Lock()
			gate.writer = false
			gate.cond.Broadcast()
			gate.mu.Unlock()
		})
	}
}

// BuildIndex precomputes per-depth incoming reachability sets for
// every impact-seed node in g and stores them under Node.Meta as
// []string slices keyed reach_d1 / reach_d2 / reach_d3. Tiers are
// per-depth (a node appears in at most one tier per seed). The build
// generation is stamped under MetaReachBuild so consumers can detect
// stale entries after partial rebuilds.
//
// Cost: O(N · E_avg) where E_avg is the average reach-3 fan-in
// (typically <200 nodes per seed on real call graphs). Empirically
// completes in well under a second on 50k-node graphs. Run after all
// graph-shaping passes settle (resolver, semantic enrichment, cross-
// repo edges, gRPC stub resolution).
//
// Safe to call repeatedly: existing reach_d* entries are overwritten
// and the build counter advances each time so any consumer that read
// an entry from a prior generation will fall back to a live walk.
func BuildIndex(g graph.Store) *Stats {
	return BuildIndexCtx(context.Background(), g)
}

// BuildIndexCtx is BuildIndex with intra-stage progress reporting.
// Pulls a progress.Reporter from ctx (no-op when none is attached) and
// emits per-seed progress every reachProgressEvery seeds — the pass
// otherwise looks hung from the outside, since "reach" is one of the
// longest stages on monorepo-scale graphs (~200 s on k8s with 150 k
// impact seeds). Pure operator-visibility instrumentation: the per-
// report call is cheap (no I/O when the reporter is the default no-op).
func BuildIndexCtx(ctx context.Context, g graph.Store) *Stats {
	if g == nil {
		return &Stats{}
	}
	reporter := progress.FromContext(ctx)
	releaseTopology, ok := beginTopologyReadContext(ctx)
	if !ok {
		return &Stats{}
	}
	mu := g.ResolveMutex()
	if !lockContext(ctx, mu) {
		releaseTopology()
		return &Stats{}
	}
	build := atomic.AddUint64(&buildCounter, 1)
	mu.Unlock()
	releaseTopology()
	stats := &Stats{Build: build}

	nodes := g.AllNodes()
	// Sort by ID so the deterministic iteration order produces stable
	// reach slices — important for snapshot determinism and for tests
	// that compare reach payloads across runs.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	// Pre-count impact seeds so the progress denominator is real, not
	// the total node count (the loop skips ~80% of nodes — files,
	// imports, params, vars, …).
	var seedTotal int
	for _, n := range nodes {
		if n != nil && ImpactSeedKind(n.Kind) {
			seedTotal++
		}
	}
	reporter.Report("reachability index", 0, seedTotal)

	const reachProgressEvery = 1000
	seedsDone := 0
	// Persist complete copy-on-write records in bounded batches. Traversal is
	// deliberately outside topology/resolve locks: a full eager build can take
	// minutes, and holding either lock for that duration starves synchronous
	// edits. Each short publication reacquires both locks and checks the build
	// generation; a topology mutation makes the pending batch stale and aborts
	// the build rather than publishing a mixed snapshot.
	type pendingRecord struct {
		id        string
		tiers     [3]tier
		truncated bool
	}
	batchCap := min(seedTotal, reachPublishBatchSize)
	pending := make([]pendingRecord, 0, batchCap)
	publish := func() bool {
		if len(pending) == 0 {
			return true
		}
		release, acquired := beginTopologyReadContext(ctx)
		if !acquired {
			return false
		}
		if !lockContext(ctx, mu) {
			release()
			return false
		}
		if build != atomic.LoadUint64(&buildCounter) {
			mu.Unlock()
			release()
			return false
		}
		stamped := make([]*graph.Node, 0, len(pending))
		for _, record := range pending {
			n := g.GetNode(record.id)
			if n == nil || !ImpactSeedKind(n.Kind) {
				continue
			}
			published := cloneNodeWithMeta(n)
			writeRecord(published.Meta, build, record.tiers, record.truncated)
			stamped = append(stamped, published)
		}
		if len(stamped) > 0 {
			g.AddBatch(stamped, nil)
		}
		mu.Unlock()
		release()
		pending = pending[:0]
		return true
	}
	for _, n := range nodes {
		if n == nil || !ImpactSeedKind(n.Kind) {
			continue
		}
		if ctx.Err() != nil || build != atomic.LoadUint64(&buildCounter) {
			break
		}
		tiers, truncated := compute(ctx, g, n.ID)
		pending = append(pending, pendingRecord{id: n.ID, tiers: tiers, truncated: truncated})
		if len(pending) == reachPublishBatchSize && !publish() {
			break
		}
		stats.NodesIndexed++
		stats.EntriesD1 += len(tiers[0].IDs)
		stats.EntriesD2 += len(tiers[1].IDs)
		stats.EntriesD3 += len(tiers[2].IDs)

		seedsDone++
		if seedsDone%reachProgressEvery == 0 {
			reporter.Report("reachability index", seedsDone, seedTotal)
		}
	}
	// Flush the final partial batch. AddBatch with no edges only upserts the
	// nodes; a stale generation or cancelled context deliberately drops it.
	_ = publish()
	reporter.Report("reachability index", seedsDone, seedTotal)
	return stats
}

// tier holds the per-depth precomputed payload: a parallel triple of
// (ID, edge-confidence, edge-confidence-label) so the fast path can
// hydrate an ImpactEntry without a single GetInEdges call at query
// time. Sorted by ID for stable snapshot output and test parity.
type tier struct {
	IDs    []string
	Conf   []float64
	Labels []string
}

// setOrDeleteStrings keeps Meta lean — empty tiers are removed rather
// than stored as []string{} so cold-start gob payloads stay small and
// downstream code can rely on "key absent" == "no callers at this tier".
func setOrDeleteStrings(m map[string]any, key string, value []string) {
	if len(value) == 0 {
		delete(m, key)
		return
	}
	m[key] = value
}

// setOrDeleteFloats mirrors setOrDeleteStrings for the parallel
// confidence arrays.
func setOrDeleteFloats(m map[string]any, key string, value []float64) {
	if len(value) == 0 {
		delete(m, key)
		return
	}
	m[key] = value
}

// cloneNodeWithMeta makes a copy-on-write carrier for a reach record.
// Node.Meta is otherwise a regular Go map: editing the canonical map in
// place while Lookup reads it is both a data race and a partial-publication
// hazard. Values are shallow-copied because reach only replaces its own
// immutable slices and never mutates metadata owned by another subsystem.
func cloneNodeWithMeta(n *graph.Node) *graph.Node {
	clone := *n
	clone.Meta = make(map[string]any, len(n.Meta)+11)
	for key, value := range n.Meta {
		clone.Meta[key] = value
	}
	return &clone
}

// writeRecord writes every tier onto a private metadata map and sets the
// completeness marker last. The containing node must not be published to the
// Store until this function returns.
func writeRecord(meta map[string]any, build uint64, tiers [3]tier, truncated bool) {
	delete(meta, MetaReachComplete)
	setOrDeleteStrings(meta, MetaReachD1, tiers[0].IDs)
	setOrDeleteStrings(meta, MetaReachD2, tiers[1].IDs)
	setOrDeleteStrings(meta, MetaReachD3, tiers[2].IDs)
	setOrDeleteFloats(meta, MetaReachD1Conf, tiers[0].Conf)
	setOrDeleteFloats(meta, MetaReachD2Conf, tiers[1].Conf)
	setOrDeleteFloats(meta, MetaReachD3Conf, tiers[2].Conf)
	setOrDeleteStrings(meta, MetaReachD1Label, tiers[0].Labels)
	setOrDeleteStrings(meta, MetaReachD2Label, tiers[1].Labels)
	setOrDeleteStrings(meta, MetaReachD3Label, tiers[2].Labels)
	// A node with no callers deliberately has no tier keys. These two
	// fields distinguish that valid empty record from an interrupted write.
	meta[MetaReachBuild] = build
	meta[MetaReachTruncated] = truncated
	meta[MetaReachComplete] = true
}

// compute walks incoming edges from seed up to depth 3 and returns
// per-depth tiers carrying every ID encountered plus the
// representative in-edge's confidence + label. Each ID appears in at
// most one tier (BFS visited set is shared across depths). Edges are
// filtered with ReachableEdge so the result matches AnalyzeImpact;
// file / import nodes are walked through for fan-out but excluded
// from the tier slices.
func compute(ctx context.Context, g graph.Store, seedID string) ([3]tier, bool) {
	var result [3]tier
	truncated := false
	edgesRemaining := maxLookupEdges
	visited := map[string]struct{}{seedID: {}}
	current := []string{seedID}
	for depth := 1; depth <= 3 && len(current) > 0; depth++ {
		if ctx.Err() != nil || edgesRemaining <= 0 {
			truncated = true
			break
		}
		// Batch the whole BFS level's incoming-edge fetch into one
		// backend round-trip. The per-node g.GetInEdges(id) form issued
		// one query per node on disk backends — an
		// O(reachable-nodes) query storm that turned a single
		// AnalyzeImpact live walk into a multi-minute (timeout) call on
		// a disk backend. GetInEdgesByNodeIDs collapses it to one query per depth.
		inEdges, limited, err := getInEdgesBounded(ctx, g, current, edgesRemaining)
		if err != nil {
			truncated = true
			break
		}
		edgesRemaining -= edgeCount(inEdges)

		// First pass: discover this level's new From-nodes in
		// deterministic (current-order, edge-order) order, recording the
		// representative in-edge for each.
		type cand struct {
			from string
			conf float64
			kind graph.EdgeKind
		}
		var next []string
		var cands []cand
		for _, id := range current {
			for _, e := range inEdges[id] {
				if ctx.Err() != nil {
					truncated = true
					break
				}
				if !ReachableEdge(e.Kind) {
					continue
				}
				if _, seen := visited[e.From]; seen {
					continue
				}
				visited[e.From] = struct{}{}
				next = append(next, e.From)
				cands = append(cands, cand{from: e.From, conf: e.Confidence, kind: e.Kind})
			}
		}

		// Batch the node-kind lookups too — the original called
		// g.GetNode(e.From) once per discovered node (a second per-node
		// query storm on disk backends). File / import nodes are still
		// walked through for fan-out (they stay in `next`) but excluded
		// from the result tiers, exactly as before.
		ids := make([]string, len(cands))
		for i := range cands {
			ids[i] = cands[i].from
		}
		nodes := g.GetNodesByIDs(ids)
		slot := depth - 1
		for _, c := range cands {
			n := nodes[c.from]
			if n == nil || n.Kind == graph.KindFile || n.Kind == graph.KindImport {
				continue
			}
			result[slot].IDs = append(result[slot].IDs, c.from)
			result[slot].Conf = append(result[slot].Conf, c.conf)
			result[slot].Labels = append(result[slot].Labels,
				graph.ConfidenceLabelFor(c.kind, c.conf))
		}
		current = next
		if limited {
			truncated = true
			break
		}
	}
	for i := range result {
		sortTierByID(&result[i])
	}
	return result, truncated
}

type boundedIncomingEdgeReader interface {
	GetInEdgesByNodeIDsContext(context.Context, []string, int) (map[string][]*graph.Edge, bool, error)
}

func getInEdgesBounded(ctx context.Context, g graph.Store, ids []string, limit int) (map[string][]*graph.Edge, bool, error) {
	if reader, ok := g.(boundedIncomingEdgeReader); ok {
		return reader.GetInEdgesByNodeIDsContext(ctx, ids, limit)
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	all := g.GetInEdgesByNodeIDs(ids)
	out := make(map[string][]*graph.Edge, len(all))
	count := 0
	for _, id := range ids {
		for _, edge := range all[id] {
			if count >= limit {
				return out, true, nil
			}
			out[id] = append(out[id], edge)
			count++
		}
	}
	return out, false, nil
}

func edgeCount(byNode map[string][]*graph.Edge) int {
	total := 0
	for _, edges := range byNode {
		total += len(edges)
	}
	return total
}

// sortTierByID sorts a tier's parallel arrays in lock-step by ID so
// repeated builds produce identical snapshots and consumers can
// binary-search for membership.
func sortTierByID(t *tier) {
	n := len(t.IDs)
	if n <= 1 {
		return
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return t.IDs[idx[a]] < t.IDs[idx[b]] })
	ids := make([]string, n)
	conf := make([]float64, n)
	labels := make([]string, n)
	for newPos, oldPos := range idx {
		ids[newPos] = t.IDs[oldPos]
		conf[newPos] = t.Conf[oldPos]
		labels[newPos] = t.Labels[oldPos]
	}
	t.IDs = ids
	t.Conf = conf
	t.Labels = labels
}

// ClearIndex removes reach_d* and reach_build entries from every node
// and bumps the build counter so any cached lookups dated to a prior
// generation are invalidated. Use when the graph topology has shifted
// so far that a full rebuild is cheaper than incremental invalidation.
func ClearIndex(g graph.Store) {
	if g == nil {
		return
	}
	releaseTopology := beginTopologyRead(g)
	defer releaseTopology()
	mu := g.ResolveMutex()
	mu.Lock()
	defer mu.Unlock()
	atomic.AddUint64(&buildCounter, 1)
	cleared := make([]*graph.Node, 0, reachPublishBatchSize)
	for _, n := range g.AllNodes() {
		if n == nil || !hasReachMeta(n.Meta) {
			continue
		}
		clone := cloneNodeWithMeta(n)
		for _, k := range reachMetaKeys {
			delete(clone.Meta, k)
		}
		cleared = append(cleared, clone)
		if len(cleared) == reachPublishBatchSize {
			g.AddBatch(cleared, nil)
			cleared = cleared[:0]
		}
	}
	if len(cleared) > 0 {
		g.AddBatch(cleared, nil)
	}
}

func hasReachMeta(meta map[string]any) bool {
	for _, key := range reachMetaKeys {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

// Entry is one precomputed reach record: a node ID and the
// representative in-edge's confidence + confidence-label so the
// AnalyzeImpact fast path can hydrate an ImpactEntry with zero
// GetInEdges calls.
type Entry struct {
	ID    string
	Conf  float64
	Label string
}

// Lookup returns the per-depth reach for seedID. On a fresh cache hit
// (build counter matches current generation and the completion marker
// is present) it returns the cached tiers in sub-millisecond. On a miss —
// first call for this seed, an interrupted/legacy record, or
// the global build counter has advanced past the stamped value
// because the graph mutated — it runs the BFS on demand under
// g.ResolveMutex(), caches the result onto n.Meta, and returns the
// fresh tiers. Returns hit=false only when seedID names no node or
// names a node whose kind is not an impact seed (KindFunction,
// KindMethod, KindType, KindInterface).
//
// This is the "lazy reach index" — the eager BuildIndex pass that
// used to walk every impact seed during cold-index has been removed
// from the IndexCtx hot path because the breakeven was untenable on
// monorepo graphs: ~2000 s of cold-index work on k8s to save ~10 ms
// per query, requiring ~200 k queries to break even. The lazy form
// pays the 10 ms only on the first AnalyzeImpact call that names a
// given seed, then caches forever. BuildIndex remains available for
// `gortex enrich reach` (explicit prebuild) and for callers that
// want to pay the cost up front under controlled conditions.
//
// Callers must not already hold g.ResolveMutex(): Lookup acquires that
// non-reentrant mutex to serialize every Node.Meta read with graph-wide
// metadata writers. Current production callers enter from analysis/MCP paths,
// outside resolver critical sections.
func Lookup(g graph.Store, seedID string) (d1, d2, d3 []Entry, hit bool) {
	d1, d2, d3, hit, truncated := LookupContext(context.Background(), g, seedID)
	// The legacy API has no channel for lower-bound status. Never call a
	// bounded record an exact cache hit; status-aware impact consumers use
	// LookupContext below.
	return d1, d2, d3, hit && !truncated
}

// LookupCached reads an already-published record without triggering BFS.
// Whole-repository ranking calls this form: lazily expanding reach for every
// candidate is an accidental eager index build and can issue hundreds of
// thousands of SQLite queries. Missing records fail closed to the caller's
// direct-fan-in fallback; bounded records retain their truncation signal.
func LookupCached(g graph.Store, seedID string) (d1, d2, d3 []Entry, hit, truncated bool) {
	if g == nil {
		return nil, nil, nil, false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	releaseTopology, ok := beginTopologyReadContext(ctx)
	if !ok {
		return nil, nil, nil, false, true
	}
	mu := g.ResolveMutex()
	if !lockContext(ctx, mu) {
		releaseTopology()
		return nil, nil, nil, false, true
	}
	build := atomic.LoadUint64(&buildCounter)
	n := g.GetNode(seedID)
	if n != nil && ImpactSeedKind(n.Kind) {
		d1, d2, d3, truncated, hit = readCached(n, build)
	}
	stable := build == atomic.LoadUint64(&buildCounter)
	mu.Unlock()
	releaseTopology()
	if !stable {
		return nil, nil, nil, false, true
	}
	return d1, d2, d3, hit, truncated
}

// LookupContext returns a complete reach record or a bounded lower-bound
// record. Expensive BFS work deliberately happens outside topology and
// resolver locks. Publication re-enters both locks, verifies the generation,
// and retries if a mutation crossed the optimistic compute window. This keeps
// synchronous edits from waiting behind breadth-first graph traversal.
func LookupContext(parent context.Context, g graph.Store, seedID string) (d1, d2, d3 []Entry, hit, truncated bool) {
	if g == nil {
		return nil, nil, nil, false, false
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, lookupTimeout)
	defer cancel()

	for {
		releaseTopology, ok := beginTopologyReadContext(ctx)
		if !ok {
			return nil, nil, nil, false, true
		}
		mu := g.ResolveMutex()
		if !lockContext(ctx, mu) {
			releaseTopology()
			return nil, nil, nil, false, true
		}
		currentBuild := atomic.LoadUint64(&buildCounter)
		n := g.GetNode(seedID)
		if n == nil || !ImpactSeedKind(n.Kind) {
			mu.Unlock()
			releaseTopology()
			return nil, nil, nil, false, false
		}
		if d1, d2, d3, cachedTruncated, cached := readCached(n, currentBuild); cached {
			stable := currentBuild == atomic.LoadUint64(&buildCounter)
			mu.Unlock()
			releaseTopology()
			if stable {
				return d1, d2, d3, true, cachedTruncated
			}
			continue
		}
		mu.Unlock()
		releaseTopology()

		// The expensive portion is optimistic and lock-free with respect to
		// indexing. A concurrent topology writer invalidates currentBuild;
		// the publication gate below then discards this snapshot and retries.
		tiers, traversalTruncated := compute(ctx, g, seedID)
		if ctx.Err() != nil {
			traversalTruncated = true
		}

		releaseTopology, ok = beginTopologyReadContext(ctx)
		if !ok {
			return entriesForTier(tiers[0]), entriesForTier(tiers[1]), entriesForTier(tiers[2]), true, true
		}
		if !lockContext(ctx, mu) {
			releaseTopology()
			return entriesForTier(tiers[0]), entriesForTier(tiers[1]), entriesForTier(tiers[2]), true, true
		}
		if currentBuild != atomic.LoadUint64(&buildCounter) {
			mu.Unlock()
			releaseTopology()
			if ctx.Err() != nil {
				return entriesForTier(tiers[0]), entriesForTier(tiers[1]), entriesForTier(tiers[2]), true, true
			}
			continue
		}
		n = g.GetNode(seedID)
		if n == nil || !ImpactSeedKind(n.Kind) {
			mu.Unlock()
			releaseTopology()
			return nil, nil, nil, false, false
		}
		if d1, d2, d3, cachedTruncated, cached := readCached(n, currentBuild); cached {
			mu.Unlock()
			releaseTopology()
			return d1, d2, d3, true, cachedTruncated
		}
		published := cloneNodeWithMeta(n)
		writeRecord(published.Meta, currentBuild, tiers, traversalTruncated)
		g.AddNode(published)
		stable := currentBuild == atomic.LoadUint64(&buildCounter)
		mu.Unlock()
		releaseTopology()
		if !stable {
			if ctx.Err() != nil {
				return entriesForTier(tiers[0]), entriesForTier(tiers[1]), entriesForTier(tiers[2]), true, true
			}
			continue
		}
		return entriesForTier(tiers[0]), entriesForTier(tiers[1]), entriesForTier(tiers[2]), true, traversalTruncated
	}
}

func entriesForTier(t tier) []Entry {
	if len(t.IDs) == 0 {
		return nil
	}
	out := make([]Entry, len(t.IDs))
	for i, id := range t.IDs {
		out[i].ID = id
		if i < len(t.Conf) {
			out[i].Conf = t.Conf[i]
		}
		if i < len(t.Labels) {
			out[i].Label = t.Labels[i]
		}
	}
	return out
}

// readCached reads the stamped reach tiers off n.Meta when the stamp
// matches currentBuild and the record carries a completion marker.
// Returns ok=false when either marker is missing (never built, legacy,
// or interrupted), stale (graph has changed since), or has the wrong
// Go type.
func readCached(n *graph.Node, currentBuild uint64) (d1, d2, d3 []Entry, truncated, ok bool) {
	if n.Meta == nil {
		return nil, nil, nil, false, false
	}
	raw, present := n.Meta[MetaReachBuild]
	if !present {
		return nil, nil, nil, false, false
	}
	stamped, valid := safeUint64(raw)
	if !valid {
		return nil, nil, nil, false, false
	}
	if stamped != currentBuild {
		return nil, nil, nil, false, false
	}
	complete, _ := n.Meta[MetaReachComplete].(bool)
	if !complete {
		// A generation stamp without an explicit completion marker is an
		// interrupted/legacy publication. Treat it as a miss so Lookup
		// recomputes instead of silently interpreting missing tiers as an
		// intentionally empty blast radius.
		return nil, nil, nil, false, false
	}
	if rawTruncated, present := n.Meta[MetaReachTruncated]; present {
		var valid bool
		truncated, valid = rawTruncated.(bool)
		if !valid {
			return nil, nil, nil, false, false
		}
	}
	var tierValid bool
	if d1, tierValid = readTier(n.Meta, MetaReachD1, MetaReachD1Conf, MetaReachD1Label); !tierValid {
		return nil, nil, nil, false, false
	}
	if d2, tierValid = readTier(n.Meta, MetaReachD2, MetaReachD2Conf, MetaReachD2Label); !tierValid {
		return nil, nil, nil, false, false
	}
	if d3, tierValid = readTier(n.Meta, MetaReachD3, MetaReachD3Conf, MetaReachD3Label); !tierValid {
		return nil, nil, nil, false, false
	}
	seen := make(map[string]struct{}, len(d1)+len(d2)+len(d3))
	for _, entries := range [][]Entry{d1, d2, d3} {
		for _, entry := range entries {
			if entry.ID == "" {
				return nil, nil, nil, false, false
			}
			if _, duplicate := seen[entry.ID]; duplicate {
				return nil, nil, nil, false, false
			}
			seen[entry.ID] = struct{}{}
		}
	}
	return d1, d2, d3, truncated, true
}

func safeUint64(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case uint:
		return uint64(n), true
	case uint32:
		return uint64(n), true
	case int:
		if n >= 0 {
			return uint64(n), true
		}
	case int64:
		if n >= 0 {
			return uint64(n), true
		}
	case float64:
		// float64(math.MaxUint64) rounds up to 2^64, so the upper bound
		// must be strict or an out-of-range legacy JSON number can wrap.
		if n >= 0 && n < math.Exp2(64) && math.Trunc(n) == n {
			return uint64(n), true
		}
	}
	return 0, false
}

// InvalidateIndex advances the global build counter so every future
// Lookup recomputes against the new graph state. Call this whenever
// the graph mutates in a way that could change reach sets — at the
// end of every IndexCtx / IncrementalReindex / global-pass run.
//
// The cached Meta entries on nodes that survived the mutation are
// not deleted; they're simply tagged with a stale build counter, so
// the next Lookup on each falls through to a fresh compute. This is
// strictly cheaper than walking all nodes to clear Meta — the
// invalidation is O(1) and only the seeds actually queried pay the
// recompute cost.
func InvalidateIndex() {
	atomic.AddUint64(&buildCounter, 1)
}

// readTier reconstructs an []Entry from the parallel arrays. Missing
// confidence / label keys (or shorter slices) zero-fill so older
// snapshots that lack the parallel data degrade gracefully — the
// caller still sees the ID set, just with zero confidence.
func readTier(meta map[string]any, idsKey, confKey, labelKey string) ([]Entry, bool) {
	ids, idsPresent, valid := safeStringSlice(meta[idsKey])
	_, confPresent := meta[confKey]
	_, labelsPresent := meta[labelKey]
	if !idsPresent {
		return nil, !confPresent && !labelsPresent
	}
	if !valid {
		return nil, false
	}
	conf, _, confValid := safeFloatSlice(meta[confKey])
	labels, _, labelsValid := safeStringSlice(meta[labelKey])
	if (confPresent && (!confValid || len(conf) != len(ids))) ||
		(labelsPresent && (!labelsValid || len(labels) != len(ids))) {
		return nil, false
	}
	out := make([]Entry, len(ids))
	for i, id := range ids {
		out[i].ID = id
		if i < len(conf) {
			out[i].Conf = conf[i]
		}
		if i < len(labels) {
			out[i].Label = labels[i]
		}
	}
	return out, true
}

func safeStringSlice(v any) ([]string, bool, bool) {
	if v == nil {
		return nil, false, true
	}
	switch values := v.(type) {
	case []string:
		return values, true, true
	case []any:
		out := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, true, false
			}
			out[i] = text
		}
		return out, true, true
	default:
		return nil, true, false
	}
}

func safeFloatSlice(v any) ([]float64, bool, bool) {
	if v == nil {
		return nil, false, true
	}
	switch values := v.(type) {
	case []float64:
		return values, true, true
	case []any:
		out := make([]float64, len(values))
		for i, value := range values {
			switch number := value.(type) {
			case float64:
				out[i] = number
			case int:
				out[i] = float64(number)
			case int64:
				out[i] = float64(number)
			case uint64:
				out[i] = float64(number)
			default:
				return nil, true, false
			}
		}
		return out, true, true
	default:
		return nil, true, false
	}
}

// BuildCounter returns the current generation tag. Tests use it to
// assert that a rebuild actually bumped the counter.
func BuildCounter() uint64 {
	return atomic.LoadUint64(&buildCounter)
}
