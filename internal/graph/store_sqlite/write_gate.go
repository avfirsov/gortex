package store_sqlite

import (
	"context"
	"sync"
)

// sqliteWriteGate is a zero-value, context-aware binary semaphore. It keeps
// the Lock/Unlock surface used by existing mutation paths while allowing
// maintenance and bounded resolver work to stop waiting when their context
// expires. A channel is used instead of racing a helper goroutine against a
// sync.Mutex; the latter leaks a waiter that can acquire the mutex after the
// caller has already returned.
type sqliteWriteGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *sqliteWriteGate) init() {
	g.once.Do(func() {
		g.token = make(chan struct{}, 1)
		g.token <- struct{}{}
	})
}

func (g *sqliteWriteGate) Lock() {
	_ = g.LockContext(context.Background())
}

func (g *sqliteWriteGate) LockContext(ctx context.Context) error {
	g.init()
	// A single select may choose the token nondeterministically when ctx is
	// already canceled and the gate is available. Check before waiting, then
	// re-check after acquisition and restore the token on cancellation so a
	// maintenance deadline can never start work after it has expired.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
		if err := ctx.Err(); err != nil {
			g.token <- struct{}{}
			return err
		}
		return nil
	}
}

func (g *sqliteWriteGate) TryLock() bool {
	g.init()
	select {
	case <-g.token:
		return true
	default:
		return false
	}
}

func (g *sqliteWriteGate) Unlock() {
	g.init()
	select {
	case g.token <- struct{}{}:
	default:
		panic("store_sqlite: unlock of unlocked write gate")
	}
}
