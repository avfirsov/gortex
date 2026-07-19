package resolver

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestUnresolvedEdgeStreamBoundsLegacyCorpus(t *testing.T) {
	store := graph.New()
	edges := make([]*graph.Edge, 0, resolvePendingPageRows*2+17)
	for i := 0; i < cap(edges); i++ {
		edges = append(edges, &graph.Edge{
			From: fmt.Sprintf("source-%06d", i), To: fmt.Sprintf("unresolved::target-%06d", i),
			Kind: graph.EdgeCalls, FilePath: "x.go", Line: i + 1,
		})
	}
	store.AddBatch(nil, edges)

	stream := newUnresolvedEdgeStream(store)
	defer stream.close()
	total, peak := 0, 0
	for {
		page, done, err := stream.nextPage()
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > peak {
			peak = len(page)
		}
		total += len(page)
		if done {
			break
		}
	}
	if total != len(edges) {
		t.Fatalf("streamed %d edges, want %d", total, len(edges))
	}
	if peak > resolvePendingPageRows {
		t.Fatalf("peak retained page = %d, bound = %d", peak, resolvePendingPageRows)
	}
}

func TestResolveGuardSpoolPagesBoundChangedJobs(t *testing.T) {
	spool, err := newResolveGuardSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()

	jobs := make([]reindexJob, 0, resolvePendingPageRows*2+31)
	for i := 0; i < cap(jobs); i++ {
		edge := &graph.Edge{From: fmt.Sprintf("r::source-%06d", i), FilePath: "x.go", Line: i + 1}
		jobs = append(jobs, reindexJob{
			edge: edge, oldTo: fmt.Sprintf("unresolved::target-%06d", i),
			newTo: fmt.Sprintf("r::target-%06d", i), kind: graph.EdgeCalls,
			confidence: 0.5, origin: graph.OriginTextMatched,
		})
	}
	if err := spool.appendJobs([][]reindexJob{jobs}); err != nil {
		t.Fatal(err)
	}
	if spool.count != len(jobs) {
		t.Fatalf("spooled %d jobs, want %d", spool.count, len(jobs))
	}

	total, peak, done := 0, 0, false
	for !done {
		page, exhausted, err := spool.nextPage(resolvePendingPageRows)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > peak {
			peak = len(page)
		}
		total += len(page)
		done = exhausted
	}
	if total != len(jobs) || peak > resolvePendingPageRows {
		t.Fatalf("guard replay total=%d peak=%d, want total=%d peak<=%d", total, peak, len(jobs), resolvePendingPageRows)
	}
}

func TestDeferredLSPSpoolKeysetOrderDedupAndPageBound(t *testing.T) {
	spool, err := newDeferredLSPSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.close()

	work := make([]deferredLSPEdge, 0, resolvePendingPageRows*2+43)
	for i := 0; i < cap(work); i++ {
		edge := &graph.Edge{
			From: fmt.Sprintf("source-%06d", i), To: fmt.Sprintf("resolved-%06d", i),
			Kind: graph.EdgeCalls, FilePath: fmt.Sprintf("file-%06d.go", cap(work)-i), Line: i + 1,
		}
		work = append(work, deferredLSPEdge{edge: edge, target: fmt.Sprintf("target-%06d", i)})
	}
	if err := spool.append(work); err != nil {
		t.Fatal(err)
	}
	duplicate := work[0]
	duplicate.edge = cloneEdgeForResolve(work[0].edge)
	duplicate.edge.To = "updated-target"
	duplicate.carried = true
	if err := spool.append([]deferredLSPEdge{duplicate}); err != nil {
		t.Fatal(err)
	}
	if got := spool.count(); got != len(work) {
		t.Fatalf("deduped work count = %d, want %d", got, len(work))
	}

	iterator := spool.iterator(nil)
	var previous deferredLSPWorkKey
	havePrevious := false
	total, peak := 0, 0
	updatedSeen := false
	for {
		page, done, err := iterator.next(resolvePendingPageRows)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > peak {
			peak = len(page)
		}
		for _, record := range page {
			if havePrevious && deferredLSPWorkKeyLess(record.key, previous) {
				t.Fatalf("spool order regressed: %#v before %#v", previous, record.key)
			}
			previous, havePrevious = record.key, true
			if record.key == deferredLSPWorkKeyFor(duplicate) {
				updatedSeen = record.currentTo == "updated-target" && record.carried
			}
		}
		total += len(page)
		if done {
			break
		}
	}
	if total != len(work) || peak > resolvePendingPageRows || !updatedSeen {
		t.Fatalf("LSP replay total=%d peak=%d updated=%v; want total=%d peak<=%d updated", total, peak, updatedSeen, len(work), resolvePendingPageRows)
	}
}
