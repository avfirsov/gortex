package trigram

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
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

// GrepLiteralBounded is the localization-oriented warm search. Unlike the
// general grep path, it accepts only identifier-boundary occurrences, keeps at
// most one representative per file, and lets callers prioritize candidate
// classes before the file and result caps are applied.
func (s *Searcher) GrepLiteralBounded(
	ctx context.Context,
	query string,
	limit int,
	maxFiles int,
	preferPath func(string) bool,
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
	return grepLiteralPathsBounded(ctx, s.root, paths, query, limit, maxFiles, preferPath)
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

// GrepLiteralPathsBounded is the cold counterpart to GrepLiteralBounded. It
// scans only the supplied already-known files and never builds search state.
func GrepLiteralPathsBounded(
	ctx context.Context,
	root string,
	paths []string,
	query string,
	limit int,
	maxFiles int,
	preferPath func(string) bool,
) ([]Match, BoundedSearchStats) {
	return grepLiteralPathsBounded(ctx, root, paths, query, limit, maxFiles, preferPath)
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

func grepLiteralPathsBounded(
	ctx context.Context,
	root string,
	paths []string,
	query string,
	limit int,
	maxFiles int,
	preferPath func(string) bool,
) ([]Match, BoundedSearchStats) {
	stats := BoundedSearchStats{CandidateFiles: len(paths)}
	if query == "" || ctx.Err() != nil {
		stats.Incomplete = ctx.Err() != nil
		return nil, stats
	}
	if preferPath != nil {
		paths = prioritizeBoundedPaths(paths, preferPath)
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
		accepted := false
		for scanner.Scan() {
			line++
			if line&63 == 0 && ctx.Err() != nil {
				stats.Incomplete = true
				cancelled = true
				break
			}
			text := scanner.Text()
			if !containsIdentifierBounded(text, query) {
				continue
			}
			matches = append(matches, Match{Path: rel, Line: line, Text: text})
			accepted = true
			break
		}
		if err := scanner.Err(); err != nil {
			stats.Incomplete = true
		}
		_ = f.Close()
		if cancelled {
			break
		}
		if accepted && limit > 0 && len(matches) >= limit {
			stats.Incomplete = true
			return matches, stats
		}
	}
	return matches, stats
}

func prioritizeBoundedPaths(paths []string, prefer func(string) bool) []string {
	prioritized := make([]string, 0, len(paths))
	for _, path := range paths {
		if prefer(path) {
			prioritized = append(prioritized, path)
		}
	}
	for _, path := range paths {
		if !prefer(path) {
			prioritized = append(prioritized, path)
		}
	}
	return prioritized
}

func containsIdentifierBounded(text, query string) bool {
	for offset := 0; offset <= len(text)-len(query); {
		rel := strings.Index(text[offset:], query)
		if rel < 0 {
			return false
		}
		start := offset + rel
		end := start + len(query)
		first, _ := utf8.DecodeRuneInString(query)
		last, _ := utf8.DecodeLastRuneInString(query)
		leftOK := !isIdentifierRune(first) || start == 0
		if !leftOK {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			leftOK = !isIdentifierRune(previous)
		}
		rightOK := !isIdentifierRune(last) || end == len(text)
		if !rightOK {
			next, _ := utf8.DecodeRuneInString(text[end:])
			rightOK = !isIdentifierRune(next)
		}
		if leftOK && rightOK {
			return true
		}
		offset = start + len(query)
	}
	return false
}

func isIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
