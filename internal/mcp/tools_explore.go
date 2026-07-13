package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

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

// exploreTarget is one ranked candidate plus its 1-hop neighborhood,
// gathered before rendering so the renderer can honour the token budget.
type exploreTarget struct {
	node    *graph.Node
	score   float64
	callers []*graph.Node
	callees []*graph.Node
	source  string // full body (may be empty for non-source kinds)
}

const (
	exploreDraftPrimaryLimit = 3
	exploreDraftTotalLimit   = 5
)

type exploreDraftEntry struct {
	node             *graph.Node
	evidence         string
	exact            bool
	overlap          int
	direct           bool
	structural       bool
	structuralShared int
	structuralLocal  bool
	parentExact      bool
	parentOverlap    int
	parentRank       int
}

// exploreAnswerDraft puts a small, ready-to-use evidence set before the full
// neighborhood. The ranked head always leads; the remaining slots promote
// query-aligned deeper targets or graph neighbors without hiding any detailed
// candidate below.
func exploreAnswerDraft(task string, targets []exploreTarget) []exploreDraftEntry {
	query := shapeExploreQuery(task)
	if rerank.ClassifyQuery(query) == rerank.QueryClassConcept {
		query = stripLeadingExploreDirective(query)
	}
	queryTerms := exploreTerminalTerms(query)
	makeEntry := func(n *graph.Node, evidence string, direct bool, parentRank int) (exploreDraftEntry, int) {
		overlap, longest := exploreDraftTermOverlap(queryTerms, n)
		return exploreDraftEntry{
			node:       n,
			evidence:   evidence,
			exact:      exploreDraftExactAnchor(query, n),
			overlap:    overlap,
			direct:     direct,
			parentRank: parentRank,
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
		entry, _ := makeEntry(targets[i].node, fmt.Sprintf("ranked #%d", i+1), true, i)
		appendEntry(entry)
	}

	var aligned, structuralCallers, structuralCallees []exploreDraftEntry
	consider := func(n *graph.Node, evidence string, direct bool, parentRank int) {
		if n == nil || (!direct && exploreDraftIsTestNode(n)) {
			return
		}
		entry, longest := makeEntry(n, evidence, direct, parentRank)
		if !entry.exact && entry.overlap < 2 && !(entry.overlap == 1 && longest >= 5) {
			return
		}
		aligned = append(aligned, entry)
	}
	for i, target := range targets {
		if i >= exploreDraftPrimaryLimit {
			consider(target.node, fmt.Sprintf("ranked #%d", i+1), true, i)
		}
		for _, n := range target.callers {
			consider(n, fmt.Sprintf("caller of ranked #%d", i+1), false, i)
		}
		for _, n := range target.callees {
			consider(n, fmt.Sprintf("callee of ranked #%d", i+1), false, i)
		}

		if target.node == nil {
			continue
		}
		parent, longest := makeEntry(target.node, "", true, i)
		strongParent := parent.exact || parent.overlap >= 2 || (parent.overlap == 1 && longest >= 5)
		if !strongParent || exploreIdentifierSegmentCount(target.node.Name) < 2 {
			continue
		}
		collectStructural := func(n *graph.Node, evidence string, dst *[]exploreDraftEntry) {
			if n == nil || exploreDraftIsTestNode(n) || (n.Kind != graph.KindFunction && n.Kind != graph.KindMethod) || exploreIdentifierSegmentCount(n.Name) < 2 {
				return
			}
			entry, _ := makeEntry(n, evidence, false, i)
			shared, local := exploreStructuralSignals(target.node, n)
			if shared == 0 && !entry.exact && entry.overlap == 0 && !local {
				return
			}
			entry.structural = true
			entry.structuralShared = shared
			entry.structuralLocal = local
			entry.parentExact = parent.exact
			entry.parentOverlap = parent.overlap
			*dst = append(*dst, entry)
		}
		for _, n := range target.callers {
			collectStructural(n, fmt.Sprintf("caller of ranked #%d", i+1), &structuralCallers)
		}
		for _, n := range target.callees {
			collectStructural(n, fmt.Sprintf("callee of ranked #%d", i+1), &structuralCallees)
		}
	}

	sort.SliceStable(aligned, func(i, j int) bool {
		return exploreDraftEntryLess(aligned[i], aligned[j])
	})
	sort.SliceStable(structuralCallers, func(i, j int) bool {
		return exploreStructuralEntryLess(structuralCallers[i], structuralCallers[j])
	})
	sort.SliceStable(structuralCallees, func(i, j int) bool {
		return exploreStructuralEntryLess(structuralCallees[i], structuralCallees[j])
	})
	appendFirst := func(candidates []exploreDraftEntry) {
		for _, entry := range candidates {
			if appendEntry(entry) {
				return
			}
		}
	}
	appendFirst(structuralCallers)
	appendFirst(structuralCallees)
	for _, entry := range aligned {
		if len(entries) >= exploreDraftTotalLimit {
			break
		}
		appendEntry(entry)
	}
	for i := exploreDraftPrimaryLimit; i < len(targets) && len(entries) < exploreDraftTotalLimit; i++ {
		entry, _ := makeEntry(targets[i].node, fmt.Sprintf("ranked #%d", i+1), true, i)
		appendEntry(entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return exploreDraftEntryLess(entries[i], entries[j])
	})
	return entries
}

func exploreDraftEntryLess(a, b exploreDraftEntry) bool {
	priority := func(entry exploreDraftEntry) int {
		switch {
		case entry.exact && entry.direct:
			return 0
		case entry.exact:
			return 1
		case entry.overlap > 0 && entry.direct:
			return 2
		case entry.overlap > 0:
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
	if a.structuralShared != b.structuralShared {
		return a.structuralShared > b.structuralShared
	}
	if a.exact != b.exact {
		return a.exact
	}
	if a.overlap != b.overlap {
		return a.overlap > b.overlap
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

func exploreDraftTermOverlap(queryTerms map[string]struct{}, n *graph.Node) (count, longest int) {
	if n == nil || len(queryTerms) == 0 {
		return 0, 0
	}
	text := n.Name + " " + n.QualName + " " + nodeDisplayPath(n)
	if sig, _ := n.Meta["signature"].(string); sig != "" {
		text += " " + sig
	}
	for term := range exploreTerminalTerms(text) {
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

func exploreDraftExactAnchor(query string, n *graph.Node) bool {
	if n == nil {
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

	matched := func(target exploreTarget) int {
		if target.node == nil {
			return 0
		}
		n := target.node
		text := n.Name + " " + n.QualName + " " + n.FilePath
		if sig, _ := n.Meta["signature"].(string); sig != "" {
			text += " " + sig
		}
		candidateTerms := exploreTerminalTerms(text)
		count := 0
		for term := range queryTerms {
			if _, ok := candidateTerms[term]; ok {
				count++
			}
		}
		return count
	}

	headMatches := matched(targets[0])
	union := make(map[string]struct{})
	for i := 0; i < len(targets) && i < 3; i++ {
		if targets[i].node == nil {
			continue
		}
		n := targets[i].node
		text := n.Name + " " + n.QualName + " " + n.FilePath
		if sig, _ := n.Meta["signature"].(string); sig != "" {
			text += " " + sig
		}
		for term := range exploreTerminalTerms(text) {
			if _, relevant := queryTerms[term]; relevant {
				union[term] = struct{}{}
			}
		}
	}

	// Paths, signatures, and identifier-shaped queries carry explicit anchors.
	if class == rerank.QueryClassPath || class == rerank.QueryClassSymbol || class == rerank.QueryClassSignature {
		return headMatches > 0
	}
	// Broad prompts need several independent anchors in the top result. This
	// keeps a multi-area audit from becoming terminal merely because one generic
	// word matched somewhere in the neighborhood.
	if len(queryTerms) > 10 {
		return headMatches >= 3 && len(union) >= 3
	}
	if len(queryTerms) == 1 {
		return headMatches == 1
	}
	if headMatches >= 1 && len(union) >= 2 {
		return true
	}
	// A sharply separated ranked head is useful supporting evidence, but only
	// when it also has a visible lexical anchor to the request.
	if headMatches > 0 && targets[0].score > 0 {
		if len(targets) == 1 || targets[1].score <= 0 || targets[0].score/targets[1].score >= 1.5 {
			return true
		}
	}
	return false
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
		if pipeline := eng.Rerank(); pipeline != nil {
			ranked = pipeline.Rerank(searchQuery, ranked, rctx)
		}
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
	_, prod = diversifyByFile(prodNodes, prod, defaultMaxPerFile)
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
	if len(cands) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"EXPLORE — %s\n\nNo ranked symbols matched this request. Widen the wording, use search(operation:\"text\", query:\"<literal>\") for a literal lead, or search(operation:\"files\", query:\"<filename>\") for a path lead.",
			truncateOneLine(task, 200))), nil
	}

	ringOpts := query.QueryOptions{Depth: 1, Limit: exploreRingCap * 3, Detail: "brief", WorkspaceID: resolved.WorkspaceID}
	targets := make([]exploreTarget, 0, len(cands))
	for _, c := range cands {
		if c == nil || c.Node == nil {
			continue
		}
		n := c.Node
		t := exploreTarget{node: n, score: c.Score}
		if callers := eng.GetCallers(n.ID, ringOpts); callers != nil {
			t.callers = ringNeighbors(callers.Nodes, n.ID, exploreRingCap)
		}
		if callees := eng.GetCallChain(n.ID, ringOpts); callees != nil {
			t.callees = ringNeighbors(callees.Nodes, n.ID, exploreRingCap)
		}
		t.source = s.manifestSymbolSource(ctx, n)
		targets = append(targets, t)
	}

	return mcp.NewToolResultText(s.renderExplore(task, targets, budget)), nil
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
	answerReady := exploreAnswerReady(task, targets)
	if answerReady {
		b.WriteString("LOCALIZATION COMPLETE: use the strongest supported rows below now. A files/symbols/evidence/where request is localization-only even when it describes a bug: answer now. For a requested implementation change, proceed directly to impact, edit, and test. Do not make another localization, search, or read call.\n\n")
	} else {
		b.WriteString("BEST-SUPPORTED LOCALIZATION: use the evidence below with stated uncertainty. If one decisive target is absent, make at most one focused exact-ID read or exact literal/path/symbol search, then stop localization. Do not fan out or rerun broad exploration.\n\n")
	}
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
	for _, entry := range draft {
		if !entry.direct || len(fullBodyIDs) >= exploreFullBodyLimit {
			continue
		}
		fullBodyIDs[exploreDraftNodeKey(entry.node)] = struct{}{}
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
	b.WriteString("END OF LOCALIZATION — answer from this evidence or proceed with the requested change; do not continue searching.\n")
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

// unicodeIsLetter is a tiny ASCII-fast letter check (the quoted-literal
// rubric only needs letter-vs-not).
func unicodeIsLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
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
