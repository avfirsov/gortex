package mcp

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/trigram"
)

const (
	exploreSourceLiteralRecallMaxHits  = 24
	exploreSourceLiteralRecallMaxFiles = 0
	exploreSourceLiteralRecallBudget   = 75 * time.Millisecond
)

type exploreSourceLiteralHit struct {
	nodeID string
	rank   int
}

type exploreSourceLiteralRecall struct {
	hits      []exploreSourceLiteralHit
	ambiguous bool
}

type exploreSourceLiteralSearch struct {
	matches          []trigram.Match
	incomplete       bool
	backend          string
	owned            bool
	lookupRepoPrefix string
}

// exploreHighestInformationQuotedLiteral picks one deterministic source-search
// key. Quoted terms have already passed the noise filter; rune length is a
// language-neutral proxy for selectivity and avoids multiplying repository
// scans when a task contains several literals.
func exploreHighestInformationQuotedLiteral(terms []string) string {
	best := ""
	bestLen := 0
	for _, term := range terms {
		if n := utf8.RuneCountInString(term); n > bestLen {
			best, bestLen = term, n
		}
	}
	return best
}

// gatherExploreSourceLiteralRecall reuses the bounded raw-text path behind
// search(operation:"text") only when content_fts could not produce an exact
// symbol candidate. It searches one repository and one literal, maps each
// 1-based line hit to the smallest enclosing code symbol, and returns IDs for
// the caller's existing single batch graph hydration.
func (s *Server) gatherExploreSourceLiteralRecall(
	ctx context.Context,
	terms []string,
	repoPrefix string,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	term := exploreHighestInformationQuotedLiteral(terms)
	if term == "" {
		return exploreSourceLiteralRecall{}
	}

	started := time.Now()
	searchCtx, cancelSearch := context.WithTimeout(ctx, exploreSourceLiteralRecallBudget)
	search := s.searchExploreSourceLiteral(searchCtx, term, repoPrefix, scope)
	searchErr := searchCtx.Err()
	cancelSearch()
	if ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}

	// Discovery and graph mapping have independent bounded phases. A backend
	// may return useful partial matches as its deadline expires; reusing that
	// expired context would discard those matches before they can be mapped to
	// enclosing symbols. The request context remains the parent bound, while
	// each phase gets the same small local budget.
	recall, mappingErr := s.mapDiscoveredExploreSourceLiteralMatches(
		ctx, term, search, scope, searchErr,
	)
	if s.logger != nil {
		contextError := ""
		if searchErr != nil {
			contextError = "search: " + searchErr.Error()
		}
		if mappingErr != nil {
			if contextError != "" {
				contextError += "; "
			}
			contextError += "mapping: " + mappingErr.Error()
		}
		firstMatchPath := ""
		if len(search.matches) > 0 {
			firstMatchPath = search.matches[0].Path
		}
		fields := []zap.Field{
			zap.Int("term_runes", utf8.RuneCountInString(term)),
			zap.String("requested_repo_prefix", repoPrefix),
			zap.String("lookup_repo_prefix", search.lookupRepoPrefix),
			zap.String("first_match_path", firstMatchPath),
			zap.String("backend", search.backend),
			zap.Bool("owned", search.owned),
			zap.Int("raw_matches", len(search.matches)),
			zap.Int("mapped_symbols", len(recall.hits)),
			zap.Bool("incomplete", search.incomplete),
			zap.String("context_error", contextError),
			zap.Duration("elapsed", time.Since(started)),
		}
		if len(recall.hits) == 0 || search.incomplete || contextError != "" {
			s.logger.Info("mcp: explore source literal recall incomplete", fields...)
		} else {
			s.logger.Debug("mcp: explore source literal recall", fields...)
		}
	}
	return recall
}

func (s *Server) mapDiscoveredExploreSourceLiteralMatches(
	ctx context.Context,
	term string,
	search exploreSourceLiteralSearch,
	scope query.QueryOptions,
	discoveryErr error,
) (exploreSourceLiteralRecall, error) {
	mappingCtx, cancelMapping := context.WithTimeout(ctx, exploreSourceLiteralRecallBudget)
	recall := s.mapExploreSourceLiteralMatchesContext(mappingCtx, term, search.matches, scope)
	mappingErr := mappingCtx.Err()
	cancelMapping()
	recall.ambiguous = recall.ambiguous || search.incomplete || discoveryErr != nil || mappingErr != nil
	return recall, mappingErr
}

func (s *Server) mapExploreSourceLiteralMatches(
	term string,
	matches []trigram.Match,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	return s.mapExploreSourceLiteralMatchesContext(context.Background(), term, matches, scope)
}

func (s *Server) mapExploreSourceLiteralMatchesContext(
	ctx context.Context,
	term string,
	matches []trigram.Match,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || len(matches) == 0 || ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	saturated := len(matches) >= exploreSourceLiteralRecallMaxHits
	if len(matches) > exploreSourceLiteralRecallMaxHits {
		matches = matches[:exploreSourceLiteralRecallMaxHits]
	}

	// Multi-repo grep stamps paths with the repository prefix. During an
	// isolated single-repo session the graph can still contain unprefixed file
	// paths, so admit one exact, scope-derived alias into the same index build.
	// Exact paths remain authoritative; this is not a fuzzy suffix match and it
	// does not add another AllNodes scan.
	repoPrefix := exploreSourceLiteralSingleRepoPrefix(scope)
	exactPaths := make([]string, 0, len(matches))
	aliasPaths := make([]string, 0, len(matches))
	exactSeen := make(map[string]struct{}, len(matches))
	aliasSeen := make(map[string]struct{}, len(matches))
	aliases := make(map[string]string, len(matches))
	for _, match := range matches {
		if !exploreTextHasExactLiteral(match.Text, term) {
			continue
		}
		if _, duplicate := exactSeen[match.Path]; !duplicate {
			exactSeen[match.Path] = struct{}{}
			exactPaths = append(exactPaths, match.Path)
		}
		if alias := exploreSourceLiteralUnprefixedPath(match.Path, repoPrefix); alias != "" {
			aliases[match.Path] = alias
			if _, duplicate := aliasSeen[alias]; !duplicate {
				aliasSeen[alias] = struct{}{}
				aliasPaths = append(aliasPaths, alias)
			}
		}
	}
	sort.Strings(exactPaths)
	sort.Strings(aliasPaths)
	orderedPaths := make([]string, 0, len(exactPaths)+len(aliasPaths))
	orderedPaths = append(orderedPaths, exactPaths...)
	for _, alias := range aliasPaths {
		if _, isExact := exactSeen[alias]; !isExact {
			orderedPaths = append(orderedPaths, alias)
		}
	}
	indexes := s.buildFileSymbolIndexForOrderedPathsContext(ctx, orderedPaths)
	seen := make(map[string]struct{}, len(matches))
	hits := make([]exploreSourceLiteralHit, 0, len(matches))
	for rank, match := range matches {
		if !exploreTextHasExactLiteral(match.Text, term) {
			continue
		}
		index := indexes[match.Path]
		if index == nil {
			index = indexes[aliases[match.Path]]
		}
		if index == nil {
			continue
		}
		node := index.smallestEnclosing(match.Line)
		if node == nil || node.ID == "" || !scope.ScopeAllows(node) {
			continue
		}
		if _, duplicate := seen[node.ID]; duplicate {
			continue
		}
		seen[node.ID] = struct{}{}
		hits = append(hits, exploreSourceLiteralHit{nodeID: node.ID, rank: rank})
	}
	return exploreSourceLiteralRecall{
		hits:      hits,
		ambiguous: saturated || len(hits) > 1,
	}
}

func exploreSourceLiteralSingleRepoPrefix(scope query.QueryOptions) string {
	prefix := ""
	for candidate, allowed := range scope.RepoAllow {
		if !allowed {
			continue
		}
		candidate = strings.TrimSuffix(strings.TrimSpace(candidate), "/")
		if candidate == "" {
			continue
		}
		if prefix != "" && prefix != candidate {
			return ""
		}
		prefix = candidate
	}
	return prefix
}

func exploreSourceLiteralUnprefixedPath(path, repoPrefix string) string {
	marker := repoPrefix + "/"
	if repoPrefix == "" || !strings.HasPrefix(path, marker) || len(path) == len(marker) {
		return ""
	}
	return strings.TrimPrefix(path, marker)
}

// searchExploreSourceLiteral mirrors search_text's literal backend while
// deliberately refusing an unscoped multi-repository fan-out. The caller's
// session locality supplies repoPrefix in normal operation.
func (s *Server) searchExploreSourceLiteral(
	ctx context.Context,
	term string,
	repoPrefix string,
	scope query.QueryOptions,
) exploreSourceLiteralSearch {
	if s.multiIndexer != nil {
		if repoPrefix == "" {
			haveScopedPrefix := false
			for prefix, allowed := range scope.RepoAllow {
				if !allowed {
					continue
				}
				prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
				if haveScopedPrefix && repoPrefix != prefix {
					return exploreSourceLiteralSearch{backend: "multi-ambiguous-scope"}
				}
				repoPrefix = prefix
				haveScopedPrefix = true
			}
		}
		result := s.multiIndexer.GrepLiteralForRepoBounded(
			ctx, repoPrefix, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
		if result.Owned {
			return exploreSourceLiteralSearch{
				matches:          result.Matches,
				incomplete:       result.Incomplete,
				backend:          "multi",
				owned:            true,
				lookupRepoPrefix: result.RepoPrefix,
			}
		}
		// Once MultiIndexer owns any repository, an unresolved prefix is an
		// ownership failure rather than permission to scan the base indexer.
		// Falling through here can leak matches from a different repository.
		if result.Configured {
			return exploreSourceLiteralSearch{
				backend:          "multi-unresolved",
				lookupRepoPrefix: repoPrefix,
			}
		}
	}
	if s.indexer != nil {
		matches, incomplete := s.indexer.GrepLiteralBounded(
			ctx, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
		return exploreSourceLiteralSearch{
			matches:          matches,
			incomplete:       incomplete,
			backend:          "direct",
			owned:            true,
			lookupRepoPrefix: s.indexer.RepoPrefix(),
		}
	}
	return exploreSourceLiteralSearch{backend: "none", lookupRepoPrefix: repoPrefix}
}
