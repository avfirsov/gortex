package trigram

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
)

// BoundedSearchStats reports whether a search examined its whole candidate
// set. Incomplete is deliberately conservative: a file cap, cancellation,
// unreadable file, scanner error, or result cap can all hide another match.
type BoundedSearchStats struct {
	CandidateFiles int
	ScannedFiles   int
	Incomplete     bool
}

// GrepBounded uses the existing trigram candidate index but bounds both the
// number of opened files and the caller's context. It never builds an index.
func (s *Searcher) GrepBounded(
	ctx context.Context,
	query string,
	limit int,
	maxFiles int,
) ([]Match, BoundedSearchStats) {
	if s == nil || query == "" {
		return nil, BoundedSearchStats{}
	}
	candidates := s.ix.Candidates(query)
	paths := make([]string, 0, len(candidates))
	for _, docID := range candidates {
		if int(docID) < len(s.paths) {
			paths = append(paths, s.paths[docID])
		}
	}
	return grepPathsBounded(ctx, s.root, paths, query, limit, maxFiles)
}

// GrepPathsBounded is the cold-search counterpart used before a repository's
// lazy trigram searcher has been built. The supplied paths are already-known
// indexed files; scanning them does not create persistent or in-memory index
// state.
func GrepPathsBounded(
	ctx context.Context,
	root string,
	paths []string,
	query string,
	limit int,
	maxFiles int,
) ([]Match, BoundedSearchStats) {
	return grepPathsBounded(ctx, root, paths, query, limit, maxFiles)
}

func grepPathsBounded(
	ctx context.Context,
	root string,
	paths []string,
	query string,
	limit int,
	maxFiles int,
) ([]Match, BoundedSearchStats) {
	stats := BoundedSearchStats{CandidateFiles: len(paths)}
	if query == "" || ctx.Err() != nil {
		stats.Incomplete = ctx.Err() != nil
		return nil, stats
	}
	if maxFiles > 0 && len(paths) > maxFiles {
		paths = paths[:maxFiles]
		stats.Incomplete = true
	}

	matchCapacity := 8
	if limit > 0 && limit < matchCapacity {
		matchCapacity = limit
	}
	matches := make([]Match, 0, matchCapacity)
	scanBuffer := make([]byte, 64*1024)
	for _, rel := range paths {
		if ctx.Err() != nil {
			stats.Incomplete = true
			break
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			stats.Incomplete = true
			continue
		}
		stats.ScannedFiles++
		scanner := bufio.NewScanner(f)
		scanner.Buffer(scanBuffer, 4*1024*1024)
		line := 0
		cancelled := false
		for scanner.Scan() {
			line++
			if line&63 == 0 && ctx.Err() != nil {
				stats.Incomplete = true
				cancelled = true
				break
			}
			text := scanner.Text()
			if !strings.Contains(text, query) {
				continue
			}
			matches = append(matches, Match{Path: rel, Line: line, Text: text})
			if limit > 0 && len(matches) >= limit {
				stats.Incomplete = true
				_ = f.Close()
				return matches, stats
			}
		}
		if err := scanner.Err(); err != nil {
			stats.Incomplete = true
		}
		_ = f.Close()
		if cancelled {
			break
		}
	}
	return matches, stats
}
