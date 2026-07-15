package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/elide"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
	"github.com/zzet/gortex/internal/search/rerank"
)

// exploreToolDescription is the one-shot localization verb's advertised
// contract — engineered to be the obvious opening move for any
// task-shaped request. It promises the whole first exploration phase in
// a single call so the agent never decomposes localization into a string
// of granular search/read/callers turns (the measured turn-economy loss).
const exploreToolDescription = "Start here for any task, bug report, or " +
	"\"where is / how does X work\" question. Describe the request in plain " +
	"words (paste the issue, name the area) and get the localized neighborhood " +
	"in ONE call: the ranked likely-involved symbols with their source and call " +
	"paths (callers + callees), plus the files to change — the whole exploration " +
	"phase (5-10 search/read/callers calls) folded into one. Answer or edit " +
	"straight from it; it states when the neighborhood is complete."

// explore tuning. These are generic retrieval parameters — fan-out
// widths and a token ceiling — with no dependence on any particular
// corpus, query vocabulary, or benchmark. The verb takes arbitrary free
// text; nothing here is derived from a fixed task set.
const (
	exploreDefaultBudgetTokens = 1600
	exploreMinBudgetTokens     = 1000
	exploreMaxBudgetTokens     = 24000
	exploreDefaultMaxSymbols   = 10
	exploreMaxMaxSymbols       = 30
	exploreRingCap             = 5 // callers / callees shown per target
	exploreCharsPerToken       = 4 // coarse token estimate for budgeting
	// Full bodies are the largest repeated-context cost. Candidate headers,
	// locations, signatures, and graph rings preserve recall for the whole
	// neighborhood; only the top targets need full source to make the first
	// response answer-ready.
	exploreFullBodyLimit = 2
	// exploreBodyBudgetShare caps any single full body at this fraction of
	// the total budget, so one huge top-ranked symbol cannot starve the
	// rest of the neighborhood of their bodies.
	exploreBodyBudgetShare = 3
)

// registerExploreTool wires the one-shot localization verb into the tool
// surface. It ships eagerly in the coding-agent + core presets (see the
// preset roster in tool_presets.go) so it is the first thing a task-shaped
// session reaches for.
func (s *Server) registerExploreTool() {
	s.addTool(
		mcp.NewTool("explore",
			mcp.WithDescription(exploreToolDescription),
			mcp.WithString("task", mcp.Required(), mcp.Description("Natural-language description of the task, bug report, or question to localize (e.g. paste an issue body, or 'the retry backoff never triggers on a 429').")),
			mcp.WithNumber("max_symbols", mcp.Description("Max ranked candidate symbols (default 10).")),
			mcp.WithNumber("token_budget", mcp.Description("Approximate source-packing budget in tokens (default 1600). Candidate locations and signatures are always listed and may exceed it; full source is reserved for the highest-ranked targets.")),
			mcp.WithString("repo", mcp.Description("Filter results to a specific repository prefix")),
			mcp.WithString("path", mcp.Description("Restrict the neighborhood to one or more sub-paths (comma-separated), anchored at the repo root — a monorepo-service slice.")),
		),
		s.handleExplore,
	)
}

// exploreTarget is one ranked candidate plus its bounded neighborhood,
// gathered before rendering so the renderer can honour the token budget.
type exploreTarget struct {
	node          *graph.Node
	score         float64
	callers       []*graph.Node
	callees       []*graph.Node
	causalCallees []exploreCausalNeighbor
	source        string // full body (may be empty for non-source kinds)
	exactContent  bool   // verified full quoted-literal hit from content_fts
}

type exploreCausalNeighbor struct {
	node *graph.Node
	hop  int
}

const (
	exploreCausalSeedLimit       = 3
	exploreCausalDepth           = 4
	exploreCausalAdmissionBudget = 10 * time.Millisecond
	exploreDraftPrimaryLimit     = 3
	exploreDraftTotalLimit       = 5
)

type exploreDraftEntry struct {
	node                *graph.Node
	evidence            string
	exact               bool
	overlap             int
	bodyOverlap         int
	rareOverlap         int
	bodyDensity         int
	generic             bool
	direct              bool
	structural          bool
	structuralShared    int
	structuralLocal     bool
	structuralCallee    bool
	structuralCrossFile bool
	structuralHop       int
	parentExact         bool
	parentOverlap       int
	parentRank          int
}

// exploreAnswerDraft puts a small, ready-to-use evidence set before the full
// neighborhood. The ranked head always leads; the remaining slots promote
// query-aligned deeper targets or graph neighbors without hiding any detailed
// candidate below.
func exploreQueryIsConceptTask(query string) bool {
	query = strings.TrimSpace(stripLeadingExploreDirective(query))
	if query == "" {
		return false
	}
	class := rerank.ClassifyQuery(query)
	lower := strings.ToLower(query)
	// Catch declaration-shaped signatures whose return type is project-specific
	// and therefore may not be recognized by the generic query classifier. A
	// call at the end of prose is not a declaration: its pre-call prefix is a
	// sentence, not a compact return-type/name pair.
	open, close := strings.Index(lower, "("), strings.LastIndex(lower, ")")
	if open > 0 && close > open {
		suffix := strings.TrimSpace(lower[close+1:])
		terminalDeclaration := suffix == "" || suffix == ";" || suffix == "{" || suffix == "const" || suffix == "noexcept" || strings.HasPrefix(suffix, "->") || strings.HasPrefix(suffix, ": ") || strings.HasPrefix(suffix, "throws ")
		prefixFields := strings.Fields(strings.TrimSpace(lower[:open]))
		if terminalDeclaration && len(prefixFields) >= 2 && len(prefixFields) <= 6 {
			previous := strings.Trim(prefixFields[len(prefixFields)-2], "*&[]<>,:;")
			switch previous {
			case "a", "an", "and", "at", "before", "by", "calls", "does", "for", "from", "how", "in", "into", "of", "on", "or", "reaches", "the", "through", "to", "via", "when", "where", "why", "with":
				// Natural-language call site; continue with concept detection.
			default:
				return false
			}
		}
	}
	if class == rerank.QueryClassConcept {
		return true
	}

	// A symbol, path, or signature embedded in a natural-language task must not
	// turn the whole task into an exact lookup. Pure lookup shapes are compact;
	// prose has several ordinary words around a bounded amount of code syntax.
	fields := strings.Fields(query)
	if len(fields) < 8 || len(query) < 48 {
		return false
	}
	for _, prefix := range []string{
		"func ", "fn ", "pub ", "async ", "unsafe ", "extern ",
		"def ", "class ", "interface ", "trait ", "type ",
		"public ", "private ", "protected ", "internal ", "static ",
		"export ", "declare ", "function ", "const ", "constexpr ",
		"void ", "bool ", "char ", "int ", "long ", "short ",
		"float ", "double ", "auto ", "unsigned ", "signed ", "std::", "@",
	} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	plain, codeShaped := 0, 0
	for _, field := range fields {
		token := strings.Trim(field, ".,;:!?\"'")
		if token == "" {
			continue
		}
		if strings.ContainsAny(token, `/\\(){}[]<>=&|*_:;`) {
			codeShaped++
			continue
		}
		plain++
	}
	return plain >= 6 && plain*2 >= len(fields) && codeShaped*2 < len(fields)
}

func exploreQueryHasCallAnchor(query, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	isIdentifierByte := func(b byte) bool {
		return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	for offset := 0; offset < len(query); {
		index := strings.Index(query[offset:], name)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !isIdentifierByte(query[index-1])
		after := index + len(name)
		for after < len(query) && (query[after] == ' ' || query[after] == '\t') {
			after++
		}
		if beforeOK && after < len(query) && query[after] == '(' {
			return true
		}
		offset = index + len(name)
	}
	return false
}

func exploreQueryHasPathSymbolAnchor(query string, n *graph.Node) bool {
	if n == nil {
		return false
	}
	normalizedPath := exploreNormalizedPath(nodeDisplayPath(n))
	name := exploreNormalizedAnchor(n.Name)
	qualName := exploreNormalizedAnchor(n.RetrievalMetadata().QualName)
	if qualName == "" || qualName == name {
		if separator := strings.LastIndex(n.ID, "::"); separator >= 0 {
			qualName = exploreNormalizedAnchor(n.ID[separator+2:])
		}
	}
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, "`'\"()[]{}<>,;")
		separator := strings.LastIndex(field, "::")
		if separator <= 0 || !strings.ContainsAny(field[:separator], "/\\") {
			continue
		}
		if exploreNormalizedPath(field[:separator]) != normalizedPath {
			continue
		}
		symbol := exploreNormalizedAnchor(field[separator+2:])
		if symbol == name || symbol == qualName || strings.HasSuffix(qualName, " "+symbol) {
			return true
		}
	}
	return false
}

func exploreAdmitCausalSeed(conceptTask, explicit, strong bool, admitted int, elapsed time.Duration) bool {
	return conceptTask && !explicit && strong && admitted < exploreCausalSeedLimit && elapsed < exploreCausalAdmissionBudget
}

func exploreStrongCausalSeed(query string, target exploreTarget) bool {
	n := target.node
	if n == nil || (n.Kind != graph.KindFunction && n.Kind != graph.KindMethod) || exploreDraftIsTestNode(n) || exploreIdentifierSegmentCount(n.Name) < 2 {
		return false
	}
	queryTerms := exploreTerminalTerms(query)
	overlap, longest := exploreDraftTermOverlap(queryTerms, n)
	bodyOverlap := exploreDraftTermSetOverlap(queryTerms, exploreTerminalTerms(target.source))
	return overlap >= 2 || bodyOverlap >= 2 || (overlap == 1 && longest >= 5)
}

// minimumExploreCausalHops derives a deterministic, bounded reachable set from
// the edges already returned by GetCallChain. It never re-queries the graph:
// the traversal cost is linear in the at-most-limit subgraph admitted above.
func minimumExploreCausalHops(seedID string, sg *query.SubGraph, scope query.QueryOptions, maxDepth, limit int) []exploreCausalNeighbor {
	if seedID == "" || sg == nil || maxDepth < 1 || limit < 1 {
		return nil
	}
	nodes := make(map[string]*graph.Node, len(sg.Nodes))
	for _, n := range sg.Nodes {
		if n != nil && n.ID != "" {
			nodes[n.ID] = n
		}
	}
	adjacent := make(map[string][]string, len(sg.Edges))
	for _, edge := range sg.Edges {
		if edge == nil || (edge.Kind != graph.EdgeCalls && edge.Kind != graph.EdgeMatches) || edge.From == "" || edge.To == "" {
			continue
		}
		adjacent[edge.From] = append(adjacent[edge.From], edge.To)
	}
	for from := range adjacent {
		sort.Strings(adjacent[from])
	}

	hops := map[string]int{seedID: 0}
	queue := []string{seedID}
	result := make([]exploreCausalNeighbor, 0, min(limit, len(nodes)))
	for head := 0; head < len(queue); head++ {
		from := queue[head]
		fromHop := hops[from]
		if fromHop >= maxDepth {
			continue
		}
		for _, to := range adjacent[from] {
			if _, seen := hops[to]; seen {
				continue
			}
			n := nodes[to]
			if n == nil || !exploreNodeWithinQueryScope(n, scope) {
				continue
			}
			hop := fromHop + 1
			hops[to] = hop
			queue = append(queue, to)
			if exploreDraftIsTestNode(n) || (n.Kind != graph.KindFunction && n.Kind != graph.KindMethod) {
				continue
			}
			result = append(result, exploreCausalNeighbor{node: n, hop: hop})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].hop != result[j].hop {
			return result[i].hop < result[j].hop
		}
		return exploreDraftNodeKey(result[i].node) < exploreDraftNodeKey(result[j].node)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func exploreAnswerDraft(task string, targets []exploreTarget) []exploreDraftEntry {
	query := shapeExploreQuery(task)
	conceptTask := exploreQueryIsConceptTask(query)
	if conceptTask {
		query = stripLeadingExploreDirective(query)
	}
	queryTerms := exploreTerminalTerms(query)

	// Cache body term sets once per direct candidate. The bounded frequency
	// table supplies a small inverse-frequency/density tie-break without
	// repeatedly tokenizing source or allowing body prose to dominate names.
	bodyTermCache := make(map[string]map[string]struct{}, len(targets))
	candidateTermCache := make(map[string]map[string]struct{}, len(targets))
	termFrequency := make(map[string]int, len(queryTerms))
	if conceptTask {
		for _, target := range targets {
			if target.node == nil {
				continue
			}
			key := exploreDraftNodeKey(target.node)
			if _, cached := candidateTermCache[key]; cached {
				continue
			}
			bodyTerms := exploreTerminalTerms(target.source)
			bodyTermCache[key] = bodyTerms
			candidateTerms := exploreDraftNodeTerms(target.node)
			for term := range bodyTerms {
				candidateTerms[term] = struct{}{}
			}
			candidateTermCache[key] = candidateTerms
			for term := range queryTerms {
				if _, matched := candidateTerms[term]; matched {
					termFrequency[term]++
				}
			}
		}
	}
	makeEntry := func(n *graph.Node, source, evidence string, direct bool, parentRank int) (exploreDraftEntry, int) {
		overlap, longest := exploreDraftTermOverlap(queryTerms, n)
		explicitAnchor := exploreLocalizationExplicitAnchor(query, n)
		exact := exploreDraftExactAnchor(query, n) || explicitAnchor
		// Concept prompts often mention a nearby test helper while asking how the
		// production path works. Do not let that lexical anchor outrank the
		// implementation. A genuinely explicit symbol/path request remains exact.
		conceptTestHelper := conceptTask && exploreDraftIsTestNode(n) && !explicitAnchor
		if conceptTestHelper {
			exact = false
		}
		bodyOverlap, rareOverlap, bodyDensity := 0, 0, 0
		if conceptTask && n != nil {
			key := exploreDraftNodeKey(n)
			bodyTerms, cached := bodyTermCache[key]
			if !cached && source != "" {
				bodyTerms = exploreTerminalTerms(source)
				bodyTermCache[key] = bodyTerms
			}
			bodyOverlap = exploreDraftTermSetOverlap(queryTerms, bodyTerms)
			if candidateTerms, cached := candidateTermCache[key]; cached {
				for term := range queryTerms {
					if termFrequency[term] == 1 {
						if _, matched := candidateTerms[term]; matched && rareOverlap < 2 {
							rareOverlap++
						}
					}
				}
			}
			denominator := len(bodyTerms)
			if denominator > 12 {
				denominator = 12
			}
			if denominator > 0 && bodyOverlap >= 2 {
				switch {
				case bodyOverlap*2 >= denominator:
					bodyDensity = 2
				case bodyOverlap*4 >= denominator:
					bodyDensity = 1
				}
			}
		}
		return exploreDraftEntry{
			node:        n,
			evidence:    evidence,
			exact:       exact,
			overlap:     overlap,
			bodyOverlap: bodyOverlap,
			rareOverlap: rareOverlap,
			bodyDensity: bodyDensity,
			generic:     conceptTask && !exact && (conceptTestHelper || exploreDraftGenericCandidate(n, source)),
			direct:      direct,
			parentRank:  parentRank,
		}, longest
	}

	entries := make([]exploreDraftEntry, 0, exploreDraftTotalLimit)
	seen := make(map[string]struct{}, exploreDraftTotalLimit)
	appendEntry := func(entry exploreDraftEntry) bool {
		if entry.node == nil {
			return false
		}
		key := exploreDraftNodeKey(entry.node)
		if _, ok := seen[key]; ok {
			return false
		}
		seen[key] = struct{}{}
		entries = append(entries, entry)
		return true
	}

	// Preserve the retrieval head, then reserve at most one structural caller
	// and one structural callee. The global quotas keep graph expansion useful
	// without allowing a generic call neighborhood to consume the whole draft.
	for i := 0; i < len(targets) && i < exploreDraftPrimaryLimit; i++ {
		entry, _ := makeEntry(targets[i].node, targets[i].source, fmt.Sprintf("ranked #%d", i+1), true, i)
		appendEntry(entry)
	}

	var aligned, structuralNeighbors []exploreDraftEntry
	consider := func(n *graph.Node, source, evidence string, direct bool, parentRank int) {
		if n == nil || (!direct && exploreDraftIsTestNode(n)) {
			return
		}
		entry, longest := makeEntry(n, source, evidence, direct, parentRank)
		if !entry.exact && entry.bodyOverlap < 2 && entry.overlap < 2 && (entry.overlap != 1 || longest < 5) {
			return
		}
		aligned = append(aligned, entry)
	}
	for i, target := range targets {
		if i >= exploreDraftPrimaryLimit {
			consider(target.node, target.source, fmt.Sprintf("ranked #%d", i+1), true, i)
		}
		for _, n := range target.callers {
			consider(n, "", fmt.Sprintf("caller of ranked #%d", i+1), false, i)
		}
		for _, n := range target.callees {
			consider(n, "", fmt.Sprintf("callee of ranked #%d", i+1), false, i)
		}

		if target.node == nil || !conceptTask || exploreDraftIsTestNode(target.node) {
			continue
		}
		parent, longest := makeEntry(target.node, target.source, "", true, i)
		strongParent := parent.exact || parent.overlap >= 2 || parent.bodyOverlap >= 2 || (parent.overlap == 1 && longest >= 5)
		if !strongParent || (!parent.exact && exploreIdentifierSegmentCount(target.node.Name) < 2) {
			continue
		}
		collectStructural := func(n *graph.Node, evidence string, callee bool, hop int) {
			if n == nil || exploreDraftIsTestNode(n) || (n.Kind != graph.KindFunction && n.Kind != graph.KindMethod) || (hop <= 1 && exploreIdentifierSegmentCount(n.Name) < 3) {
				return
			}
			entry, _ := makeEntry(n, "", evidence, false, i)
			shared, local := exploreStructuralSignals(target.node, n)
			crossFile := nodeDisplayPath(target.node) != nodeDisplayPath(n)
			// A concrete cross-file callee is causal graph evidence even when its
			// name shares no useful query token with the parent. Multi-hop nodes
			// compete for the same single structural slot as direct neighbors.
			causalCrossFile := callee && crossFile
			if shared == 0 && !entry.exact && entry.overlap == 0 && entry.bodyOverlap == 0 && !local && !causalCrossFile {
				return
			}
			entry.structural = true
			entry.structuralShared = shared
			entry.structuralLocal = local
			entry.structuralCallee = callee
			entry.structuralCrossFile = crossFile
			entry.structuralHop = hop
			entry.parentExact = parent.exact
			entry.parentOverlap = parent.overlap
			structuralNeighbors = append(structuralNeighbors, entry)
		}
		for _, n := range target.callers {
			collectStructural(n, fmt.Sprintf("caller of ranked #%d", i+1), false, 1)
		}
		for _, n := range target.callees {
			collectStructural(n, fmt.Sprintf("callee of ranked #%d", i+1), true, 1)
		}
		for _, neighbor := range target.causalCallees {
			collectStructural(neighbor.node, fmt.Sprintf("%d-hop callee of ranked #%d", neighbor.hop, i+1), true, neighbor.hop)
		}
	}

	sort.SliceStable(aligned, func(i, j int) bool {
		return exploreDraftEntryLess(aligned[i], aligned[j])
	})
	sort.SliceStable(structuralNeighbors, func(i, j int) bool {
		return exploreStructuralEntryLess(structuralNeighbors[i], structuralNeighbors[j])
	})
	// Reserve one structural slot across both directions. A single causal
	// implementation edge adds useful breadth without displacing two ranked
	// candidates from the compact draft.
	for _, entry := range structuralNeighbors {
		if appendEntry(entry) {
			break
		}
	}
	for _, entry := range aligned {
		if len(entries) >= exploreDraftTotalLimit {
			break
		}
		appendEntry(entry)
	}
	for i := exploreDraftPrimaryLimit; i < len(targets) && len(entries) < exploreDraftTotalLimit; i++ {
		entry, _ := makeEntry(targets[i].node, targets[i].source, fmt.Sprintf("ranked #%d", i+1), true, i)
		appendEntry(entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return exploreDraftEntryLess(entries[i], entries[j])
	})
	return entries
}

func exploreDraftEntryLess(a, b exploreDraftEntry) bool {
	alignment := func(entry exploreDraftEntry) int {
		body := entry.bodyOverlap
		if body > 2 {
			body = 2
		}
		return entry.overlap + body
	}
	priority := func(entry exploreDraftEntry) int {
		switch {
		case entry.exact && entry.direct:
			return 0
		case entry.exact:
			return 1
		case alignment(entry) > 0 && entry.direct:
			return 2
		case alignment(entry) > 0:
			return 3
		case entry.structural:
			return 4
		case entry.direct:
			return 5
		default:
			return 6
		}
	}
	if ap, bp := priority(a), priority(b); ap != bp {
		return ap < bp
	}
	if aa, ba := alignment(a), alignment(b); aa != ba {
		return aa > ba
	}
	if a.rareOverlap != b.rareOverlap {
		return a.rareOverlap > b.rareOverlap
	}
	if a.bodyDensity != b.bodyDensity {
		return a.bodyDensity > b.bodyDensity
	}
	// Exact anchors and stronger term alignment already won above. Generic
	// declarations are only a bounded tie-break within the same evidence
	// family; they can never demote a better-aligned candidate.
	if !a.exact && !b.exact && a.direct == b.direct && a.structural == b.structural && a.generic != b.generic {
		return !a.generic
	}
	if a.overlap != b.overlap {
		return a.overlap > b.overlap
	}
	if a.parentOverlap != b.parentOverlap {
		return a.parentOverlap > b.parentOverlap
	}
	if a.direct != b.direct {
		return a.direct
	}
	if a.parentRank != b.parentRank {
		return a.parentRank < b.parentRank
	}
	return exploreDraftNodeKey(a.node) < exploreDraftNodeKey(b.node)
}

func exploreStructuralEntryLess(a, b exploreDraftEntry) bool {
	causalAligned := func(entry exploreDraftEntry) bool {
		return entry.structuralCallee && entry.structuralCrossFile && (entry.exact || entry.overlap > 0 || entry.bodyOverlap >= 2 || entry.rareOverlap > 0)
	}
	if ac, bc := causalAligned(a), causalAligned(b); ac != bc {
		return ac
	}
	if a.exact != b.exact {
		return a.exact
	}
	if a.overlap != b.overlap {
		return a.overlap > b.overlap
	}
	if a.structuralShared != b.structuralShared {
		return a.structuralShared > b.structuralShared
	}
	if a.generic != b.generic {
		return !a.generic
	}
	if a.structuralCallee != b.structuralCallee {
		return a.structuralCallee
	}
	if a.structuralCrossFile != b.structuralCrossFile {
		return a.structuralCrossFile
	}
	if a.structuralHop != b.structuralHop {
		return a.structuralHop < b.structuralHop
	}
	if a.structuralLocal != b.structuralLocal {
		return a.structuralLocal
	}
	if a.parentExact != b.parentExact {
		return a.parentExact
	}
	if a.parentOverlap != b.parentOverlap {
		return a.parentOverlap > b.parentOverlap
	}
	if a.parentRank != b.parentRank {
		return a.parentRank < b.parentRank
	}
	return exploreDraftNodeKey(a.node) < exploreDraftNodeKey(b.node)
}

func exploreIdentifierSegmentCount(name string) int {
	return len(rerank.Tokenize(name))
}

func exploreIdentifierTerms(name string) map[string]struct{} {
	generic := map[string]struct{}{
		"and": {}, "for": {}, "from": {}, "get": {}, "has": {}, "into": {},
		"is": {}, "new": {}, "of": {}, "or": {}, "set": {}, "the": {},
		"to": {}, "with": {}, "without": {},
	}
	terms := make(map[string]struct{})
	for _, term := range rerank.Tokenize(name) {
		term = strings.ToLower(term)
		if len(term) < 3 {
			continue
		}
		if _, skip := generic[term]; skip {
			continue
		}
		terms[term] = struct{}{}
	}
	return terms
}

func exploreStructuralSignals(parent, child *graph.Node) (shared int, local bool) {
	parentTerms := exploreIdentifierTerms(parent.Name)
	for term := range exploreIdentifierTerms(child.Name) {
		if _, ok := parentTerms[term]; ok {
			shared++
		}
	}
	parentPath := nodeDisplayPath(parent)
	childPath := nodeDisplayPath(child)
	parentSlash := strings.LastIndex(parentPath, "/")
	childSlash := strings.LastIndex(childPath, "/")
	if parentSlash >= 0 && childSlash >= 0 && parentPath != childPath {
		local = parentPath[:parentSlash] == childPath[:childSlash]
	}
	return shared, local
}

func exploreDraftIsTestNode(n *graph.Node) bool {
	if n == nil {
		return false
	}
	if auditIsTestNode(n) {
		return true
	}
	path := strings.ToLower(strings.ReplaceAll(nodeDisplayPath(n), "\\", "/"))
	segmented := "/" + strings.Trim(path, "/") + "/"
	for _, dir := range []string{"/__tests__/", "/spec/", "/specs/", "/test/", "/tests/"} {
		if strings.Contains(segmented, dir) {
			return true
		}
	}
	base := path
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	return strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") ||
		strings.Contains(base, ".spec.") || strings.Contains(base, ".test.")
}

func exploreDraftNodeTerms(n *graph.Node) map[string]struct{} {
	if n == nil {
		return map[string]struct{}{}
	}
	retrieval := n.RetrievalMetadata()
	return exploreTerminalTerms(n.Name + " " + retrieval.QualName + " " + nodeDisplayPath(n) + " " + retrieval.Signature)
}

func exploreDraftTermSetOverlap(queryTerms, candidateTerms map[string]struct{}) int {
	count := 0
	for term := range queryTerms {
		if _, ok := candidateTerms[term]; ok {
			count++
		}
	}
	return count
}

func exploreDraftTermOverlap(queryTerms map[string]struct{}, n *graph.Node) (count, longest int) {
	if n == nil || len(queryTerms) == 0 {
		return 0, 0
	}
	for term := range exploreDraftNodeTerms(n) {
		if _, ok := queryTerms[term]; !ok {
			continue
		}
		count++
		if len(term) > longest {
			longest = len(term)
		}
	}
	return count, longest
}

// exploreDraftGenericCandidate identifies structurally generic API context:
// interface/trait declarations, declaration-only methods, tiny accessors, and
// one-step receiver forwarders. It is language-neutral and only participates
// as a Concept-query tie-break; exact anchors bypass it at the call site.
func exploreDraftGenericCandidate(n *graph.Node, source string) bool {
	if n == nil {
		return false
	}
	nameTerms := rerank.Tokenize(n.Name)
	if len(nameTerms) > 0 && len(nameTerms) <= 3 {
		switch strings.ToLower(nameTerms[0]) {
		case "get", "set", "is", "has":
			return true
		}
	}

	retrieval := n.RetrievalMetadata()
	declaration := strings.ToLower(strings.TrimSpace(retrieval.Signature + "\n" + source))
	for _, keyword := range []string{"interface ", "trait ", "protocol "} {
		if strings.Contains(declaration, keyword) {
			return true
		}
	}
	trimmed := strings.TrimSpace(declaration)
	if strings.HasSuffix(trimmed, ";") && (strings.Contains(trimmed, "(") || strings.Contains(trimmed, " function ")) {
		return true
	}
	if strings.TrimSpace(source) == "" {
		return false
	}

	lines := make([]string, 0, 8)
	for _, line := range strings.Split(strings.ToLower(source), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 10 {
		return false
	}
	code := " " + strings.Join(lines, " ") + " "
	if len(code) > 700 {
		return false
	}
	for _, control := range []string{" if ", " for ", " while ", " loop ", " match ", " switch ", " let ", " try ", " catch "} {
		if strings.Contains(code, control) {
			return false
		}
	}
	// Receiver syntax varies, but trivial implementations consistently contain
	// a member access plus either one return/call or one field assignment.
	hasMemberAccess := strings.Contains(code, ".")
	hasOneStepReturn := strings.Contains(code, " return ") && strings.Contains(code, "(")
	hasAccessorAssignment := strings.Contains(code, "=")
	return hasMemberAccess && (hasOneStepReturn || hasAccessorAssignment)
}

func exploreDraftExactAnchor(query string, n *graph.Node) bool {
	if n == nil {
		return false
	}
	if pathAnchors, hasDirectory := exploreQueryPathAnchors(query); hasDirectory {
		normalizedPath := exploreNormalizedPath(nodeDisplayPath(n))
		for _, anchor := range pathAnchors {
			if normalizedPath == anchor {
				return true
			}
		}
		// Directory-qualified requests are strict: neither a coincident symbol
		// name nor the same basename in another directory is an exact draft hit.
		return false
	}
	normalize := func(text string) string {
		return strings.ToLower(strings.Join(rerank.Tokenize(text), " "))
	}
	genericName := map[string]struct{}{
		"clear": {}, "convert": {}, "get": {}, "set": {}, "write": {},
	}
	query = " " + normalize(query) + " "
	for _, anchor := range []string{n.Name, n.QualName} {
		anchor = normalize(anchor)
		if _, generic := genericName[anchor]; generic {
			continue
		}
		if len(anchor) >= 3 && strings.Contains(query, " "+anchor+" ") {
			return true
		}
	}
	path := nodeDisplayPath(n)
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		path = path[slash+1:]
	}
	path = normalize(path)
	return len(path) >= 3 && strings.Contains(query, " "+path+" ")
}

func exploreDraftNodeKey(n *graph.Node) string {
	if n == nil {
		return ""
	}
	if n.ID != "" {
		return n.ID
	}
	return fmt.Sprintf("%s|%s|%s|%d", nodeDisplayPath(n), n.Kind, n.Name, n.StartLine)
}

func exploreDraftSymbol(n *graph.Node) string {
	if n.QualName != "" {
		return n.QualName
	}
	return n.Name
}

// exploreAnswerReady decides whether the ranked head is strong enough to end
// localization. A result is terminal only when its symbols visibly align with
// the shaped query; rank alone is not enough because broad review/audit prompts
// can otherwise elevate common identifiers such as current, change, or client.
func exploreAnswerReady(task string, targets []exploreTarget) bool {
	if len(targets) == 0 || targets[0].node == nil {
		return false
	}
	query := shapeExploreQuery(task)
	class := rerank.ClassifyQuery(query)
	if class == rerank.QueryClassConcept {
		query = stripLeadingExploreDirective(query)
	}
	queryTerms := exploreTerminalTerms(query)
	if len(queryTerms) == 0 {
		return false
	}

	matchedNode := func(node *graph.Node) int {
		if node == nil {
			return 0
		}
		candidateTerms := exploreTerminalTerms(exploreTerminalNodeText(node))
		count := 0
		for term := range queryTerms {
			if _, ok := candidateTerms[term]; ok {
				count++
			}
		}
		return count
	}
	head := targets[0]
	headMatches := matchedNode(head.node)

	// Paths, signatures, and identifier-shaped queries carry explicit anchors.
	// A shared token is not enough: the ranked head must cover the complete
	// path, qualified symbol, or signature anchor from the request.
	if class == rerank.QueryClassPath || class == rerank.QueryClassSymbol || class == rerank.QueryClassSignature {
		return exploreLocalizationExplicitAnchor(query, head.node)
	}
	// A verified full quoted literal is direct implementation evidence. Prefix
	// FTS matches never set this bit, so a nearby vocabulary collision cannot
	// make a concept request terminal.
	if head.exactContent {
		return true
	}

	// Structural evidence is useful only when a directly connected helper
	// independently covers multiple task concepts. Merely spreading one token
	// each across unrelated top-ranked symbols is not sufficient.
	structuralAligned := false
	for _, neighbors := range [][]*graph.Node{head.callers, head.callees} {
		for _, neighbor := range neighbors {
			if matchedNode(neighbor) >= 2 {
				structuralAligned = true
				break
			}
		}
		if structuralAligned {
			break
		}
	}

	// Broad prompts require more evidence concentrated in the ranked head.
	if len(queryTerms) > 10 {
		return headMatches >= 3 || (headMatches >= 1 && structuralAligned)
	}
	if len(queryTerms) == 1 {
		return false
	}
	return headMatches >= 2 || (headMatches >= 1 && structuralAligned)
}

func exploreNormalizedAnchor(text string) string {
	return strings.ToLower(strings.Join(rerank.Tokenize(text), " "))
}

func exploreNormalizedPath(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "`'\"()[]{}<>,;")
	text = strings.ReplaceAll(text, "\\", "/")
	text = strings.TrimPrefix(text, "./")
	parts := strings.Split(text, "/")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := exploreNormalizedAnchor(part); value != "" {
			normalized = append(normalized, value)
		}
	}
	return strings.Join(normalized, "/")
}

func exploreQueryPathAnchors(query string) ([]string, bool) {
	anchors := make([]string, 0, 1)
	hasDirectory := false
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, "`'\"()[]{}<>,;")
		if !strings.ContainsAny(field, "/\\") {
			continue
		}
		hasDirectory = true
		if anchor := exploreNormalizedPath(field); anchor != "" {
			anchors = append(anchors, anchor)
		}
	}
	return anchors, hasDirectory
}

func exploreLocalizationExplicitAnchor(query string, n *graph.Node) bool {
	if n == nil {
		return false
	}
	normalizedQueryText := exploreNormalizedAnchor(query)
	normalizedQuery := " " + normalizedQueryText + " "
	retrieval := n.RetrievalMetadata()
	name := exploreNormalizedAnchor(n.Name)
	qualName := exploreNormalizedAnchor(retrieval.QualName)
	if qualName == "" || qualName == name {
		if separator := strings.LastIndex(n.ID, "::"); separator >= 0 {
			qualName = exploreNormalizedAnchor(n.ID[separator+2:])
		}
	}
	if exploreQueryHasPathSymbolAnchor(query, n) || exploreQueryHasCallAnchor(query, n.Name) {
		return true
	}
	if qualName != "" && qualName != name && strings.Contains(normalizedQuery, " "+qualName+" ") {
		return true
	}

	path := nodeDisplayPath(n)
	normalizedPath := exploreNormalizedPath(path)
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, "`'\"()[]{}<>,;")
		separator := strings.LastIndex(field, "::")
		if separator <= 0 || !strings.ContainsAny(field[:separator], "/\\") {
			continue
		}
		if exploreNormalizedPath(field[:separator]) != normalizedPath {
			continue
		}
		symbol := exploreNormalizedAnchor(field[separator+2:])
		if symbol == name || symbol == qualName || strings.HasSuffix(qualName, " "+symbol) {
			return true
		}
	}
	pathAnchors, hasDirectory := exploreQueryPathAnchors(query)
	if hasDirectory {
		for _, anchor := range pathAnchors {
			if normalizedPath == anchor {
				return true
			}
		}
		// A directory-qualified request must never degrade to a basename match;
		// that would make same-name files in different packages terminal.
		return false
	}
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		path = path[slash+1:]
	}
	basename := exploreNormalizedAnchor(path)
	if len(basename) >= 3 && strings.Contains(normalizedQuery, " "+basename+" ") {
		return true
	}

	class := rerank.ClassifyQuery(query)
	if class == rerank.QueryClassSignature {
		signature := exploreNormalizedAnchor(retrieval.Signature)
		return signature != "" && strings.Contains(normalizedQuery, " "+signature+" ")
	}
	// An unqualified symbol cannot disambiguate same-name methods. Permit it
	// only for nodes whose canonical name is itself unqualified.
	return class == rerank.QueryClassSymbol && qualName == name && normalizedQueryText == name
}

// exploreLocalizationExplicitTarget returns only a concrete path, qualified
// symbol, signature, or call anchor. Concept-term overlap is intentionally not
// accepted here: it can rank a neighborhood but must not prescribe the one
// allowed post-localization source read.
func exploreLocalizationExplicitTarget(task string, targets []exploreTarget) string {
	query := shapeExploreQuery(task)
	if rerank.ClassifyQuery(query) == rerank.QueryClassConcept {
		query = stripLeadingExploreDirective(query)
	}
	for _, target := range targets {
		if target.node != nil && exploreLocalizationExplicitAnchor(query, target.node) {
			return target.node.ID
		}
	}
	return ""
}

// exploreLocalizationExactTarget selects the strongest exact-like target for
// ordering and backwards-compatible callers. Explicit qualified/path anchors
// win; concept overlap may still pick a stable evidence head, but the facade
// uses exploreLocalizationExplicitTarget before issuing a refinement read.
// natural-language tasks, candidates with more independent task terms win and
// inverse candidate frequency breaks ties so repeated generic setters do not
// outrank a rarer conjunctive implementation target. Retrieval order is the
// stable final tie-breaker.
func exploreLocalizationExactTarget(task string, targets []exploreTarget) string {
	query := shapeExploreQuery(task)
	class := rerank.ClassifyQuery(query)
	if class == rerank.QueryClassConcept {
		query = stripLeadingExploreDirective(query)
	}
	for _, target := range targets {
		if target.node != nil && exploreLocalizationExplicitAnchor(query, target.node) {
			return target.node.ID
		}
	}
	queryTerms := exploreTerminalTerms(query)
	if len(queryTerms) == 0 {
		return ""
	}

	matches := make([]map[string]struct{}, len(targets))
	frequency := make(map[string]int)
	for i, target := range targets {
		matches[i] = make(map[string]struct{})
		if target.node == nil {
			continue
		}
		for term := range exploreTerminalTerms(exploreTerminalNodeText(target.node)) {
			if _, relevant := queryTerms[term]; !relevant {
				continue
			}
			matches[i][term] = struct{}{}
			frequency[term]++
		}
	}

	best := -1
	bestOverlap := -1
	bestRarity := -1
	for i, target := range targets {
		if target.node == nil {
			continue
		}
		overlap := len(matches[i])
		rarity := 0
		for term := range matches[i] {
			if frequency[term] > 0 {
				rarity += 1000 / frequency[term]
			}
		}
		if best < 0 || overlap > bestOverlap || (overlap == bestOverlap && rarity > bestRarity) {
			best = i
			bestOverlap = overlap
			bestRarity = rarity
		}
	}
	if best < 0 || bestOverlap == 0 {
		return ""
	}
	// Concept localization needs two independent lexical anchors before it may
	// prescribe an exact source read. They may jointly support an equal-evidence
	// neighborhood, in which case retrieval order remains the stable tie-breaker.
	// Repeating one generic token (for example walk/walker/walking) still leaves
	// frequency with one key and cannot turn retrieval order into exactness.
	if class == rerank.QueryClassConcept && bestOverlap < 2 && len(frequency) < 2 {
		return ""
	}
	return targets[best].node.ID
}

func exploreTerminalNodeText(n *graph.Node) string {
	if n == nil {
		return ""
	}
	retrieval := n.RetrievalMetadata()
	return strings.TrimSpace(strings.Join([]string{n.Name, retrieval.QualName, n.FilePath, retrieval.Signature}, " "))
}

var exploreTerminalGenericTerms = map[string]struct{}{
	"audit": {}, "behavior": {}, "blocker": {}, "branch": {}, "change": {},
	"check": {}, "code": {}, "correctness": {}, "current": {}, "file": {},
	"find": {}, "fix": {}, "identify": {}, "implement": {}, "investigate": {},
	"issue": {}, "problem": {}, "quality": {}, "release": {}, "review": {},
	"source": {}, "symbol": {}, "task": {}, "validate": {},
}

func exploreTerminalTerms(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, raw := range rerank.Tokenize(text) {
		term := strings.ToLower(strings.TrimSpace(raw))
		if len(term) < 3 {
			continue
		}
		if _, stop := assistStopWords[term]; stop {
			continue
		}
		term = exploreTerminalTermRoot(term)
		if _, generic := exploreTerminalGenericTerms[term]; generic {
			continue
		}
		terms[term] = struct{}{}
	}
	return terms
}

func exploreTerminalTermRoot(term string) string {
	if len(term) > 4 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") {
		return strings.TrimSuffix(term, "s")
	}
	return term
}

// handleExplore is the one-shot localization verb: free text in, a ranked
// neighborhood (symbols + source + call paths + file map + completeness
// cue) out, bounded by a token budget, in a single response.
func (s *Server) handleExplore(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task := strings.TrimSpace(req.GetString("task", ""))
	if task == "" {
		return mcp.NewToolResultError("task is required"), nil
	}
	// Classify artifact intent from the untouched task. Query shaping below is
	// intentionally optimized for source symbols and may discard the exact
	// paths, keys, flags, and environment names needed by config/CI searches.
	artifactIntent := classifyExploreArtifactIntent(task)
	maxSymbols := clampInt(req.GetInt("max_symbols", exploreDefaultMaxSymbols), 1, exploreMaxMaxSymbols)
	budget := clampInt(req.GetInt("token_budget", exploreDefaultBudgetTokens), exploreMinBudgetTokens, exploreMaxBudgetTokens)

	resolved, errResult := s.resolveScope(ctx, req, IntentLocate)
	if errResult != nil {
		return errResult, nil
	}
	eng := s.engineFor(ctx)
	if eng == nil {
		return mcp.NewToolResultError("no indexed repository is available; run index_repository first"), nil
	}
	opts := query.QueryOptions{
		WorkspaceID: resolved.WorkspaceID,
		ProjectID:   resolved.ProjectID,
		RepoAllow:   resolved.RepoAllow,
	}
	// The lane is a strict no-op for ordinary code tasks. Active artifact
	// requests reuse existing file nodes and content FTS under fixed fan-out
	// caps, including declaration-free config files found by explicit path.
	artifactLane := s.gatherExploreArtifactLane(ctx, artifactIntent, opts)
	// The task text is pasted verbatim by the agent and is frequently a
	// whole issue report — a title, then a body of repro commands, stack
	// traces, environment tables and issue-template prompts. Fed raw to
	// retrieval, that body's command-line flags and log lines out-weigh the
	// one-line defect description and pull ranking toward the flag-definition
	// and entry-point files instead of the fix site. shapeExploreQuery
	// distils the report to its retrieval signal (lead weighted, boilerplate
	// dropped) before search; a short focused query is passed through
	// untouched. The original task is still shown in the header so the agent
	// sees what it asked.
	searchQuery := shapeExploreQuery(task)
	queryClass := rerank.ClassifyQuery(searchQuery)
	if queryClass == rerank.QueryClassConcept {
		searchQuery = stripLeadingExploreDirective(searchQuery)
	}
	rctx := s.buildRerankContext(ctx, searchQuery)
	// Over-fetch, then keep the top maxSymbols that are real localization
	// targets — params / locals / closures / imports are never a place a
	// developer edits to fix a report, and they otherwise consume ranking
	// slots and clutter the file map. Test-source symbols are demoted, not
	// dropped: production code is where a report is resolved, but a task
	// genuinely about tests still gets them when production hits run out.
	fetch := exploreCandidateFetchLimit(maxSymbols, queryClass)
	var ranked []*rerank.Candidate
	if queryClass == rerank.QueryClassConcept {
		// Gather the hybrid primary channel without truncating away vector-only
		// candidates, add one text-only concept bag, then run the session-aware
		// reranker exactly once over the union. Channel ranks survive the merge,
		// so semantic intent remains useful outside signature-rich languages.
		primaryOpts := opts
		primaryOpts.SkipInnerRerank = true
		ranked = eng.GatherSymbolCandidates(searchQuery, fetch, primaryOpts, rctx)
		terms := exploreConceptRecallTerms(searchQuery)
		if hasExploreExpansionTerms(searchQuery, terms) {
			expansionOpts := opts
			expansionOpts.SkipInnerRerank = true
			expansionOpts.SkipVectorChannel = true
			expansionOpts.SkipExactNameSplice = true
			expanded := eng.GatherSymbolCandidates(strings.Join(terms, " "), fetch, expansionOpts, rctx)
			ranked = mergeExploreCandidates(ranked, expanded, fetch)
		}
		// Gathering deliberately over-fetches each retrieval channel so scope
		// filtering cannot erase relevant results. Bound the union before the
		// graph-aware reranker: its centrality and edge hydration costs scale
		// with every candidate, not just the final response size.
		ranked = limitExploreCandidates(ranked, fetch*2)
		if content := s.gatherExploreQuotedContentCandidates(ctx, task, fetch, opts); len(content) > 0 {
			ranked = mergeExploreCandidates(ranked, content, 0)
			ranked = limitExploreCandidates(ranked, fetch*2)
		}
		if pipeline := eng.Rerank(); pipeline != nil {
			ranked = pipeline.Rerank(searchQuery, ranked, rctx)
		}
		ranked = rerankExploreConceptCoverage(searchQuery, ranked)
	} else {
		ranked = eng.SearchSymbolsRanked(searchQuery, fetch, opts, rctx)
	}
	// Resilience ladder: a warm-restarted daemon can transiently return an
	// empty scoped ranked result (workspace stamps not yet backfilled, or
	// search bundles served before their node payloads re-materialise)
	// while the index itself is fine. A one-shot verb must not answer
	// "nothing matched" for an index that is merely re-warming, so relax in
	// two steps — unscoped ranked, then unscoped BM25 — re-applying the
	// repo boundary as a post-filter to preserve multi-repo hygiene.
	repoAllowed := func(n *graph.Node) bool {
		return len(resolved.RepoAllow) == 0 || resolved.RepoAllow[n.RepoPrefix]
	}
	if len(ranked) == 0 {
		for _, c := range eng.SearchSymbolsRanked(searchQuery, fetch, query.QueryOptions{}, rctx) {
			if c != nil && c.Node != nil && repoAllowed(c.Node) {
				ranked = append(ranked, c)
			}
		}
	}
	if len(ranked) == 0 {
		// Last rung: the per-term OR-merge the ranked search handler itself
		// falls back on — whole-sentence MATCH semantics differ between the
		// in-memory and disk-resident search backends, and per-term fetch +
		// merge works on both.
		nodes, _ := fetchAndMergeBM25Timed(eng, searchQuery, exploreLexicalTerms(searchQuery), fetch, opts, nil)
		if len(nodes) == 0 && (opts.WorkspaceID != "" || opts.ProjectID != "" || len(opts.RepoAllow) > 0) {
			nodes, _ = fetchAndMergeBM25Timed(eng, searchQuery, exploreLexicalTerms(searchQuery), fetch, query.QueryOptions{}, nil)
		}
		for i, n := range nodes {
			if n != nil && repoAllowed(n) {
				ranked = append(ranked, &rerank.Candidate{Node: n, TextRank: i, VectorRank: -1})
			}
		}
	}
	var prod, test []*rerank.Candidate
	for _, c := range ranked {
		if c == nil || c.Node == nil || !exploreLocalizableKind(c.Node.Kind) {
			continue
		}
		isTest, _ := c.Node.Meta["is_test"].(bool)
		if isTest || !exploreCodeDefinitionKind(c.Node.Kind) {
			test = append(test, c)
		} else {
			prod = append(prod, c)
		}
	}
	// Natural-language localization can retrieve large exact-name collision
	// sets (`client` fields, `Validate` methods, generated accessors). BM25 is
	// right that each item matches, but a useful neighborhood needs distinct
	// concepts. Keep one data declaration and at most two callable/type members
	// per repeated name, moving the remaining collisions behind distinct
	// definitions. Identifier/path lookups retain literal ordering.
	prod = diversifyRepeatedExploreNames(prod, queryClass)
	// Bounded per-file diversification (the same demote-only mechanism the
	// ranked search head uses): a localization neighborhood that spans
	// files beats one file's cluster of sibling shims crowding out every
	// other candidate. Nothing is dropped — capped files' extra hits move
	// below not-yet-capped files.
	prodNodes := make([]*graph.Node, len(prod))
	for i, c := range prod {
		prodNodes[i] = c.Node
	}
	if exploreShouldDiversifyByFile(queryClass) {
		_, prod = diversifyByFile(prodNodes, prod, defaultMaxPerFile)
	}
	cands := prod
	if len(cands) > maxSymbols {
		cands = cands[:maxSymbols]
	} else if len(cands) < maxSymbols {
		room := maxSymbols - len(cands)
		if room > len(test) {
			room = len(test)
		}
		cands = append(cands, test[:room]...)
	}
	if len(cands) == 0 && len(artifactLane.targets) == 0 {
		if req.GetBool("localize", false) {
			return s.completeEmptyLocalization(ctx, task, budget), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"EXPLORE — %s\n\nNo ranked symbols matched this request. Widen the wording, use search(operation:\"text\", query:\"<literal>\") for a literal lead, or search(operation:\"files\", query:\"<filename>\") for a path lead.",
			truncateOneLine(task, 200))), nil
	}

	ringOpts := query.QueryOptions{
		Depth: 1, Limit: exploreRingCap * 3, Detail: "brief",
		WorkspaceID: resolved.WorkspaceID, ProjectID: resolved.ProjectID, RepoAllow: resolved.RepoAllow,
	}
	explicitTarget := false
	if _, hasPath := exploreQueryPathAnchors(searchQuery); hasPath {
		explicitTarget = true
	}
	if !explicitTarget {
		for _, c := range cands {
			if c != nil && c.Node != nil && exploreLocalizationExplicitAnchor(searchQuery, c.Node) {
				explicitTarget = true
				break
			}
		}
	}
	causalAdmitted := 0
	var causalElapsed time.Duration
	artifactTargets := artifactLane.targets
	targets := make([]exploreTarget, 0, len(artifactTargets)+len(cands))
	// Artifact evidence leads only when the strong classifier activated its
	// lane. Ordinary source localization retains the exact prior target order.
	targets = append(targets, artifactTargets...)
	for _, c := range cands {
		if c == nil || c.Node == nil {
			continue
		}
		n := c.Node
		t := exploreTarget{node: n, score: c.Score}
		if c.Signals != nil {
			t.exactContent = c.Signals[exploreContentRecallExactSignal] > 0
		}
		t.source = s.manifestSymbolSource(ctx, n)
		if callers := eng.GetCallers(n.ID, ringOpts); callers != nil {
			t.callers = ringNeighbors(callers.Nodes, n.ID, exploreRingCap)
		}

		calleeOpts := ringOpts
		expanded := exploreAdmitCausalSeed(
			queryClass == rerank.QueryClassConcept,
			explicitTarget,
			exploreStrongCausalSeed(searchQuery, t),
			causalAdmitted,
			causalElapsed,
		)
		if expanded {
			calleeOpts.Depth = exploreCausalDepth
			causalAdmitted++
		}
		started := time.Now()
		callees := eng.GetCallChain(n.ID, calleeOpts)
		if expanded {
			causalElapsed += time.Since(started)
		}
		if callees != nil {
			if expanded {
				neighbors := minimumExploreCausalHops(n.ID, callees, opts, exploreCausalDepth, ringOpts.Limit)
				direct := make([]*graph.Node, 0, exploreRingCap)
				for _, neighbor := range neighbors {
					if neighbor.hop == 1 {
						direct = append(direct, neighbor.node)
					} else {
						t.causalCallees = append(t.causalCallees, neighbor)
					}
				}
				if len(neighbors) > 0 {
					t.callees = ringNeighbors(direct, n.ID, exploreRingCap)
				} else {
					t.callees = ringNeighbors(callees.Nodes, n.ID, exploreRingCap)
				}
			} else {
				t.callees = ringNeighbors(callees.Nodes, n.ID, exploreRingCap)
			}
		}
		targets = append(targets, t)
	}
	// Direct retrieval owns the ranked head. Once graph promotion has selected
	// a cross-file boundary, materialize exactly that one node so both text and
	// structured responses can reserve a source body without a broad read.
	targets = s.materializeExploreStructuralSource(ctx, task, targets, opts)

	if !req.GetBool("localize", false) {
		return mcp.NewToolResultText(s.renderExplore(task, targets, budget)), nil
	}
	symbolTargets := targets[len(artifactTargets):]
	answerReady := exploreAnswerReady(task, symbolTargets) || artifactLane.ready
	// File evidence can make localization answer-ready, but it never becomes a
	// synthetic exact-symbol read. Exact reads remain declaration-only.
	exactSymbol := exploreLocalizationExplicitTarget(task, symbolTargets)
	if !answerReady && exactSymbol == "" {
		anchors := make([]string, 0, 3)
		for _, target := range targets {
			if target.node == nil {
				continue
			}
			anchors = append(anchors, fmt.Sprintf("%s (%s)", target.node.ID, nodeDisplayPath(target.node)))
			if len(anchors) == cap(anchors) {
				break
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf(
			"localization confidence is insufficient: no candidate has an explicit file/symbol anchor or two distinct task-term matches; top candidates: %s. Add a concrete file or symbol anchor, or use explore(operation:\"task\") when investigation must continue.",
			strings.Join(anchors, ", "))), nil
	}
	completion := newLocalizationCompletion(answerReady, exactSymbol)
	s.localizationFor(ctx).armForTask(completion, task)
	return newLocalizationExploreResultForTask(completion, task, targets, budget), nil
}

// localizationExploreEnvelope is the compact, machine-readable result for an
// explicit localization-only request. Ordinary explore(task) retains the
// human-oriented legacy rendering; localize does not duplicate it.
type localizationExploreEnvelope struct {
	Completion localizationCompletion `json:"completion"`
	Files      []string               `json:"files"`
	Symbols    []string               `json:"symbols"`
	Evidence   []localizationEvidence `json:"evidence"`
}

type localizationEvidence struct {
	Rank      int      `json:"rank"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	QualName  string   `json:"qual_name,omitempty"`
	Kind      string   `json:"kind"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	EndLine   int      `json:"end_line,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Callers   []string `json:"callers,omitempty"`
	Callees   []string `json:"callees,omitempty"`
	Source    string   `json:"source,omitempty"`
}

func (s *Server) completeEmptyLocalization(ctx context.Context, _ string, budget int) *mcp.CallToolResult {
	completion := localizationCompletion{
		State:            localizationStateInactive,
		Scope:            "localization",
		RequiredAction:   "continue",
		AllowedToolCalls: 0,
	}
	// An empty result supersedes any previous task contract but must not arm
	// answer-ready or an exact-read allowance. Navigation remains available for
	// the caller to continue investigation with a better anchor.
	s.localizationFor(ctx).reset()
	return newLocalizationExploreResult(completion, []exploreTarget{}, budget)
}

// exploreAllowsStructuralBody keeps graph-expanded source reads exclusive to
// broad concept localization. Explicit directory-qualified paths are guarded
// independently because prose around a path can still classify as Concept;
// literal symbol and signature queries retain their direct-only behavior.
func exploreAllowsStructuralBody(task string) bool {
	shaped := shapeExploreQuery(task)
	class := rerank.ClassifyQuery(shaped)
	_, directoryQualified := exploreQueryPathAnchors(shaped)
	return !directoryQualified && class != rerank.QueryClassSymbol && class != rerank.QueryClassSignature
}

// explorePreferredFullBodyIDs reserves the first source slot for the strongest
// direct answer and, when available, the second for the promoted cross-file
// structural boundary. Remaining direct targets fill unused slots.
func explorePreferredFullBodyIDs(task string, targets []exploreTarget, draft []exploreDraftEntry, limit int) []string {
	if limit <= 0 {
		return nil
	}
	sources := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.node != nil && target.source != "" {
			sources[exploreDraftNodeKey(target.node)] = struct{}{}
		}
	}
	result := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	appendEntry := func(entry exploreDraftEntry) bool {
		if entry.node == nil || len(result) >= limit {
			return false
		}
		key := exploreDraftNodeKey(entry.node)
		if _, hasSource := sources[key]; !hasSource {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		result = append(result, key)
		return true
	}
	if draft == nil {
		draft = exploreAnswerDraft(task, targets)
	}
	if !exploreAllowsStructuralBody(task) {
		for _, entry := range draft {
			if entry.direct && entry.exact {
				appendEntry(entry)
			}
		}
		return result
	}
	for _, entry := range draft {
		if entry.direct && appendEntry(entry) {
			break
		}
	}
	for _, entry := range draft {
		if entry.structural && entry.structuralCrossFile && appendEntry(entry) {
			break
		}
	}
	for _, entry := range draft {
		if entry.direct {
			appendEntry(entry)
		}
	}
	return result
}

// materializeExploreStructuralSource loads source for at most one promoted
// cross-file structural boundary. Graph expansion discovers the node without a
// broad source read; this performs the same bounded symbol-range read used for
// direct targets only after the draft has selected that boundary.
func (s *Server) materializeExploreStructuralSource(ctx context.Context, task string, targets []exploreTarget, scope query.QueryOptions) []exploreTarget {
	return materializeExploreStructuralSourceWithReader(ctx, task, targets, scope, s.manifestSymbolSource)
}

func materializeExploreStructuralSourceWithReader(
	ctx context.Context,
	task string,
	targets []exploreTarget,
	scope query.QueryOptions,
	readSource func(context.Context, *graph.Node) string,
) []exploreTarget {
	if len(targets) == 0 || !exploreAllowsStructuralBody(task) || ctx.Err() != nil || readSource == nil {
		return targets
	}
	draft := exploreAnswerDraft(task, targets)
	present := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.node != nil {
			present[exploreDraftNodeKey(target.node)] = struct{}{}
		}
	}
	for _, entry := range draft {
		if entry.node == nil || !entry.structural || !entry.structuralCrossFile {
			continue
		}
		key := exploreDraftNodeKey(entry.node)
		if _, exists := present[key]; exists {
			return targets
		}
		if !exploreNodeWithinQueryScope(entry.node, scope) {
			return targets
		}
		// Cancellation may arrive while graph promotion is ranking candidates;
		// check at the actual source-read boundary so an abandoned request never
		// opens a file merely to populate an output that cannot be delivered.
		if ctx.Err() != nil {
			return targets
		}
		source := readSource(ctx, entry.node)
		if strings.TrimSpace(source) == "" {
			return targets
		}
		materialized := make([]exploreTarget, 0, len(targets)+1)
		materialized = append(materialized, targets...)
		return append(materialized, exploreTarget{node: entry.node, source: source})
	}
	return targets
}

func exploreNodeWithinQueryScope(n *graph.Node, scope query.QueryOptions) bool {
	if n == nil {
		return false
	}
	if scope.WorkspaceID != "" && n.WorkspaceID != scope.WorkspaceID {
		return false
	}
	if scope.ProjectID != "" && n.ProjectID != scope.ProjectID {
		return false
	}
	if len(scope.RepoAllow) > 0 && !scope.RepoAllow[n.RepoPrefix] {
		return false
	}
	return true
}

// localizationEvidenceTargets projects the same bounded answer-draft evidence
// used by ordinary rendering into the structured terminal envelope. Promoted
// callers/callees may replace low-ranked direct candidates, but never increase
// the response cardinality. Direct targets retain their source and neighbors.
func localizationEvidenceTargets(task, exactID string, targets []exploreTarget) []exploreTarget {
	return localizationEvidenceTargetsFromDraft(task, exactID, targets, nil)
}

func localizationEvidenceTargetsFromDraft(task, exactID string, targets []exploreTarget, draft []exploreDraftEntry) []exploreTarget {
	if len(targets) == 0 {
		return targets
	}
	limit := len(targets)
	direct := make(map[string]exploreTarget, len(targets))
	for _, target := range targets {
		if target.node != nil {
			direct[exploreDraftNodeKey(target.node)] = target
		}
	}
	selected := make([]exploreTarget, 0, limit)
	seen := make(map[string]struct{}, limit)
	appendTarget := func(target exploreTarget) {
		if len(selected) >= limit || target.node == nil {
			return
		}
		key := exploreDraftNodeKey(target.node)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		selected = append(selected, target)
	}
	if draft == nil && strings.TrimSpace(task) != "" {
		draft = exploreAnswerDraft(task, targets)
	}
	appendTarget(targets[0])
	// The completion contract names the exact refinement target. Reserve it
	// before promoted structural neighbors so a tight evidence limit cannot
	// advertise an exact_symbol that is absent from files/symbols/evidence.
	if exactID != "" {
		for _, target := range targets {
			if target.node != nil && target.node.ID == exactID {
				appendTarget(target)
				break
			}
		}
	}
	appendEntry := func(entry exploreDraftEntry) {
		if entry.node == nil {
			return
		}
		if target, ok := direct[exploreDraftNodeKey(entry.node)]; ok {
			appendTarget(target)
		} else {
			appendTarget(exploreTarget{node: entry.node})
		}
	}
	// Reserve bounded space for promoted implementations before lower-ranked
	// direct retrieval hits consume the envelope.
	for _, entry := range draft {
		if entry.structural || !entry.direct {
			appendEntry(entry)
		}
	}
	for _, entry := range draft {
		appendEntry(entry)
	}
	for _, target := range targets {
		appendTarget(target)
	}
	return selected
}

func newLocalizationExploreResult(completion localizationCompletion, targets []exploreTarget, budget int) *mcp.CallToolResult {
	return newLocalizationExploreResultForTask(completion, "", targets, budget)
}

const (
	localizationEnvelopeBytesPerToken = 4
	localizationMaxNameRunes          = 120
	localizationMaxQualNameRunes      = 180
	localizationMaxSignatureRunes     = 240
	localizationMaxNeighborIDs        = 3
	localizationMaxNeighborIDRunes    = 96
)

func compactLocalizationField(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func localizationEnvelopeFits(envelope localizationExploreEnvelope, maxBytes int) bool {
	body, err := json.Marshal(envelope)
	return err == nil && len(body) <= maxBytes
}

func newLocalizationExploreResultForTask(completion localizationCompletion, task string, targets []exploreTarget, budget int) *mcp.CallToolResult {
	var draft []exploreDraftEntry
	if strings.TrimSpace(task) != "" {
		draft = exploreAnswerDraft(task, targets)
	}
	preferredBodyIDs := explorePreferredFullBodyIDs(task, targets, draft, exploreFullBodyLimit)
	targets = localizationEvidenceTargetsFromDraft(task, completion.ExactSymbol, targets, draft)
	envelope := localizationExploreEnvelope{
		Completion: completion,
		Files:      make([]string, 0),
		Symbols:    make([]string, 0),
		Evidence:   make([]localizationEvidence, 0),
	}
	maxBytes := budget * localizationEnvelopeBytesPerToken
	if maxBytes < 1 {
		maxBytes = 1
	}

	mandatoryCount := 0
	if len(targets) > 0 {
		mandatoryCount = 1
	}
	if completion.ExactSymbol != "" {
		for index, target := range targets {
			if target.node != nil && target.node.ID == completion.ExactSymbol && index+1 > mandatoryCount {
				mandatoryCount = index + 1
				break
			}
		}
	}

	seenFiles := make(map[string]struct{})
	acceptedTargets := make([]exploreTarget, 0, len(targets))
	for _, target := range targets {
		if target.node == nil {
			continue
		}
		n := target.node
		path := nodeDisplayPath(n)
		retrieval := n.RetrievalMetadata()
		evidence := localizationEvidence{
			Rank: len(envelope.Evidence) + 1, ID: n.ID,
			Name: compactLocalizationField(n.Name, localizationMaxNameRunes),
			Kind: string(n.Kind), File: path,
			Line: n.StartLine, EndLine: n.EndLine,
			QualName:  compactLocalizationField(retrieval.QualName, localizationMaxQualNameRunes),
			Signature: compactLocalizationField(retrieval.Signature, localizationMaxSignatureRunes),
			Callers:   boundedLocalizationNeighborIDs(target.callers, localizationMaxNeighborIDs),
			Callees:   boundedLocalizationNeighborIDs(target.callees, localizationMaxNeighborIDs),
		}

		candidate := envelope
		candidate.Files = append([]string(nil), envelope.Files...)
		candidate.Symbols = append([]string(nil), envelope.Symbols...)
		candidate.Evidence = append([]localizationEvidence(nil), envelope.Evidence...)
		if _, seen := seenFiles[path]; !seen {
			candidate.Files = append(candidate.Files, path)
		}
		candidate.Symbols = append(candidate.Symbols, n.ID)
		candidate.Evidence = append(candidate.Evidence, evidence)
		mandatory := len(envelope.Evidence) < mandatoryCount
		if !localizationEnvelopeFits(candidate, maxBytes) {
			if !mandatory {
				continue
			}
			// Primary and exact identifiers are contractual. If their optional
			// metadata would exceed the envelope, retain the identifiers and
			// locations while shedding expansion details first.
			evidence.QualName = ""
			evidence.Signature = ""
			evidence.Callers = nil
			evidence.Callees = nil
			candidate.Evidence[len(candidate.Evidence)-1] = evidence
		}
		envelope = candidate
		seenFiles[path] = struct{}{}
		acceptedTargets = append(acceptedTargets, target)
	}

	// Add full bodies only when the complete serialized response still fits.
	// JSON marshaling here accounts for metadata arrays and escaping that the
	// previous source-only token estimate omitted. Preferred IDs reserve one
	// slot for a promoted cross-file causal boundary when it has source.
	bodyOrder := make([]int, 0, exploreFullBodyLimit)
	seenBody := make(map[int]struct{}, exploreFullBodyLimit)
	appendBodyIndex := func(index int) {
		if index < 0 || index >= len(acceptedTargets) || len(bodyOrder) >= exploreFullBodyLimit {
			return
		}
		if _, exists := seenBody[index]; exists {
			return
		}
		seenBody[index] = struct{}{}
		bodyOrder = append(bodyOrder, index)
	}
	for _, preferredID := range preferredBodyIDs {
		for index, target := range acceptedTargets {
			if target.node != nil && exploreDraftNodeKey(target.node) == preferredID {
				appendBodyIndex(index)
				break
			}
		}
	}
	for index := range acceptedTargets {
		appendBodyIndex(index)
	}
	for _, index := range bodyOrder {
		if acceptedTargets[index].source == "" {
			continue
		}
		candidate := envelope
		candidate.Evidence = append([]localizationEvidence(nil), envelope.Evidence...)
		candidate.Evidence[index].Source = acceptedTargets[index].source
		if localizationEnvelopeFits(candidate, maxBytes) {
			envelope = candidate
		}
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return mcp.NewToolResultError("encode localization result: " + err.Error())
	}
	return mcp.NewToolResultText(string(body))
}

func boundedLocalizationNeighborIDs(nodes []*graph.Node, limit int) []string {
	ids := localizationNeighborIDs(nodes)
	if limit >= 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	for index := range ids {
		ids[index] = compactLocalizationField(ids[index], localizationMaxNeighborIDRunes)
	}
	return ids
}

func localizationNeighborIDs(nodes []*graph.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.ID != "" {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

// renderExplore lays out the ranked neighborhood as a compact, agent-facing
// text block: likely targets (with call paths + source), a file map, and a
// trailing completeness cue — the measured antidote to the cross-check turn.
// Source bodies are packed newest-first until the token budget fills, then
// demoted to signature stubs; every candidate location is always listed.
func (s *Server) renderExplore(task string, targets []exploreTarget, budget int) string {
	var b strings.Builder
	files := map[string][]string{}
	fileOrder := []string{}
	addFile := func(path, sym string) {
		if _, ok := files[path]; !ok {
			fileOrder = append(fileOrder, path)
		}
		files[path] = append(files[path], sym)
	}

	fmt.Fprintf(&b, "EXPLORE — %s\n\n", truncateOneLine(task, 200))
	b.WriteString("RANKED LOCALIZATION: use the evidence below with stated uncertainty. Do not fan out or rerun broad exploration. Localization-only callers receive a separate completion contract; diagnosis and change callers continue from this evidence.\n\n")
	draft := exploreAnswerDraft(task, targets)
	b.WriteString("## Answer draft\n")
	for _, entry := range draft {
		if !entry.direct && entry.node.ID != "" {
			fmt.Fprintf(&b, "- FILE: %s  ·  SYMBOL: %s  ·  ID: %s  ·  EVIDENCE: %s\n",
				nodeLoc(entry.node), exploreDraftSymbol(entry.node), entry.node.ID, entry.evidence)
			continue
		}
		fmt.Fprintf(&b, "- FILE: %s  ·  SYMBOL: %s  ·  EVIDENCE: %s\n",
			nodeLoc(entry.node), exploreDraftSymbol(entry.node), entry.evidence)
	}
	fullBodyIDs := make(map[string]struct{}, exploreFullBodyLimit)
	for _, id := range explorePreferredFullBodyIDs(task, targets, draft, exploreFullBodyLimit) {
		fullBodyIDs[id] = struct{}{}
	}
	b.WriteString("\n## Likely targets (most-relevant first)\n")
	b.WriteString("Graph-verified details follow; all candidate locations and signatures are retained, with full source for the strongest direct draft targets.\n")

	used := estimateTokens(b.String())
	truncated := false
	for i, t := range targets {
		n := t.node
		path := nodeDisplayPath(n)
		addFile(path, n.Name)

		var head strings.Builder
		fmt.Fprintf(&head, "\n%d. %s  %s  ·  %s  ·  id: %s\n", i+1, n.Name, n.Kind, nodeLoc(n), n.ID)
		if len(t.callers) > 0 {
			fmt.Fprintf(&head, "   ^ callers: %s\n", joinNeighbors(t.callers))
		}
		if len(t.callees) > 0 {
			fmt.Fprintf(&head, "   v calls:   %s\n", joinNeighbors(t.callees))
		}
		b.WriteString(head.String())
		used += estimateTokens(head.String())

		// Source body: full for the strongest direct draft targets while the
		// budget holds (no single body may take more than 1/exploreBodyBudgetShare
		// of the whole budget), signature stub otherwise. The header/locations
		// above are always emitted so
		// file-hit / symbol-hit never depend on budget.
		body := ""
		if t.source != "" {
			cost := estimateTokens(t.source)
			_, preferred := fullBodyIDs[exploreDraftNodeKey(n)]
			if preferred && used+cost <= budget && cost <= budget/exploreBodyBudgetShare {
				body = t.source
			} else {
				if sig, err := elide.CompressString(t.source, n.Language); err == nil && sig != "" {
					body = sig
				} else {
					body = firstLines(t.source, 3)
				}
				if used+estimateTokens(body) > budget {
					body = ""
				}
				truncated = true
			}
		}
		if body != "" {
			fmt.Fprintf(&b, "```%s\n%s\n```\n", fenceLang(n.Language), strings.TrimRight(body, "\n"))
			used += estimateTokens(body)
		}
	}

	b.WriteString("\n## Candidate files\n")
	for _, f := range fileOrder {
		fmt.Fprintf(&b, "- %s  ·  %s\n", f, strings.Join(dedupStrings(files[f]), ", "))
	}

	fmt.Fprintf(&b, "\n— Coverage: %d ranked candidate symbol(s) across %d file(s); callers/callees resolved server-side. Locations and IDs above are citeable.\n",
		len(targets), len(fileOrder))
	b.WriteString("END OF LOCALIZATION — localization-only callers answer from this evidence; diagnosis and change callers proceed from it.\n")
	if truncated {
		fmt.Fprintf(&b, "Some bodies are signature-only under the %d-token budget; every candidate location remains listed.\n", budget)
	}
	return b.String()
}

// exploreCodeDefinitionKind reports whether a node kind is a code
// definition a developer edits to resolve a report. Non-code graph
// nodes (doc sections, packages, resources, contracts, ...) can rank —
// they are demoted to the fallback pool alongside test symbols rather
// than dropped, so a genuinely docs-shaped task still reaches them.
func exploreCodeDefinitionKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindFunction, graph.KindMethod, graph.KindType,
		graph.KindInterface, graph.KindField, graph.KindConstant,
		graph.KindVariable, graph.KindEnumMember, graph.KindMacro:
		return true
	default:
		return false
	}
}

// exploreDataDefinitionKind identifies leaf declarations whose repeated
// generic names carry little extra localization information. These remain real
// edit targets: the first/highest-ranked occurrence stays in place, and every
// occurrence stays available below the diversified head.
func exploreDataDefinitionKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindField, graph.KindConstant, graph.KindVariable, graph.KindEnumMember:
		return true
	default:
		return false
	}
}

func exploreShouldDiversifyByFile(class rerank.QueryClass) bool {
	return class == rerank.QueryClassConcept
}

// exploreCandidateFetchLimit leaves enough headroom for concept localization
// to survive generic exact-name collisions and non-localizable graph nodes
// before filtering and diversification. Literal identifier/path queries keep
// the tighter historical window because their exact ordering is the intent.
func exploreCandidateFetchLimit(maxSymbols int, class rerank.QueryClass) int {
	factor := 4
	if class == rerank.QueryClassConcept {
		factor = 8
	}
	return clampInt(maxSymbols*factor, maxSymbols, 80)
}

// diversifyRepeatedExploreNames performs stable, bounded name diversification
// for concept queries. One data declaration and two callable/type definitions
// per normalized name remain in the head; every overflow candidate is retained
// behind distinct concepts. Keeping two callables preserves useful overload or
// receiver families without letting a common method such as Validate consume
// the whole neighborhood.
func diversifyRepeatedExploreNames(cands []*rerank.Candidate, class rerank.QueryClass) []*rerank.Candidate {
	if class != rerank.QueryClassConcept || len(cands) < 2 {
		return cands
	}
	seen := make(map[string]int, len(cands))
	duplicates := make([]*rerank.Candidate, 0)
	head := cands[:0]
	for _, cand := range cands {
		if cand == nil || cand.Node == nil {
			head = append(head, cand)
			continue
		}
		name := strings.ToLower(strings.TrimSpace(cand.Node.Name))
		if name == "" {
			head = append(head, cand)
			continue
		}
		limit := 2
		if exploreDataDefinitionKind(cand.Node.Kind) {
			limit = 1
		}
		if seen[name] >= limit {
			duplicates = append(duplicates, cand)
			continue
		}
		seen[name]++
		head = append(head, cand)
	}
	return append(head, duplicates...)
}

// exploreLocalizableKind reports whether a node kind is a place a
// developer would actually edit to resolve a report — the localization
// targets. Params, locals, closures, generic params, imports and file
// nodes are structurally never edit targets, so they are dropped from
// both the ranked candidate set and the call-path rings.
func exploreLocalizableKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindParam, graph.KindLocal, graph.KindClosure,
		graph.KindGenericParam, graph.KindImport, graph.KindFile:
		return false
	default:
		return true
	}
}

// ringNeighbors filters a traversal result's nodes to real neighbors (not
// the focus node itself, not param/local/import noise), capped.
func ringNeighbors(nodes []*graph.Node, selfID string, cap int) []*graph.Node {
	out := make([]*graph.Node, 0, cap)
	for _, n := range nodes {
		if n == nil || n.ID == selfID || !exploreLocalizableKind(n.Kind) {
			continue
		}
		out = append(out, n)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// joinNeighbors renders a neighbor ring as "name (path:line), name (path:line)".
func joinNeighbors(nodes []*graph.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parts = append(parts, fmt.Sprintf("%s (%s)", n.Name, nodeLoc(n)))
	}
	return strings.Join(parts, ", ")
}

// nodeLoc is the citeable "path:startLine-endLine" (or "path:line") location.
func nodeLoc(n *graph.Node) string {
	path := nodeDisplayPath(n)
	if n.EndLine > n.StartLine {
		return fmt.Sprintf("%s:%d-%d", path, n.StartLine, n.EndLine)
	}
	if n.StartLine > 0 {
		return fmt.Sprintf("%s:%d", path, n.StartLine)
	}
	return path
}

// nodeDisplayPath is the repo-relative file path (the scorer's suffix-match
// target and the agent's citeable path).
func nodeDisplayPath(n *graph.Node) string {
	if n.FilePath != "" {
		return n.FilePath
	}
	return n.AbsoluteFilePath
}

// fenceLang maps a node language to a Markdown fence label (best-effort).
func fenceLang(lang string) string {
	if lang == "" {
		return ""
	}
	return lang
}

// stripLeadingExploreDirective removes generic agent-task verbs from the start
// of a multi-word concept query. These verbs describe what the agent should do,
// not the subsystem being localized; letting an exact symbol named Audit or
// Validate own that token can otherwise dominate every domain term. Identifier
// and short queries are left untouched by the caller/query-length guard.
func stripLeadingExploreDirective(query string) string {
	fields := strings.Fields(query)
	if len(fields) < 4 {
		return query
	}
	word := func(s string) string {
		return strings.ToLower(strings.Trim(s, "\"'`.,;:()[]{}<>!?—-"))
	}
	fillers := map[string]struct{}{
		"please": {}, "can": {}, "could": {}, "would": {}, "you": {}, "help": {}, "me": {}, "to": {},
	}
	directives := map[string]struct{}{
		"audit": {}, "check": {}, "diagnose": {}, "explain": {}, "find": {}, "fix": {},
		"implement": {}, "improve": {}, "investigate": {}, "review": {}, "trace": {},
		"understand": {}, "update": {}, "validate": {},
	}
	i := 0
	for i < len(fields) && i < 4 {
		if _, ok := fillers[word(fields[i])]; !ok {
			break
		}
		i++
	}
	if i >= len(fields) {
		return query
	}
	if _, ok := directives[word(fields[i])]; !ok {
		return query
	}
	i++
	if i < len(fields) && (word(fields[i]) == "and" || word(fields[i]) == "then") {
		i++
		if i < len(fields) {
			if _, ok := directives[word(fields[i])]; ok {
				i++
			}
		}
	}
	if len(fields)-i < 2 {
		return query
	}
	return strings.Join(fields[i:], " ")
}

// Query-shaping tuning. Generic structural thresholds — no corpus-specific
// vocabulary or benchmark-derived terms.
const (
	// shapeMinReportChars is the size above which a multi-line task is
	// treated as a pasted report worth distilling. Below it (or single-line)
	// the task is a focused query and passes through untouched.
	shapeMinReportChars = 300
	// shapeBodyMaxRunes bounds the distilled body so its bulk cannot re-drown
	// the weighted lead under BM25 length normalisation.
	shapeBodyMaxRunes = 400
	// shapeMinLineWords drops lines shorter than this many words — the
	// environment answers ("14.1.0", "Cargo", "macOS 26.5") and one-word
	// section headers a report body is padded with.
	shapeMinLineWords = 4
)

var (
	// A fenced code block: repro commands, log dumps, stack traces, sample
	// code. High-noise for LOCALIZATION (it names the invocation, not the
	// fix site), so it is removed before the body is distilled.
	reFenceBlock = regexp.MustCompile("(?s)```.*?```")
	reInlineCode = regexp.MustCompile("`[^`]*`")
	reURL        = regexp.MustCompile(`https?://\S+`)
	// Collapse runs of whitespace (incl. newlines) to single spaces.
	reWhitespace = regexp.MustCompile(`\s+`)
)

// shapeExploreQuery distils a pasted issue/report into the query that best
// localizes it, using only markdown/text structure — no vocabulary, no
// language model, nothing derived from any task set.
//
// The problem it solves: an agent commonly pastes a whole issue as the task —
// a one-line title followed by a body of repro commands, stack traces,
// environment tables and issue-template prompts. Fed raw to BM25 the body's
// command-line flags and log lines out-weigh the single defect sentence and
// pull ranking toward the flag-definition / entry-point files rather than the
// fix site. The fix is structural de-noising:
//
//   - the first non-empty line is the lead (a report's headline) and is
//     repeated once so its high-signal tokens are not drowned by the body;
//   - fenced code blocks, inline code and URLs are dropped from the body;
//   - body lines that are markdown headers/quotes, issue-template prompts
//     (they end in "?") or too short to be prose (environment answers, section
//     labels) are dropped;
//   - the surviving prose is bounded so it cannot re-drown the lead.
//
// A single-line (or short multi-line) task takes the inline path instead:
// structural noise tokens — commit-SHA-shaped hex, bare version strings —
// are dropped (they can never name a code symbol) and, when the query has
// clause structure, its lead clause is repeated so the headline's tokens
// out-weigh the trailing detail. A clean prose/identifier query with no
// noise tokens is returned byte-for-byte unchanged.
func shapeExploreQuery(task string) string {
	trimmed := strings.TrimSpace(task)
	// Focused / inline query: single line, or too short to carry a report
	// body worth distilling. Token-level shaping only, gated on the
	// presence of structural noise; a clean query passes through
	// untouched.
	if !strings.ContainsAny(trimmed, "\n\r") || len(trimmed) < shapeMinReportChars {
		return shapeInlineQuery(task)
	}

	// Lead = the first non-empty line (the report's headline).
	lead := ""
	rest := trimmed
	for _, ln := range strings.Split(trimmed, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			lead = s
			if idx := strings.Index(trimmed, ln); idx >= 0 {
				rest = trimmed[idx+len(ln):]
			}
			break
		}
	}

	body := shapeReportBody(rest)
	// Weight the lead by repeating it once, then append the distilled body.
	shaped := lead + ". " + lead
	if body != "" {
		shaped += ". " + body
	}
	return strings.TrimSpace(reWhitespace.ReplaceAllString(shaped, " "))
}

// shapeReportBody strips code / URLs and boilerplate lines from a report body
// and returns the surviving prose, whitespace-collapsed and rune-bounded.
func shapeReportBody(body string) string {
	body = reFenceBlock.ReplaceAllString(body, " ")
	body = reInlineCode.ReplaceAllString(body, " ")
	body = reURL.ReplaceAllString(body, " ")
	var keep []string
	for _, ln := range strings.Split(body, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "#") || strings.HasPrefix(s, ">") {
			continue // markdown header / block quote
		}
		if strings.HasSuffix(s, "?") {
			continue // issue-template prompt ("What version are you using?")
		}
		if len(strings.Fields(s)) < shapeMinLineWords {
			continue // environment answer / one-word section label
		}
		keep = append(keep, s)
	}
	prose := reWhitespace.ReplaceAllString(strings.Join(keep, " "), " ")
	prose = strings.TrimSpace(prose)
	if r := []rune(prose); len(r) > shapeBodyMaxRunes {
		prose = strings.TrimSpace(string(r[:shapeBodyMaxRunes]))
	}
	return prose
}

// Inline (single-line) query shaping. An agent that paraphrases a report
// into one line carries the report's noise with it — command-line flag
// tokens, quoted pattern/regex literals, commit-SHA hex, bare version
// strings. Those token classes are structurally detectable with no
// vocabulary, and their presence marks the query as report-derived
// rather than hand-focused, so it is worth shaping. Measured behavior on
// report-derived queries drove the transform's shape:
//
//   - commit-SHA-shaped hex and bare version strings are DROPPED — they
//     can never name a code symbol, so they are pure ranking noise;
//   - the lead clause (text before the first ";", " - " or ": "
//     separator) is REPEATED once — the same headline-weighting the
//     report path applies to a title, since a paraphrase puts the defect
//     statement first and trailing detail after a separator;
//   - flag tokens and quoted literals are left VERBATIM: the FTS
//     tokenizer already reads "--word" as the bare word, and measurement
//     showed that removing or down-weighting them costs rank (a flag
//     name carries the feature vocabulary; a quoted literal can carry
//     the only discriminating token) — they serve as the trigger, not
//     as targets.
//
// A query with none of these token classes is returned byte-for-byte
// unchanged.
const (
	// shapeInlineMinLeadChars is the minimum length for a lead clause to
	// be worth repeating — below it the "clause" is a sentence fragment
	// whose repetition would over-weight one or two words.
	shapeInlineMinLeadChars = 20
	// shaMinHexLen / shaMaxHexLen bound a commit-SHA-shaped token: git
	// abbreviates to >=7 hex chars; a full SHA-1 is 40.
	shaMinHexLen = 7
	shaMaxHexLen = 40
)

var (
	// A command-line flag token: "--long-flag" or a lone "-x", token-
	// initial (start or whitespace) so hyphenated prose ("case-insensitive",
	// "re-searches") never matches.
	reInlineFlag = regexp.MustCompile(`(?:^|\s)(?:--[A-Za-z][A-Za-z0-9_-]*|-[A-Za-z])(?:[\s,.;:]|$)`)
	// A hex run in the SHA length band. Candidates are verified in code
	// (must contain both a digit and a hex letter) because RE2 has no
	// lookahead — that check keeps decimal numbers (issue ids) and
	// letter-only words out.
	reInlineHexRun = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// A bare version string: 1.9.0, v2.14.1, 14.1 — dotted digits with an
	// optional leading v. Never a symbol name.
	reInlineVersion = regexp.MustCompile(`\bv?\d+\.\d+(?:\.\d+)*\b`)
	// A double-quoted span on one line ("e.x|ex", "slice index ...").
	reInlineQuoted = regexp.MustCompile(`"[^"\n]*"`)
)

// shapeInlineQuery is the single-line/short-query arm of
// shapeExploreQuery: drop provably-inert tokens and weight the lead
// clause, gated on structural noise so a clean query is untouched.
func shapeInlineQuery(task string) string {
	if !hasInlineNoise(task) {
		return task
	}
	cleaned := dropInertTokens(task)
	if lead := inlineLeadClause(cleaned); lead != "" {
		cleaned += " " + lead
	}
	return cleaned
}

// hasInlineNoise reports whether the query carries any structurally-
// detectable report noise: a flag token, a commit-SHA-shaped hex token,
// a bare version string, or a quoted pattern/regex literal.
func hasInlineNoise(task string) bool {
	if reInlineFlag.MatchString(task) || reInlineVersion.MatchString(task) {
		return true
	}
	for _, m := range reInlineHexRun.FindAllString(task, -1) {
		if isSHAToken(m) {
			return true
		}
	}
	for _, m := range reInlineQuoted.FindAllString(task, -1) {
		if quotedLiteralIsNoise(strings.Trim(m, `"`)) {
			return true
		}
	}
	return false
}

// isSHAToken verifies a hex-run candidate looks like a commit hash: the
// SHA length band plus at least one digit AND one hex letter, so a
// decimal number (an issue id) or a letter-only word never qualifies.
func isSHAToken(s string) bool {
	if len(s) < shaMinHexLen || len(s) > shaMaxHexLen {
		return false
	}
	hasDigit, hasLetter := false, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'a' && r <= 'f':
			hasLetter = true
		}
	}
	return hasDigit && hasLetter
}

// quotedLiteralIsNoise classifies a quoted span's content: regex/pattern
// literals (they carry regex metacharacters, or are short non-alphabetic
// fragments like "e-x") are noise; a quoted plain word or prose phrase
// ("setState", an error message) is signal and never triggers shaping.
func quotedLiteralIsNoise(content string) bool {
	if strings.ContainsAny(content, `|\^$*+?[]{}()<>/=~`) {
		return true
	}
	if len(content) <= 5 {
		for _, r := range content {
			if !unicodeIsLetter(r) {
				return true
			}
		}
	}
	return false
}

// unicodeIsLetter uses Unicode categories so quoted identifiers and locale
// values remain useful retrieval anchors in every indexed language.
func unicodeIsLetter(r rune) bool {
	return unicode.IsLetter(r)
}

func exploreLiteralWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// exploreTextHasExactLiteral distinguishes a true quoted-literal occurrence
// from content_fts prefix recall (for example, `ku` must not be considered an
// exact hit merely because the body contains `kubernetes`). SearchContent
// snippets are unhighlighted, so a bounded case-folded substring scan with
// Unicode-aware word boundaries is sufficient and does not read source again.
func exploreTextHasExactLiteral(text, literal string) bool {
	literal = strings.TrimSpace(literal)
	if text == "" || literal == "" {
		return false
	}
	text = strings.ToLower(text)
	literal = strings.ToLower(literal)
	first, _ := utf8.DecodeRuneInString(literal)
	last, _ := utf8.DecodeLastRuneInString(literal)
	for offset := 0; offset <= len(text)-len(literal); {
		relative := strings.Index(text[offset:], literal)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(literal)
		beforeOK := true
		if start > 0 && exploreLiteralWordRune(first) {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			beforeOK = !exploreLiteralWordRune(previous)
		}
		afterOK := true
		if end < len(text) && exploreLiteralWordRune(last) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			afterOK = !exploreLiteralWordRune(next)
		}
		if beforeOK && afterOK {
			return true
		}
		offset = start + 1
	}
	return false
}

// dropInertTokens removes commit-SHA-shaped hex and bare version tokens
// and collapses the leftover whitespace. Nothing else is touched.
func dropInertTokens(task string) string {
	out := reInlineHexRun.ReplaceAllStringFunc(task, func(m string) string {
		if isSHAToken(m) {
			return ""
		}
		return m
	})
	out = reInlineVersion.ReplaceAllString(out, "")
	return strings.TrimSpace(reWhitespace.ReplaceAllString(out, " "))
}

// inlineLeadClause returns the query's lead clause — the text before the
// first ";", " - " or ": " separator (":" only when single, so a
// namespaced identifier's "::" never splits) — when that lead is a
// proper, non-trivial prefix of the query. Empty when the query has no
// clause structure worth weighting.
func inlineLeadClause(task string) string {
	t := strings.TrimSpace(task)
	end := -1
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case ';':
			end = i
		case '-':
			// " - " — a spaced dash, not a hyphen or a flag.
			if i > 0 && t[i-1] == ' ' && i+1 < len(t) && t[i+1] == ' ' {
				end = i
			}
		case ':':
			// ": " with a non-colon before it — "walk: a scoped" splits,
			// "ignore::WalkBuilder" does not.
			if i > 0 && t[i-1] != ':' && t[i-1] != ' ' && i+1 < len(t) && t[i+1] == ' ' {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return ""
	}
	lead := strings.TrimSpace(t[:end])
	if len(lead) < shapeInlineMinLeadChars || len(lead) >= len(t) {
		return ""
	}
	return lead
}

const (
	exploreContentRecallRankSignal  = "explore_content_rank"
	exploreContentRecallTermSignal  = "explore_content_terms"
	exploreContentRecallExactSignal = "explore_content_exact"
	exploreQuotedRecallMaxTerms     = 3
	exploreQuotedRecallMaxPerTerm   = 12
)

// exploreQuotedRecallTerms extracts only explicit, high-signal literal anchors
// from prose. The ordinary symbol corpus intentionally excludes function bodies;
// these literals are the bounded bridge to the existing source-content FTS for
// errors, configuration values, protocol names, and other evidence that exists
// only inside an implementation. Regex/pattern literals remain in the shaped
// symbol query but are not sent to content recall because their punctuation
// decomposes into noisy one-character terms.
func exploreQuotedRecallTerms(task string) []string {
	literals := make([]string, 0, exploreQuotedRecallMaxTerms)
	for _, match := range reInlineQuoted.FindAllString(task, -1) {
		if len(match) >= 2 {
			literals = append(literals, match[1:len(match)-1])
		}
	}
	// Backticks are common in agent-written tasks for exact config values and
	// error fragments. Scan them without treating apostrophes in prose as quote
	// delimiters.
	for rest := task; ; {
		start := strings.IndexByte(rest, '`')
		if start < 0 {
			break
		}
		rest = rest[start+1:]
		end := strings.IndexByte(rest, '`')
		if end < 0 {
			break
		}
		literals = append(literals, rest[:end])
		rest = rest[end+1:]
	}

	seen := make(map[string]struct{}, len(literals))
	out := make([]string, 0, min(len(literals), exploreQuotedRecallMaxTerms))
	for _, literal := range literals {
		literal = strings.TrimSpace(literal)
		if len(literal) < 2 || len(literal) > 128 || quotedLiteralIsNoise(literal) {
			continue
		}
		key := strings.ToLower(literal)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, literal)
		if len(out) >= exploreQuotedRecallMaxTerms {
			break
		}
	}
	return out
}

// gatherExploreQuotedContentCandidates performs at most three bounded indexed
// point searches. It never scans source files and never materializes bodies:
// content_fts returns symbol IDs, then one batched graph lookup supplies nodes.
func (s *Server) gatherExploreQuotedContentCandidates(ctx context.Context, task string, limit int, scope query.QueryOptions) []*rerank.Candidate {
	if s == nil || s.graph == nil || ctx.Err() != nil {
		return nil
	}
	content, ok := s.graph.(graph.ContentSearcher)
	if !ok {
		return nil
	}
	terms := exploreQuotedRecallTerms(task)
	if len(terms) == 0 {
		return nil
	}
	perTerm := clampInt(limit/4, 4, exploreQuotedRecallMaxPerTerm)
	repoPrefix := ""
	if len(scope.RepoAllow) == 1 {
		for prefix, allowed := range scope.RepoAllow {
			if allowed {
				repoPrefix = prefix
			}
		}
	}
	if repoPrefix == "" {
		repoPrefix, _ = s.sessionLocality(ctx)
	}

	order := make([]string, 0, len(terms)*perTerm)
	bestRank := make(map[string]int, len(terms)*perTerm)
	termCount := make(map[string]int, len(terms)*perTerm)
	exactHit := make(map[string]bool, len(terms)*perTerm)
	for _, term := range terms {
		if ctx.Err() != nil {
			break
		}
		hits, err := content.SearchContent(term, repoPrefix, perTerm)
		if err != nil {
			continue
		}
		seenForTerm := make(map[string]struct{}, len(hits))
		for rank, hit := range hits {
			if hit.NodeID == "" {
				continue
			}
			if _, duplicate := seenForTerm[hit.NodeID]; duplicate {
				continue
			}
			seenForTerm[hit.NodeID] = struct{}{}
			if previous, exists := bestRank[hit.NodeID]; !exists {
				order = append(order, hit.NodeID)
				bestRank[hit.NodeID] = rank
			} else if rank < previous {
				bestRank[hit.NodeID] = rank
			}
			termCount[hit.NodeID]++
			if exploreTextHasExactLiteral(hit.Snippet, term) {
				exactHit[hit.NodeID] = true
			}
		}
	}
	if len(order) == 0 {
		return nil
	}
	nodes := s.graph.GetNodesByIDs(order)
	candidates := make([]*rerank.Candidate, 0, len(order))
	for _, id := range order {
		node := nodes[id]
		if node == nil || !scope.ScopeAllows(node) {
			continue
		}
		rank := bestRank[id]
		signals := map[string]float64{
			exploreContentRecallRankSignal: 1 / float64(rank+1),
			exploreContentRecallTermSignal: float64(termCount[id]),
		}
		if exactHit[id] {
			signals[exploreContentRecallExactSignal] = 1
		}
		candidates = append(candidates, &rerank.Candidate{
			Node:       node,
			TextRank:   rank,
			VectorRank: -1,
			Signals:    signals,
		})
	}
	return candidates
}

// rerankExploreConceptCoverage corrects the principal weakness of prefix-OR
// symbol retrieval: one rare identifier can otherwise outrank an implementation
// whose metadata covers several independent task concepts. Explicit anchors and
// bounded exact body-literal hits remain strongest. Vector-backed candidates
// retain their semantic ordering; coverage only reorders lexical-only rows.
func rerankExploreConceptCoverage(query string, candidates []*rerank.Candidate) []*rerank.Candidate {
	if rerank.ClassifyQuery(query) != rerank.QueryClassConcept || len(candidates) < 2 {
		return candidates
	}
	queryTerms := exploreTerminalTerms(query)
	if len(queryTerms) < 2 {
		return candidates
	}
	type evidence struct {
		explicit     bool
		exactContent bool
		overlap      int
		contentTerms float64
		contentRank  float64
	}
	metrics := make(map[*rerank.Candidate]evidence, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.Node == nil {
			continue
		}
		retrieval := candidate.Node.RetrievalMetadata()
		doc := retrieval.Doc
		if len(doc) > 768 {
			doc = doc[:768]
		}
		// Candidate-side token maps are disproportionately expensive on the
		// 80-row over-fetch window. Query terms are already normalized and at
		// least three bytes long; a lowercase retrieval projection plus bounded
		// substring checks preserves camelCase/path matching with no per-term
		// allocations.
		text := strings.ToLower(strings.Join([]string{
			candidate.Node.Name, retrieval.QualName, nodeDisplayPath(candidate.Node),
			retrieval.Signature, doc,
		}, " "))
		overlap := 0
		for term := range queryTerms {
			if strings.Contains(text, term) {
				overlap++
			}
		}
		metric := evidence{explicit: exploreLocalizationExplicitAnchor(query, candidate.Node), overlap: overlap}
		if candidate.Signals != nil {
			metric.contentTerms = candidate.Signals[exploreContentRecallTermSignal]
			metric.contentRank = candidate.Signals[exploreContentRecallRankSignal]
			metric.exactContent = candidate.Signals[exploreContentRecallExactSignal] > 0
		}
		metrics[candidate] = metric
	}
	coverageTier := func(overlap int) int {
		switch {
		case overlap >= 4:
			return 3
		case overlap >= 2:
			return 2
		case overlap == 1:
			return 1
		default:
			return 0
		}
	}
	priorityTier := func(candidate *rerank.Candidate) int {
		metric := metrics[candidate]
		if metric.explicit {
			return 2
		}
		if metric.exactContent {
			return 1
		}
		return 0
	}
	// Explicit anchors and verified full quoted literals may cross semantic
	// candidates. All other coverage evidence only reorders lexical-only slots,
	// preserving both the positions and relative order learned by the vector
	// pipeline.
	sort.SliceStable(candidates, func(i, j int) bool {
		return priorityTier(candidates[i]) > priorityTier(candidates[j])
	})
	priorityEnd := 0
	for priorityEnd < len(candidates) && priorityTier(candidates[priorityEnd]) > 0 {
		priorityEnd++
	}
	positions := make([]int, 0, len(candidates)-priorityEnd)
	lexical := make([]*rerank.Candidate, 0, len(candidates)-priorityEnd)
	for i := priorityEnd; i < len(candidates); i++ {
		candidate := candidates[i]
		if candidate != nil && candidate.VectorRank < 0 {
			positions = append(positions, i)
			lexical = append(lexical, candidate)
		}
	}
	sort.SliceStable(lexical, func(i, j int) bool {
		a, b := lexical[i], lexical[j]
		am, bm := metrics[a], metrics[b]
		if am.contentTerms != bm.contentTerms {
			return am.contentTerms > bm.contentTerms
		}
		if am.contentRank != bm.contentRank {
			return am.contentRank > bm.contentRank
		}
		at, bt := coverageTier(am.overlap), coverageTier(bm.overlap)
		if at != bt {
			return at > bt
		}
		if am.overlap != bm.overlap {
			return am.overlap > bm.overlap
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return false
	})
	for i, position := range positions {
		candidates[position] = lexical[i]
	}
	return candidates
}

// mergeExploreCandidates unions retrieval passes by symbol ID without mutating
// either input. Expansion text ranks are offset so a reduced concept bag can
// add recall without claiming the same authority as the original query.
func mergeExploreCandidates(primary, expanded []*rerank.Candidate, expansionRankOffset int) []*rerank.Candidate {
	if expansionRankOffset < 0 {
		expansionRankOffset = 0
	}
	byID := make(map[string]*rerank.Candidate, len(primary)+len(expanded))
	out := make([]*rerank.Candidate, 0, len(primary)+len(expanded))
	add := func(candidate *rerank.Candidate, expansion bool) {
		if candidate == nil || candidate.Node == nil {
			return
		}
		clone := *candidate
		if expansion && clone.TextRank >= 0 {
			clone.TextRank += expansionRankOffset
		}
		if current, ok := byID[clone.Node.ID]; ok {
			// A primary lexical rank always wins. Expansion may add a weaker
			// lexical signal to a semantic-only primary candidate.
			if clone.TextRank >= 0 && current.TextRank < 0 {
				current.TextRank = clone.TextRank
			} else if !expansion && clone.TextRank >= 0 && clone.TextRank < current.TextRank {
				current.TextRank = clone.TextRank
			}
			if clone.VectorRank >= 0 && (current.VectorRank < 0 || clone.VectorRank < current.VectorRank) {
				current.VectorRank = clone.VectorRank
			}
			// Request-local recall annotations survive dedup when a source-body
			// hit names a symbol already present in the ordinary metadata channel.
			for _, key := range []string{exploreContentRecallRankSignal, exploreContentRecallTermSignal, exploreContentRecallExactSignal} {
				if clone.Signals == nil || clone.Signals[key] <= 0 {
					continue
				}
				if current.Signals == nil {
					current.Signals = make(map[string]float64, 2)
				}
				if clone.Signals[key] > current.Signals[key] {
					current.Signals[key] = clone.Signals[key]
				}
			}
			return
		}
		byID[clone.Node.ID] = &clone
		out = append(out, &clone)
	}
	for _, candidate := range primary {
		add(candidate, false)
	}
	for _, candidate := range expanded {
		add(candidate, true)
	}
	return out
}

// hasExploreExpansionTerms reports whether concept normalization introduced a
// genuinely new discriminative term. A subset/equivalent bag would repeat the
// same FTS and materialization work without adding a retrieval channel.
func hasExploreExpansionTerms(query string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	raw := rerank.Tokenize(query)
	base := make(map[string]struct{}, len(raw)*2)
	for _, token := range raw {
		base[token] = struct{}{}
		base[exploreTerminalTermRoot(token)] = struct{}{}
	}
	for _, token := range search.NormalizeFTSTokens(raw) {
		base[token] = struct{}{}
	}
	for _, term := range search.NormalizeFTSTokens(terms) {
		if _, ok := base[term]; !ok {
			return true
		}
	}
	return false
}

// limitExploreCandidates cheaply fuses text and vector ranks before the full
// graph-aware reranker. It keeps vector-only evidence while bounding centrality
// and edge-hydration work to a predictable multiple of the response size.
func limitExploreCandidates(candidates []*rerank.Candidate, limit int) []*rerank.Candidate {
	if limit <= 0 || len(candidates) <= limit {
		return candidates
	}
	bounded := append([]*rerank.Candidate(nil), candidates...)
	rrf := func(candidate *rerank.Candidate) float64 {
		if candidate == nil || candidate.Node == nil {
			return -1
		}
		score := 0.0
		if candidate.TextRank >= 0 {
			score += 1 / (60 + float64(candidate.TextRank+1))
		}
		if candidate.VectorRank >= 0 {
			score += 1 / (60 + float64(candidate.VectorRank+1))
		}
		return score
	}
	sort.SliceStable(bounded, func(i, j int) bool {
		return rrf(bounded[i]) > rrf(bounded[j])
	})
	return bounded[:limit]
}

// exploreConceptRecallTerms preserves the query's discriminative concepts in
// first-seen order for the ordinary concept recall channel. The same generic
// and stopword filter used by answer-readiness keeps agent verbs and task
// boilerplate from consuming the bounded expansion bag.
func exploreConceptRecallTerms(text string) []string {
	const maxTerms = 12
	allowed := exploreTerminalTerms(text)
	seen := make(map[string]struct{}, len(allowed))
	out := make([]string, 0, min(len(allowed), maxTerms))
	for _, raw := range rerank.Tokenize(text) {
		term := exploreTerminalTermRoot(strings.ToLower(strings.TrimSpace(raw)))
		if _, ok := allowed[term]; !ok {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
		if len(out) >= maxTerms {
			break
		}
	}
	return out
}

// exploreLexicalTerms splits free task text into the distinct word/identifier
// terms (length >= 3, capped) that feed the per-term BM25 OR-merge fallback.
// Purely lexical — no vocabulary, no language model.
func exploreLexicalTerms(task string) []string {
	const maxTerms = 12
	seen := map[string]struct{}{}
	var out []string
	for _, f := range strings.Fields(task) {
		f = strings.Trim(f, "\"'`.,;:()[]{}<>!?—-")
		if len(f) < 3 {
			continue
		}
		key := strings.ToLower(f)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
		if len(out) >= maxTerms {
			break
		}
	}
	return out
}

func estimateTokens(s string) int { return len(s) / exploreCharsPerToken }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func truncateOneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
