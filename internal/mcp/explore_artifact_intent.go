package mcp

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/trigram"
)

const (
	exploreArtifactPathLimit    = 12
	exploreArtifactProbeLimit   = 2
	exploreArtifactTextHitLimit = 16
	exploreArtifactResultLimit  = 5
	exploreArtifactSnippetLimit = 8 << 10
)

type exploreArtifactIntent struct {
	active        bool
	explicitCount int
	semantic      bool
	paths         []string
	probes        []string
}

type exploreArtifactHit struct {
	file        *graph.Node
	path        string
	snippet     string
	declaration string
	pathHit     bool
	contentHit  bool
	fullPath    bool
	exactBase   string
	uniqueBase  bool
	score       int
}

type exploreArtifactLane struct {
	targets []exploreTarget
	ready   bool
}

var (
	exploreArtifactPathRE  = regexp.MustCompile(`[A-Za-z0-9_@+.-]*(?:[\\/][A-Za-z0-9_@+.-]+)+|[A-Za-z0-9_@+-]+(?:\.[A-Za-z0-9_@+-]+)+`)
	exploreArtifactProbeRE = regexp.MustCompile("`[^`\\n]{2,96}`|\\\"[^\\\"\\n]{2,96}\\\"|'[^'\\n]{2,96}'|(?:^|\\s)--?[A-Za-z][A-Za-z0-9_.-]{1,63}|\\b[A-Z][A-Z0-9_]{2,63}\\b")
	exploreArtifactCallRE  = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:(?:::|\.)[A-Za-z_][A-Za-z0-9_]*)?\s*\(`)
)

func classifyExploreArtifactIntent(task string) exploreArtifactIntent {
	var out exploreArtifactIntent
	seen := make(map[string]struct{})
	addPath := func(raw string) {
		raw = strings.Trim(raw, "`'\"()[]{}<>,;:")
		key := strings.ToLower(strings.ReplaceAll(raw, "\\", "/"))
		if !exploreArtifactFile(raw) || len(out.paths) == exploreArtifactPathLimit {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out.paths = append(out.paths, raw)
	}
	for _, raw := range exploreArtifactPathRE.FindAllString(task, -1) {
		addPath(raw)
	}
	for i, field := range strings.Fields(task) {
		if i == exploreArtifactPathLimit*4 {
			break
		}
		addPath(field) // extensionless Dockerfile/Makefile/etc.
	}
	out.explicitCount = len(out.paths)

	artifactScore, sourceScore := 0, 0
	for _, word := range exploreArtifactWords(task) {
		if exploreArtifactWord(word) {
			artifactScore++
			if exploreArtifactPathWord(word) && len(out.paths) < exploreArtifactPathLimit {
				if _, ok := seen[word]; !ok {
					seen[word] = struct{}{}
					out.paths = append(out.paths, word)
				}
			}
		}
		if exploreSourceWord(word) {
			sourceScore++
		}
	}
	for _, raw := range exploreArtifactPathRE.FindAllString(task, -1) {
		if exploreSourceExtension(filepath.Ext(raw)) {
			sourceScore += 2
		}
	}
	if exploreArtifactCallRE.MatchString(task) {
		sourceScore += 2
	}
	out.semantic = artifactScore >= 2
	for _, raw := range exploreArtifactProbeRE.FindAllString(task, -1) {
		probe := strings.TrimSpace(strings.Trim(raw, "`'\""))
		probe = strings.TrimSpace(probe)
		if len(probe) < 2 || exploreArtifactFile(probe) || strings.EqualFold(probe, "CI") {
			continue
		}
		key := strings.ToLower(probe)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.probes = append(out.probes, probe)
		if len(out.probes) == exploreArtifactProbeLimit {
			break
		}
	}
	out.active = (out.explicitCount > 0 || (sourceScore == 0 && out.semantic)) && (len(out.paths) > 0 || len(out.probes) > 0)
	return out
}

const (
	exploreArtifactNames      = "|dockerfile|makefile|justfile|gemfile|brewfile|cargo.toml|cargo.lock|go.mod|go.sum|package.json|package-lock.json|pnpm-lock.yaml|yarn.lock|pom.xml|directory.build.props|directory.build.targets|tsconfig.json|"
	exploreArtifactExtensions = "|.cfg|.conf|.config|.csproj|.editorconfig|.env|.fsproj|.gradle|.hcl|.ini|.json|.lock|.manifest|.props|.properties|.sln|.targets|.tf|.toml|.vbproj|.xml|.yaml|.yml|"
	exploreArtifactWordsSet   = "|artifact|artifacts|build|ci|configuration|config|coverage|deploy|deployment|environment|infra|infrastructure|manifest|package|pipeline|release|setting|settings|workflow|workflows|"
	exploreArtifactPathsSet   = "|ci|coverage|deployment|manifest|pipeline|release|workflow|workflows|"
	exploreSourceWordsSet     = "|callee|caller|class|constructor|function|handler|implementation|interface|method|parser|resolver|struct|symbol|trait|type|"
	exploreSourceExtsSet      = "|.c|.cc|.cpp|.cs|.dart|.ex|.exs|.go|.h|.hpp|.java|.js|.jsx|.kt|.lua|.php|.py|.rb|.rs|.scala|.swift|.ts|.tsx|"
)

func exploreInSet(set, value string) bool {
	return strings.Contains(set, "|"+strings.ToLower(value)+"|")
}

func exploreArtifactFile(value string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")))
	return exploreInSet(exploreArtifactNames, base) || base == ".env" || strings.HasPrefix(base, ".env.") || exploreInSet(exploreArtifactExtensions, filepath.Ext(base))
}

func exploreArtifactWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
}

func exploreArtifactWord(word string) bool     { return exploreInSet(exploreArtifactWordsSet, word) }
func exploreArtifactPathWord(word string) bool { return exploreInSet(exploreArtifactPathsSet, word) }
func exploreSourceWord(word string) bool       { return exploreInSet(exploreSourceWordsSet, word) }
func exploreSourceExtension(ext string) bool   { return exploreInSet(exploreSourceExtsSet, ext) }

// gatherExploreArtifactLane reuses search(files)' graph file nodes and
// search(text)'s trigram backend. The inactive path returns before either I/O.
func (s *Server) gatherExploreArtifactLane(ctx context.Context, intent exploreArtifactIntent, scope query.QueryOptions) exploreArtifactLane {
	if !intent.active || (len(intent.paths) == 0 && len(intent.probes) == 0) || s == nil || s.graph == nil || ctx.Err() != nil {
		return exploreArtifactLane{}
	}
	files := make([]*exploreArtifactHit, 0, 64)
	byPath := make(map[string]*exploreArtifactHit)
	for node := range s.graph.NodesByKind(graph.KindFile) {
		if node == nil || !s.nodeInSessionScope(ctx, node) || !scope.ScopeAllows(node) {
			continue
		}
		rel := repoRelativePath(node)
		hit := &exploreArtifactHit{file: node, path: rel}
		files = append(files, hit)
		key := strings.ToLower(strings.ReplaceAll(rel, "\\", "/"))
		byPath[key] = hit
		if node.RepoPrefix != "" && !strings.HasPrefix(key, strings.ToLower(node.RepoPrefix)+"/") {
			byPath[strings.ToLower(node.RepoPrefix)+"/"+key] = hit
		}
	}
	exactBasenames := make(map[string]int)
	for _, hit := range files {
		for i, term := range intent.paths {
			score, ok := scoreFilenameMatch(term, filepath.Base(hit.path), hit.path, false)
			if !ok {
				continue
			}
			hit.pathHit = true
			hit.score += score
			if i >= intent.explicitCount {
				continue
			}
			normalizedTerm := strings.TrimPrefix(strings.ReplaceAll(term, "\\", "/"), "./")
			normalizedHit := strings.TrimPrefix(strings.ReplaceAll(hit.path, "\\", "/"), "./")
			switch {
			case strings.Contains(normalizedTerm, "/") && strings.EqualFold(normalizedTerm, normalizedHit):
				hit.fullPath = true
				hit.score += 20
			case strings.EqualFold(filepath.Base(term), filepath.Base(hit.path)):
				hit.exactBase = strings.ToLower(filepath.Base(term))
				exactBasenames[hit.exactBase]++
				hit.score += 20
			}
		}
	}
	for _, hit := range files {
		hit.uniqueBase = hit.exactBase != "" && exactBasenames[hit.exactBase] == 1
	}
	sort.Slice(files, func(i, j int) bool { return files[i].score > files[j].score })
	if len(files) > exploreArtifactPathLimit {
		files = files[:exploreArtifactPathLimit]
	}
	kept := make(map[*graph.Node]*exploreArtifactHit, len(files))
	for _, hit := range files {
		if hit.pathHit {
			kept[hit.file] = hit
		}
	}

	for _, probe := range intent.probes {
		if ctx.Err() != nil || (s.multiIndexer == nil && s.indexer == nil) {
			break
		}
		var matches []trigram.Match
		if s.multiIndexer != nil && scope.RepoAllow != nil {
			matches = s.multiIndexer.GrepTextForRepos(probe, scope.RepoAllow, exploreArtifactTextHitLimit)
		} else if s.multiIndexer != nil {
			matches = s.multiIndexer.GrepText(probe, exploreArtifactTextHitLimit)
		} else {
			matches = s.indexer.GrepText(probe, exploreArtifactTextHitLimit)
		}
		for _, match := range s.enrichTextMatches(matches) {
			hit := byPath[strings.ToLower(strings.ReplaceAll(match.Path, "\\", "/"))]
			if hit == nil || !exploreArtifactFile(hit.path) {
				continue
			}
			hit.contentHit = true
			hit.score += 5
			hit.declaration = match.SymbolID
			if hit.snippet == "" {
				hit.snippet = truncateExploreArtifactSnippet(match.Text)
			}
			kept[hit.file] = hit
		}
	}
	results := make([]*exploreArtifactHit, 0, len(kept))
	for _, hit := range kept {
		results = append(results, hit)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].path < results[j].path
	})
	if len(results) > exploreArtifactResultLimit {
		results = results[:exploreArtifactResultLimit]
	}
	ids := make([]string, 0, len(results))
	for _, hit := range results {
		if hit.declaration != "" {
			ids = append(ids, hit.declaration)
		}
	}
	declarations := s.graph.GetNodesByIDs(ids)
	lane := exploreArtifactLane{targets: make([]exploreTarget, 0, len(results))}
	for _, hit := range results {
		node := hit.file
		if declaration := declarations[hit.declaration]; declaration != nil {
			node = declaration
		}
		lane.targets = append(lane.targets, exploreTarget{node: node, source: hit.snippet, score: float64(hit.score), exactContent: hit.contentHit})
	}
	runnerUp := 0
	if len(results) > 1 {
		runnerUp = results[1].score
	}
	if len(results) > 0 {
		lane.ready = exploreArtifactTerminal(intent, results[0], runnerUp)
	}
	return lane
}

func truncateExploreArtifactSnippet(snippet string) string {
	if len(snippet) <= exploreArtifactSnippetLimit {
		return snippet
	}
	return strings.ToValidUTF8(snippet[:exploreArtifactSnippetLimit], "")
}

func exploreArtifactTerminal(intent exploreArtifactIntent, best *exploreArtifactHit, runnerUp int) bool {
	if !intent.active || best == nil {
		return false
	}
	if best.fullPath || best.uniqueBase {
		return true
	}
	return (intent.semantic || intent.explicitCount > 0) && len(intent.probes) > 0 && best.pathHit && best.contentHit && best.score-runnerUp >= 5
}
