package reach

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

type panicOnAddNodeStore struct {
	graph.Store
}

func (panicOnAddNodeStore) AddNode(*graph.Node) {
	panic("LookupContext must not write reach metadata through AddNode")
}

func TestLookupContextLazyMissIsReadOnly(t *testing.T) {
	backing := graph.New()
	backing.AddNode(&graph.Node{ID: "seed", Name: "seed", Kind: graph.KindFunction})
	store := panicOnAddNodeStore{Store: backing}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	_, _, _, hit, truncated := LookupContext(ctx, store, "seed")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("lazy read-only lookup took %s; want <= 500ms", elapsed)
	}
	if !hit {
		t.Fatal("lazy lookup did not return a bounded result")
	}
	if truncated {
		t.Fatal("uncontended lazy lookup unexpectedly truncated")
	}
}

func TestLookupContextExpiredContextIsBoundedAndConservative(t *testing.T) {
	backing := graph.New()
	backing.AddNode(&graph.Node{ID: "seed", Name: "seed", Kind: graph.KindFunction})
	store := panicOnAddNodeStore{Store: backing}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	_, _, _, hit, truncated := LookupContext(ctx, store, "seed")
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("expired lookup took %s; want <= 100ms", elapsed)
	}
	if !hit {
		t.Fatal("expired lookup must return a bounded conservative result")
	}
	if !truncated {
		t.Fatal("expired lookup must report truncation")
	}
}

func TestLookupContextRepeatedLazyMissIsStableAndReadOnly(t *testing.T) {
	backing, ids := newCallChain(t, 4)
	store := panicOnAddNodeStore{Store: backing}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	d1a, d2a, d3a, hit, truncated := LookupContext(ctx, store, ids[3])
	if !hit || truncated {
		t.Fatalf("first lookup: hit=%v truncated=%v", hit, truncated)
	}
	d1b, d2b, d3b, hit, truncated := LookupContext(ctx, store, ids[3])
	if !hit || truncated {
		t.Fatalf("second lookup: hit=%v truncated=%v", hit, truncated)
	}
	if !reflect.DeepEqual(d1a, d1b) || !reflect.DeepEqual(d2a, d2b) || !reflect.DeepEqual(d3a, d3b) {
		t.Fatalf("repeated lazy lookup changed topology: first=(%v,%v,%v) second=(%v,%v,%v)", d1a, d2a, d3a, d1b, d2b, d3b)
	}
}
