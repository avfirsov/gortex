package mcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestHealthBroadcaster_PublishOnce_NoSnapshotFn — without a snapFn
// nothing is sent. This is the pre-AttachHealthSnapshot state.
func TestHealthBroadcaster_PublishOnce_NoSnapshotFn(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, nil, zap.NewNop())
	b.subscribers["A"] = true

	b.publishOnce()
	assert.Empty(t, fake.snapshot())
}

// TestHealthBroadcaster_PublishOnce_FanOut — with a subscriber and a
// snapFn, publishOnce delivers a notification.
func TestHealthBroadcaster_PublishOnce_FanOut(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, func() map[string]any {
		return map[string]any{"uptime_seconds": int64(10), "sessions": 1}
	}, zap.NewNop())
	b.subscribers["A"] = true

	b.publishOnce()
	calls := fake.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "notifications/daemon_health", calls[0].method)
	assert.Equal(t, int64(10), calls[0].params["uptime_seconds"])
	assert.Equal(t, 1, calls[0].params["sessions"])
	assert.NotEmpty(t, calls[0].params["ts"], "ts must be stamped")
}

// TestHealthBroadcaster_DeltaFilter — identical snapshots are
// suppressed; content change fires.
func TestHealthBroadcaster_DeltaFilter(t *testing.T) {
	fake := &fakeSpecificSender{}
	var value atomic.Int64
	value.Store(1)
	b := newHealthBroadcaster(fake, func() map[string]any {
		return map[string]any{"num_goroutine": int(value.Load())}
	}, zap.NewNop())
	b.subscribers["A"] = true

	b.publishOnce()
	b.publishOnce()
	value.Store(2)
	b.publishOnce()
	b.publishOnce()

	require.Len(t, fake.snapshot(), 2, "delta filter must suppress identical snapshots")
}

// TestHealthBroadcaster_Subscribe_FiresInitialReplay — a fresh
// subscriber gets a snapshot immediately, tagged initial_replay.
func TestHealthBroadcaster_Subscribe_FiresInitialReplay(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, func() map[string]any {
		return map[string]any{"uptime_seconds": int64(5)}
	}, zap.NewNop())

	replayed := b.subscribe("A")
	defer b.stop()
	require.True(t, replayed)

	calls := fake.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, true, calls[0].params["initial_replay"])
}

// TestHealthBroadcaster_TickerStartsAndStops — the first subscribe
// starts the ticker goroutine; the last unsubscribe stops it. We
// inspect stopTicker rather than waiting for clock-real ticks
// because the min publish interval is 1 s and a test that sleeps
// past that would slow the suite down.
func TestHealthBroadcaster_TickerStartsAndStops(t *testing.T) {
	fake := &fakeSpecificSender{}
	counter := &atomic.Int64{}
	b := newHealthBroadcaster(fake, func() map[string]any {
		v := counter.Add(1)
		return map[string]any{"tick": v}
	}, zap.NewNop())

	require.Nil(t, b.stopTicker, "ticker not running before any subscribe")

	b.subscribe("A")
	b.mu.Lock()
	running := b.stopTicker != nil
	b.mu.Unlock()
	assert.True(t, running, "ticker must start on first subscribe")

	b.unsubscribe("A")
	b.mu.Lock()
	stillRunning := b.stopTicker != nil
	b.mu.Unlock()
	assert.False(t, stillRunning, "ticker must stop on last unsubscribe")
}

// TestHealthBroadcaster_SetInterval_Clamps — sub-second and >5min
// intervals are clamped.
func TestHealthBroadcaster_SetInterval_Clamps(t *testing.T) {
	b := newHealthBroadcaster(nil, nil, zap.NewNop())

	b.setInterval(10 * time.Millisecond)
	assert.Equal(t, minHealthInterval, b.interval)

	b.setInterval(1 * time.Hour)
	assert.Equal(t, maxHealthInterval, b.interval)

	b.setInterval(30 * time.Second)
	assert.Equal(t, 30*time.Second, b.interval)
}

// TestHealthBroadcaster_SetSnapshotFn — replacing snapFn is honored
// on the next publishOnce.
func TestHealthBroadcaster_SetSnapshotFn(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, nil, zap.NewNop())
	b.subscribers["A"] = true

	b.publishOnce()
	require.Empty(t, fake.snapshot())

	b.setSnapshotFn(func() map[string]any { return map[string]any{"x": 1} })
	b.publishOnce()
	require.Len(t, fake.snapshot(), 1)
}

// TestHealthBroadcaster_EmptySnapshotSkipped — a snapFn that returns
// an empty map causes no fan-out (avoids stamping a useless `ts: …`
// payload when there's no real signal).
func TestHealthBroadcaster_EmptySnapshotSkipped(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, func() map[string]any { return map[string]any{} }, zap.NewNop())
	b.subscribers["A"] = true

	b.publishOnce()
	assert.Empty(t, fake.snapshot())
}

// TestServer_ReleaseSession_UnsubscribesHealth — Server cleanup
// path drops health subscribers.
func TestServer_ReleaseSession_UnsubscribesHealth(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{healthBroadcaster: newHealthBroadcaster(fake, func() map[string]any { return map[string]any{"x": 1} }, zap.NewNop())}
	srv.healthBroadcaster.subscribe("A")
	defer srv.healthBroadcaster.stop()
	require.Equal(t, 1, srv.healthBroadcaster.subscriberCount())

	srv.ReleaseSession("A")
	assert.Equal(t, 0, srv.healthBroadcaster.subscriberCount())
}

// TestRegisterHealthTools_Wiring — the subscribe/unsubscribe tool
// handlers operate on the broadcaster and return JSON results.
func TestRegisterHealthTools_Wiring(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{healthBroadcaster: newHealthBroadcaster(fake, func() map[string]any { return map[string]any{"x": 1} }, zap.NewNop())}
	defer srv.healthBroadcaster.stop()

	req := mcp.CallToolRequest{}
	res, err := srv.handleSubscribeHealth(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, srv.healthBroadcaster.subscriberCount())

	res, err = srv.handleUnsubscribeHealth(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 0, srv.healthBroadcaster.subscriberCount())
}

// TestHealthBroadcaster_Snapshot_TracksLastPublish — snapshot()
// returns nil before any publish has happened and the last-sent
// payload after.
func TestHealthBroadcaster_Snapshot_TracksLastPublish(t *testing.T) {
	fake := &fakeSpecificSender{}
	b := newHealthBroadcaster(fake, func() map[string]any { return map[string]any{"x": 42} }, zap.NewNop())
	require.Nil(t, b.snapshot(), "no publish yet")

	b.subscribers["A"] = true
	b.publishOnce()
	snap := b.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, 42, snap["x"])
	assert.NotEmpty(t, snap["ts"])
}

// TestServer_NotificationsStatus_ExposesAllChannels — graph_stats /
// gortex://stats carries a `notifications` block reporting subscriber
// counts and last-known states for every wired broadcaster.
func TestServer_NotificationsStatus_ExposesAllChannels(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{
		readinessBroadcaster: newReadinessBroadcaster(fake, zap.NewNop()),
		healthBroadcaster:    newHealthBroadcaster(fake, func() map[string]any { return map[string]any{"uptime_seconds": int64(7)} }, zap.NewNop()),
		staleRefsBroadcaster: newStaleRefsBroadcaster(fake, nil, newSessionState(), zap.NewNop()),
	}
	srv.readinessBroadcaster.subscribe("R")
	srv.readinessBroadcaster.publish(map[string]any{"phase": "ready", "ready": true})
	srv.healthBroadcaster.subscribers["H"] = true
	srv.healthBroadcaster.publishOnce()
	srv.staleRefsBroadcaster.subscribe("S")

	ns := srv.notificationsStatus()
	require.NotNil(t, ns)

	r := ns["workspace_readiness"].(map[string]any)
	assert.Equal(t, 1, r["subscribers"])
	require.NotNil(t, r["last_state"])
	assert.Equal(t, "ready", r["last_state"].(map[string]any)["phase"])

	h := ns["daemon_health"].(map[string]any)
	assert.Equal(t, 1, h["subscribers"])
	require.NotNil(t, h["last_snapshot"])
	assert.Equal(t, int64(7), h["last_snapshot"].(map[string]any)["uptime_seconds"])

	st := ns["stale_refs"].(map[string]any)
	assert.Equal(t, 1, st["subscribers"])
}

// TestServer_NotificationsStatus_NilWhenUnwired — a Server without
// any broadcaster wired returns nil so graph_stats output stays
// uncluttered for single-shot CLI modes.
func TestServer_NotificationsStatus_NilWhenUnwired(t *testing.T) {
	srv := &Server{}
	assert.Nil(t, srv.notificationsStatus())
}

// TestServer_AttachHealthSnapshot_Replaces — re-attaching a new
// snapshot fn is honored on the next publish.
func TestServer_AttachHealthSnapshot_Replaces(t *testing.T) {
	fake := &fakeSpecificSender{}
	srv := &Server{healthBroadcaster: newHealthBroadcaster(fake, func() map[string]any { return map[string]any{"v": 1} }, zap.NewNop())}
	srv.healthBroadcaster.subscribers["A"] = true
	srv.healthBroadcaster.publishOnce()

	srv.AttachHealthSnapshot(func() map[string]any { return map[string]any{"v": 2} })
	srv.healthBroadcaster.publishOnce()

	calls := fake.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, 1, calls[0].params["v"])
	assert.Equal(t, 2, calls[1].params["v"])
}
