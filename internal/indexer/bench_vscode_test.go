package indexer

import (
	"context"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/progress"
)

// TestVSCodeProfile indexes a TS-heavy external repo (vscode/src/vs, ~5600
// TS files, 101MB) with CPU profiling enabled. Skipped unless the repo
// exists. Dumps the profile to /tmp/gortex-vscode.prof so `go tool pprof`
// can pick it up. Not a unit test — a measurement harness.
func TestVSCodeProfile(t *testing.T) {
	root := "/Users/zzet/code/oss/vscode/src/vs"
	if _, err := os.Stat(root); err != nil {
		t.Skipf("vscode not available: %v", err)
	}
	f, err := os.Create("/tmp/gortex-vscode.prof")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close profile file: %v", err)
		}
	}()
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatal(err)
	}
	defer pprof.StopCPUProfile()

	reporter := &timingReporter{}

	start := time.Now()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := New(g, reg, config.IndexConfig{}, zap.NewNop())
	ctx := progress.WithReporter(context.Background(), reporter)
	result, err := idx.IndexCtx(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("vscode/src/vs indexed in %s — files=%d nodes=%d edges=%d errors=%d",
		elapsed, result.FileCount, result.NodeCount, result.EdgeCount, len(result.Errors))
	for i, ts := range reporter.timings {
		var dur time.Duration
		if i+1 < len(reporter.timings) {
			dur = reporter.timings[i+1].at.Sub(ts.at)
		} else {
			dur = time.Since(ts.at)
		}
		t.Logf("  [%7s] %s", dur.Round(time.Millisecond), ts.stage)
	}
}

type stageTick struct {
	stage string
	at    time.Time
}

type timingReporter struct {
	last    string
	timings []stageTick
}

func (r *timingReporter) Report(stage string, _, _ int) {
	if stage == r.last {
		return
	}
	r.timings = append(r.timings, stageTick{stage: stage, at: time.Now()})
	r.last = stage
}

var _ progress.Reporter = (*timingReporter)(nil)
