package graph

import (
	"fmt"
	"testing"
)

func BenchmarkGraph_AddNode(b *testing.B) {
	g := New()
	for i := range b.N {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("file%d.go::func%d", i/10, i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i),
		})
	}
}

func BenchmarkGraph_AddEdge(b *testing.B) {
	g := New()
	// Pre-populate nodes.
	for i := range 1000 {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("node%d", i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i),
		})
	}
	b.ResetTimer()
	for i := range b.N {
		g.AddEdge(&Edge{
			From: fmt.Sprintf("node%d", i%1000),
			To:   fmt.Sprintf("node%d", (i+1)%1000),
			Kind: EdgeCalls,
		})
	}
}

func BenchmarkGraph_GetNode(b *testing.B) {
	g := New()
	for i := range 1000 {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("node%d", i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i),
		})
	}
	b.ResetTimer()
	for i := range b.N {
		g.GetNode(fmt.Sprintf("node%d", i%1000))
	}
}

func BenchmarkGraph_FindNodesByName(b *testing.B) {
	g := New()
	for i := range 1000 {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("node%d", i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i%50), // 50 unique names
		})
	}
	b.ResetTimer()
	for i := range b.N {
		g.FindNodesByName(fmt.Sprintf("func%d", i%50))
	}
}

func BenchmarkGraph_AllNodes(b *testing.B) {
	g := New()
	for i := range 1000 {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("node%d", i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i),
		})
	}
	b.ResetTimer()
	for b.Loop() {
		g.AllNodes()
	}
}

func BenchmarkGraph_Stats(b *testing.B) {
	g := New()
	for i := range 500 {
		g.AddNode(&Node{
			ID:   fmt.Sprintf("node%d", i),
			Kind: KindFunction,
			Name: fmt.Sprintf("func%d", i),
		})
	}
	for i := range 500 {
		g.AddNode(&Node{
			ID:       fmt.Sprintf("type%d", i),
			Kind:     KindType,
			Name:     fmt.Sprintf("Type%d", i),
			Language: "go",
		})
	}
	b.ResetTimer()
	for b.Loop() {
		g.Stats()
	}
}
