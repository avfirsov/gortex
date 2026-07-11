package indexer

import (
	"context"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/clones"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/progress"
)

// cloneSigMetaKey is the Node.Meta key under which a function/method's
// base64-encoded MinHash signature is stored. The graph-wide LSH pass
// reads it back out — keeping the signature on the node makes the pass
// a pure graph walk (no file IO), correct under incremental reindex,
// and safe across multi-repo graphs.
const cloneSigMetaKey = "clone_sig"

// cloneTokensMetaKey is the Node.Meta key under which the normalised-
// token count of a function/method body is stored alongside the clone
// signature. Used by the length-stratified LSH pass to bucket items
// into overlapping size classes so a pair with size ratio > ~1.6
// (Jaccard ≤ 0.625, well below the 0.82 clone threshold) is never
// considered as a candidate.
const cloneTokensMetaKey = "clone_tokens"

// cloneShinglesMetaKey is the Node.Meta key under which a function /
// method's raw shingle hash set is stashed during the per-file parse,
// so the global CMS-filter pass (finaliseCloneSignatures) can decide
// which shingles to exclude before computing the final MinHash
// signature. The entry is deleted from Meta as soon as the signature
// lands — it is intentionally short-lived because the shingle set is
// large (≈ tokens − 2 entries per body) and persisting it across the
// clone-detection pass would waste tens of MB on a monorepo.
const cloneShinglesMetaKey = "clone_shingles"

// CMS-filter tuning.
//
// cmsBoilerplateRatio: a shingle appearing in more than this fraction
// of bodies is treated as boilerplate and excluded from signature
// computation. 1% is the textbook value used by near-duplicate web
// indexing systems and balances precision (false-clone suppression)
// against recall (genuine clones whose shared content happens to use
// a moderately common idiom).
//
// cmsMinCorpus: below this many bodies the global frequency
// distribution is too thin for the threshold to be meaningful — a
// 200-body repo has no shingle that legitimately appears in 2 bodies
// without already being noise — so we fall back to unfiltered MinHash.
// Around this size the LSH pass is also fast enough that filtering
// gains nothing.
//
// minSurvivingShingles: after filtering, a body with fewer
// discriminative shingles than this is dropped from clone detection
// entirely. MinHash over a handful of shingles produces random slot
// values that collide unpredictably in LSH bands; the body is then a
// false-clone factory, not a real clone source. Boilerplate-dominated
// bodies (e.g. trivial controller / DTO wrappers) land here.
const (
	cmsBoilerplateRatio  = 0.01
	minSurvivingShingles = 8
)

// cmsMinCorpus is the body-count floor below which the CMS boilerplate
// filter is disabled (useFilter=false) and the pass falls back to
// unfiltered MinHash — see the doc comment above for the rationale and
// default. It is a package-level var (not a const) purely so the clone
// equivalence tests can temporarily lower it to force useFilter=true on a
// small fixture and exercise the filtered batch/incremental paths; restore
// it via t.Cleanup. Production never mutates it — the default semantics are
// unchanged.
var cmsMinCorpus = 2000

// applyCloneSignatures is the per-file half of clone detection. It runs
// inside applyCoverageDomains (gated on the "clones" coverage domain),
// slices each function/method body out of the file source, computes a
// MinHash signature, and stamps it on the node's Meta. Bodies below
// clones.MinTokens normalised tokens produce no signature and are
// silently skipped — they are dominated by boilerplate and would only
// add noise to the LSH buckets.
//
// Allocation note: the body slicing path computes one []int of line
// offsets per file and one string per emitted body. The previous
// implementation went through splitLines (which materialises the
// whole source as N per-line Go strings) and a quadratic concat in
// bodyText (each iteration grew the output via "out += ..."). Profile
// showed bodyText + splitLinesUpTo at 3+ GiB per 30 s window — both
// are now O(file_bytes) one-shot allocations.
func applyCloneSignatures(src []byte, result *parser.ExtractionResult) {
	if result == nil || len(result.Nodes) == 0 {
		return
	}
	// Compute newline offsets once per file rather than splitting the
	// source into N Go strings. offsets[i] is the byte index where
	// line i+1 (1-indexed) starts; the sentinel offsets[len(offsets)-1]
	// is len(src) so the slice math doesn't need a special case for
	// the last line.
	offsets := lineOffsets(src)
	for _, n := range result.Nodes {
		if n == nil {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		body := bodyTextFromOffsets(src, offsets, n.StartLine, n.EndLine)
		if body == "" {
			continue
		}
		// Stash the deduplicated shingle set rather than the final
		// MinHash signature: signature computation is deferred to the
		// global CMS-filter pass (finaliseCloneSignatures), which
		// derives a per-corpus boilerplate-shingle set and excludes it
		// from each body's signature. The shingle slice is short-lived
		// on Meta — finaliseCloneSignatures clears it after stamping
		// the real signature.
		shingles, tokens, ok := clones.Shingles(body)
		if !ok {
			continue
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta[cloneShinglesMetaKey] = shingles
		n.Meta[cloneTokensMetaKey] = tokens
	}
}

// lineOffsets returns the byte offsets of each line in src. For a file
// with N lines the result has length N+1: the first entry is 0, each
// subsequent entry is the byte index immediately after a '\n', and the
// final sentinel is len(src) so callers can slice the last line as
// src[offsets[N-1]:offsets[N]] without special-casing EOF.
//
// One allocation (the []int) instead of N (one string per line via
// strings.Split). Lifetime is per-file: the caller drops the slice
// when the file's worker batch finishes.
func lineOffsets(src []byte) []int {
	// Reserve a generous initial capacity to avoid repeated slice
	// growth on typical source files (~ 200 lines). The slice grows
	// from here for larger files; small files waste a bit of headroom
	// that goes back to the GC immediately.
	offsets := make([]int, 1, 256)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	offsets = append(offsets, len(src))
	return offsets
}

// bodyTextFromOffsets returns src[startLine..endLine] (both 1-indexed,
// inclusive) as one Go string. The trailing newline of the last
// included line is stripped so output matches the old line-join
// semantics ("a\nb" not "a\nb\n"). Returns "" for degenerate or
// out-of-bounds ranges, matching bodyText.
func bodyTextFromOffsets(src []byte, offsets []int, startLine, endLine int) string {
	if startLine <= 0 || endLine < startLine {
		return ""
	}
	lo := startLine - 1
	hi := endLine
	// len(offsets) = lineCount + 1 (sentinel). lineCount = len(offsets) - 1.
	lineCount := len(offsets) - 1
	if lo >= lineCount {
		return ""
	}
	if hi > lineCount {
		hi = lineCount
	}
	startOff := offsets[lo]
	endOff := offsets[hi]
	// Strip the trailing '\n' that bounds the last included line so the
	// output matches the line-join semantics callers and tests expect.
	if endOff > startOff && endOff <= len(src) && endOff-1 >= 0 && src[endOff-1] == '\n' {
		endOff--
	}
	return string(src[startOff:endOff])
}

// bodyText returns the source spanning [startLine, endLine] (both
// 1-indexed, inclusive) joined by newlines. Kept as a legacy helper
// for the unit-test surface; production callers go through
// applyCloneSignatures → bodyTextFromOffsets, which avoids both the
// whole-source string copy in splitLines and the O(N²) concat below.
func bodyText(lines []string, startLine, endLine int) string {
	if startLine <= 0 || endLine < startLine {
		return ""
	}
	lo := startLine - 1
	hi := endLine
	if lo >= len(lines) {
		return ""
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	// Precompute the joined size so the strings.Builder grows once,
	// turning the previous O(N²) "out += ..." into O(total_bytes).
	total := 0
	for i := lo; i < hi; i++ {
		total += len(lines[i])
		if i > lo {
			total++ // separating '\n'
		}
	}
	var b strings.Builder
	b.Grow(total)
	for i := lo; i < hi; i++ {
		if i > lo {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	return b.String()
}

// computeCloneSigFromShingles is the per-body signature kernel shared by
// the whole-graph finalise pass (finaliseCloneSignatures) and the
// incremental maintainer (incrementalCloneIndex.UpdateFuncs). Both paths
// MUST route through this function so a body's signature is byte-identical
// regardless of which path stamped it — that is what lets the equivalence
// test assert exact set equality between the batch and incremental clone
// edges.
//
// cms is the corpus Count-Min Sketch; threshold is the boilerplate cutoff
// (a shingle whose CMS count exceeds it is dropped). useFilter selects the
// branch:
//
//   - useFilter true: exclude high-frequency shingles, then require the
//     surviving set to clear minSurvivingShingles before computing MinHash.
//   - useFilter false: keep every shingle and apply no floor (legacy
//     small-corpus behaviour) — cms may be nil in this branch.
//
// Returns the signature and ok=false when the body is dropped from clone
// detection (empty / below the surviving floor) — the caller then leaves
// the node without a clone_sig, exactly as the batch pass does.
func computeCloneSigFromShingles(cms *clones.CMS, threshold uint32, useFilter bool, shingles []uint64) (clones.Signature, bool) {
	var filtered []uint64
	if useFilter {
		filtered = make([]uint64, 0, len(shingles))
		for _, sh := range shingles {
			if cms.Count(sh) > threshold {
				continue
			}
			filtered = append(filtered, sh)
		}
	} else {
		filtered = shingles
	}
	floor := minSurvivingShingles
	if !useFilter {
		// Without filtering, every shingle survives — fall back to the
		// legacy gate so we don't silently drop bodies the old code
		// would have kept.
		floor = 0
	}
	return clones.SignatureFromShingles(filtered, floor)
}

// finaliseCloneSignatures runs after every file's shingles have been
// stamped on its function / method nodes (by applyCloneSignatures
// during the per-file parse). It builds a Count-Min Sketch of shingle
// frequencies across every body in the graph, then walks the bodies
// again and computes a MinHash signature excluding shingles that
// exceed the boilerplate threshold (present in > cmsBoilerplateRatio
// of bodies). The stashed shingle set is cleared from Meta as soon as
// the signature lands so the LSH pass downstream sees the same
// node-shape the legacy path produced — just with cleaner signatures.
//
// Bodies whose surviving shingle count falls below minSurvivingShingles
// are dropped from clone detection entirely (no clone_sig stamp): a
// body whose token stream is dominated by boilerplate is, by
// definition, a controller / DTO / dispatch shape rather than
// distinguishable code, and including it in MinHash would just produce
// random LSH collisions.
//
// Below cmsMinCorpus bodies the corpus is too small for the
// frequency distribution to be meaningful; the pass falls back to
// unfiltered MinHash so small repos preserve the legacy behaviour.
//
// Caller must hold g.ResolveMutex() — the function mutates Node.Meta
// (deletes clone_shingles, sets clone_sig) across nodes that other
// graph-wide passes (markTestSymbolsAndEmitEdges, ResolveTemporalCalls,
// reach.BuildIndex) also touch under the same mutex.
//
// Repo-scoped: only bodies whose n.RepoPrefix == repoPrefix enter the
// CMS / signature passes, so a multi-repo graph computes each repo's
// boilerplate sketch and per-body signatures from that repo's bodies
// alone — clone detection is per-repository. A standalone single-repo
// Indexer uses repoPrefix == "" and its nodes carry RepoPrefix == "",
// so the equality matches every node and behaviour is unchanged.
// (GetRepoNodes can't be used here: GetRepoNodes("") is empty for the
// in-memory / single-repo store — see incrementalCloneIndex.Rebuild —
// so the AllNodes + equality filter is the form that works for both
// regimes, since "" == "" matches every node.)
// cloneRepoNodes returns the nodes the per-repo clone passes must walk. In
// daemon multi-repo mode repoPrefix is non-empty, so GetRepoNodes selects just
// that repo's nodes (one backend query, and one meta decode per repo node)
// instead of decoding every node in a many-repo graph only to discard the other
// repos' — the whole-graph AllNodes scan these passes used to run per repo.
// In single-repo / in-memory mode repoPrefix is "" and those nodes are not
// tracked in the per-repo buckets GetRepoNodes reads, so the AllNodes fallback
// (whose "" == n.RepoPrefix filter matches every node) is the only form that
// works there. Callers keep their n.RepoPrefix == repoPrefix guard: a no-op on
// the GetRepoNodes path, load-bearing on the AllNodes fallback. The clone passes
// read blob-only Meta (clone_sig / clone_tokens / clone_shingles), so the full
// GetRepoNodes — not the meta-less light reader — is required here.
func cloneRepoNodes(g graph.Store, repoPrefix string) []*graph.Node {
	if repoPrefix != "" {
		return g.GetRepoNodes(repoPrefix)
	}
	return g.AllNodes()
}

func finaliseCloneSignatures(g graph.Store, repoPrefix string) {
	// First pass: collect every body that has stashed shingles. We
	// capture the *graph.Node pointers up front so the CMS-build pass
	// and the signature-compute pass don't both re-walk g.AllNodes().
	bodies := make([]*graph.Node, 0, 8192)
	for _, n := range cloneRepoNodes(g, repoPrefix) {
		if n == nil || n.Meta == nil {
			continue
		}
		if n.RepoPrefix != repoPrefix {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		if _, ok := n.Meta[cloneShinglesMetaKey].([]uint64); !ok {
			continue
		}
		bodies = append(bodies, n)
	}
	if len(bodies) == 0 {
		return
	}

	useFilter := len(bodies) >= cmsMinCorpus
	var cms *clones.CMS
	var threshold uint32
	if useFilter {
		// Default sketch sizing — see the CMS doc comment for the
		// width/depth → ε/δ derivation. 1 MB peak for a transient,
		// per-build pass is comfortably below any constraint.
		cms = clones.NewCMS(65536, 4)
		for _, n := range bodies {
			shingles, _ := n.Meta[cloneShinglesMetaKey].([]uint64)
			for _, sh := range shingles {
				cms.Add(sh)
			}
		}
		threshold = uint32(float64(len(bodies)) * cmsBoilerplateRatio)
		if threshold < 1 {
			threshold = 1
		}
	}

	// Persist each body's raw shingle set to the clone_shingles sidecar
	// BEFORE deleting it from Meta. This loop walks EVERY body in the
	// corpus — both the survivors (which get a clone_sig below) and the
	// boilerplate-dropped bodies (which do not) — persisting any with a
	// non-empty shingle set. That is deliberate: incrementalCloneIndex.
	// Rebuild reseeds its CMS + corpus from these rows and must mirror
	// the bodies set this pass used to build its own CMS / threshold,
	// which is ALL eligible bodies, not just survivors. Persisting only
	// survivors here would under-seed Rebuild's sketch and skew the
	// incremental threshold away from the batch one. Meta stays lean
	// (the shingle set is large and only the CMS pass needs it), but the
	// durable sidecar copy lets a warm restart rebuild the incremental
	// CMS without re-parsing every body. Accumulate per node.RepoPrefix
	// so a multi-repo graph reseeds each repo's CMS in isolation.
	// Backends that don't implement CloneShingleWriter (no on-disk store)
	// simply skip this — the in-session incremental index caches shingles
	// in memory regardless.
	if w, ok := g.(graph.CloneShingleWriter); ok {
		byPrefix := make(map[string]map[string][]uint64)
		for _, n := range bodies {
			shingles, _ := n.Meta[cloneShinglesMetaKey].([]uint64)
			if len(shingles) == 0 {
				continue
			}
			rows := byPrefix[n.RepoPrefix]
			if rows == nil {
				rows = make(map[string][]uint64)
				byPrefix[n.RepoPrefix] = rows
			}
			rows[n.ID] = shingles
		}
		for prefix, rows := range byPrefix {
			_ = w.BulkSetCloneShingles(prefix, rows)
		}
	}

	// Second pass: signature computation. Each body either lands a
	// fresh clone_sig (signature over surviving shingles) or is
	// dropped entirely (no clone_sig, never enters detection items
	// list). In both cases clone_shingles is removed from Meta. The
	// per-body kernel is computeCloneSigFromShingles — the incremental
	// maintainer calls the same kernel so signatures match exactly.
	for _, n := range bodies {
		shingles, _ := n.Meta[cloneShinglesMetaKey].([]uint64)
		sig, ok := computeCloneSigFromShingles(cms, threshold, useFilter, shingles)
		delete(n.Meta, cloneShinglesMetaKey)
		if !ok {
			// Boilerplate-dominated or empty after filter — drop
			// from clone detection. detectClonesAndEmitEdges skips
			// nodes without a clone_sig.
			continue
		}
		n.Meta[cloneSigMetaKey] = clones.EncodeSignature(sig)
	}
}

// CloneDetectionStats summarises one detectClonesAndEmitEdges run for
// the caller's logger. Exposed so the orchestrator can surface what the
// per-bucket cap dropped — a high skippedBucketItems means the
// workspace has a lot of templated boilerplate that LSH would have
// over-fanned-out on.
type CloneDetectionStats struct {
	Items              int // function/method nodes with a signature
	Pairs              int // detected clone pairs (after Jaccard filter)
	Edges              int // EdgeSimilarTo emitted (≈ 2·Pairs, modulo dedup)
	SkippedBuckets     int // LSH buckets dropped for exceeding maxBucketSize
	SkippedBucketItems int // total items inside the dropped buckets
	DiffusedPairs      int // semantically-related pairs surviving threshold+cap
	DiffusedEdges      int // EdgeSemanticallyRelated emitted (= 2·DiffusedPairs)
}

// detectClonesAndEmitEdges is the graph-wide half of clone detection.
// It collects every function/method node carrying a clone_sig, runs
// the MinHash + LSH pass over their signatures, and materialises a
// symmetric pair of EdgeSimilarTo edges for each detected clone pair.
//
// threshold is the Jaccard similarity cutoff; pass 0 to use the
// clones package default. Returns clone stats including the per-bucket
// cap telemetry — the orchestrator logs that so a high skip count is
// visible during warmup.
//
// The pass is a full recompute and is idempotent: graph.AddEdge dedupes
// by edgeKey so re-emitting an unchanged pair is a no-op, and stale
// edges cannot survive — when either endpoint's file is reindexed,
// EvictFile removes that node's edges in both directions before this
// pass re-runs.
//
// repoPrefix scopes the pass to one repository's nodes: every whole-graph
// walk it drives (finalise, item gather, diffusion) is filtered to
// n.RepoPrefix == repoPrefix so no cross-repo candidate pair is ever
// formed. A standalone single-repo Indexer passes "" and its nodes carry
// RepoPrefix == "", so the equality matches all nodes and the single-repo
// result is unchanged.
func detectClonesAndEmitEdges(g graph.Store, repoPrefix string, threshold float64) CloneDetectionStats {
	return detectClonesAndEmitEdgesCtx(context.Background(), g, repoPrefix, threshold)
}

// detectClonesAndEmitEdgesCtx is the context-aware sibling of
// detectClonesAndEmitEdges. It emits sub-stage progress markers via
// the reporter attached to ctx (see progress.WithReporter): clone
// detection is the longest single stage on monorepo-scale graphs and
// without intra-stage reporters an operator sees just one
// "clone detection pass" marker followed by minutes of silence — no
// way to tell finalise-signatures from LSH from edge-emission.
func detectClonesAndEmitEdgesCtx(ctx context.Context, g graph.Store, repoPrefix string, threshold float64) CloneDetectionStats {
	var stats CloneDetectionStats
	if g == nil {
		return stats
	}
	reporter := progress.FromContext(ctx)
	// Serialise against other graph-wide passes that mutate Node.Meta
	// (markTestSymbolsAndEmitEdges, ResolveTemporalCalls, reach.BuildIndex,
	// releases enrichment). Without this lock, the AllNodes walk below
	// reads n.Meta while one of those writers mutates the same map and
	// the runtime aborts with "concurrent map read and map write" — the
	// observed daemon crash. Shares g.ResolveMutex() so all such passes
	// rendezvous on the same lock the resolver already uses.
	g.ResolveMutex().Lock()
	defer g.ResolveMutex().Unlock()

	// Finalise pending signatures: applyCloneSignatures stamped the
	// raw shingle set on each function/method node during the per-file
	// parse. This pass builds a Count-Min Sketch of corpus-wide shingle
	// frequencies, then computes the MinHash signature for each body
	// after excluding shingles whose frequency exceeds the boilerplate
	// threshold. The expensive LSH candidate enumeration that comes
	// next then runs over signatures that reflect discriminative
	// content only — k8s-style controller-pattern bodies stop colliding
	// on shared "if v err return v" / "( v . v )" shingles, which is
	// what drives the LSH bucket explosion at monorepo scale.
	//
	// Runs under the existing g.ResolveMutex() so the Meta mutations
	// (delete clone_shingles, set clone_sig) don't race the AllNodes
	// walk below.
	reporter.Report("clones: CMS-finalise signatures", 0, 0)
	finaliseCloneSignatures(g, repoPrefix)

	reporter.Report("clones: gather items", 0, 0)
	var items []clones.Item
	for _, n := range cloneRepoNodes(g, repoPrefix) {
		if n == nil || n.Meta == nil {
			continue
		}
		// Scope to this repo's nodes so no cross-repo candidate pair is
		// ever formed. "" matches every node (single-repo / in-memory).
		if n.RepoPrefix != repoPrefix {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		enc, ok := n.Meta[cloneSigMetaKey].(string)
		if !ok || enc == "" {
			continue
		}
		sig, ok := clones.DecodeSignature(enc)
		if !ok {
			continue
		}
		// Read the stamped token count when present. Legacy nodes
		// indexed before the stamp was added simply get TokenCount=0,
		// which lengthClassesOf treats as "unknown" → all classes,
		// preserving the unstratified behaviour for them.
		tokens := 0
		switch v := n.Meta[cloneTokensMetaKey].(type) {
		case int:
			tokens = v
		case int64:
			tokens = int(v)
		case float64:
			tokens = int(v)
		}
		items = append(items, clones.Item{ID: n.ID, Sig: sig, TokenCount: tokens})
	}
	stats.Items = len(items)
	if len(items) < 2 {
		return stats
	}

	reporter.Report("clones: LSH + Jaccard filter", len(items), 0)
	detected, sb, sbi := clones.DetectPairsStratifiedWithStats(items, threshold)
	stats.SkippedBuckets = sb
	stats.SkippedBucketItems = sbi
	stats.Pairs = len(detected)
	reporter.Report("clones: emit similarity edges", len(detected), 0)
	directPairs := make(map[[2]string]struct{}, len(detected))
	for _, p := range detected {
		from := g.GetNode(p.A)
		to := g.GetNode(p.B)
		if from == nil || to == nil {
			continue
		}
		emitSimilarEdge(g, from, to, p.Similarity)
		emitSimilarEdge(g, to, from, p.Similarity)
		stats.Edges += 2
		// Record the canonicalised (A<B) clone pair so the diffusion
		// pass below never re-emits a direct clone as a merely
		// semantically-related edge — the two edge kinds partition.
		directPairs[canonicalPair(p.A, p.B)] = struct{}{}
	}

	// Graph-diffusion smoothing. Runs here, after the direct clone
	// edges are materialised, while detectClonesAndEmitEdges still
	// holds g.ResolveMutex — the diffusion pass mutates Node-adjacent
	// edge state and must rendezvous on the same lock as the clone
	// pass it extends.
	reporter.Report("clones: diffuse similarity edges", 0, 0)
	dp, de := diffuseSimilarityEdges(g, detected, directPairs)
	stats.DiffusedPairs = dp
	stats.DiffusedEdges = de
	return stats
}

// Diffusion-pass tuning constants. The graph-diffusion smoothing pass
// blends direct clone similarities across one shared neighbour, then
// threshold-gates and caps the result so the semantically-related edge
// set stays bounded — it must never explode the graph's edge count.
const (
	// diffusionDamping discounts a two-hop blended score relative to
	// the direct clone similarities it is derived from. The diffused
	// score for a pair (A,C) bridged by B is
	//   damping · similarity(A,B) · similarity(B,C)
	// — a product (already ≤ each factor) further damped, so a
	// transitive relation is always weaker evidence than either
	// direct clone link it rests on. 0.9 keeps a strong A~B~C chain
	// comfortably above the emit threshold while still ranking it
	// below a genuine clone.
	diffusionDamping = 0.9
	// diffusionThreshold is the minimum diffused score for a pair to
	// be materialised as an EdgeSemanticallyRelated edge. Set below
	// the clone DefaultThreshold (0.82): the whole point of the pass
	// is to surface relatedness the clone filter rejected, so the
	// gate must admit sub-clone scores — but high enough that a chain
	// through two weak (~0.5) clone links is dropped as noise.
	diffusionThreshold = 0.55
	// diffusionMaxNeighbors caps the clone-graph fan-out considered
	// per node. A node in a large clone cluster (templated
	// boilerplate) would otherwise contribute a quadratic burst of
	// diffused pairs; bounding the per-node neighbour set keeps the
	// pass near-linear. Neighbours are taken in descending direct
	// similarity so the strongest links survive the cap.
	diffusionMaxNeighbors = 16
	// diffusionMaxPairs is the hard ceiling on emitted
	// semantically-related pairs across the whole graph. Pairs are
	// ranked by diffused score (descending) before the cut, so the
	// strongest relations survive when the ceiling binds. Two
	// directed edges are emitted per surviving pair.
	diffusionMaxPairs = 50000
)

// canonicalPair returns the (smaller, larger) ordering of two IDs so a
// pair has a single key regardless of argument order.
func canonicalPair(a, b string) [2]string {
	if a <= b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

// diffusionEdge is one weighted link in the in-memory similarity graph
// the diffusion pass walks — a neighbour ID and the direct clone score.
type diffusionEdge struct {
	id    string
	score float64
}

// diffuseSimilarityEdges is the graph-diffusion smoothing pass. It
// takes the direct clone pairs produced by the LSH filter, builds the
// undirected similarity graph they describe, and for every pair (A,C)
// joined through a shared neighbour B derives a damped two-hop score.
// Surviving pairs (above diffusionThreshold, not already a direct
// clone, capped at diffusionMaxPairs) are materialised as a symmetric
// pair of EdgeSemanticallyRelated edges.
//
// The blend is a bounded 1-to-2-hop transitive product — not a dense
// O(n²) diffusion. It is deterministic: neighbour lists are sorted, the
// score for a pair is the max over its bridging neighbours (an
// associative reduction independent of visitation order), and the
// final cap cuts a score-sorted slice with ID tie-breaks.
//
// directPairs carries the canonicalised clone pairs already emitted as
// EdgeSimilarTo; any pair in that set is skipped so semantically_related
// and similar_to partition cleanly.
func diffuseSimilarityEdges(g graph.Store, pairs []clones.Pair, directPairs map[[2]string]struct{}) (diffusedPairs, diffusedEdges int) {
	if g == nil || len(pairs) < 2 {
		return 0, 0
	}

	// Adjacency: id → its similar neighbours with direct scores. Each
	// undirected clone pair contributes an entry on both endpoints.
	adj := make(map[string][]diffusionEdge)
	for _, p := range pairs {
		adj[p.A] = append(adj[p.A], diffusionEdge{id: p.B, score: p.Similarity})
		adj[p.B] = append(adj[p.B], diffusionEdge{id: p.A, score: p.Similarity})
	}

	// Sort each neighbour list by descending score (ID tie-break) and
	// apply the per-node fan-out cap. Sorting also makes the pair
	// enumeration below deterministic.
	for id, nbrs := range adj {
		sort.Slice(nbrs, func(i, j int) bool {
			if nbrs[i].score != nbrs[j].score {
				return nbrs[i].score > nbrs[j].score
			}
			return nbrs[i].id < nbrs[j].id
		})
		if len(nbrs) > diffusionMaxNeighbors {
			adj[id] = nbrs[:diffusionMaxNeighbors]
		}
	}

	// For each bridge node B, every unordered pair of its neighbours
	// (A,C) is a candidate two-hop relation. The diffused score is the
	// damped product of the two clone links; when multiple bridges
	// connect the same (A,C) the strongest (max) bridge wins.
	best := make(map[[2]string]float64)
	bridges := make([]string, 0, len(adj))
	for id := range adj {
		bridges = append(bridges, id)
	}
	sort.Strings(bridges)
	for _, b := range bridges {
		nbrs := adj[b]
		for i := range nbrs {
			for j := i + 1; j < len(nbrs); j++ {
				a, c := nbrs[i].id, nbrs[j].id
				if a == c {
					continue
				}
				key := canonicalPair(a, c)
				if _, isClone := directPairs[key]; isClone {
					continue // a direct clone — stays similar_to only
				}
				score := diffusionDamping * nbrs[i].score * nbrs[j].score
				if score < diffusionThreshold {
					continue
				}
				if score > best[key] {
					best[key] = score
				}
			}
		}
	}
	if len(best) == 0 {
		return 0, 0
	}

	// Rank surviving pairs by diffused score so the global cap keeps
	// the strongest relations; ID tie-breaks keep the cut deterministic.
	type diffusedPair struct {
		a, c  string
		score float64
	}
	ranked := make([]diffusedPair, 0, len(best))
	for key, score := range best {
		ranked = append(ranked, diffusedPair{a: key[0], c: key[1], score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].a != ranked[j].a {
			return ranked[i].a < ranked[j].a
		}
		return ranked[i].c < ranked[j].c
	})
	if len(ranked) > diffusionMaxPairs {
		ranked = ranked[:diffusionMaxPairs]
	}

	for _, rp := range ranked {
		from := g.GetNode(rp.a)
		to := g.GetNode(rp.c)
		if from == nil || to == nil {
			continue
		}
		emitSemanticallyRelatedEdge(g, from, to, rp.score)
		emitSemanticallyRelatedEdge(g, to, from, rp.score)
		diffusedPairs++
		diffusedEdges += 2
	}
	return diffusedPairs, diffusedEdges
}

// emitSimilarEdge adds one directed EdgeSimilarTo edge carrying the
// estimated Jaccard similarity. The edge is anchored at the source
// node's file/line for locality. Origin is ast_inferred — the
// relationship is a statistical estimate over normalised tokens, not a
// structural fact.
func emitSimilarEdge(g graph.Store, from, to *graph.Node, similarity float64) {
	g.AddEdge(&graph.Edge{
		From:       from.ID,
		To:         to.ID,
		Kind:       graph.EdgeSimilarTo,
		FilePath:   from.FilePath,
		Line:       from.StartLine,
		Confidence: similarity,
		Origin:     graph.OriginASTInferred,
		Meta:       map[string]any{"similarity": similarity},
	})
}

// emitSemanticallyRelatedEdge adds one directed EdgeSemanticallyRelated
// edge carrying the diffused similarity score. Like emitSimilarEdge the
// edge is anchored at the source node's file/line and origin is
// ast_inferred — the score is a statistical estimate over normalised
// tokens, here additionally smoothed across the similarity graph.
func emitSemanticallyRelatedEdge(g graph.Store, from, to *graph.Node, similarity float64) {
	g.AddEdge(&graph.Edge{
		From:       from.ID,
		To:         to.ID,
		Kind:       graph.EdgeSemanticallyRelated,
		FilePath:   from.FilePath,
		Line:       from.StartLine,
		Confidence: similarity,
		Origin:     graph.OriginASTInferred,
		Meta:       map[string]any{"similarity": similarity},
	})
}
