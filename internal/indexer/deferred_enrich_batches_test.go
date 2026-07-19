package indexer

import "testing"

func TestDeferredEnrichQueueOrdersHeavyGoFirst(t *testing.T) {
	nonA := &Indexer{repoPrefix: "non-a"}
	nonB := &Indexer{repoPrefix: "non-b"}
	goHeavy := &Indexer{repoPrefix: "go-heavy"}
	goLight := &Indexer{repoPrefix: "go-light"}
	nonC := &Indexer{repoPrefix: "non-c"}
	languages := map[*Indexer][]string{
		nonA:    {"python"},
		nonB:    {"typescript"},
		goHeavy: {"go"},
		goLight: {"go", "python"},
		nonC:    {"rust"},
	}
	goNodes := map[*Indexer]int{
		goHeavy: goHeavyEnrichNodeThreshold,
		// A grammar repo's Go bindings: seconds of go/packages work — it
		// schedules like any light repo instead of fragmenting the pool.
		goLight: 100,
	}

	queue := deferredEnrichQueue([]*Indexer{nonA, nonB, goHeavy, goLight, nonC}, languages, goNodes)
	// The heavy repo leads so the go/packages gate chain starts on the first
	// lane immediately; every other repo keeps its incoming (spec-grouped)
	// relative order.
	want := []*Indexer{goHeavy, nonA, nonB, goLight, nonC}
	if len(queue) != len(want) {
		t.Fatalf("queue length = %d, want %d", len(queue), len(want))
	}
	for i := range want {
		if queue[i] != want[i] {
			t.Fatalf("queue[%d] = %q, want %q", i, queue[i].repoPrefix, want[i].repoPrefix)
		}
	}
}
