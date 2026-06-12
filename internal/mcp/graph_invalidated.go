package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// graphInvalidatedBroadcaster fans `notifications/graph_invalidated`
// to subscribed MCP sessions whenever the indexed graph is rebuilt
// (a re-warm / re-analysis pass — see Server.RunAnalysis).
//
// Unlike `notifications/stale_refs`, which is per-session and filtered
// to a session's working set, this is a *coarse* signal: "the graph
// moved under you — drop any cached query results and re-pull what
// you need." Every subscriber receives the same payload. It is the
// hot-reload primitive a long-lived MCP client needs to stay
// consistent with a daemon that re-indexes on file changes.
//
// The payload is `{node_count, edge_count, reason, ts}`. node/edge
// counts let a client cheaply detect whether the graph actually
// changed shape; reason names what triggered the rebuild.
type graphInvalidatedBroadcaster struct {
	server specificNotificationSender
	logger *zap.Logger

	mu          sync.RWMutex
	subscribers map[string]bool
}

func newGraphInvalidatedBroadcaster(srv specificNotificationSender, logger *zap.Logger) *graphInvalidatedBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &graphInvalidatedBroadcaster{
		server:      srv,
		logger:      logger,
		subscribers: make(map[string]bool),
	}
}

// broadcast sends one `notifications/graph_invalidated` payload to
// every subscribed session. A no-op when nothing is subscribed.
func (b *graphInvalidatedBroadcaster) broadcast(nodeCount, edgeCount int, reason string) {
	if b == nil || b.server == nil {
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

	params := map[string]any{
		"node_count": nodeCount,
		"edge_count": edgeCount,
		"reason":     reason,
		"ts":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, sid := range subs {
		if err := b.server.SendNotificationToSpecificClient(sid, "notifications/graph_invalidated", params); err != nil {
			b.logger.Debug("send graph_invalidated failed",
				zap.String("session", sid),
				zap.Error(err))
		}
	}
}

func (b *graphInvalidatedBroadcaster) subscribe(sessionID string) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	b.subscribers[sessionID] = true
	b.mu.Unlock()
}

func (b *graphInvalidatedBroadcaster) unsubscribe(sessionID string) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	delete(b.subscribers, sessionID)
	b.mu.Unlock()
}

func (b *graphInvalidatedBroadcaster) subscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// registerGraphInvalidatedTools wires the subscribe / unsubscribe MCP
// tools for the `notifications/graph_invalidated` topic.
func (s *Server) registerGraphInvalidatedTools() {
	s.addTool(
		mcp.NewTool("subscribe_graph_invalidated",
			mcp.WithDescription("Opt the current MCP session into `notifications/graph_invalidated` push events. Whenever the daemon rebuilds the graph (a re-warm / re-analysis after files change), you receive `{node_count, edge_count, reason, ts}` — a coarse \"the graph moved, drop cached results\" signal. Unlike `subscribe_stale_refs` this is not filtered to your working set; every subscriber gets it. Pair with `unsubscribe_graph_invalidated`."),
		),
		s.handleSubscribeGraphInvalidated,
	)
	s.addTool(
		mcp.NewTool("unsubscribe_graph_invalidated",
			mcp.WithDescription("Opt the current MCP session out of `notifications/graph_invalidated` push events. Idempotent."),
		),
		s.handleUnsubscribeGraphInvalidated,
	)
}

func (s *Server) handleSubscribeGraphInvalidated(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.graphInvalidatedBroadcaster == nil {
		return mcp.NewToolResultError("graph_invalidated broadcaster is not configured"), nil
	}
	id := SessionIDFromContext(ctx)
	if id == "" {
		id = "embedded"
	}
	s.graphInvalidatedBroadcaster.subscribe(id)
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"subscribed":  true,
		"session_id":  id,
		"subscribers": s.graphInvalidatedBroadcaster.subscriberCount(),
	})
}

func (s *Server) handleUnsubscribeGraphInvalidated(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.graphInvalidatedBroadcaster == nil {
		return mcp.NewToolResultError("graph_invalidated broadcaster is not configured"), nil
	}
	id := SessionIDFromContext(ctx)
	if id == "" {
		id = "embedded"
	}
	s.graphInvalidatedBroadcaster.unsubscribe(id)
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"subscribed":  false,
		"session_id":  id,
		"subscribers": s.graphInvalidatedBroadcaster.subscriberCount(),
	})
}
