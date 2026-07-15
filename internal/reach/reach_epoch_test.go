package reach

import "testing"

func TestLookupRejectsPersistedRecordFromPreviousProcessEpoch(t *testing.T) {
	g, ids := newCallChain(t, 3)
	seed := g.GetNode(ids[2])
	if seed == nil {
		t.Fatal("seed node missing")
	}
	stale := *seed
	stale.Meta = map[string]any{
		MetaReachBuild:     BuildCounter(),
		MetaReachEpoch:     "previous-daemon-process",
		MetaReachComplete:  true,
		MetaReachTruncated: false,
	}
	g.AddNode(&stale)

	d1, d2, d3, hit, truncated := LookupContext(t.Context(), g, ids[2])
	if !hit || truncated {
		t.Fatalf("lookup status hit=%v truncated=%v, want exact recomputation", hit, truncated)
	}
	if got, want := joinIDs(d1), ids[1]; got != want {
		t.Fatalf("d1 = %q, want %q; stale empty record was accepted", got, want)
	}
	if got, want := joinIDs(d2), ids[0]; got != want {
		t.Fatalf("d2 = %q, want %q", got, want)
	}
	if len(d3) != 0 {
		t.Fatalf("d3 = %v, want empty", d3)
	}
}

func TestBuildIndexStampsCurrentProcessEpoch(t *testing.T) {
	g, ids := newCallChain(t, 2)
	BuildIndex(g)
	seed := g.GetNode(ids[1])
	if seed == nil {
		t.Fatal("seed node missing")
	}
	if got, _ := seed.Meta[MetaReachEpoch].(string); got != reachProcessEpoch {
		t.Fatalf("reach epoch = %q, want current process epoch %q", got, reachProcessEpoch)
	}
}
