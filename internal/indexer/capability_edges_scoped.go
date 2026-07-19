package indexer

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// synthesizeCapabilityEdgesForFiles is the exact-file partial counterpart to
// the global capability pass. It reconciles changed sources and only the
// transitive receiver callers whose indirect-mutation truth depends on them.
// The caller holds ResolveMutex.
func synthesizeCapabilityEdgesForFiles(
	g graph.Store,
	changedFiles []string,
) (readsEnv, execProc, fieldAccess int) {
	files := make([]string, 0, len(changedFiles))
	seenFiles := make(map[string]struct{}, len(changedFiles))
	for _, file := range changedFiles {
		if file == "" {
			continue
		}
		if _, seen := seenFiles[file]; seen {
			continue
		}
		seenFiles[file] = struct{}{}
		files = append(files, file)
	}
	if len(files) == 0 {
		return 0, 0, 0
	}

	fileNodes := g.GetFileNodesByPaths(files)
	changedNodes := make([]*graph.Node, 0)
	seedMethods := make([]*graph.Node, 0)
	for _, file := range files {
		for _, node := range fileNodes[file] {
			if node == nil {
				continue
			}
			changedNodes = append(changedNodes, node)
			if node.Kind == graph.KindMethod {
				seedMethods = append(seedMethods, node)
			}
		}
	}
	if len(changedNodes) == 0 {
		return 0, 0, 0
	}
	indirect, impactedMethods := indirectMutationEdgesForMethods(g, seedMethods)

	sourceSet := make(map[string]struct{}, len(changedNodes)+len(impactedMethods))
	sourceIDs := make([]string, 0, len(changedNodes)+len(impactedMethods))
	for _, node := range changedNodes {
		if _, seen := sourceSet[node.ID]; seen {
			continue
		}
		sourceSet[node.ID] = struct{}{}
		sourceIDs = append(sourceIDs, node.ID)
	}
	for id := range impactedMethods {
		if _, seen := sourceSet[id]; seen {
			continue
		}
		sourceSet[id] = struct{}{}
		sourceIDs = append(sourceIDs, id)
	}
	adjacency := g.GetOutEdgesByNodeIDs(sourceIDs)

	fieldTargetIDs := make([]string, 0)
	seenFieldTargets := make(map[string]struct{})
	for _, source := range sourceIDs {
		for _, edge := range adjacency[source] {
			if edge == nil || (edge.Kind != graph.EdgeReads && edge.Kind != graph.EdgeWrites) {
				continue
			}
			if _, seen := seenFieldTargets[edge.To]; seen {
				continue
			}
			seenFieldTargets[edge.To] = struct{}{}
			fieldTargetIDs = append(fieldTargetIDs, edge.To)
		}
	}
	fieldTargets := g.GetNodesByIDs(fieldTargetIDs)

	type edgeSpec struct {
		from, to, origin, file string
		line                   int
		kind                   graph.EdgeKind
		meta                   map[string]any
	}
	pending := make([]edgeSpec, 0)
	seen := make(map[string]bool)
	add := func(from, to string, kind graph.EdgeKind, origin, file string, line int, meta map[string]any) bool {
		key := string(kind) + "\x00" + from + "\x00" + to
		if via, _ := meta["via"].(string); via != "" {
			key += "\x00" + via
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		pending = append(pending, edgeSpec{
			from: from, to: to, kind: kind, origin: origin, file: file, line: line, meta: meta,
		})
		return true
	}
	procNodes := make(map[string]*graph.Node)
	for _, source := range sourceIDs {
		for _, edge := range adjacency[source] {
			if edge == nil {
				continue
			}
			switch edge.Kind {
			case graph.EdgeReadsConfig:
				if strings.Contains(edge.To, "cfg::env::") && add(
					edge.From, edge.To, graph.EdgeReadsEnv, graph.OriginASTResolved,
					edge.FilePath, edge.Line, nil,
				) {
					readsEnv++
				}
			case graph.EdgeReads, graph.EdgeWrites:
				field := fieldTargets[edge.To]
				if field == nil || field.Kind != graph.KindField {
					continue
				}
				access := "read"
				if edge.Kind == graph.EdgeWrites {
					access = "write"
				}
				if add(edge.From, edge.To, graph.EdgeAccessesField, graph.OriginASTResolved,
					edge.FilePath, edge.Line, map[string]any{"access": access}) {
					fieldAccess++
				}
			case graph.EdgeCalls:
				mechanism := processExecMechanism(edge.To)
				if mechanism == "" {
					continue
				}
				procID := "string::process::" + mechanism
				if procNodes[procID] == nil {
					procNodes[procID] = &graph.Node{
						ID: procID, Kind: graph.KindString, Name: mechanism,
						Meta: map[string]any{"context": "process", "mechanism": mechanism},
					}
				}
				if add(edge.From, procID, graph.EdgeExecutesProcess, graph.OriginASTInferred,
					edge.FilePath, edge.Line, nil) {
					execProc++
				}
			}
		}
	}
	for _, spec := range indirect {
		if add(spec.from, spec.to, graph.EdgeAccessesField, graph.OriginASTInferred,
			spec.file, spec.line, map[string]any{
				"access": "write", "indirect": true, "via": spec.via,
			}) {
			fieldAccess++
		}
	}

	_, supported, err := graph.EvictEdgesFromSourcesByKindsBackground(g, sourceIDs, []graph.EdgeKind{
		graph.EdgeReadsEnv, graph.EdgeExecutesProcess, graph.EdgeAccessesField,
	})
	if err != nil || !supported {
		return 0, 0, 0
	}
	nodes := make([]*graph.Node, 0, len(procNodes))
	for _, node := range procNodes {
		nodes = append(nodes, node)
	}
	edges := make([]*graph.Edge, 0, len(pending))
	for _, spec := range pending {
		edges = append(edges, &graph.Edge{
			From: spec.from, To: spec.to, Kind: spec.kind,
			FilePath: spec.file, Line: spec.line, Origin: spec.origin, Meta: spec.meta,
		})
	}
	g.AddBatch(nodes, edges)
	return readsEnv, execProc, fieldAccess
}
