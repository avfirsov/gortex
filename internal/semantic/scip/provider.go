package scip

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/platform"
	"github.com/zzet/gortex/internal/semantic"
)

// Provider runs a SCIP indexer and imports the results into the graph.
type Provider struct {
	command   string
	args      []string
	languages []string
	logger    *zap.Logger
	// definitionsOnly runs the fast path: ingest only definition
	// occurrences (the symbol map + coverage) and skip the expensive
	// reference-edge pass. Used for the C# / .NET coverage helper,
	// where compiler-grade symbol coverage is wanted without paying
	// for full reference resolution.
	definitionsOnly bool
}

var _ semantic.ContextEnricher = (*Provider)(nil)
var _ semantic.PreselectionDeadlineEnricher = (*Provider)(nil)

// NewProvider creates a SCIP provider for the given command and languages.
func NewProvider(command string, args []string, languages []string, _ int, logger *zap.Logger) *Provider {
	return &Provider{
		command:   command,
		args:      args,
		languages: languages,
		logger:    logger,
	}
}

// WithDefinitionsOnly enables the definitions-only fast path: the
// provider ingests only definition occurrences (symbol map + coverage)
// and skips the reference-edge pass. Builder-style.
func (p *Provider) WithDefinitionsOnly() *Provider {
	p.definitionsOnly = true
	return p
}

func (p *Provider) Name() string        { return "scip-" + p.languages[0] }
func (p *Provider) Languages() []string { return p.languages }
func (p *Provider) Close() error        { return nil }

func (p *Provider) UsePreselectionDeadline() {}

func (p *Provider) Available() bool {
	_, err := exec.LookPath(p.command)
	return err == nil
}

func (p *Provider) Enrich(g graph.Store, repoRoot string) (*semantic.EnrichResult, error) {
	return p.EnrichRepoContext(context.Background(), g, "", repoRoot, nil)
}

func (p *Provider) EnrichRepo(g graph.Store, repoPrefix, repoRoot string) (*semantic.EnrichResult, error) {
	return p.EnrichRepoContext(context.Background(), g, repoPrefix, repoRoot, nil)
}

// EnrichRepoContext makes both the external indexer and the in-process import
// part of the Manager-owned lifecycle. CommandContext terminates the child on
// cancellation; the graph import checks ctx before it begins mutating.
func (p *Provider) EnrichRepoContext(ctx context.Context, g graph.Store, repoPrefix, repoRoot string, _ semantic.EnrichDeadlinePolicy) (*semantic.EnrichResult, error) {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}

	// Run the SCIP indexer.
	indexFile, err := p.runIndexerContext(ctx, repoRoot)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return scipPartialResult(p, start, ctxErr), nil
		}
		return nil, fmt.Errorf("scip indexer failed: %w", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(indexFile)) }()

	// Parse the SCIP index.
	index, err := ParseSCIPFile(indexFile)
	if err != nil {
		return nil, fmt.Errorf("scip parse failed: %w", err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return scipPartialResult(p, start, ctxErr), nil
	}

	// Build symbol map and enrich the graph.
	result := p.enrichFromIndexScoped(g, index, repoPrefix, repoRoot)
	result.Provider = p.Name()
	result.Language = p.languages[0]
	result.DurationMs = time.Since(start).Milliseconds()

	return result, nil
}

func scipPartialResult(p *Provider, start time.Time, err error) *semantic.EnrichResult {
	result := &semantic.EnrichResult{
		Provider:    p.Name(),
		Language:    p.languages[0],
		DurationMs:  time.Since(start).Milliseconds(),
		Partial:     true,
		AbortReason: err.Error(),
		BoundReason: semantic.EnrichBoundBudget,
	}
	return result
}

func (p *Provider) EnrichFile(g graph.Store, repoRoot, filePath string) (*semantic.EnrichResult, error) {
	// SCIP doesn't support incremental indexing well — re-run full enrichment.
	// For large repos, this should be gated by the watch debounce.
	return nil, nil
}

func (p *Provider) runIndexerContext(ctx context.Context, repoRoot string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "gortex-scip-*")
	if err != nil {
		return "", err
	}

	outputPath := filepath.Join(tmpDir, "index.scip")

	args := make([]string, len(p.args))
	copy(args, p.args)
	args = append(args, "--output", outputPath)

	cmd := exec.CommandContext(ctx, p.command, args...)
	platform.ConfigureBackgroundCommand(cmd)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "SCIP_OUTPUT="+outputPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("%s failed: %w\noutput: %s", p.command, err, string(output))
	}

	// Check if the output file exists.
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		// Some SCIP indexers use different output conventions.
		// Try common alternatives.
		alternatives := []string{
			filepath.Join(repoRoot, "index.scip"),
			filepath.Join(repoRoot, "dump.scip"),
		}
		for _, alt := range alternatives {
			if _, err := os.Stat(alt); err == nil {
				// Move to our tmp dir.
				data, err := os.ReadFile(alt)
				if err == nil {
					_ = os.WriteFile(outputPath, data, 0644)
					_ = os.Remove(alt)
					return outputPath, nil
				}
			}
		}
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("scip output not found at %s", outputPath)
	}

	return outputPath, nil
}

// enrichFromIndex maps SCIP data to the Gortex graph.
func (p *Provider) enrichFromIndex(g graph.Store, index *SCIPIndex, repoRoot string) *semantic.EnrichResult {
	return p.enrichFromIndexScoped(g, index, "", repoRoot)
}

func (p *Provider) enrichFromIndexScoped(g graph.Store, index *SCIPIndex, repoPrefix, repoRoot string) *semantic.EnrichResult {
	result := &semantic.EnrichResult{}
	symMap := semantic.NewSymbolMap()
	nodesByFile, nodesByID := scipNodesForDocuments(g, index, repoPrefix, p.languages)

	// Phase 1: Build symbol mapping from definitions.
	for _, doc := range index.Documents {
		relPath := scipGraphPath(repoPrefix, doc.RelativePath)
		for _, occ := range doc.Occurrences {
			if !occ.IsDefinition() {
				continue
			}
			line := occ.StartLine()
			node := matchSCIPNodeByLine(nodesByFile[relPath], line, true)
			if node == nil {
				// Try by name.
				symName := extractSymbolName(occ.Symbol)
				if symName != "" {
					node = matchSCIPNodeByName(nodesByFile[relPath], symName)
				}
			}
			if node != nil {
				symMap.Add(occ.Symbol, node.ID)
				result.SymbolsCovered++
			}
		}
	}

	// Count total symbols through language predicates. A provider language list
	// is tiny, and each store evaluates it without materializing unrelated nodes.
	seenLanguages := make(map[string]struct{}, len(p.languages))
	for _, language := range p.languages {
		if language == "" {
			continue
		}
		if _, duplicate := seenLanguages[language]; duplicate {
			continue
		}
		seenLanguages[language] = struct{}{}
		for _, n := range g.GetRepoNodesByLanguage(repoPrefix, language) {
			if n != nil && n.Kind != graph.KindFile && n.Kind != graph.KindImport {
				result.SymbolsTotal++
			}
		}
	}

	if result.SymbolsTotal > 0 {
		result.CoveragePercent = float64(result.SymbolsCovered) / float64(result.SymbolsTotal) * 100
	}

	// Definitions-only fast path: the symbol map + coverage are done;
	// skip the per-reference node lookup and edge resolution.
	if p.definitionsOnly {
		if p.logger != nil {
			p.logger.Debug("scip: definitions-only fast path; skipping reference pass",
				zap.Int("symbols_covered", result.SymbolsCovered))
		}
		return result
	}

	type referenceCandidate struct {
		refNode   *graph.Node
		defNodeID string
		filePath  string
		line      int
	}
	var references []referenceCandidate
	type implementationCandidate struct {
		implID  string
		ifaceID string
	}
	var implementations []implementationCandidate
	var endpoints []graph.EdgeEndpoint

	// Collect exact reference and implementation endpoint frontiers first. One
	// predicate-shaped lookup below replaces point adjacency reads per SCIP
	// occurrence and per relationship.
	for _, doc := range index.Documents {
		relPath := scipGraphPath(repoPrefix, doc.RelativePath)
		for _, occ := range doc.Occurrences {
			if occ.IsDefinition() {
				continue
			}

			// Find the Gortex node at the reference site.
			refLine := occ.StartLine()
			refNode := matchSCIPNodeByLine(nodesByFile[relPath], refLine, false)
			if refNode == nil {
				continue
			}

			// Find the Gortex node for the definition being referenced.
			defNodeID, ok := symMap.GortexID(occ.Symbol)
			if !ok {
				continue
			}
			references = append(references, referenceCandidate{refNode: refNode, defNodeID: defNodeID, filePath: relPath, line: refLine})
			endpoints = append(endpoints, graph.EdgeEndpoint{From: refNode.ID, To: defNodeID})
		}
	}

	for _, doc := range index.Documents {
		for _, sym := range doc.Symbols {
			for _, rel := range sym.Relationships {
				if !rel.IsImplementation {
					continue
				}
				implID, ok := symMap.GortexID(sym.Symbol)
				if !ok {
					continue
				}
				ifaceID, ok := symMap.GortexID(rel.Symbol)
				if !ok {
					continue
				}
				implementations = append(implementations, implementationCandidate{implID: implID, ifaceID: ifaceID})
				endpoints = append(endpoints, graph.EdgeEndpoint{From: implID, To: ifaceID})
			}
		}
	}

	candidates := graph.LookupEdgeCandidates(g, endpoints, nil)
	var confirmedEdges []*graph.Edge
	var addedEdges []*graph.Edge

	// Phase 2: Process reference occurrences — confirm/add edges.
	for _, ref := range references {
		existing := candidates.Endpoint(ref.refNode.ID, ref.defNodeID)
		if existing != nil {
			if existing.Confidence < 1.0 {
				semantic.ConfirmEdge(existing, p.Name())
				confirmedEdges = append(confirmedEdges, existing)
				result.EdgesConfirmed++
			}
		} else {
			// Determine edge kind from context.
			kind := inferEdgeKind(ref.refNode, nodesByID[ref.defNodeID])
			if kind != "" {
				edge := semantic.NewSemanticEdge(ref.refNode.ID, ref.defNodeID, kind, ref.filePath, ref.line, p.Name())
				addedEdges = append(addedEdges, edge)
				candidates.Add(edge)
				result.EdgesAdded++
			}
		}
	}

	// Phase 3: Process implementation relationships.
	for _, implementation := range implementations {
		existing := candidates.EndpointKind(implementation.implID, implementation.ifaceID, graph.EdgeImplements)
		if existing != nil {
			semantic.ConfirmEdge(existing, p.Name())
			confirmedEdges = append(confirmedEdges, existing)
			result.EdgesConfirmed++
		} else if implNode := nodesByID[implementation.implID]; implNode != nil {
			edge := semantic.NewSemanticEdge(implementation.implID, implementation.ifaceID, graph.EdgeImplements,
				implNode.FilePath, implNode.StartLine, p.Name())
			addedEdges = append(addedEdges, edge)
			candidates.Add(edge)
			result.EdgesAdded++
		}
	}

	// Phase 4: Enrich node metadata from symbol documentation.
	// Collect stamped nodes and round-trip them through the store at the
	// end — EnrichNodeMeta mutates Node.Meta in place, which does not
	// persist on disk backends (GetNode returns a per-call copy). See
	// semantic.EnrichNodeMeta.
	stampedNodes := make(map[string]*graph.Node)
	for _, doc := range index.Documents {
		for _, sym := range doc.Symbols {
			nodeID, ok := symMap.GortexID(sym.Symbol)
			if !ok {
				continue
			}
			node := nodesByID[nodeID]
			if node == nil {
				continue
			}

			if len(sym.Documentation) > 0 {
				// Parse type info from hover documentation.
				typeInfo := extractTypeFromDocs(sym.Documentation)
				if typeInfo != "" {
					semantic.EnrichNodeMeta(node, "semantic_type", typeInfo, p.Name())
					result.NodesEnriched++
					stampedNodes[node.ID] = node
				}
			}
		}
	}
	nodeBatch := make([]*graph.Node, 0, len(stampedNodes))
	for _, node := range stampedNodes {
		nodeBatch = append(nodeBatch, node)
	}
	if len(nodeBatch) > 0 || len(addedEdges) > 0 {
		g.AddBatch(nodeBatch, addedEdges)
	}
	if len(confirmedEdges) > 0 {
		if persister, ok := g.(graph.EdgeMetaBatchPersister); ok {
			persister.PersistEdgeAttributesBatch(confirmedEdges)
		} else {
			// Core in-memory edges are live pointers. Adapter stores without the
			// optional attribute capability retain compatibility through one
			// batched upsert, never a persistence call per occurrence.
			g.AddBatch(nil, confirmedEdges)
		}
	}

	return result
}

var scipMatchNodeKinds = []graph.NodeKind{
	graph.KindPackage, graph.KindFunction, graph.KindMethod, graph.KindType,
	graph.KindInterface, graph.KindVariable, graph.KindContract, graph.KindField,
	graph.KindParam, graph.KindClosure, graph.KindLocal, graph.KindBuiltin,
	graph.KindConstant, graph.KindEnumMember, graph.KindGenericParam, graph.KindModule,
	graph.KindTable, graph.KindColumn, graph.KindConfigKey, graph.KindFlag,
	graph.KindEvent, graph.KindMigration, graph.KindFixture, graph.KindTodo,
	graph.KindTeam, graph.KindRelease, graph.KindLicense, graph.KindString,
	graph.KindResource, graph.KindKustomization, graph.KindImage, graph.KindArtifact,
	graph.KindDoc, graph.KindRationale, graph.KindTopic, graph.KindMacro,
	graph.KindContractBridge,
}

func scipNodesForDocuments(g graph.Store, index *SCIPIndex, repoPrefix string, languages []string) (map[string][]*graph.Node, map[string]*graph.Node) {
	fileSet := make(map[string]struct{}, len(index.Documents))
	files := make([]string, 0, len(index.Documents))
	for _, doc := range index.Documents {
		graphPath := scipGraphPath(repoPrefix, doc.RelativePath)
		if graphPath == "" {
			continue
		}
		if _, duplicate := fileSet[graphPath]; duplicate {
			continue
		}
		fileSet[graphPath] = struct{}{}
		files = append(files, graphPath)
	}
	var nodes []*graph.Node
	if finder, ok := g.(graph.NodesInFilesByKindFinder); ok {
		nodes = finder.NodesInFilesByKind(files, scipMatchNodeKinds)
	} else {
		seenLanguages := make(map[string]struct{}, len(languages))
		for _, language := range languages {
			if language == "" {
				continue
			}
			if _, duplicate := seenLanguages[language]; duplicate {
				continue
			}
			seenLanguages[language] = struct{}{}
			for _, node := range g.GetNodesByLanguage(language) {
				if node != nil {
					if _, wanted := fileSet[node.FilePath]; wanted {
						nodes = append(nodes, node)
					}
				}
			}
		}
	}
	byFile := make(map[string][]*graph.Node, len(files))
	byID := make(map[string]*graph.Node, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		byFile[node.FilePath] = append(byFile[node.FilePath], node)
		byID[node.ID] = node
	}
	return byFile, byID
}

func scipGraphPath(repoPrefix, relativePath string) string {
	path := filepath.ToSlash(filepath.Clean(relativePath))
	if path == "." || path == "" {
		return ""
	}
	if repoPrefix == "" || path == repoPrefix || strings.HasPrefix(path, repoPrefix+"/") {
		return path
	}
	return repoPrefix + "/" + strings.TrimPrefix(path, "/")
}

func matchSCIPNodeByLine(nodes []*graph.Node, line int, nearest bool) *graph.Node {
	var best *graph.Node
	bestSize := int(^uint(0) >> 1)
	for _, n := range nodes {
		if n.Kind == graph.KindFile || n.Kind == graph.KindImport {
			continue
		}
		if n.StartLine <= line && line <= n.EndLine {
			size := n.EndLine - n.StartLine
			if size < bestSize {
				best = n
				bestSize = size
			}
		}
	}
	if best != nil || !nearest {
		return best
	}
	bestDistance := int(^uint(0) >> 1)
	for _, node := range nodes {
		if node == nil || node.Kind == graph.KindFile || node.Kind == graph.KindImport {
			continue
		}
		distance := node.StartLine - line
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			best = node
			bestDistance = distance
		}
	}
	if bestDistance > 2 {
		return nil
	}
	return best
}

func matchSCIPNodeByName(nodes []*graph.Node, name string) *graph.Node {
	for _, node := range nodes {
		if node != nil && node.Name == name {
			return node
		}
	}
	return nil
}

// inferEdgeKind determines the edge kind from the node types.
func inferEdgeKind(from, to *graph.Node) graph.EdgeKind {
	if to == nil {
		return ""
	}
	switch to.Kind {
	case graph.KindFunction, graph.KindMethod:
		return graph.EdgeCalls
	case graph.KindType, graph.KindInterface:
		if from.Kind == graph.KindFunction || from.Kind == graph.KindMethod {
			return graph.EdgeReferences
		}
		return graph.EdgeReferences
	default:
		return graph.EdgeReferences
	}
}

// extractSymbolName extracts the short symbol name from a SCIP symbol URI.
// SCIP symbols look like: "scip-go gomod github.com/foo/bar v1.0.0 pkg/Foo.Bar()."
func extractSymbolName(symbol string) string {
	parts := strings.Fields(symbol)
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	// Remove trailing punctuation.
	last = strings.TrimRight(last, "().")
	// Extract the last component after '/'.
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	// Extract after '.' or '#'.
	if idx := strings.LastIndex(last, "."); idx >= 0 {
		last = last[idx+1:]
	}
	if idx := strings.LastIndex(last, "#"); idx >= 0 {
		last = last[idx+1:]
	}
	return last
}

// extractTypeFromDocs extracts type information from SCIP documentation strings.
func extractTypeFromDocs(docs []string) string {
	for _, doc := range docs {
		// SCIP documentation often contains the type signature as the first line.
		lines := strings.SplitN(doc, "\n", 2)
		if len(lines) > 0 {
			line := strings.TrimSpace(lines[0])
			// Look for Go-style type signatures.
			if strings.HasPrefix(line, "func ") ||
				strings.HasPrefix(line, "type ") ||
				strings.HasPrefix(line, "var ") ||
				strings.HasPrefix(line, "const ") {
				return line
			}
			// Look for type annotations like "string", "int", "*Foo".
			if !strings.Contains(line, " ") && len(line) > 0 && len(line) < 100 {
				return line
			}
		}
	}
	return ""
}

// SCIPIndex represents a parsed SCIP index.
type SCIPIndex struct {
	Documents       []SCIPDocument   `json:"documents"`
	ExternalSymbols []SCIPSymbolInfo `json:"external_symbols"`
}

// SCIPDocument represents a single file in the SCIP index.
type SCIPDocument struct {
	RelativePath string           `json:"relative_path"`
	Occurrences  []SCIPOccurrence `json:"occurrences"`
	Symbols      []SCIPSymbolInfo `json:"symbols"`
}

// SCIPOccurrence represents a symbol occurrence in a document.
type SCIPOccurrence struct {
	Range       []int32 `json:"range"`
	Symbol      string  `json:"symbol"`
	SymbolRoles int32   `json:"symbol_roles"`
}

// IsDefinition returns true if this occurrence is a definition.
func (o *SCIPOccurrence) IsDefinition() bool {
	return o.SymbolRoles&1 != 0 // SymbolRole_Definition = 1
}

// StartLine returns the 1-indexed start line of the occurrence.
func (o *SCIPOccurrence) StartLine() int {
	if len(o.Range) >= 1 {
		return int(o.Range[0]) + 1 // SCIP uses 0-indexed lines
	}
	return 0
}

// SCIPSymbolInfo holds information about a symbol.
type SCIPSymbolInfo struct {
	Symbol        string             `json:"symbol"`
	Documentation []string           `json:"documentation"`
	Relationships []SCIPRelationship `json:"relationships"`
}

// SCIPRelationship describes a relationship between symbols.
type SCIPRelationship struct {
	Symbol           string `json:"symbol"`
	IsImplementation bool   `json:"is_implementation"`
	IsReference      bool   `json:"is_reference"`
	IsTypeDefinition bool   `json:"is_type_definition"`
}

// ParseSCIPFile reads and parses a SCIP index file.
// SCIP uses Protocol Buffers, but we support JSON-encoded SCIP for simplicity.
// For production use with protobuf, this would use the scip protobuf schema.
func ParseSCIPFile(path string) (*SCIPIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try JSON first (for testing and compatibility).
	var index SCIPIndex
	if err := json.Unmarshal(data, &index); err == nil && len(index.Documents) > 0 {
		return &index, nil
	}

	// Try protobuf decoding.
	idx, err := decodeSCIPProtobuf(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCIP file (tried JSON and protobuf): %w", err)
	}
	return idx, nil
}

// decodeSCIPProtobuf decodes a SCIP protobuf file into our internal types.
// This is a minimal protobuf decoder for the SCIP schema without requiring
// the full protobuf dependency.
func decodeSCIPProtobuf(data []byte) (*SCIPIndex, error) {
	// Minimal protobuf wire format decoder for SCIP Index message.
	// SCIP Index has: field 1 = Metadata, field 2 = Document[], field 3 = ExternalSymbol[]
	//
	// Document has: field 4 = relative_path, field 2 = Occurrence[], field 3 = SymbolInformation[]
	// Occurrence has: field 1 = range (repeated int32), field 2 = symbol (string), field 3 = symbol_roles (int32)
	// SymbolInformation has: field 1 = symbol (string), field 3 = documentation (repeated string), field 4 = Relationship[]
	// Relationship has: field 1 = symbol (string), field 2 = is_implementation (bool)

	index := &SCIPIndex{}

	reader := &protoReader{data: data}
	for reader.hasMore() {
		fieldNum, wireType, err := reader.readTag()
		if err != nil {
			return nil, err
		}

		switch fieldNum {
		case 1: // Metadata — skip
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		case 2: // Document
			docData, err := reader.readBytes(wireType)
			if err != nil {
				return nil, err
			}
			doc, err := decodeSCIPDocument(docData)
			if err != nil {
				return nil, err
			}
			index.Documents = append(index.Documents, *doc)
		case 3: // ExternalSymbol
			symData, err := reader.readBytes(wireType)
			if err != nil {
				return nil, err
			}
			sym, err := decodeSCIPSymbolInfo(symData)
			if err != nil {
				return nil, err
			}
			index.ExternalSymbols = append(index.ExternalSymbols, *sym)
		default:
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		}
	}

	return index, nil
}

func decodeSCIPDocument(data []byte) (*SCIPDocument, error) {
	doc := &SCIPDocument{}
	reader := &protoReader{data: data}

	for reader.hasMore() {
		fieldNum, wireType, err := reader.readTag()
		if err != nil {
			return nil, err
		}

		switch fieldNum {
		case 4: // relative_path (string)
			s, err := reader.readString(wireType)
			if err != nil {
				return nil, err
			}
			doc.RelativePath = s
		case 2: // occurrence (message)
			occData, err := reader.readBytes(wireType)
			if err != nil {
				return nil, err
			}
			occ, err := decodeSCIPOccurrence(occData)
			if err != nil {
				return nil, err
			}
			doc.Occurrences = append(doc.Occurrences, *occ)
		case 3: // symbol (SymbolInformation message)
			symData, err := reader.readBytes(wireType)
			if err != nil {
				return nil, err
			}
			sym, err := decodeSCIPSymbolInfo(symData)
			if err != nil {
				return nil, err
			}
			doc.Symbols = append(doc.Symbols, *sym)
		default:
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		}
	}

	return doc, nil
}

func decodeSCIPOccurrence(data []byte) (*SCIPOccurrence, error) {
	occ := &SCIPOccurrence{}
	reader := &protoReader{data: data}

	for reader.hasMore() {
		fieldNum, wireType, err := reader.readTag()
		if err != nil {
			return nil, err
		}

		switch fieldNum {
		case 1: // range (repeated int32, packed)
			if wireType == 2 { // length-delimited (packed)
				rangeData, err := reader.readBytes(wireType)
				if err != nil {
					return nil, err
				}
				rr := &protoReader{data: rangeData}
				for rr.hasMore() {
					v, err := rr.readVarint()
					if err != nil {
						return nil, err
					}
					occ.Range = append(occ.Range, int32(v))
				}
			} else { // varint (non-packed, repeated)
				v, err := reader.readVarint()
				if err != nil {
					return nil, err
				}
				occ.Range = append(occ.Range, int32(v))
			}
		case 2: // symbol (string)
			s, err := reader.readString(wireType)
			if err != nil {
				return nil, err
			}
			occ.Symbol = s
		case 3: // symbol_roles (int32)
			v, err := reader.readVarint()
			if err != nil {
				return nil, err
			}
			occ.SymbolRoles = int32(v)
		default:
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		}
	}

	return occ, nil
}

func decodeSCIPSymbolInfo(data []byte) (*SCIPSymbolInfo, error) {
	sym := &SCIPSymbolInfo{}
	reader := &protoReader{data: data}

	for reader.hasMore() {
		fieldNum, wireType, err := reader.readTag()
		if err != nil {
			return nil, err
		}

		switch fieldNum {
		case 1: // symbol (string)
			s, err := reader.readString(wireType)
			if err != nil {
				return nil, err
			}
			sym.Symbol = s
		case 3: // documentation (repeated string)
			s, err := reader.readString(wireType)
			if err != nil {
				return nil, err
			}
			sym.Documentation = append(sym.Documentation, s)
		case 4: // relationship (message)
			relData, err := reader.readBytes(wireType)
			if err != nil {
				return nil, err
			}
			rel, err := decodeSCIPRelationship(relData)
			if err != nil {
				return nil, err
			}
			sym.Relationships = append(sym.Relationships, *rel)
		default:
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		}
	}

	return sym, nil
}

func decodeSCIPRelationship(data []byte) (*SCIPRelationship, error) {
	rel := &SCIPRelationship{}
	reader := &protoReader{data: data}

	for reader.hasMore() {
		fieldNum, wireType, err := reader.readTag()
		if err != nil {
			return nil, err
		}

		switch fieldNum {
		case 1: // symbol (string)
			s, err := reader.readString(wireType)
			if err != nil {
				return nil, err
			}
			rel.Symbol = s
		case 2: // is_implementation (bool)
			v, err := reader.readVarint()
			if err != nil {
				return nil, err
			}
			rel.IsImplementation = v != 0
		case 3: // is_reference (bool)
			v, err := reader.readVarint()
			if err != nil {
				return nil, err
			}
			rel.IsReference = v != 0
		case 4: // is_type_definition (bool)
			v, err := reader.readVarint()
			if err != nil {
				return nil, err
			}
			rel.IsTypeDefinition = v != 0
		default:
			if err := reader.skipField(wireType); err != nil {
				return nil, err
			}
		}
	}

	return rel, nil
}
