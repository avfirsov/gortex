package resolver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// persistedEdgeSnapshot is an immutable copy of every logical column stored
// for an edge. The identity columns alone are not enough for yielded resolver
// work: an interactive edit may replace a row with the same unique key but
// different provenance or receiver metadata.
type persistedEdgeSnapshot struct {
	valid           bool
	from            string
	to              string
	kind            graph.EdgeKind
	filePath        string
	line            int
	confidence      float64
	confidenceLabel string
	origin          string
	tier            string
	crossRepo       bool
	meta            []byte
}

// persistedEdgeSpoolSnapshot is the pointer-free wire form used by the guard
// and deferred-LSP disk spools. Exported fields keep encoding/gob compatible;
// Meta is already the immutable canonical payload, not a live map.
type persistedEdgeSpoolSnapshot struct {
	From            string
	To              string
	Kind            graph.EdgeKind
	FilePath        string
	Line            int
	Confidence      float64
	ConfidenceLabel string
	Origin          string
	Tier            string
	CrossRepo       bool
	Meta            []byte
}

func spoolSnapshotPersistedEdge(edge *graph.Edge) persistedEdgeSpoolSnapshot {
	snapshot := snapshotPersistedEdge(edge)
	return persistedEdgeSpoolSnapshot{
		From: snapshot.from, To: snapshot.to, Kind: snapshot.kind,
		FilePath: snapshot.filePath, Line: snapshot.line,
		Confidence: snapshot.confidence, ConfidenceLabel: snapshot.confidenceLabel,
		Origin: snapshot.origin, Tier: snapshot.tier, CrossRepo: snapshot.crossRepo,
		Meta: append([]byte(nil), snapshot.meta...),
	}
}

func (wire persistedEdgeSpoolSnapshot) snapshot() persistedEdgeSnapshot {
	return persistedEdgeSnapshot{
		valid: true,
		from:  wire.From, to: wire.To, kind: wire.Kind,
		filePath: wire.FilePath, line: wire.Line,
		confidence: wire.Confidence, confidenceLabel: wire.ConfidenceLabel,
		origin: wire.Origin, tier: wire.Tier, crossRepo: wire.CrossRepo,
		meta: append([]byte(nil), wire.Meta...),
	}
}

func snapshotPersistedEdge(edge *graph.Edge) persistedEdgeSnapshot {
	if edge == nil {
		return persistedEdgeSnapshot{}
	}
	return persistedEdgeSnapshot{
		valid:           true,
		from:            edge.From,
		to:              edge.To,
		kind:            edge.Kind,
		filePath:        edge.FilePath,
		line:            edge.Line,
		confidence:      edge.Confidence,
		confidenceLabel: edge.ConfidenceLabel,
		origin:          edge.Origin,
		tier:            edge.Tier,
		crossRepo:       edge.CrossRepo,
		meta:            canonicalPersistedEdgeMeta(edge.Meta),
	}
}

func (snapshot persistedEdgeSnapshot) identity() graph.EdgeIdentity {
	return graph.EdgeIdentity{
		From: snapshot.from, To: snapshot.to, Kind: snapshot.kind,
		FilePath: snapshot.filePath, Line: snapshot.line,
	}
}

func (snapshot persistedEdgeSnapshot) matches(edge *graph.Edge) bool {
	if !snapshot.valid || edge == nil {
		return false
	}
	if snapshot.from != edge.From || snapshot.to != edge.To || snapshot.kind != edge.Kind ||
		snapshot.filePath != edge.FilePath || snapshot.line != edge.Line ||
		snapshot.confidence != edge.Confidence || snapshot.confidenceLabel != edge.ConfidenceLabel ||
		snapshot.origin != edge.Origin || snapshot.tier != edge.Tier || snapshot.crossRepo != edge.CrossRepo {
		return false
	}
	return bytes.Equal(snapshot.meta, canonicalPersistedEdgeMeta(edge.Meta))
}

// canonicalPersistedEdgeMeta produces an immutable, collision-free encoding
// of Meta. It includes dynamic Go types and floating-point bits, unlike plain
// JSON, and sorts map entries so a store materialising a fresh map compares
// byte-for-byte with the pre-yield snapshot.
func canonicalPersistedEdgeMeta(meta map[string]any) []byte {
	if len(meta) == 0 {
		return nil
	}
	return appendCanonicalPersistedValue(nil, reflect.ValueOf(meta))
}

func appendCanonicalPersistedValue(dst []byte, value reflect.Value) []byte {
	if !value.IsValid() {
		return append(dst, 0)
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return append(dst, 0)
		}
		return appendCanonicalPersistedValue(dst, value.Elem())
	}

	dst = append(dst, byte(value.Kind())+1)
	dst = appendCanonicalPersistedString(dst, value.Type().PkgPath()+"\x00"+value.Type().String())
	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			return append(dst, 1)
		}
		return append(dst, 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return binary.AppendVarint(dst, value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return binary.AppendUvarint(dst, value.Uint())
	case reflect.Float32:
		return binary.LittleEndian.AppendUint32(dst, math.Float32bits(float32(value.Float())))
	case reflect.Float64:
		return binary.LittleEndian.AppendUint64(dst, math.Float64bits(value.Float()))
	case reflect.Complex64:
		complexValue := complex64(value.Complex())
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(real(complexValue)))
		return binary.LittleEndian.AppendUint32(dst, math.Float32bits(imag(complexValue)))
	case reflect.Complex128:
		complexValue := value.Complex()
		dst = binary.LittleEndian.AppendUint64(dst, math.Float64bits(real(complexValue)))
		return binary.LittleEndian.AppendUint64(dst, math.Float64bits(imag(complexValue)))
	case reflect.String:
		return appendCanonicalPersistedString(dst, value.String())
	case reflect.Pointer:
		if value.IsNil() {
			return append(dst, 0)
		}
		dst = append(dst, 1)
		return appendCanonicalPersistedValue(dst, value.Elem())
	case reflect.Slice, reflect.Array:
		// The stores persist nil and empty slices identically as zero items.
		dst = binary.AppendUvarint(dst, uint64(value.Len()))
		for i := 0; i < value.Len(); i++ {
			dst = appendCanonicalPersistedValue(dst, value.Index(i))
		}
		return dst
	case reflect.Map:
		// The stores likewise persist nil and empty maps identically.
		type entry struct {
			key   []byte
			value []byte
		}
		entries := make([]entry, 0, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			entries = append(entries, entry{
				key:   appendCanonicalPersistedValue(nil, iterator.Key()),
				value: appendCanonicalPersistedValue(nil, iterator.Value()),
			})
		}
		sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].key, entries[j].key) < 0 })
		dst = binary.AppendUvarint(dst, uint64(len(entries)))
		for _, item := range entries {
			dst = binary.AppendUvarint(dst, uint64(len(item.key)))
			dst = append(dst, item.key...)
			dst = binary.AppendUvarint(dst, uint64(len(item.value)))
			dst = append(dst, item.value...)
		}
		return dst
	case reflect.Struct:
		// Persisted struct values use JSON in the SQLite codec (for example
		// contract shapes). JSON is deterministic for a fixed struct type.
		if value.CanInterface() {
			if encoded, err := json.Marshal(value.Interface()); err == nil {
				return appendCanonicalPersistedBytes(dst, encoded)
			}
		}
	}
	// Func/chan/unsafe-pointer values cannot be persisted by any store codec.
	return dst
}

func appendCanonicalPersistedString(dst []byte, value string) []byte {
	return appendCanonicalPersistedBytes(dst, []byte(value))
}

func appendCanonicalPersistedBytes(dst, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

// resolveJobLiveness batches the inter-chunk stale-edge guard. A page can sit
// across several resolve-mutex yields, so an interactive edit may evict work
// before its chunk is applied. Exact-capable stores fetch only the requested
// five-column logical keys; other stores retain one set-oriented site lookup.
// Both paths compare the complete immutable persisted payload before applying
// yielded work.
type resolveJobLiveness struct {
	exact      map[graph.EdgeIdentity]*graph.Edge
	candidates graph.EdgeCandidateSet
}

// edgeMutationRevisioner is an optional store capability. Production Graph
// and SQLite stores expose a monotonic edge-state revision; wrappers and
// third-party stores remain correct by taking the conservative validation
// path on every chunk.
type edgeMutationRevisioner interface {
	EdgeMutationRevision() uint64
}

func loadEdgeMutationRevision(store graph.Store) (uint64, bool) {
	revisioned, ok := store.(edgeMutationRevisioner)
	if !ok {
		return 0, false
	}
	return revisioned.EdgeMutationRevision(), true
}

// mutationRevisioner invalidates resolver lookup/pass caches for both node and
// edge writes. It is deliberately separate from edgeMutationRevisioner: a
// node-only interleave must rebuild candidates but cannot stale an edge job.
type mutationRevisioner interface {
	MutationRevision() uint64
}

func loadMutationRevision(store graph.Store) (uint64, bool) {
	revisioned, ok := store.(mutationRevisioner)
	if !ok {
		return 0, false
	}
	return revisioned.MutationRevision(), true
}

func loadResolveJobLiveness(store graph.Store, jobs [][]reindexJob) resolveJobLiveness {
	finder, exactCapable := store.(graph.EdgeIdentityBatchFinder)
	var identities []graph.EdgeIdentity
	var sites []graph.EdgeSite
	if exactCapable {
		identities = make([]graph.EdgeIdentity, 0, resolveJobCount(jobs))
	} else {
		sites = make([]graph.EdgeSite, 0, resolveJobCount(jobs))
	}

	for i := range jobs {
		for j := range jobs[i] {
			job := &jobs[i][j]
			if !job.preResolution.valid {
				// ResolveAll normally captures this while constructing the job.
				// Keep direct/bespoke callers exact as well.
				job.preResolution = snapshotPersistedEdge(job.edge)
			}
			if !job.preResolution.valid {
				continue
			}
			if exactCapable {
				// The snapshot still carries OldKind and the old target here.
				// Resolver branches may already have computed a promoted kind or
				// rebound target; those identify the future row, not the live one.
				identities = append(identities, job.preResolution.identity())
				continue
			}
			sites = append(sites, graph.EdgeSite{
				From: job.preResolution.from,
				Line: job.preResolution.line,
				Kind: job.preResolution.kind,
			})
		}
	}
	if exactCapable {
		found := finder.FindEdgesByIdentities(identities)
		if found == nil {
			found = make(map[graph.EdgeIdentity]*graph.Edge)
		}
		return resolveJobLiveness{exact: found}
	}
	return resolveJobLiveness{candidates: graph.LookupEdgeCandidates(store, nil, sites)}
}

func resolveJobCount(jobs [][]reindexJob) int {
	total := 0
	for i := range jobs {
		total += len(jobs[i])
	}
	return total
}

// loadEdgeLiveness is the current-shape variant used by deferred passes after
// the initial reindex has applied. It preserves the same exact value semantics
// as edgeStillLive while collapsing N point queries into one batched lookup.
func loadEdgeLiveness(store graph.Store, edges []*graph.Edge) resolveJobLiveness {
	finder, exactCapable := store.(graph.EdgeIdentityBatchFinder)
	if exactCapable {
		identities := make([]graph.EdgeIdentity, 0, len(edges))
		for _, edge := range edges {
			if edge != nil {
				identities = append(identities, graph.EdgeIdentityFor(edge))
			}
		}
		found := finder.FindEdgesByIdentities(identities)
		if found == nil {
			found = make(map[graph.EdgeIdentity]*graph.Edge)
		}
		return resolveJobLiveness{exact: found}
	}

	sites := make([]graph.EdgeSite, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		sites = append(sites, graph.EdgeSite{From: edge.From, Line: edge.Line, Kind: edge.Kind})
	}
	return resolveJobLiveness{candidates: graph.LookupEdgeCandidates(store, nil, sites)}
}

func (l resolveJobLiveness) containsEdge(edge *graph.Edge) bool {
	if edge == nil {
		return false
	}
	snapshot := snapshotPersistedEdge(edge)
	if l.exact != nil {
		return snapshot.matches(l.exact[snapshot.identity()])
	}
	for _, candidate := range l.candidates.Site(snapshot.from, snapshot.line, snapshot.kind) {
		if snapshot.matches(candidate) {
			return true
		}
	}
	return false
}

func (l resolveJobLiveness) contains(job reindexJob) bool {
	snapshot := job.preResolution
	if !snapshot.valid {
		return false
	}
	if l.exact != nil {
		return snapshot.matches(l.exact[snapshot.identity()])
	}
	for _, candidate := range l.candidates.Site(snapshot.from, snapshot.line, snapshot.kind) {
		if snapshot.matches(candidate) {
			return true
		}
	}
	return false
}
