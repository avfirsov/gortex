package mcp

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/trigram"
)

const (
	exploreSourceLiteralRecallMaxHits  = 24
	exploreSourceLiteralRecallMaxFiles = 512
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

	searchCtx, cancel := context.WithTimeout(ctx, exploreSourceLiteralRecallBudget)
	defer cancel()
	matches, incomplete := s.searchExploreSourceLiteral(searchCtx, term, repoPrefix, scope)
	if ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	recall := s.mapExploreSourceLiteralMatches(term, matches, scope)
	recall.ambiguous = recall.ambiguous || incomplete
	return recall
}

func (s *Server) mapExploreSourceLiteralMatches(
	term string,
	matches []trigram.Match,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || len(matches) == 0 {
		return exploreSourceLiteralRecall{}
	}
	saturated := len(matches) >= exploreSourceLiteralRecallMaxHits
	if len(matches) > exploreSourceLiteralRecallMaxHits {
		matches = matches[:exploreSourceLiteralRecallMaxHits]
	}

	paths := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if exploreTextHasExactLiteral(match.Text, term) {
			paths[match.Path] = struct{}{}
		}
	}
	indexes := s.buildFileSymbolIndexForPaths(paths)
	seen := make(map[string]struct{}, len(matches))
	hits := make([]exploreSourceLiteralHit, 0, len(matches))
	for rank, match := range matches {
		if !exploreTextHasExactLiteral(match.Text, term) {
			continue
		}
		index := indexes[match.Path]
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

// searchExploreSourceLiteral mirrors search_text's literal backend while
// deliberately refusing an unscoped multi-repository fan-out. The caller's
// session locality supplies repoPrefix in normal operation.
func (s *Server) searchExploreSourceLiteral(
	ctx context.Context,
	term string,
	repoPrefix string,
	scope query.QueryOptions,
) ([]trigram.Match, bool) {
	if s.multiIndexer != nil {
		if repoPrefix == "" {
			for prefix, allowed := range scope.RepoAllow {
				if !allowed {
					continue
				}
				if repoPrefix != "" {
					return nil, false
				}
				repoPrefix = prefix
			}
		}
		if repoPrefix == "" {
			return nil, false
		}
		return s.multiIndexer.GrepLiteralForRepoBounded(
			ctx, repoPrefix, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
	}
	if s.indexer != nil {
		return s.indexer.GrepLiteralBounded(
			ctx, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
	}
	return nil, false
}
