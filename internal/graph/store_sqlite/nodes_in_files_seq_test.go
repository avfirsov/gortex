package store_sqlite

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// The streaming projection must agree with the slice form exactly: same
// node set, groups keyed by file in the caller's deduped order, each group
// ID-sorted, zero-node files never yielded.
func TestNodesInFilesByKindSeqMatchesFinder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "seq_parity.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	var nodes []*graph.Node
	for f := 0; f < 5; f++ {
		file := fmt.Sprintf("pkg/f%d.go", f)
		for n := 0; n < 4; n++ {
			kind := graph.KindFunction
			if n%2 == 1 {
				kind = graph.KindLocal // excluded from the queried kind set
			}
			nodes = append(nodes, &graph.Node{
				ID:       fmt.Sprintf("%s::s%d", file, n),
				Name:     fmt.Sprintf("s%d", n),
				Kind:     kind,
				FilePath: file,
				Language: "go",
			})
		}
	}
	s.AddBatch(nodes, nil)

	// f9.go does not exist; f1.go is requested twice to exercise dedup.
	files := []string{"pkg/f3.go", "pkg/f1.go", "pkg/f9.go", "pkg/f1.go", "pkg/f0.go"}
	kinds := []graph.NodeKind{graph.KindFunction, graph.KindMethod}

	var seqIDs []string
	var yieldedFiles []string
	for file, group := range s.NodesInFilesByKindSeq(files, kinds) {
		yieldedFiles = append(yieldedFiles, file)
		if len(group) == 0 {
			t.Fatalf("file %s yielded an empty group", file)
		}
		if !sort.SliceIsSorted(group, func(i, j int) bool { return group[i].ID < group[j].ID }) {
			t.Fatalf("group for %s not ID-sorted", file)
		}
		for _, n := range group {
			if n.FilePath != file {
				t.Fatalf("group for %s contains node from %s", file, n.FilePath)
			}
			seqIDs = append(seqIDs, n.ID)
		}
	}
	wantFiles := []string{"pkg/f3.go", "pkg/f1.go", "pkg/f0.go"}
	if fmt.Sprint(yieldedFiles) != fmt.Sprint(wantFiles) {
		t.Fatalf("yielded files = %v, want %v (caller order, empties dropped)", yieldedFiles, wantFiles)
	}

	sliceNodes := s.NodesInFilesByKind(files, kinds)
	if !sort.SliceIsSorted(sliceNodes, func(i, j int) bool { return sliceNodes[i].ID < sliceNodes[j].ID }) {
		t.Fatal("NodesInFilesByKind result not ID-sorted")
	}
	var sliceIDs []string
	for _, n := range sliceNodes {
		sliceIDs = append(sliceIDs, n.ID)
	}
	sort.Strings(seqIDs)
	if fmt.Sprint(seqIDs) != fmt.Sprint(sliceIDs) {
		t.Fatalf("seq nodes = %v, slice nodes = %v", seqIDs, sliceIDs)
	}

	// Early termination must not run past the consumer's stop.
	var first string
	for file := range s.NodesInFilesByKindSeq(files, kinds) {
		first = file
		break
	}
	if first != "pkg/f3.go" {
		t.Fatalf("first yielded file = %q, want pkg/f3.go", first)
	}
}
