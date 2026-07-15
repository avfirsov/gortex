package store_sqlite

import (
	"database/sql"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type nodeSummaryAllocationScanner struct {
	promoted bool
}

func (s nodeSummaryAllocationScanner) Scan(dest ...any) error {
	*dest[0].(*string) = "repo/pkg/a.go::A"
	*dest[1].(*graph.NodeKind) = graph.KindFunction
	*dest[2].(*string) = "A"
	*dest[3].(*string) = "repo.pkg.A"
	*dest[4].(*string) = "pkg/a.go"
	*dest[5].(*int) = 11
	*dest[6].(*int) = 19
	*dest[7].(*int) = 2
	*dest[8].(*int) = 8
	*dest[9].(*string) = "go"
	*dest[10].(*string) = "repo"
	*dest[11].(*string) = "workspace"
	*dest[12].(*string) = "project"
	if s.promoted {
		*dest[13].(*sql.NullString) = sql.NullString{String: "func A()", Valid: true}
		*dest[15].(*sql.NullString) = sql.NullString{String: "large promoted documentation", Valid: true}
	}
	return nil
}

var nodeSummaryAllocationSink *graph.Node

func TestScanNodeSummaryOmitsMetaAndAllocatesLess(t *testing.T) {
	n, err := scanNodeSummary(nodeSummaryAllocationScanner{})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "repo/pkg/a.go::A" || n.Kind != graph.KindFunction || n.Name != "A" ||
		n.QualName != "repo.pkg.A" || n.FilePath != "pkg/a.go" || n.StartLine != 11 ||
		n.EndLine != 19 || n.StartColumn != 2 || n.EndColumn != 8 || n.Language != "go" ||
		n.RepoPrefix != "repo" || n.WorkspaceID != "workspace" || n.ProjectID != "project" {
		t.Fatalf("summary scan altered identity/location fields: %#v", n)
	}
	if n.Meta != nil {
		t.Fatalf("summary scan materialized Meta: %#v", n.Meta)
	}

	summaryAllocs := testing.AllocsPerRun(1000, func() {
		nodeSummaryAllocationSink, _ = scanNodeSummary(nodeSummaryAllocationScanner{})
	})
	promotedAllocs := testing.AllocsPerRun(1000, func() {
		nodeSummaryAllocationSink, _ = scanNodeLight(nodeSummaryAllocationScanner{promoted: true})
	})
	if summaryAllocs >= promotedAllocs {
		t.Fatalf("summary scan allocations = %.1f, promoted scan = %.1f; want summary lower", summaryAllocs, promotedAllocs)
	}
}

func BenchmarkScanNodeSummary(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		nodeSummaryAllocationSink, _ = scanNodeSummary(nodeSummaryAllocationScanner{})
	}
}

func BenchmarkScanNodeLightPromoted(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		nodeSummaryAllocationSink, _ = scanNodeLight(nodeSummaryAllocationScanner{promoted: true})
	}
}
