package search

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type lightOnlyReader struct {
	graph.Reader
	lightCalls int
}

func (r *lightOnlyReader) AllNodes() []*graph.Node {
	panic("auto concepts used the full node scan")
}

func (r *lightOnlyReader) AllNodesLight() []*graph.Node {
	r.lightCalls++
	return r.Reader.AllNodes()
}

func TestBuildAutoConceptsUsesLightNodeProjection(t *testing.T) {
	base := graph.New()
	base.AddNode(&graph.Node{ID: "repo/a.go::ParseRequest", Kind: graph.KindFunction, Name: "ParseRequest"})
	base.AddNode(&graph.Node{ID: "repo/b.go::ParseResponse", Kind: graph.KindFunction, Name: "ParseResponse"})

	reader := &lightOnlyReader{Reader: base}
	concepts := BuildAutoConcepts(reader)
	if !concepts.InVocabulary("parse") {
		t.Fatal("light projection lost symbol names used by auto concepts")
	}
	if reader.lightCalls != 1 {
		t.Fatalf("light node scan calls = %d, want 1", reader.lightCalls)
	}
}
