package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// connClientSession adapts one daemon-socket connection to mcp-go's
// server.ClientSession so server-initiated notifications
// (notifications/tools/list_changed after a tools_search promotion,
// graph_invalidated, diagnostics pushes, ...) reach stdio-proxy
// clients. Without this registration, HandleMessage-dispatched
// sessions are invisible to SendNotificationToAllClients: the
// broadcast iterates registered sessions only, so unix-socket clients
// silently miss every push — the streamable HTTP transport and the
// embedded stdio server were the only paths that ever delivered one.
//
// The pump goroutine serialises each notification and hands it to the
// transport-owned write callback; the daemon interleaves those frames
// with request replies under its own connection write lock.
type connClientSession struct {
	id          string
	notifCh     chan mcp.JSONRPCNotification
	done        chan struct{}
	initialized atomic.Bool
}

func (c *connClientSession) SessionID() string { return c.id }
func (c *connClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return c.notifCh
}
func (c *connClientSession) Initialize()       { c.initialized.Store(true) }
func (c *connClientSession) Initialized() bool { return c.initialized.Load() }

// connNotifBuffer sizes the per-session notification channel. mcp-go
// delivers non-blocking and drops on overflow, so the buffer only has
// to absorb short bursts (a promotion sweep fires a single frame).
const connNotifBuffer = 16

// ConnectSession registers a daemon-socket connection as a live MCP
// client session on the underlying mcp-go server. write must deliver
// one complete JSON-RPC frame to the client (no trailing newline) and
// be safe to call from the pump goroutine concurrently with the
// request/reply path; once it errors the pump stops (the daemon's
// teardown calls DisconnectSession shortly after).
//
// Pair every ConnectSession with a DisconnectSession — the mcp-go
// session registry and the pump goroutine live until then.
func (s *Server) ConnectSession(sessionID string, write func([]byte) error) error {
	if s.mcpServer == nil {
		return fmt.Errorf("connect session %s: mcp server not initialised", sessionID)
	}
	cs := &connClientSession{
		id:      sessionID,
		notifCh: make(chan mcp.JSONRPCNotification, connNotifBuffer),
		done:    make(chan struct{}),
	}
	if _, loaded := s.connSessions.LoadOrStore(sessionID, cs); loaded {
		return fmt.Errorf("connect session %s: already connected", sessionID)
	}
	if err := s.mcpServer.RegisterSession(context.Background(), cs); err != nil {
		s.connSessions.Delete(sessionID)
		return fmt.Errorf("connect session %s: %w", sessionID, err)
	}
	go func() {
		for {
			select {
			case n := <-cs.notifCh:
				frame, err := json.Marshal(n)
				if err != nil {
					continue
				}
				if write(frame) != nil {
					return
				}
			case <-cs.done:
				return
			}
		}
	}()
	return nil
}

// DisconnectSession unregisters a session connected via ConnectSession
// and stops its notification pump. Unknown ids are a no-op, so callers
// can invoke it unconditionally on connection teardown.
func (s *Server) DisconnectSession(sessionID string) {
	v, ok := s.connSessions.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	if s.mcpServer != nil {
		s.mcpServer.UnregisterSession(context.Background(), sessionID)
	}
	close(v.(*connClientSession).done)
}

// WithClientSession attaches the connected session for sessionID to
// ctx so HandleMessage runs session-aware. This is what lets mcp-go's
// initialize handler mark the session initialized — the gate
// sendNotificationToAllClients applies before delivering a broadcast —
// and what enables session-targeted APIs generally. A sessionID
// without a connected session returns ctx unchanged.
func (s *Server) WithClientSession(ctx context.Context, sessionID string) context.Context {
	if v, ok := s.connSessions.Load(sessionID); ok {
		return s.mcpServer.WithContext(ctx, v.(server.ClientSession))
	}
	return ctx
}
