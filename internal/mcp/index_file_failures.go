package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/query"
)

const indexFailureSampleLimit = 20

// Registrations include repositories with no successful file nodes. Read their
// failures through the request reader so another checkout cannot supply them.
func (s *Server) readIndexFileFailures(ctx context.Context, resolved ResolvedScope) ([]indexer.FileIndexFailure, error) {
	reader := s.readerFor(ctx)
	if reader == nil {
		return nil, nil
	}
	prefixes := map[string]bool{}
	if s.multiIndexer != nil {
		for _, prefix := range s.multiIndexer.RepoPrefixes() {
			prefixes[prefix] = true
		}
	}
	if s.indexer != nil {
		prefixes[s.indexer.RepoPrefix()] = true
	}
	var failures []indexer.FileIndexFailure
	var readErr error
	scope := query.QueryOptions{WorkspaceID: resolved.WorkspaceID, ProjectID: resolved.ProjectID, RepoAllow: resolved.RepoAllow}
	for prefix := range prefixes {
		if len(resolved.RepoAllow) > 0 && !resolved.RepoAllow[prefix] {
			continue
		}
		owner := s.indexer
		if s.multiIndexer != nil {
			owner = s.multiIndexer.GetIndexer(prefix)
		}
		node := &graph.Node{RepoPrefix: prefix}
		if owner != nil {
			node.WorkspaceID, node.ProjectID = owner.WorkspaceID(), owner.ProjectID()
		}
		project := node.ProjectID
		if project == "" {
			project = prefix
		}
		if !scope.ScopeAllows(node) || (resolved.ProjectID != "" && project != resolved.ProjectID) {
			continue
		}
		rows, err := indexer.LoadFileIndexFailuresWithError(reader, prefix)
		if err != nil {
			readErr = errors.Join(readErr, fmt.Errorf("read index failure state for repository %q: %w", prefix, err))
			continue
		}
		failures = append(failures, rows...)
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].Path < failures[j].Path })
	return failures, readErr
}

// Initial read failures have no graph node. Persisted ownership attributes them
// without requiring a successful prior index, or treating them as global nodes.
func scopedIndexFileFailures(failures []indexer.FileIndexFailure, resolved ResolvedScope, paths []string) []indexer.FileIndexFailure {
	opts := query.QueryOptions{WorkspaceID: resolved.WorkspaceID, ProjectID: resolved.ProjectID, RepoAllow: resolved.RepoAllow}
	paths = normalizePathPrefixes(paths)
	var scoped []indexer.FileIndexFailure
	for _, failure := range failures {
		if len(resolved.RepoAllow) > 0 && !resolved.RepoAllow[failure.RepoPrefix] {
			continue
		}
		project := failure.ProjectID
		if project == "" {
			project = failure.RepoPrefix
		}
		if resolved.ProjectID != "" && project != resolved.ProjectID {
			continue
		}
		node := &graph.Node{FilePath: failure.Path, RepoPrefix: failure.RepoPrefix, WorkspaceID: failure.WorkspaceID, ProjectID: failure.ProjectID}
		if !opts.ScopeAllows(node) {
			continue
		}
		if len(paths) > 0 && !pathMatchesAnyPrefix(failure.Path, expandPathPrefixesWithRepos(paths, []string{failure.RepoPrefix})) {
			continue
		}
		scoped = append(scoped, failure)
	}
	return scoped
}

func indexFileFailureSummary(failures []indexer.FileIndexFailure) map[string]any {
	unreadable := 0
	byRepo, unreadableByRepo := map[string]int{}, map[string]int{}
	for _, failure := range failures {
		byRepo[failure.RepoPrefix]++
		if failure.PermissionDenied {
			unreadable++
			unreadableByRepo[failure.RepoPrefix]++
		}
	}
	result := map[string]any{
		"failed_file_count":        len(failures),
		"unreadable_file_count":    unreadable,
		"failed_files_by_repo":     byRepo,
		"unreadable_files_by_repo": unreadableByRepo,
	}
	if len(failures) > 0 {
		result["failed_files"] = failures[:min(len(failures), indexFailureSampleLimit)]
		if len(failures) > indexFailureSampleLimit {
			result["failed_files_truncated"] = true
		}
	}
	return result
}

func (s *Server) indexFileFailureWarning(ctx context.Context, resolved ResolvedScope, paths []string) map[string]any {
	allFailures, readErr := s.readIndexFileFailures(ctx, resolved)
	failures := scopedIndexFileFailures(allFailures, resolved, paths)
	if len(failures) == 0 && readErr == nil {
		return nil
	}
	warning := indexFileFailureSummary(failures)
	warning["code"] = "index_incomplete"
	warning["message"] = fmt.Sprintf("%d in-scope file(s) could not be indexed. Results may omit current code or contain stale symbols; zero matches do not prove absence. Fix the reported file access or indexing errors and reindex the affected paths.", len(failures))
	if readErr != nil {
		warning["index_state_read_error"] = readErr.Error()
		warning["message"] = "Index failure state could not be read for this scope; result completeness is unknown. Results may omit current code or retain stale symbols, and zero matches do not prove absence. " + readErr.Error()
	}
	return warning
}

func stampIndexFileFailureWarning(resp map[string]any, warning map[string]any) {
	if warning != nil {
		resp["index_complete"] = false
		resp["index_warning"] = warning
	}
}

// Keep one text block so later freshness decoration still reaches GCX headers.
// Compact and TOON output gain a body-visible qualification without changing
// their result rows or pagination fields.
func decorateIndexFileFailureResult(result *mcp.CallToolResult, warning map[string]any) *mcp.CallToolResult {
	if result == nil || warning == nil {
		return result
	}
	fields := map[string]any{"index_complete": false, "index_warning": warning}
	result = mergeResultMeta(result, fields)
	text, ok := singleTextContent(result)
	if !ok {
		return result
	}
	// GCX headers carry scalar fields; the full structured warning remains
	// on the response metadata while its qualification is visible in-band.
	gcxFields := map[string]any{
		"index_complete":        false,
		"index_warning":         "index_incomplete",
		"index_warning_message": warning["message"],
		"failed_file_count":     warning["failed_file_count"],
		"unreadable_file_count": warning["unreadable_file_count"],
	}
	if body, isGCX := injectGCXHeaderMeta(text, gcxFields); isGCX {
		return rebuildTextResult(result, body)
	}
	message, _ := warning["message"].(string)
	quotedMessage, _ := json.Marshal(message)
	return rebuildTextResult(result, strings.TrimRight(text, "\n")+"\nindex_incomplete: "+string(quotedMessage)+"\n")
}

func indexHealthFailureCounts(reader graph.Reader, totalDetected, fileNodes int, parseErrors []indexer.IndexError, failures []indexer.FileIndexFailure) (int, int) {
	if len(failures) == 0 {
		return totalDetected, max(totalDetected-len(parseErrors), 0)
	}
	parsePaths := make(map[string]bool, len(parseErrors))
	for _, failure := range parseErrors {
		parsePaths[failure.FilePath] = true
	}
	missing, additionalFailures := 0, 0
	for _, failure := range failures {
		if reader.GetNode(failure.Path) == nil {
			missing++
		}
		prefixedParseError := parsePaths[failure.Path]
		relative := failure.Path
		if failure.RepoPrefix != "" {
			relative = strings.TrimPrefix(relative, failure.RepoPrefix+"/")
		}
		relativeParseError := parsePaths[relative]
		if !prefixedParseError && !relativeParseError {
			additionalFailures++
		}
	}
	totalDetected = max(totalDetected, fileNodes+missing)
	return totalDetected, max(totalDetected-len(parseErrors)-additionalFailures, 0)
}

func indexHealthParseFailureCount(value any) int {
	switch failures := value.(type) {
	case []indexer.IndexError:
		return len(failures)
	case map[string]string:
		return len(failures)
	default:
		return 0
	}
}
