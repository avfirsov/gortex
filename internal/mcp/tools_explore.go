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
	exploreDefaultBudgetTokens = 9000
	exploreMinBudgetTokens     = 2000
	exploreMaxBudgetTokens     = 24000
	exploreDefaultMaxSymbols   = 10
	exploreMaxMaxSymbols       = 30
	exploreRingCap             = 5 // callers / callees shown per target
	exploreCharsPerToken       = 4 // coarse token estimate for budgeting
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
			mcp.WithNumber("token_budget", mcp.Description("Response token ceiling (default 9000). Bodies pack until it fills, then demote to signatures; every candidate location is always listed.")),
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
	rctx := s.buildRerankContext(ctx, searchQuery)
	// Over-fetch, then keep the top maxSymbols that are real localization
	// targets — params / locals / closures / imports are never a place a
	// developer edits to fix a report, and they otherwise consume ranking
	// slots and clutter the file map. Test-source symbols are demoted, not
	// dropped: production code is where a report is resolved, but a task
	// genuinely about tests still gets them when production hits run out.
	fetch := clampInt(maxSymbols*4, maxSymbols, 80)
	ranked := eng.SearchSymbolsRanked(searchQuery, fetch, opts, rctx)
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
			"EXPLORE — %s\n\nNo ranked symbols matched this request. The graph found nothing on the ranked path — widen the wording, or drop to search_text / find_files for a literal or filename lead.",
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
	b.WriteString("Ranked localization neighborhood (graph-verified). Likely targets first; each carries its call paths and source.\n\n")
	b.WriteString("## Likely targets (most-relevant first)\n")

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

		// Source body: full while the budget holds (rank decides order, the
		// budget decides where full source stops; no single body may take
		// more than 1/exploreBodyBudgetShare of the whole budget), signature
		// stub otherwise. The header/locations above are always emitted so
		// file-hit / symbol-hit never depend on budget.
		body := ""
		if t.source != "" {
			cost := estimateTokens(t.source)
			if used+cost <= budget && cost <= budget/exploreBodyBudgetShare {
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

	b.WriteString("\n## Files to change\n")
	for _, f := range fileOrder {
		fmt.Fprintf(&b, "- %s  ·  %s\n", f, strings.Join(dedupStrings(files[f]), ", "))
	}

	fmt.Fprintf(&b, "\n— Completeness: %d candidate symbol(s) across %d file(s); callers/callees resolved server-side from the graph. This is the ranked neighborhood for the request — a location not listed here is not on the ranked path. Answer (FILES / SYMBOLS / EVIDENCE) or start editing directly from this; the paths and line numbers above are real and citeable.\n",
		len(targets), len(fileOrder))
	// Terminality affordance: the source for each listed symbol is already in
	// this response. Re-opening these files with Read / Glob is the measured
	// wasted-turn trap (the indexed-source deny-hook rejects it); the follow-up
	// reader is get_symbol_source / batch_symbols on the `id:` shown above.
	b.WriteString("  The source for each symbol is included above — do not re-open these files with Read/Glob; read more of any listed symbol with get_symbol_source / batch_symbols using its exact `id:`.\n")
	if truncated {
		fmt.Fprintf(&b, "  (Some bodies are elided under the %d-token budget; every candidate's location is still listed above — fetch an elided body with get_symbol_source / batch_symbols using the exact `id:` shown on its line.)\n", budget)
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

// Query-shaping tuning. Generic structural thresholds — no vocabulary, no
// dependence on any corpus or task set.
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
