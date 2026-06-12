package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// staleRefsBroadcaster fans `notifications/stale_refs` events to
// subscribed MCP sessions when the watcher's symbol-change callback
// reports that a re-indexed file removed or signature-mutated symbols
// the session has touched.
//
// "Touched" is read off the session's working set — the recently
// viewed / modified files, viewed symbols, and modified files
// recorded on sessionState (the same surface `recover_session`
// reads). A subscriber only ever sees notifications for paths /
// symbols *that session* has interacted with; another session's
// activity is invisible to it.
//
// Delivery shape: one notification per (session, file_path). When a
// re-index touches multiple sessions' working sets, each session
// gets its own filtered payload. A session whose working set has
// nothing in common with the changed file gets nothing.
//
// Delta filter: per (session, file_path) we hash the
// (removed_symbol_ids, changed_signature_ids) tuple and suppress
// identical re-emits. This avoids fanning out the same "x.go's
// HandleX was removed" payload on every subsequent save of x.go.
type staleRefsBroadcaster struct {
	server   specificNotificationSender
	logger   *zap.Logger
	sessions *sessionMap   // session lookup for per-session working sets
	defaults *sessionState // embedded-mode fallback (no sessionMap entries)

	mu          sync.RWMutex
	subscribers map[string]bool   // session ID → subscribed
	lastHash    map[string]string // sessionID|path → last-emitted fingerprint
}

func newStaleRefsBroadcaster(srv specificNotificationSender, sessions *sessionMap, defaults *sessionState, logger *zap.Logger) *staleRefsBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &staleRefsBroadcaster{
		server:      srv,
		logger:      logger,
		sessions:    sessions,
		defaults:    defaults,
		subscribers: make(map[string]bool),
		lastHash:    make(map[string]string),
	}
}

// handleSymbolChange is the watcher-callback target. For every
// subscriber whose working set intersects the change, emit a
// `notifications/stale_refs` payload.
//
// oldSymbols: symbols that were in the graph before this re-index
// (potentially removed or replaced). newSymbols: symbols present
// after this re-index. Removed = old∖new; signature-mutated = the
// intersection where Meta["signature"] differs.
func (b *staleRefsBroadcaster) handleSymbolChange(filePath string, oldSymbols, newSymbols []*graph.Node) {
	if b == nil || b.server == nil || filePath == "" {
		return
	}
	b.mu.RLock()
	if len(b.subscribers) == 0 {
		b.mu.RUnlock()
		return
	}
	subs := make([]string, 0, len(b.subscribers))
	for id := range b.subscribers {
		subs = append(subs, id)
	}
	b.mu.RUnlock()

	removedIDs, changedIDs := diffSymbolSets(oldSymbols, newSymbols)
	if len(removedIDs) == 0 && len(changedIDs) == 0 {
		return
	}

	for _, sid := range subs {
		state := b.lookupSession(sid)
		if state == nil {
			continue
		}
		affectedRemoved, affectedChanged, viewedFile := state.intersectStaleness(filePath, removedIDs, changedIDs)
		if !viewedFile && len(affectedRemoved) == 0 && len(affectedChanged) == 0 {
			continue
		}
		key := sid + "|" + filePath
		fp := hashStalePayload(affectedRemoved, affectedChanged, viewedFile)
		b.mu.Lock()
		if b.lastHash[key] == fp {
			b.mu.Unlock()
			continue
		}
		b.lastHash[key] = fp
		b.mu.Unlock()

		params := map[string]any{
			"path":               filePath,
			"uri":                pathToFileURI(filePath),
			"viewed_file":        viewedFile,
			"removed_symbols":    affectedRemoved,
			"changed_signatures": affectedChanged,
			"ts":                 time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := b.server.SendNotificationToSpecificClient(sid, "notifications/stale_refs", params); err != nil {
			b.logger.Debug("send stale_refs failed",
				zap.String("session", sid),
				zap.String("path", filePath),
				zap.Error(err))
		}
	}
}

// subscribe records sessionID. No initial-replay payload — by design.
// staleness is about deltas, and the session has nothing to "stale"
// against until it has consumed a symbol that later changes.
func (b *staleRefsBroadcaster) subscribe(sessionID string) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	b.subscribers[sessionID] = true
	b.mu.Unlock()
}

func (b *staleRefsBroadcaster) unsubscribe(sessionID string) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	delete(b.subscribers, sessionID)
	// Drop per-session delta cache so a future resubscribe gets a
	// fresh notification on the first matching change instead of
	// being silenced by an old hash.
	for k := range b.lastHash {
		if len(k) > len(sessionID) && k[:len(sessionID)] == sessionID && k[len(sessionID)] == '|' {
			delete(b.lastHash, k)
		}
	}
	b.mu.Unlock()
}

func (b *staleRefsBroadcaster) subscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// lookupSession returns the sessionState for sessionID. Falls back
// to the embedded-mode default when no sessionMap is wired (matches
// Server.sessionFor's shape).
func (b *staleRefsBroadcaster) lookupSession(sessionID string) *sessionState {
	if b.sessions == nil {
		return b.defaults
	}
	entry := b.sessions.get(sessionID)
	if entry == nil {
		return b.defaults
	}
	return entry.session
}

// diffSymbolSets computes (removed, signature-changed) ID slices
// from before/after symbol lists. KindFile / KindImport are skipped
// — clients care about user-authored symbol churn, not the file
// node's own bookkeeping mutation.
func diffSymbolSets(oldSymbols, newSymbols []*graph.Node) (removedIDs, changedIDs []string) {
	oldMap := make(map[string]string, len(oldSymbols)) // ID → signature
	for _, n := range oldSymbols {
		if n == nil {
			continue
		}
		if n.Kind == graph.KindFile || n.Kind == graph.KindImport {
			continue
		}
		sig, _ := n.Meta["signature"].(string)
		oldMap[n.ID] = sig
	}
	newMap := make(map[string]string, len(newSymbols))
	for _, n := range newSymbols {
		if n == nil {
			continue
		}
		if n.Kind == graph.KindFile || n.Kind == graph.KindImport {
			continue
		}
		sig, _ := n.Meta["signature"].(string)
		newMap[n.ID] = sig
	}
	for id, oldSig := range oldMap {
		newSig, exists := newMap[id]
		switch {
		case !exists:
			removedIDs = append(removedIDs, id)
		case oldSig != newSig:
			changedIDs = append(changedIDs, id)
		}
	}
	return removedIDs, changedIDs
}

// intersectStaleness reports the subset of removedIDs / changedIDs
// the session has touched, plus whether the changed file itself is
// in the session's recent file / modified set. Caller holds no
// state lock — this acquires sessionState's own.
func (ss *sessionState) intersectStaleness(filePath string, removedIDs, changedIDs []string) (affectedRemoved, affectedChanged []string, viewedFile bool) {
	if ss == nil {
		return nil, nil, false
	}
	ss.mu.Lock()
	viewedSymbols := make(map[string]struct{}, len(ss.viewedSymbols))
	for _, id := range ss.viewedSymbols {
		viewedSymbols[id] = struct{}{}
	}
	for _, p := range ss.viewedFiles {
		if p == filePath {
			viewedFile = true
			break
		}
	}
	if !viewedFile {
		for _, p := range ss.modifiedFiles {
			if p == filePath {
				viewedFile = true
				break
			}
		}
	}
	ss.mu.Unlock()

	for _, id := range removedIDs {
		if _, ok := viewedSymbols[id]; ok {
			affectedRemoved = append(affectedRemoved, id)
		}
	}
	for _, id := range changedIDs {
		if _, ok := viewedSymbols[id]; ok {
			affectedChanged = append(affectedChanged, id)
		}
	}
	return affectedRemoved, affectedChanged, viewedFile
}

// hashStalePayload produces a stable fingerprint for delta suppression.
// Order-independent: sorts the input slices before hashing so the
// same change reported twice in different orderings de-dupes.
func hashStalePayload(removed, changed []string, viewedFile bool) string {
	if len(removed) == 0 && len(changed) == 0 && !viewedFile {
		return ""
	}
	r := append([]string(nil), removed...)
	c := append([]string(nil), changed...)
	sortStrings(r)
	sortStrings(c)
	buf := make([]byte, 0, 64)
	for _, s := range r {
		buf = append(buf, 'R')
		buf = append(buf, s...)
		buf = append(buf, ';')
	}
	for _, s := range c {
		buf = append(buf, 'C')
		buf = append(buf, s...)
		buf = append(buf, ';')
	}
	if viewedFile {
		buf = append(buf, 'V')
	}
	return string(buf)
}

// sortStrings is an in-place insertion sort. Small slices (the
// affected sets are typically 0-3 entries), so the standard-library
// import-cost dominates.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// registerStaleRefsTools wires the subscribe / unsubscribe MCP tools.
func (s *Server) registerStaleRefsTools() {
	s.addTool(
		mcp.NewTool("subscribe_stale_refs",
			mcp.WithDescription("Opt the current MCP session into `notifications/stale_refs` push events. When the watcher re-indexes a file and the change touches a symbol your session has viewed / modified (or the file itself is in your recent set), you receive `{path, uri, viewed_file, removed_symbols, changed_signatures, ts}`. Per-session filter — another session's activity is invisible to you. No initial replay (staleness is a delta concept). Pair with `unsubscribe_stale_refs`."),
		),
		s.handleSubscribeStaleRefs,
	)
	s.addTool(
		mcp.NewTool("unsubscribe_stale_refs",
			mcp.WithDescription("Opt the current MCP session out of `notifications/stale_refs` push events. Idempotent."),
		),
		s.handleUnsubscribeStaleRefs,
	)
}

func (s *Server) handleSubscribeStaleRefs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.staleRefsBroadcaster == nil {
		return mcp.NewToolResultError("stale_refs broadcaster is not configured"), nil
	}
	id := SessionIDFromContext(ctx)
	if id == "" {
		id = "embedded"
	}
	s.staleRefsBroadcaster.subscribe(id)
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"subscribed":  true,
		"session_id":  id,
		"subscribers": s.staleRefsBroadcaster.subscriberCount(),
	})
}

func (s *Server) handleUnsubscribeStaleRefs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.staleRefsBroadcaster == nil {
		return mcp.NewToolResultError("stale_refs broadcaster is not configured"), nil
	}
	id := SessionIDFromContext(ctx)
	if id == "" {
		id = "embedded"
	}
	s.staleRefsBroadcaster.unsubscribe(id)
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"subscribed":  false,
		"session_id":  id,
		"subscribers": s.staleRefsBroadcaster.subscriberCount(),
	})
}
