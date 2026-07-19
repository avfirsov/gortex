package graph

import "testing"

func TestGraphEdgeMutationRevisionCoversSameKeyPayloadChanges(t *testing.T) {
	g := New()
	edge := &Edge{
		From: "repo::caller", To: UnresolvedMarker + "Target", Kind: EdgeCalls,
		FilePath: "pkg/a.go", Line: 7, Confidence: 0.25,
		Meta: map[string]any{"version": "original"},
	}

	before := g.EdgeMutationRevision()
	g.AddEdge(edge)
	afterInsert := g.EdgeMutationRevision()
	if afterInsert <= before {
		t.Fatalf("insert revision=%d after %d, want advance", afterInsert, before)
	}

	replacement := *edge
	replacement.Confidence = 0.95
	replacement.Meta = map[string]any{"version": "replacement"}
	g.AddEdge(&replacement)
	afterReplacement := g.EdgeMutationRevision()
	if afterReplacement <= afterInsert {
		t.Fatalf("same-key replacement revision=%d after %d, want advance", afterReplacement, afterInsert)
	}

	if !g.SetEdgeProvenance(&replacement, OriginLSPResolved) {
		t.Fatal("provenance update was not applied")
	}
	afterProvenance := g.EdgeMutationRevision()
	if afterProvenance <= afterReplacement {
		t.Fatalf("provenance revision=%d after %d, want advance", afterProvenance, afterReplacement)
	}

	replacement.Confidence = 0.75
	g.ReindexEdges([]EdgeReindex{{Edge: &replacement, OldTo: replacement.To, OldKind: replacement.Kind}})
	afterSameKeyReindex := g.EdgeMutationRevision()
	if afterSameKeyReindex <= afterProvenance {
		t.Fatalf("same-key reindex revision=%d after %d, want advance", afterSameKeyReindex, afterProvenance)
	}

	g.AddBatch(nil, []*Edge{&replacement})
	afterBatchReplacement := g.EdgeMutationRevision()
	if afterBatchReplacement <= afterSameKeyReindex {
		t.Fatalf("batch replacement revision=%d after %d, want advance", afterBatchReplacement, afterSameKeyReindex)
	}

	if !g.RemoveEdge(replacement.From, replacement.To, replacement.Kind) {
		t.Fatal("remove replacement")
	}
	if afterRemove := g.EdgeMutationRevision(); afterRemove <= afterBatchReplacement {
		t.Fatalf("remove revision=%d after %d, want advance", afterRemove, afterBatchReplacement)
	}
}
