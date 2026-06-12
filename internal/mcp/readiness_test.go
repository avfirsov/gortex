package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestReadinessBroadcaster_NoSubscribers — publish without subscribers
// records the last-known state but fans out nothing.
func TestReadinessBroadcaster_NoSubscribers(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())

	b.publish(map[string]any{"phase": "snapshot_loaded", "ready": false})
	assert.Empty(t, fake.snapshot(), "no subscribers — no broadcast")
	assert.NotNil(t, b.snapshot(), "last-known state must be recorded for late subscribers")
}

// TestReadinessBroadcaster_InitialReplay — subscribing replays the
// last published state with `initial_replay: true`.
func TestReadinessBroadcaster_InitialReplay(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())

	b.publish(map[string]any{"phase": "parallel_parse", "ready": false, "tracked_repos": 3})

	replayed := b.subscribe("session-A")
	require.True(t, replayed, "initial replay should fire when state exists")

	calls := fake.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "notifications/workspace_readiness", calls[0].method)
	assert.Equal(t, true, calls[0].params["initial_replay"])
	assert.Equal(t, "parallel_parse", calls[0].params["phase"])
	assert.Equal(t, 3, calls[0].params["tracked_repos"])
}

// TestReadinessBroadcaster_SubscribeBeforePublish — subscribing
// before any publish has no replay; the first publish reaches the
// subscriber normally.
func TestReadinessBroadcaster_SubscribeBeforePublish(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())

	require.False(t, b.subscribe("session-A"), "no state means no replay")
	assert.Empty(t, fake.snapshot())

	b.publish(map[string]any{"phase": "ready", "ready": true})
	calls := fake.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "ready", calls[0].params["phase"])
	assert.Equal(t, true, calls[0].params["ready"])
	_, hasInitial := calls[0].params["initial_replay"]
	assert.False(t, hasInitial, "regular publish must not stamp initial_replay")
}

// TestReadinessBroadcaster_DeltaFilter — identical content (modulo
// the auto-stamped `ts` field) is suppressed; a content change
// fires.
func TestReadinessBroadcaster_DeltaFilter(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())
	b.subscribe("session-A")

	b.publish(map[string]any{"phase": "warming", "ready": false, "n": 1})
	b.publish(map[string]any{"phase": "warming", "ready": false, "n": 1})
	b.publish(map[string]any{"phase": "warming", "ready": false, "n": 2})

	require.Len(t, fake.snapshot(), 2, "exactly two deliveries after delta filter")
}

// TestReadinessBroadcaster_PerSessionDelivery — only subscribed
// sessions receive notifications.
func TestReadinessBroadcaster_PerSessionDelivery(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())
	b.subscribe("A")
	b.subscribe("B")

	b.publish(map[string]any{"phase": "ready", "ready": true})
	targets := fake.sessionsTargeted()
	assert.Equal(t, 1, targets["A"])
	assert.Equal(t, 1, targets["B"])
	assert.Equal(t, 0, targets["C"])
}

// TestReadinessBroadcaster_Unsubscribe — after unsubscribe a session
// stops receiving.
func TestReadinessBroadcaster_Unsubscribe(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newReadinessBroadcaster(fake, zap.NewNop())
	b.subscribe("A")

	b.publish(map[string]any{"phase": "x"})
	require.Len(t, fake.snapshot(), 1)

	b.unsubscribe("A")
	b.publish(map[string]any{"phase": "y"})
	require.Len(t, fake.snapshot(), 1, "no subscriber after unsubscribe")
	assert.Equal(t, 0, b.subscriberCount())
}

// TestReadinessBroadcaster_NilSender — publish with a nil sender is
// a safe no-op.
func TestReadinessBroadcaster_NilSender(t *testing.T) {
	b := newReadinessBroadcaster(nil, zap.NewNop())
	b.subscribe("A")
	b.publish(map[string]any{"phase": "ready"}) // should not panic
}

// TestServer_PublishReadiness_RejectsEmptyPhase — phase is load-
// bearing; an empty value is silently dropped (no panic, no fan-out).
func TestServer_PublishReadiness_RejectsEmptyPhase(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{readinessBroadcaster: newReadinessBroadcaster(fake, zap.NewNop())}
	srv.readinessBroadcaster.subscribe("A")

	srv.PublishReadiness("", false, nil)
	assert.Empty(t, fake.snapshot())
}

// TestServer_ReleaseSession_UnsubscribesReadiness — Server-level
// lifecycle cleanup drops readiness subscribers.
func TestServer_ReleaseSession_UnsubscribesReadiness(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{readinessBroadcaster: newReadinessBroadcaster(fake, zap.NewNop())}
	srv.readinessBroadcaster.subscribe("A")
	require.Equal(t, 1, srv.readinessBroadcaster.subscriberCount())

	srv.ReleaseSession("A")
	assert.Equal(t, 0, srv.readinessBroadcaster.subscriberCount())
}

// TestHashPayload_OrderIndependent — two payloads with the same
// content but different key-insertion orders must hash the same.
func TestHashPayload_OrderIndependent(t *testing.T) {
	a := map[string]any{"phase": "x", "n": 1, "y": "z"}
	b := map[string]any{"y": "z", "n": 1, "phase": "x"}
	assert.Equal(t, hashPayload(a), hashPayload(b))
}

// TestHashPayload_IgnoresTimestamp — the auto-stamped `ts` field
// must not participate in the fingerprint, or no two publishes
// would ever match for the delta filter.
func TestHashPayload_IgnoresTimestamp(t *testing.T) {
	a := map[string]any{"phase": "ready", "ts": "2026-05-17T10:00:00Z"}
	b := map[string]any{"phase": "ready", "ts": "2026-05-17T10:00:01Z"}
	assert.Equal(t, hashPayload(a), hashPayload(b))
}

// TestRegisterReadinessTools_Wiring — the subscribe/unsubscribe
// tool handlers operate on the broadcaster and return JSON-shaped
// results.
func TestRegisterReadinessTools_Wiring(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{readinessBroadcaster: newReadinessBroadcaster(fake, zap.NewNop())}

	req := mcp.CallToolRequest{}
	res, err := srv.handleSubscribeReadiness(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, srv.readinessBroadcaster.subscriberCount())

	res, err = srv.handleUnsubscribeReadiness(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 0, srv.readinessBroadcaster.subscriberCount())
}
