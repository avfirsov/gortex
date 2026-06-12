package mcp

import (
	"testing"

	"go.uber.org/zap"
)

func TestGraphInvalidated_SubscribeUnsubscribe(t *testing.T) {
	b := newGraphInvalidatedBroadcaster(&fakeSpecificSender{}, zap.NewNop())
	if b.subscriberCount() != 0 {
		t.Fatalf("fresh broadcaster has %d subscribers", b.subscriberCount())
	}
	b.subscribe("s1")
	b.subscribe("s2")
	b.subscribe("s1") // idempotent
	if b.subscriberCount() != 2 {
		t.Errorf("subscriberCount = %d, want 2", b.subscriberCount())
	}
	b.subscribe("") // empty id ignored
	if b.subscriberCount() != 2 {
		t.Errorf("empty session id must be ignored, count = %d", b.subscriberCount())
	}
	b.unsubscribe("s1")
	if b.subscriberCount() != 1 {
		t.Errorf("after unsubscribe count = %d, want 1", b.subscriberCount())
	}
	b.unsubscribe("s1") // idempotent
	if b.subscriberCount() != 1 {
		t.Errorf("double unsubscribe count = %d, want 1", b.subscriberCount())
	}
}

func TestGraphInvalidated_BroadcastToSubscribers(t *testing.T) {
	sender := &fakeSpecificSender{}
	b := newGraphInvalidatedBroadcaster(sender, zap.NewNop())
	b.subscribe("s1")
	b.subscribe("s2")

	b.broadcast(120, 480, "reanalysis")

	targeted := sender.sessionsTargeted()
	if targeted["s1"] != 1 || targeted["s2"] != 1 {
		t.Fatalf("expected one delivery per subscriber, got %v", targeted)
	}
	calls := sender.snapshot()
	for _, c := range calls {
		if c.method != "notifications/graph_invalidated" {
			t.Errorf("method = %q, want notifications/graph_invalidated", c.method)
		}
		if c.params["node_count"] != 120 || c.params["edge_count"] != 480 {
			t.Errorf("payload counts wrong: %v", c.params)
		}
		if c.params["reason"] != "reanalysis" {
			t.Errorf("reason = %v, want reanalysis", c.params["reason"])
		}
		if _, ok := c.params["ts"].(string); !ok {
			t.Errorf("payload missing ts: %v", c.params)
		}
	}
}

func TestGraphInvalidated_NoSubscribersNoSend(t *testing.T) {
	sender := &fakeSpecificSender{}
	b := newGraphInvalidatedBroadcaster(sender, zap.NewNop())
	b.broadcast(1, 2, "reanalysis")
	if got := len(sender.snapshot()); got != 0 {
		t.Errorf("broadcast with no subscribers sent %d notifications, want 0", got)
	}
}

// TestGraphInvalidated_RunAnalysisFires proves the broadcaster is
// wired into the real RunAnalysis path.
func TestGraphInvalidated_RunAnalysisFires(t *testing.T) {
	srv := newFullTestServer(t)
	sender := &fakeSpecificSender{}
	// Swap in a recording sender so the broadcast is observable.
	srv.graphInvalidatedBroadcaster = newGraphInvalidatedBroadcaster(sender, zap.NewNop())
	srv.graphInvalidatedBroadcaster.subscribe("watcher-session")

	srv.RunAnalysis()

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("RunAnalysis produced %d graph_invalidated notifications, want 1", len(calls))
	}
	if calls[0].method != "notifications/graph_invalidated" || calls[0].sessionID != "watcher-session" {
		t.Errorf("unexpected notification: %+v", calls[0])
	}
}
