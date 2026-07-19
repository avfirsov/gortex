package graph

import (
	"fmt"
	"testing"
)

func TestBoundedDestructiveGraphDrainLargeRepository(t *testing.T) {
	const (
		nodeCount = 25000
		maxRows   = 127
		maxBytes  = 32 << 10
	)
	g := New()
	nodes := make([]*Node, 0, nodeCount)
	edges := make([]*Edge, 0, nodeCount*2)
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("repo/pkg/node-%06d", i)
		nodes = append(nodes, &Node{
			ID: id, Name: fmt.Sprintf("Function%06d", i), Kind: KindFunction,
			FilePath: fmt.Sprintf("repo/pkg/file-%05d.go", i/10),
			Language: "go", RepoPrefix: "repo",
		})
	}
	for i := 0; i < nodeCount; i++ {
		from := nodes[i].ID
		edges = append(edges,
			&Edge{From: from, To: nodes[(i+1)%nodeCount].ID, Kind: EdgeKind("calls"), FilePath: nodes[i].FilePath, Line: i + 1},
			&Edge{From: from, To: nodes[(i+7)%nodeCount].ID, Kind: EdgeKind("references"), FilePath: nodes[i].FilePath, Line: i + 1},
		)
	}
	g.AddBatch(nodes, edges)

	seenNodes := make(map[string]struct{}, nodeCount)
	nodeBatches := 0
	for batch := range g.DrainNodeBatches(maxRows, maxBytes) {
		nodeBatches++
		if len(batch) == 0 || len(batch) > maxRows {
			t.Fatalf("node batch size %d outside 1..%d", len(batch), maxRows)
		}
		var bytes uint64
		for i, node := range batch {
			bytes += nodeBytes(node)
			if i > 0 && batch[i-1].ID > node.ID {
				t.Fatalf("node batch is not locally sorted: %q before %q", batch[i-1].ID, node.ID)
			}
			if _, duplicate := seenNodes[node.ID]; duplicate {
				t.Fatalf("node %q drained twice", node.ID)
			}
			seenNodes[node.ID] = struct{}{}
		}
		if len(batch) > 1 && bytes > maxBytes {
			t.Fatalf("node batch retained %d bytes, cap %d", bytes, maxBytes)
		}
	}
	if nodeBatches < 2 || len(seenNodes) != nodeCount {
		t.Fatalf("drained %d unique nodes in %d batches, want %d", len(seenNodes), nodeBatches, nodeCount)
	}
	if got := g.NodeCount(); got != 0 {
		t.Fatalf("NodeCount after drain = %d, want 0", got)
	}
	if est := g.RepoMemoryEstimate("repo"); est.NodeCount != 0 || est.NodeBytes != 0 {
		t.Fatalf("node memory counters survived drain: %+v", est)
	}

	seenEdges := make(map[string]struct{}, nodeCount*2)
	edgeBatches := 0
	for batch := range g.DrainEdgeBatches(maxRows, maxBytes) {
		edgeBatches++
		if len(batch) == 0 || len(batch) > maxRows {
			t.Fatalf("edge batch size %d outside 1..%d", len(batch), maxRows)
		}
		var bytes uint64
		for i, edge := range batch {
			bytes += edgeBytes(edge)
			if i > 0 && compareDrainEdges(batch[i-1], edge) > 0 {
				t.Fatalf("edge batch is not locally sorted")
			}
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", edge.From, edge.To, edge.Kind, edge.FilePath, edge.Line)
			if _, duplicate := seenEdges[key]; duplicate {
				t.Fatalf("edge %q drained twice", key)
			}
			seenEdges[key] = struct{}{}
		}
		if len(batch) > 1 && bytes > maxBytes {
			t.Fatalf("edge batch retained %d bytes, cap %d", bytes, maxBytes)
		}
	}
	if edgeBatches < 2 || len(seenEdges) != nodeCount*2 {
		t.Fatalf("drained %d unique edges in %d batches, want %d", len(seenEdges), edgeBatches, nodeCount*2)
	}
	if got := g.EdgeCount(); got != 0 {
		t.Fatalf("EdgeCount after drain = %d, want 0", got)
	}
	if est := g.RepoMemoryEstimate("repo"); est.NodeCount != 0 || est.EdgeCount != 0 || est.NodeBytes != 0 || est.EdgeBytes != 0 {
		t.Fatalf("repo memory counters survived complete drain: %+v", est)
	}
}

func TestBoundedDestructiveCompactSidecarDrain(t *testing.T) {
	const (
		rowCount = 5000
		maxRows  = 73
		maxBytes = 8 << 10
	)
	g := New()
	files := make([]FileMetaRow, 0, rowCount)
	constants := make([]ConstantValueRow, 0, rowCount)
	for i := 0; i < rowCount; i++ {
		path := fmt.Sprintf("repo/pkg/file-%05d.go", i)
		files = append(files, FileMetaRow{FilePath: path, ContentHash: fmt.Sprintf("hash-%05d", i), Size: i, NodeCount: 1})
		constants = append(constants, ConstantValueRow{NodeID: path + "::C", FilePath: path, Value: fmt.Sprintf("value-%05d", i)})
	}
	if err := g.SetFileMetas("repo", files); err != nil {
		t.Fatal(err)
	}
	if err := g.BulkSetConstantValues("repo", constants); err != nil {
		t.Fatal(err)
	}
	if err := g.SetFileMetas("sibling", []FileMetaRow{{FilePath: "sibling/keep.go"}}); err != nil {
		t.Fatal(err)
	}

	fileSeen := 0
	for batch := range g.DrainFileMetaBatches("repo", maxRows, maxBytes) {
		if len(batch) == 0 || len(batch) > maxRows {
			t.Fatalf("file-meta batch size %d outside cap", len(batch))
		}
		for i := 1; i < len(batch); i++ {
			if batch[i-1].FilePath > batch[i].FilePath {
				t.Fatal("file-meta batch is not locally sorted")
			}
		}
		fileSeen += len(batch)
	}
	if fileSeen != rowCount {
		t.Fatalf("drained %d file-meta rows, want %d", fileSeen, rowCount)
	}
	rows, err := g.FileMetasForRepo("repo")
	if err != nil || len(rows) != 0 {
		t.Fatalf("repo file metadata survived drain: rows=%d err=%v", len(rows), err)
	}
	rows, err = g.FileMetasForRepo("sibling")
	if err != nil || len(rows) != 1 {
		t.Fatalf("sibling file metadata changed: rows=%d err=%v", len(rows), err)
	}

	constantSeen := 0
	for batch := range g.DrainConstantValueBatches("repo", maxRows, maxBytes) {
		if len(batch) == 0 || len(batch) > maxRows {
			t.Fatalf("constant batch size %d outside cap", len(batch))
		}
		for i := 1; i < len(batch); i++ {
			if batch[i-1].NodeID > batch[i].NodeID {
				t.Fatal("constant batch is not locally sorted")
			}
		}
		constantSeen += len(batch)
	}
	if constantSeen != rowCount {
		t.Fatalf("drained %d constant rows, want %d", constantSeen, rowCount)
	}
	values, err := g.ConstantValuesByNodeIDs([]string{constants[0].NodeID, constants[rowCount-1].NodeID})
	if err != nil || len(values) != 0 {
		t.Fatalf("repo constant values survived drain: values=%d err=%v", len(values), err)
	}
}
