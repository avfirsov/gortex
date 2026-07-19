package semantic

import (
	"context"
	"testing"
	"time"
)

func TestApplyGateWait(t *testing.T) {
	t.Run("no gate never waits", func(t *testing.T) {
		if err := ApplyGateWait(context.Background()); err != nil {
			t.Fatalf("gateless context waited: %v", err)
		}
	})
	t.Run("open gate passes", func(t *testing.T) {
		gate := make(chan struct{})
		close(gate)
		ctx := WithApplyGate(context.Background(), gate)
		if err := ApplyGateWait(ctx); err != nil {
			t.Fatalf("open gate blocked: %v", err)
		}
	})
	t.Run("closed-later gate releases the waiter", func(t *testing.T) {
		gate := make(chan struct{})
		ctx := WithApplyGate(context.Background(), gate)
		done := make(chan error, 1)
		go func() { done <- ApplyGateWait(ctx) }()
		select {
		case err := <-done:
			t.Fatalf("waiter returned before the gate opened: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		close(gate)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("released waiter errored: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter not released by gate open")
		}
	})
	t.Run("cancellation beats a shut gate", func(t *testing.T) {
		gate := make(chan struct{})
		ctx, cancel := context.WithCancel(WithApplyGate(context.Background(), gate))
		cancel()
		if err := ApplyGateWait(ctx); err == nil {
			t.Fatal("cancelled context must surface its error, not hang")
		}
	})
	t.Run("nil gate attaches nothing", func(t *testing.T) {
		ctx := WithApplyGate(context.Background(), nil)
		if err := ApplyGateWait(ctx); err != nil {
			t.Fatalf("nil gate waited: %v", err)
		}
	})
}
