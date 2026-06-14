package exporter

// understand.go renders the gortex code graph into the Understand-Anything
// format (`understand-anything@1`, file `.understand-anything/knowledge-graph.json`)
// and a reduced `generic@1` `{nodes, edges}` projection. It is one more format
// renderer of the same graph, a sibling of cypher.go / graphml.go / mermaid.go,
// and it reuses the shared snapshot() pipeline for filtering, synthetic-stub
// handling, dedup, and stable sorting.
//
// WHY this layering: the mapping from gortex's NodeKind/EdgeKind enums to the
// UA schema's closed enums is a pure Calculation — no I/O, no clock, no git.
// buildUAGraph holds that pure core so it can be exhaustively unit-tested
// without a Store and proven deterministic. ToUnderstandAnything adapts a
// Store to that core via snapshot(). WriteUnderstandAnything is the only
// Action: it marshals and writes bytes. The CLI/Action layer is the sole
// supplier of analyzedAt (RFC3339) and gitCommitHash — never time.Now() or a
// git call inside the pure core (see business_requirements §12 MUST NOT).
//
// FIDELITY contract: the UA schema validates with zod .passthrough(), so
// gortex-specific fields ride along as passthrough keys (gortex_kind, repo,
// workspace_id on nodes; gortex_kind, confidence_label, tier, cross_repo on
// edges) and survive validation. Nothing is dropped silently — every node or
// edge that is not emitted is recorded in []Dropped with a human reason, so
// referential integrity holds and the real UA validateGraph drops zero.

import (
	"encoding/json"
	"io"
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// uaVersion / uaKind are the fixed envelope values for understand-anything@1.
const (
	uaVersion = "1.0.0"
	uaKind    = "codebase"

	// Granularity values. "slim" (default) drops the high-cardinality,
	// low-signal node kinds (params, locals, builtins, …); "full" keeps
	// them, re-classified as the generic UA `concept` type.
	GranularitySlim = "slim"
	GranularityFull = "full"

	// uaDefaultDescription is the project description stamped on every
	// export. Kept constant so the pure core never derives prose.
	uaDefaultDescription = "Exported from gortex code graph"

	// uaConcept / uaDependsOn are the documented fallback UA types for an
	// unknown gortex NodeKind / EdgeKind respectively. They are deliberate
	// (asserted by the enum-coverage test), not an accidental fallthrough.
	uaConcept    = "concept"
	uaDependsOn  = "depends_on"
	uaContains   = "contains"
	uaCrossDomn  = "cross_domain"
	uaTransforms = "transforms"

	// neutralWeight is the UA edge weight used when an edge's Confidence is
	// exactly 0 — gortex stores 0 for "resolved without a numeric score",
	// which is not the same as "zero confidence", so we map it to a neutral
	// midpoint rather than the bottom of the [0,1] band.
	neutralWeight = 0.5

	// edgeDirectionForward is the only direction L1 emits — gortex edges
	// are inherently directed source→target.
	edgeDirectionForward = "forward"
)

// uaNodeType is the authoritative gortex NodeKind → UA node type allowlist
// (business_requirements §5). A kind present here is emitted with the mapped
// UA type. A kind absent from BOTH this map and uaNodeDeny falls back to the
// `concept` type with a gortex_kind passthrough.
var uaNodeType = map[graph.NodeKind]string{
	graph.KindFile:          "file",
	graph.KindFunction:      "function",
	graph.KindMethod:        "function",
	graph.KindType:          "class",
	graph.KindInterface:     "class",
	graph.KindModule:        "module",
	graph.KindImport:        "module",
	graph.KindContract:      "endpoint",
	graph.KindTable:         "table",
	graph.KindMigration:     "table",
	graph.KindConfigKey:     "config",
	graph.KindFlag:          "config",
	graph.KindResource:      "resource",
	graph.KindImage:         "resource",
	graph.KindKustomization: "resource",
	graph.KindEvent:         "concept",
	graph.KindConstant:      "concept",
}

// uaNodeDeny is the authoritative slim-granularity denylist
// (business_requirements §5). A kind here is dropped under slim granularity
// (recorded in []Dropped with a denylist reason) and re-included as `concept`
// under --granularity full.
var uaNodeDeny = map[graph.NodeKind]bool{
	graph.KindParam:        true,
	graph.KindLocal:        true,
	graph.KindBuiltin:      true,
	graph.KindClosure:      true,
	graph.KindGenericParam: true,
	graph.KindEnumMember:   true,
	graph.KindVariable:     true,
	graph.KindColumn:       true,
}

// uaEdgeType is the authoritative gortex EdgeKind → UA edge type map
// (business_requirements §6). A kind absent from this map falls back to the
// `depends_on` UA type with a gortex_kind passthrough. member_of maps to
// `contains` but additionally requires a source/target swap — that swap is
// applied in mapEdgeKind, not encoded here.
var uaEdgeType = map[graph.EdgeKind]string{
	graph.EdgeCalls:                  "calls",
	graph.EdgeImports:                "imports",
	graph.EdgeImplements:             "implements",
	graph.EdgeExtends:                "inherits",
	graph.EdgeOverrides:              "inherits",
	graph.EdgeDefines:                uaContains,
	graph.EdgeRendersChild:           uaContains,
	graph.EdgeContains:               uaContains,
	graph.EdgePackageWorkspaceMember: uaContains,
	graph.EdgeMemberOf:               uaContains, // + swap source/target
	graph.EdgeReferences:             "related",
	graph.EdgeSimilarTo:              "similar_to",
	graph.EdgeSemanticallyRelated:    "related",
	graph.EdgeReads:                  "reads_from",
	graph.EdgeReadsConfig:            "reads_from",
	graph.EdgeReadsCol:               "reads_from",
	graph.EdgeQueries:                "reads_from",
	graph.EdgeWrites:                 "writes_to",
	graph.EdgeWritesConfig:           "writes_to",
	graph.EdgeWritesCol:              "writes_to",
	graph.EdgeValueFlow:              uaTransforms,
	graph.EdgeArgOf:                  uaTransforms,
	graph.EdgeReturnsTo:              uaTransforms,
	graph.EdgeSends:                  "publishes",
	graph.EdgeEmits:                  "publishes",
	graph.EdgeRecvs:                  "subscribes",
	graph.EdgeListensOn:              "subscribes",
	graph.EdgeSpawns:                 "triggers",
	graph.EdgeDependsOn:              uaDependsOn,
	graph.EdgeDependsOnModule:        uaDependsOn,
	graph.EdgeInstantiates:           uaDependsOn,
	graph.EdgeTypedAs:                uaDependsOn,
	graph.EdgeTests:                  "tested_by",
	graph.EdgeCoveredBy:              "tested_by",
	graph.EdgeHandlesRoute:           "routes",
	graph.EdgeModelsTable:            "defines_schema",
	graph.EdgeConfigures:             "configures",
	graph.EdgeUsesEnv:                "configures",
	graph.EdgeTogglesFlag:            "configures",
	graph.EdgeMounts:                 "serves",
	graph.EdgeExposes:                "serves",
	graph.EdgeProvides:               "serves",
	graph.EdgeConsumes:               "serves",
	graph.EdgeAnnotated:              "documents",
	graph.EdgeCrossRepoCalls:         uaCrossDomn,
	graph.EdgeCrossRepoImplements:    uaCrossDomn,
	graph.EdgeCrossRepoExtends:       uaCrossDomn,
}

// crossRepoEdgeKinds names the gortex EdgeKinds that map to UA cross_domain
// and carry a cross_repo:true passthrough. Kept as a set so mapEdgeKind can
// stamp the passthrough without re-deriving it from the kind string.
var crossRepoEdgeKinds = map[graph.EdgeKind]bool{
	graph.EdgeCrossRepoCalls:      true,
	graph.EdgeCrossRepoImplements: true,
	graph.EdgeCrossRepoExtends:    true,
}

// transformEdgeKinds names the EdgeKinds whose UA type is `transforms` and
// which are themselves slim-granularity dataflow edges — dropped under slim,
// kept under full. They follow the same slim/full gate as the denied node
// kinds so a slim export stays at the architecture/call-graph altitude.
var transformEdgeKinds = map[graph.EdgeKind]bool{
	graph.EdgeValueFlow: true,
	graph.EdgeArgOf:     true,
	graph.EdgeReturnsTo: true,
}

// UAOptions configures the Understand-Anything export. It embeds the shared
// exporter Options (Repo / Kinds / Languages / DropSynthetic filters) and adds
// the UA-specific knobs. AnalyzedAt and GitCommit are supplied by the
// Action/CLI layer ONLY — the pure core never reads the clock or shells git.
type UAOptions struct {
	Options

	// Granularity is "slim" (default, drops high-cardinality kinds) or
	// "full" (keeps them as `concept`). An empty string is treated as slim.
	Granularity string

	// Generic toggles the reduced `generic@1` `{nodes, edges}` projection
	// instead of the full understand-anything@1 envelope.
	Generic bool

	// ProjectName overrides the project name in the UA envelope. Empty lets
	// the Action layer fall back to the repo basename.
	ProjectName string

	// AnalyzedAt is an RFC3339 timestamp supplied by the Action layer.
	AnalyzedAt string

	// GitCommit is the indexed repo's commit hash supplied by the Action
	// layer.
	GitCommit string
}

// granularity normalizes the option to a known value, defaulting to slim.
func (o UAOptions) granularity() string {
	if o.Granularity == GranularityFull {
		return GranularityFull
	}
	return GranularitySlim
}

// UAGraph mirrors the understand-anything@1 KnowledgeGraph envelope. Nodes /
// Edges / Layers / Tour are always non-nil so they marshal as `[]`, never
// `null` (the UA schema requires arrays). Layers and Tour are empty for L1.
type UAGraph struct {
	Version string       `json:"version"`
	Kind    string       `json:"kind"`
	Project UAProject    `json:"project"`
	Nodes   []UANode     `json:"nodes"`
	Edges   []UAEdge     `json:"edges"`
	Layers  []UALayer    `json:"layers"`
	Tour    []UATourStep `json:"tour"`
}

// UAProject is the project-metadata block. Languages / Frameworks are non-nil
// so they marshal as `[]`.
type UAProject struct {
	Name          string   `json:"name"`
	Languages     []string `json:"languages"`
	Frameworks    []string `json:"frameworks"`
	Description   string   `json:"description"`
	AnalyzedAt    string   `json:"analyzedAt"`
	GitCommitHash string   `json:"gitCommitHash,omitempty"`
}

// UANode mirrors the UA GraphNode. summary / tags / complexity are REQUIRED by
// the schema so they carry no omitempty (tags is always a non-nil slice).
// lineRange is a pointer so it can be omitted when the node has no range.
// The trailing fields are gortex-specific passthroughs preserved by the UA
// schema's .passthrough().
type UANode struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Name       string   `json:"name"`
	FilePath   string   `json:"filePath,omitempty"`
	LineRange  *[2]int  `json:"lineRange,omitempty"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Complexity string   `json:"complexity"`

	// Passthrough (gortex-specific, survives zod .passthrough()).
	GortexKind  string `json:"gortex_kind,omitempty"`
	Repo        string `json:"repo,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// UAEdge mirrors the UA GraphEdge. weight is REQUIRED (0.0 is valid) so it
// carries no omitempty. The trailing fields are gortex-specific passthroughs.
type UAEdge struct {
	Source      string  `json:"source"`
	Target      string  `json:"target"`
	Type        string  `json:"type"`
	Direction   string  `json:"direction"`
	Description string  `json:"description,omitempty"`
	Weight      float64 `json:"weight"`

	// Passthrough (gortex-specific, survives zod .passthrough()).
	GortexKind      string `json:"gortex_kind,omitempty"`
	ConfidenceLabel string `json:"confidence_label,omitempty"`
	Tier            string `json:"tier,omitempty"`
	CrossRepo       bool   `json:"cross_repo,omitempty"`
}

// UALayer / UATourStep are the L1-empty UA envelope sections, declared so the
// JSON shape matches the schema even though L1 emits none.
type UALayer struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
}

type UATourStep struct {
	NodeID      string `json:"nodeId"`
	Explanation string `json:"explanation"`
}

// Dropped records a node or edge that was deliberately not emitted, with a
// human-readable reason. It is the audit trail that makes "no silent data
// loss" (business_requirements §12) observable: every non-emitted entity is
// here, so a reader can reconcile input counts against output counts.
type Dropped struct {
	ID     string
	Kind   string
	Reason string
}

// mapNodeKind maps a gortex NodeKind to a UA node type under the given
// granularity. It returns (uaType, drop, reason):
//
//   - allowlisted kind          → (mappedType, false, "")
//   - denylisted kind, slim     → ("", true, "denylist: <kind> dropped under slim granularity")
//   - denylisted kind, full     → ("concept", false, "")
//   - unknown kind              → ("concept", false, "") with the caller stamping gortex_kind
//
// The `concept` fallback for an unknown kind is a documented, deliberate
// default (asserted by the enum-coverage test), not an accidental fallthrough.
func mapNodeKind(kind graph.NodeKind, granularity string) (uaType string, drop bool, reason string) {
	if t, ok := uaNodeType[kind]; ok {
		return t, false, ""
	}
	if uaNodeDeny[kind] {
		if granularity == GranularityFull {
			return uaConcept, false, ""
		}
		return "", true, "denylist: " + string(kind) + " dropped under slim granularity"
	}
	// Unknown kind: documented fallback to the generic UA concept type.
	return uaConcept, false, ""
}

// mapEdgeKind maps a gortex EdgeKind to a UA edge type. It returns
// (uaType, swap, isTransform):
//
//   - member_of                 → ("contains", true, false)  source/target swapped
//   - cross_repo_* kinds        → ("cross_domain", false, false)  caller stamps cross_repo
//   - value_flow/arg_of/returns_to → ("transforms", false, true)  slim-gated by caller
//   - any other mapped kind     → (mappedType, false, false)
//   - unknown kind              → ("depends_on", false, false) with gortex_kind passthrough
//
// The `depends_on` fallback for an unknown kind is a documented, deliberate
// default (asserted by the enum-coverage test).
func mapEdgeKind(kind graph.EdgeKind) (uaType string, swap bool, isTransform bool) {
	t, ok := uaEdgeType[kind]
	if !ok {
		return uaDependsOn, false, false
	}
	swap = kind == graph.EdgeMemberOf
	isTransform = transformEdgeKinds[kind]
	return t, swap, isTransform
}

// complexityOf classifies a node into the UA "simple|moderate|complex" band
// from its outgoing-edge count and source line span (business_requirements §5):
// simple when outdeg < 4 AND span < 40; complex when outdeg > 20 OR span > 300;
// moderate otherwise. A node with no EndLine has span 0.
func complexityOf(n *graph.Node, outDegree int) string {
	span := 0
	if n.EndLine > n.StartLine {
		span = n.EndLine - n.StartLine
	}
	if outDegree > 20 || span > 300 {
		return "complex"
	}
	if outDegree < 4 && span < 40 {
		return "simple"
	}
	return "moderate"
}

// weightOf maps an edge's Confidence to the UA [0,1] weight. gortex stores
// Confidence 0 for "resolved without a numeric score" (not "no confidence"),
// so 0 maps to a neutral 0.5; every other value is clamped into [0,1].
func weightOf(confidence float64) float64 {
	if confidence == 0 {
		return neutralWeight
	}
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

// tagsOf returns the node's UA tags as a non-nil slice of its non-empty
// [Language, Kind] fields. The slice is always non-nil (UA requires `tags` to
// marshal as `[]`, never `null`) and skips empty components.
func tagsOf(n *graph.Node) []string {
	tags := make([]string, 0, 2)
	if n.Language != "" {
		tags = append(tags, n.Language)
	}
	if n.Kind != "" {
		tags = append(tags, string(n.Kind))
	}
	return tags
}

// lineRangeOf returns the node's UA lineRange, or nil when it has no end line
// (file nodes carry no range). A nil pointer is omitted from the JSON.
func lineRangeOf(n *graph.Node) *[2]int {
	if n.EndLine <= 0 {
		return nil
	}
	return &[2]int{n.StartLine, n.EndLine}
}

// summaryOf returns the node's UA summary — its qualified name when present,
// else its short name, else its ID. The ID fallback guarantees a non-empty
// summary even for anonymous or synthetic-stub nodes (the UA schema requires
// summary to be a non-empty string).
func summaryOf(n *graph.Node) string {
	if n.QualName != "" {
		return n.QualName
	}
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// buildUAGraph is the PURE core: it projects pre-snapshotted node/edge slices
// into a UAGraph and a []Dropped audit trail. No I/O, no clock, no git — every
// time-varying input (AnalyzedAt, GitCommit, ProjectName) arrives via opts,
// supplied by the Action layer. Given identical inputs it produces an
// identical UAGraph (verified by the determinism test), so the marshalled
// bytes are byte-identical across runs.
//
// Two passes. Pass 1 classifies nodes: an emitted node is recorded in keptType
// (id → uaType) and appended to nodes; a dropped node is recorded in []Dropped
// with its reason. Pass 2 maps edges, applies the member_of swap, drops the
// slim-gated transform edges, and — crucially — drops any edge whose (possibly
// swapped) source or target was not emitted, recording it as "dangling". That
// dangling-drop is what guarantees referential integrity, so the real UA
// validateGraph reports zero dropped references.
func buildUAGraph(nodes []*graph.Node, edges []*graph.Edge, opts UAOptions) (UAGraph, []Dropped) {
	gran := opts.granularity()

	// Out-degree precompute: complexityOf needs each node's outgoing count.
	outDegree := make(map[string]int, len(nodes))
	for _, e := range edges {
		outDegree[e.From]++
	}

	keptType := make(map[string]string, len(nodes))
	uaNodes := make([]UANode, 0, len(nodes))
	languages := make(map[string]bool)
	var dropped []Dropped

	// --- Pass 1: classify + keep nodes ------------------------------------
	for _, n := range nodes {
		uaType, drop, reason := mapNodeKind(n.Kind, gran)
		if drop {
			dropped = append(dropped, Dropped{ID: n.ID, Kind: string(n.Kind), Reason: reason})
			continue
		}
		node := UANode{
			ID:          n.ID,
			Type:        uaType,
			Name:        n.Name,
			FilePath:    n.FilePath,
			LineRange:   lineRangeOf(n),
			Summary:     summaryOf(n),
			Tags:        tagsOf(n),
			Complexity:  complexityOf(n, outDegree[n.ID]),
			Repo:        n.RepoPrefix,
			WorkspaceID: n.WorkspaceID,
		}
		// Stamp gortex_kind when the gortex kind is not 1:1 recoverable from
		// the UA type — i.e. whenever the kind fell back to `concept`,
		// preserving the original kind so no information is lost.
		if uaType == uaConcept {
			node.GortexKind = string(n.Kind)
		}
		uaNodes = append(uaNodes, node)
		keptType[n.ID] = uaType
		if n.Language != "" {
			languages[n.Language] = true
		}
	}

	// --- Pass 2: map + filter edges ---------------------------------------
	uaEdges := make([]UAEdge, 0, len(edges))
	for _, e := range edges {
		uaType, swap, isTransform := mapEdgeKind(e.Kind)
		// Slim granularity drops the dataflow `transforms` edges, mirroring
		// the node denylist so a slim export stays at architecture altitude.
		if isTransform && gran != GranularityFull {
			dropped = append(dropped, Dropped{
				ID:     e.From + "->" + e.To,
				Kind:   string(e.Kind),
				Reason: "denylist: " + string(e.Kind) + " (transforms) dropped under slim granularity",
			})
			continue
		}
		src, tgt := e.From, e.To
		if swap {
			src, tgt = tgt, src
		}
		if _, ok := keptType[src]; !ok {
			dropped = append(dropped, Dropped{
				ID:     e.From + "->" + e.To,
				Kind:   string(e.Kind),
				Reason: "dangling: endpoint " + src + " not emitted",
			})
			continue
		}
		if _, ok := keptType[tgt]; !ok {
			dropped = append(dropped, Dropped{
				ID:     e.From + "->" + e.To,
				Kind:   string(e.Kind),
				Reason: "dangling: endpoint " + tgt + " not emitted",
			})
			continue
		}
		edge := UAEdge{
			Source:          src,
			Target:          tgt,
			Type:            uaType,
			Direction:       edgeDirectionForward,
			Description:     e.Origin,
			Weight:          weightOf(e.Confidence),
			ConfidenceLabel: e.ConfidenceLabel,
			Tier:            e.Tier,
			CrossRepo:       e.CrossRepo,
		}
		if crossRepoEdgeKinds[e.Kind] {
			edge.CrossRepo = true
		}
		// Stamp gortex_kind whenever the UA type is a lossy fallback —
		// unknown→depends_on or cross_repo_*→cross_domain — so the original
		// gortex edge kind survives as passthrough.
		if uaType == uaDependsOn || uaType == uaCrossDomn {
			edge.GortexKind = string(e.Kind)
		}
		uaEdges = append(uaEdges, edge)
	}

	// --- Determinism: stable re-sort before assembly ----------------------
	sort.Slice(uaNodes, func(i, j int) bool { return uaNodes[i].ID < uaNodes[j].ID })
	sort.Slice(uaEdges, func(i, j int) bool {
		if uaEdges[i].Source != uaEdges[j].Source {
			return uaEdges[i].Source < uaEdges[j].Source
		}
		if uaEdges[i].Target != uaEdges[j].Target {
			return uaEdges[i].Target < uaEdges[j].Target
		}
		return uaEdges[i].Type < uaEdges[j].Type
	})

	name := opts.ProjectName
	out := UAGraph{
		Version: uaVersion,
		Kind:    uaKind,
		Project: UAProject{
			Name:          name,
			Languages:     sortedKeys(languages),
			Frameworks:    []string{},
			Description:   uaDefaultDescription,
			AnalyzedAt:    opts.AnalyzedAt,
			GitCommitHash: opts.GitCommit,
		},
		Nodes:  uaNodes,
		Edges:  uaEdges,
		Layers: []UALayer{},
		Tour:   []UATourStep{},
	}
	return out, dropped
}

// sortedKeys returns the map's keys as a sorted slice (always non-nil so the
// JSON marshals as `[]`).
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ToUnderstandAnything adapts a Store to the pure core: it takes a deterministic
// snapshot (shared filtering / synthetic-stub / dedup / sort pipeline) and
// hands the resulting slices to buildUAGraph. It is deterministic given the
// store and performs no I/O of its own.
func ToUnderstandAnything(g graph.Store, opts UAOptions) (UAGraph, []Dropped) {
	nodes, edges, _ := snapshot(g, opts.Options)
	return buildUAGraph(nodes, edges, opts)
}

// genericGraph is the reduced `generic@1` projection: just the node/edge
// shapes a generic graph consumer needs, no UA envelope.
type genericGraph struct {
	Nodes []genericNode `json:"nodes"`
	Edges []genericEdge `json:"edges"`
}

type genericNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	FilePath string `json:"filePath,omitempty"`
}

type genericEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Type   string  `json:"type"`
	Weight float64 `json:"weight"`
}

// toGeneric reduces a UAGraph to the generic@1 projection.
func toGeneric(ua UAGraph) genericGraph {
	out := genericGraph{
		Nodes: make([]genericNode, 0, len(ua.Nodes)),
		Edges: make([]genericEdge, 0, len(ua.Edges)),
	}
	for _, n := range ua.Nodes {
		out.Nodes = append(out.Nodes, genericNode{ID: n.ID, Type: n.Type, Name: n.Name, FilePath: n.FilePath})
	}
	for _, e := range ua.Edges {
		out.Edges = append(out.Edges, genericEdge{Source: e.Source, Target: e.Target, Type: e.Type, Weight: e.Weight})
	}
	return out
}

// WriteUnderstandAnything is the Action: it snapshots the store, builds the UA
// graph (or the generic@1 projection when opts.Generic), marshals it to JSON,
// writes via the byte-counting writer, and returns Stats. It mirrors the
// WriteGraphML signature exactly so it slots into the exporter family. The
// NodesSkipped / EdgesSkipped fields count the []Dropped entries by kind, so a
// caller's log line can report the full input→output accounting.
//
// This is the only function in the file that touches an io.Writer; all mapping
// logic lives in the pure core above. Logging is left to the CLI/Action layer
// (see cmd/gortex/export_understand.go) so the library stays log-free.
func WriteUnderstandAnything(w io.Writer, g graph.Store, opts UAOptions) (Stats, error) {
	cw := &countingWriter{w: w}
	ua, dropped := ToUnderstandAnything(g, opts)

	stats := Stats{
		NodesWritten: len(ua.Nodes),
		EdgesWritten: len(ua.Edges),
	}
	for _, d := range dropped {
		// Dropped IDs containing "->" are edges (From->To); the rest nodes.
		if isEdgeDropID(d.ID) {
			stats.EdgesSkipped++
		} else {
			stats.NodesSkipped++
		}
	}

	var (
		data []byte
		err  error
	)
	var payload any = ua
	if opts.Generic {
		payload = toGeneric(ua)
	}
	if opts.Pretty {
		data, err = json.MarshalIndent(payload, "", "  ")
	} else {
		data, err = json.Marshal(payload)
	}
	if err != nil {
		return stats, err
	}
	if _, err := cw.Write(data); err != nil {
		return stats, err
	}
	stats.BytesWritten = cw.n
	return stats, nil
}

// isEdgeDropID reports whether a Dropped.ID names an edge (From->To) rather
// than a node. Node IDs can themselves contain arbitrary characters, but the
// "->" join is reserved by buildUAGraph for edge drop records.
func isEdgeDropID(id string) bool {
	for i := 0; i+1 < len(id); i++ {
		if id[i] == '-' && id[i+1] == '>' {
			return true
		}
	}
	return false
}
