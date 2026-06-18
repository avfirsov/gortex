package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

func nameResolveServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t) // pkg/foo.go::Bar, pkg/foo.go::Baz
	// Make "Bar" ambiguous across two files.
	s.graph.AddNode(&graph.Node{ID: "pkg/other.go::Bar", Name: "Bar", Kind: graph.KindFunction, FilePath: "pkg/other.go"})
	// A non-definition node named "Bar" must be ignored.
	s.graph.AddNode(&graph.Node{ID: "pkg/foo.go::Bar#local", Name: "Bar", Kind: graph.KindLocal, FilePath: "pkg/foo.go"})
	s.engine = query.NewEngine(s.graph)
	return s
}

func TestResolveNameToIDs(t *testing.T) {
	s := nameResolveServer(t)
	got := s.resolveNameToIDs("Bar")
	// Two definitions, the KindLocal excluded, sorted.
	require.Equal(t, []string{"pkg/foo.go::Bar", "pkg/other.go::Bar"}, got)

	// Unique name → single id.
	require.Equal(t, []string{"pkg/foo.go::Baz"}, s.resolveNameToIDs("Baz"))
	// No match → nil.
	require.Nil(t, s.resolveNameToIDs("Nope"))
}

func TestSymbolTargetArgExactIDWins(t *testing.T) {
	s := nameResolveServer(t)
	// A full id that exists resolves to itself — never reinterpreted as a name,
	// even though "Bar" is ambiguous.
	id, cands := s.resolveSymbolTarget(context.Background(), "pkg/foo.go::Bar")
	assert.Equal(t, "pkg/foo.go::Bar", id)
	assert.Nil(t, cands)
}

func TestGetCallersUniqueBareName(t *testing.T) {
	s := nameResolveServer(t)
	// "Baz" is unique → resolves, get_callers proceeds (no disambiguation).
	res := callHandler(t, s.handleGetCallers, map[string]any{"id": "Baz"})
	m := unmarshalResult(t, res)
	if _, ambiguous := m["ambiguous"]; ambiguous {
		t.Errorf("unique bare name should resolve, not disambiguate: %+v", m)
	}
}

func TestGetCallersBareNameDisambiguation(t *testing.T) {
	s := nameResolveServer(t)
	// "Bar" is ambiguous → disambiguation result with both candidates.
	res := callHandler(t, s.handleGetCallers, map[string]any{"id": "Bar"})
	m := unmarshalResult(t, res)
	require.Equal(t, true, m["ambiguous"])
	require.Equal(t, "Bar", m["name"])
	cands, ok := m["candidates"].([]any)
	require.True(t, ok, "candidates should be a list, got %T", m["candidates"])
	require.Len(t, cands, 2)
}
