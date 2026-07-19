package store_sqlite

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestAnalysisProjectionStreamsScopedRowsAndHonorsEarlyStop(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "analysis-projection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	nodes := make([]*graph.Node, 0, 258)
	for i := 0; i < 256; i++ {
		nodes = append(nodes, &graph.Node{
			ID:       "leaf-" + formatProjectionIndex(i),
			Kind:     graph.KindVariable,
			Name:     "leaf",
			FilePath: "pkg/leaf.go",
			Meta:     map[string]any{"large": "metadata stays out of the light projection"},
		})
	}
	nodes = append(nodes,
		&graph.Node{ID: "entry", Kind: graph.KindFunction, Name: "Serve", FilePath: "cmd/main.go", Meta: map[string]any{"entry_point": true}},
		&graph.Node{ID: "method", Kind: graph.KindMethod, Name: "Run", FilePath: "pkg/run.go", Meta: map[string]any{"visibility": "public"}},
	)
	store.AddBatch(nodes, []*graph.Edge{
		{From: "entry", To: "method", Kind: graph.EdgeCalls, Meta: map[string]any{"large": "edge metadata"}},
		{From: "entry", To: "leaf-000", Kind: graph.EdgeReferences, Meta: map[string]any{"large": "unrequested"}},
	})

	seenNodes := 0
	for node := range store.NodesLightSeq() {
		seenNodes++
		if node.Meta != nil {
			t.Fatalf("light node projection decoded Meta: %#v", node.Meta)
		}
		break
	}
	if seenNodes != 1 {
		t.Fatalf("early-stop light nodes = %d, want 1", seenNodes)
	}

	seenEdges := 0
	for edge := range store.EdgesLightSeq(graph.EdgeCalls) {
		seenEdges++
		if edge.Kind != graph.EdgeCalls || edge.Meta != nil {
			t.Fatalf("unexpected light edge projection: %#v", edge)
		}
	}
	if seenEdges != 1 {
		t.Fatalf("call projection rows = %d, want 1", seenEdges)
	}

	fullKinds := map[graph.NodeKind]map[string]any{}
	for node := range store.NodesByKindsSeq(graph.KindFunction, graph.KindMethod) {
		fullKinds[node.Kind] = node.Meta
	}
	if len(fullKinds) != 2 || fullKinds[graph.KindFunction]["entry_point"] != true || fullKinds[graph.KindMethod]["visibility"] != "public" {
		t.Fatalf("full kind projection lost scoring metadata: %#v", fullKinds)
	}

	assertAnalysisProjectionUsesIndex(t, store,
		`SELECT `+edgeColsLight+` FROM edges WHERE kind IN (?, ?)`,
		[]any{string(graph.EdgeCalls), string(graph.EdgeReferences)},
		"edges_by_kind")
	assertAnalysisProjectionUsesIndex(t, store,
		`SELECT `+lookupNodeCols+` FROM nodes WHERE kind IN (?, ?)`,
		[]any{string(graph.KindFunction), string(graph.KindMethod)},
		"nodes_by_kind")
}

func formatProjectionIndex(value int) string {
	return string([]byte{'0' + byte(value/100), '0' + byte((value/10)%10), '0' + byte(value%10)})
}

func assertAnalysisProjectionUsesIndex(t *testing.T, store *Store, query string, args []any, index string) {
	t.Helper()
	rows, err := store.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), index) {
		t.Fatalf("query plan does not use %s:\n%s", index, plan.String())
	}
}
