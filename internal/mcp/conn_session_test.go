package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

func newConnSessionTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	eng := query.NewEngine(g)
	return NewServer(eng, g, nil, nil, zap.NewNop(), nil)
}

// initializeSession runs the MCP initialize handshake through the
// session-aware dispatch path — the same shape the daemon dispatcher
// uses — so mcp-go marks the connected session initialized (the gate
// broadcasts apply before delivering).
func initializeSession(t *testing.T, s *Server, sessionID string) {
	t.Helper()
	ctx := s.WithClientSession(context.Background(), sessionID)
	init := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	if reply := s.MCPServer().HandleMessage(ctx, init); reply == nil {
		t.Fatal("initialize returned no reply")
	}
}

// TestConnectSessionDeliversToolsListChanged guards the daemon-socket
// notification path end to end: a session connected via ConnectSession
// and initialized through the session-aware dispatch must receive
// notifications/tools/list_changed when a tool is added (the exact
// broadcast a tools_search promotion fires). Before ConnectSession
// existed, HandleMessage-dispatched socket sessions were invisible to
// SendNotificationToAllClients and every push was silently dropped.
func TestConnectSessionDeliversToolsListChanged(t *testing.T) {
	s := newConnSessionTestServer(t)
	frames := make(chan []byte, 4)
	if err := s.ConnectSession("sess-notify", func(b []byte) error {
		frames <- append([]byte(nil), b...)
		return nil
	}); err != nil {
		t.Fatalf("ConnectSession: %v", err)
	}
	defer s.DisconnectSession("sess-notify")

	initializeSession(t, s, "sess-notify")

	s.MCPServer().AddTool(
		mcp.NewTool("late_promoted_tool"),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	)

	select {
	case frame := <-frames:
		var n struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(frame, &n); err != nil {
			t.Fatalf("notification frame is not JSON: %v (%s)", err, frame)
		}
		if n.Method != "notifications/tools/list_changed" {
			t.Fatalf("method = %q, want notifications/tools/list_changed", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification delivered to the connected session")
	}
}

// TestConnectSessionLifecycle covers the bookkeeping edges: a duplicate
// connect is refused, an uninitialized session receives no broadcast,
// and after DisconnectSession nothing is delivered.
func TestConnectSessionLifecycle(t *testing.T) {
	s := newConnSessionTestServer(t)
	frames := make(chan []byte, 4)
	write := func(b []byte) error {
		frames <- append([]byte(nil), b...)
		return nil
	}
	if err := s.ConnectSession("sess-life", write); err != nil {
		t.Fatalf("ConnectSession: %v", err)
	}
	if err := s.ConnectSession("sess-life", write); err == nil {
		t.Fatal("duplicate ConnectSession succeeded, want error")
	}

	// Not initialized yet — a broadcast must not reach the session.
	s.MCPServer().SendNotificationToAllClients("notifications/tools/list_changed", nil)
	select {
	case f := <-frames:
		t.Fatalf("uninitialized session received %s", f)
	case <-time.After(50 * time.Millisecond):
	}

	initializeSession(t, s, "sess-life")
	s.DisconnectSession("sess-life")
	// Unknown / already-disconnected ids are a no-op.
	s.DisconnectSession("sess-life")

	s.MCPServer().SendNotificationToAllClients("notifications/tools/list_changed", nil)
	select {
	case f := <-frames:
		t.Fatalf("disconnected session received %s", f)
	case <-time.After(50 * time.Millisecond):
	}
}
