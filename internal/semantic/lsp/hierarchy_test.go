package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestLSP_Provider_PromotesCallEdgeViaCallHierarchy(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "main.go"),
		[]byte("package main\n\nfunc Hello() string { return \"\" }\n\nfunc Caller() { _ = Hello() }\n"),
		0o644,
	))

	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/prepareCallHierarchy", func(params json.RawMessage) (any, *jsonRPCError) {
		// Return one item — the Caller function.
		return []CallHierarchyItem{{
			Name: "Caller", Kind: 12,
			URI:            pathToURI(filepath.Join(repoRoot, "main.go")),
			Range:          Range{Start: Position{Line: 4, Character: 0}, End: Position{Line: 4, Character: 30}},
			SelectionRange: Range{Start: Position{Line: 4, Character: 5}, End: Position{Line: 4, Character: 11}},
		}}, nil
	})
	server.handle("callHierarchy/outgoingCalls", func(params json.RawMessage) (any, *jsonRPCError) {
		return []CallHierarchyOutgoingCall{{
			To: CallHierarchyItem{
				Name: "Hello", Kind: 12,
				URI:            pathToURI(filepath.Join(repoRoot, "main.go")),
				Range:          Range{Start: Position{Line: 2, Character: 0}, End: Position{Line: 2, Character: 30}},
				SelectionRange: Range{Start: Position{Line: 2, Character: 5}, End: Position{Line: 2, Character: 10}},
			},
		}}, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"go"})
	defer cleanup()

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::Hello", Kind: graph.KindFunction, Name: "Hello",
		FilePath: "main.go", StartLine: 3, EndLine: 3, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::Caller", Kind: graph.KindFunction, Name: "Caller",
		FilePath: "main.go", StartLine: 5, EndLine: 5, Language: "go",
	})
	g.AddEdge(&graph.Edge{
		From: "main.go::Caller", To: "main.go::Hello", Kind: graph.EdgeCalls,
		Confidence: 0.7, ConfidenceLabel: "INFERRED", Origin: graph.OriginTextMatched,
	})

	done := make(chan error, 1)
	go func() {
		_, err := p.Enrich(g, repoRoot)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	var found *graph.Edge
	for _, e := range g.GetOutEdges("main.go::Caller") {
		if e.Kind == graph.EdgeCalls && e.To == "main.go::Hello" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "expected EdgeCalls Caller→Hello")
	assert.Equal(t, graph.OriginLSPResolved, found.Origin,
		"call hierarchy should promote call origin to lsp_resolved")
}

func TestLSP_Provider_AddsImplementsViaTypeHierarchy(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "shape.ts"),
		[]byte("interface Shape { area(): number }\nclass Circle implements Shape { area() { return 0 } }\n"),
		0o644,
	))

	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/implementation", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/prepareTypeHierarchy", func(params json.RawMessage) (any, *jsonRPCError) {
		return []TypeHierarchyItem{{
			Name:           "Shape",
			URI:            pathToURI(filepath.Join(repoRoot, "shape.ts")),
			SelectionRange: Range{Start: Position{Line: 0, Character: 10}, End: Position{Line: 0, Character: 15}},
		}}, nil
	})
	server.handle("typeHierarchy/subtypes", func(params json.RawMessage) (any, *jsonRPCError) {
		return []TypeHierarchyItem{{
			Name:           "Circle",
			URI:            pathToURI(filepath.Join(repoRoot, "shape.ts")),
			SelectionRange: Range{Start: Position{Line: 1, Character: 6}, End: Position{Line: 1, Character: 12}},
		}}, nil
	})
	server.handle("typeHierarchy/supertypes", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"typescript"})
	defer cleanup()

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "shape.ts::Shape", Kind: graph.KindInterface, Name: "Shape",
		FilePath: "shape.ts", StartLine: 1, EndLine: 1, Language: "typescript",
	})
	g.AddNode(&graph.Node{
		ID: "shape.ts::Circle", Kind: graph.KindType, Name: "Circle",
		FilePath: "shape.ts", StartLine: 2, EndLine: 2, Language: "typescript",
	})

	done := make(chan error, 1)
	go func() {
		_, err := p.Enrich(g, repoRoot)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	var impl *graph.Edge
	for _, e := range g.GetOutEdges("shape.ts::Circle") {
		if e.Kind == graph.EdgeImplements && e.To == "shape.ts::Shape" {
			impl = e
			break
		}
	}
	require.NotNil(t, impl, "expected EdgeImplements Circle→Shape from typeHierarchy/subtypes")
}

func TestLSP_Provider_AddsExtendsViaTypeHierarchySupertypes(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "h.ts"),
		[]byte("class Animal {}\nclass Dog extends Animal {}\n"),
		0o644,
	))

	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/implementation", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/prepareTypeHierarchy", func(params json.RawMessage) (any, *jsonRPCError) {
		return []TypeHierarchyItem{{
			Name:           "Dog",
			URI:            pathToURI(filepath.Join(repoRoot, "h.ts")),
			SelectionRange: Range{Start: Position{Line: 1, Character: 6}, End: Position{Line: 1, Character: 9}},
		}}, nil
	})
	server.handle("typeHierarchy/supertypes", func(params json.RawMessage) (any, *jsonRPCError) {
		return []TypeHierarchyItem{{
			Name:           "Animal",
			URI:            pathToURI(filepath.Join(repoRoot, "h.ts")),
			SelectionRange: Range{Start: Position{Line: 0, Character: 6}, End: Position{Line: 0, Character: 12}},
		}}, nil
	})
	server.handle("typeHierarchy/subtypes", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"typescript"})
	defer cleanup()

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "h.ts::Animal", Kind: graph.KindType, Name: "Animal",
		FilePath: "h.ts", StartLine: 1, EndLine: 1, Language: "typescript",
	})
	g.AddNode(&graph.Node{
		ID: "h.ts::Dog", Kind: graph.KindType, Name: "Dog",
		FilePath: "h.ts", StartLine: 2, EndLine: 2, Language: "typescript",
	})

	done := make(chan error, 1)
	go func() {
		_, err := p.Enrich(g, repoRoot)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	var ext *graph.Edge
	for _, e := range g.GetOutEdges("h.ts::Dog") {
		if e.Kind == graph.EdgeExtends && e.To == "h.ts::Animal" {
			ext = e
			break
		}
	}
	require.NotNil(t, ext, "expected EdgeExtends Dog→Animal from typeHierarchy/supertypes")
}

func TestLSP_Provider_AddsMissingCallEdgeViaCallHierarchy(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "main.go"),
		[]byte("package main\n\nfunc Hello() string { return \"\" }\n\nfunc Caller() { _ = Hello() }\n"),
		0o644,
	))

	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		return nil, nil
	})
	server.handle("textDocument/prepareCallHierarchy", func(params json.RawMessage) (any, *jsonRPCError) {
		return []CallHierarchyItem{{
			Name:           "Caller",
			URI:            pathToURI(filepath.Join(repoRoot, "main.go")),
			SelectionRange: Range{Start: Position{Line: 4, Character: 5}, End: Position{Line: 4, Character: 11}},
		}}, nil
	})
	server.handle("callHierarchy/outgoingCalls", func(params json.RawMessage) (any, *jsonRPCError) {
		return []CallHierarchyOutgoingCall{{
			To: CallHierarchyItem{
				Name:           "Hello",
				URI:            pathToURI(filepath.Join(repoRoot, "main.go")),
				SelectionRange: Range{Start: Position{Line: 2, Character: 5}, End: Position{Line: 2, Character: 10}},
			},
		}}, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"go"})
	defer cleanup()

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::Hello", Kind: graph.KindFunction, Name: "Hello",
		FilePath: "main.go", StartLine: 3, EndLine: 3, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::Caller", Kind: graph.KindFunction, Name: "Caller",
		FilePath: "main.go", StartLine: 5, EndLine: 5, Language: "go",
	})
	// No pre-existing call edge — provider should ADD one.

	done := make(chan error, 1)
	go func() {
		_, err := p.Enrich(g, repoRoot)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	var added *graph.Edge
	for _, e := range g.GetOutEdges("main.go::Caller") {
		if e.Kind == graph.EdgeCalls && e.To == "main.go::Hello" {
			added = e
			break
		}
	}
	require.NotNil(t, added, "expected newly-added EdgeCalls Caller→Hello from LSP call hierarchy")
}
