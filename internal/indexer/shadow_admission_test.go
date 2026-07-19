package indexer

import (
	"sync"
	"testing"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/search"
	"go.uber.org/zap"
)

func TestShadowAdmissionIsNonBlockingAndReusable(t *testing.T) {
	budget := newShadowAdmissionBudget(100)
	first, ok := budget.tryAcquire(60)
	if !ok {
		t.Fatal("first lease rejected")
	}
	if second, ok := budget.tryAcquire(60); ok || second != nil {
		t.Fatal("over-budget lease admitted instead of selecting SQLite")
	}
	first.Release()
	first.Release() // release is idempotent on every exit path
	third, ok := budget.tryAcquire(60)
	if !ok {
		t.Fatal("budget was not reusable after release")
	}
	third.Release()
	capacity, used, peak := budget.snapshot()
	if capacity != 100 || used != 0 || peak != 60 {
		t.Fatalf("capacity/used/peak = %d/%d/%d, want 100/0/60", capacity, used, peak)
	}
}

func TestShadowAdmissionConcurrentReposNeverOvercommit(t *testing.T) {
	budget := newShadowAdmissionBudget(128)
	const contenders = 32
	attempted := sync.WaitGroup{}
	released := sync.WaitGroup{}
	attempted.Add(contenders)
	released.Add(contenders)
	release := make(chan struct{})
	accepted := make(chan *shadowAdmissionLease, contenders)
	for i := 0; i < contenders; i++ {
		go func() {
			defer released.Done()
			lease, ok := budget.tryAcquire(32)
			if ok {
				accepted <- lease
			}
			attempted.Done()
			if ok {
				<-release
				lease.Release()
			}
		}()
	}
	attempted.Wait()
	if got := len(accepted); got != 4 {
		t.Fatalf("simultaneous admitted repos = %d, want 4", got)
	}
	capacity, used, peak := budget.snapshot()
	if capacity != 128 || used != 128 || peak != 128 {
		t.Fatalf("capacity/used/peak while held = %d/%d/%d, want 128/128/128", capacity, used, peak)
	}
	close(release)
	released.Wait()
	_, used, peak = budget.snapshot()
	if used != 0 || peak != 128 {
		t.Fatalf("used/peak after release = %d/%d, want 0/128", used, peak)
	}
}

func TestShadowAdmissionIsProcessWideAcrossMultiIndexers(t *testing.T) {
	g := graph.New()
	reg := parser.NewRegistry()
	logger := zap.NewNop()
	first := NewMultiIndexer(g, reg, search.NewBM25(), nil, logger)
	second := NewMultiIndexer(g, reg, search.NewBM25(), nil, logger)
	standalone := New(g, reg, config.IndexConfig{}, logger)
	if first.shadowAdmission == nil || first.shadowAdmission != second.shadowAdmission ||
		first.shadowAdmission != standalone.shadowAdmission {
		t.Fatal("indexers do not share one process-wide shadow admission gate")
	}
}

func TestShadowAdmissionWeightRejectsPriorLargeRepoShape(t *testing.T) {
	weight := shadowAdmissionWeight(35_000, 0)
	if weight <= defaultShadowProcessBudgetBytes {
		t.Fatalf("35k-file shadow charge = %d, must exceed default 1 GiB budget", weight)
	}
}
