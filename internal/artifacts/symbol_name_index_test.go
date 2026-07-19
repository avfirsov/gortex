package artifacts

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

type allNodesCountingStore struct {
	graph.Store
	allNodesCalls int
}

func (s *allNodesCountingStore) AllNodes() []*graph.Node {
	s.allNodesCalls++
	return s.Store.AllNodes()
}

func TestSymbolNameIndexStreamsExactKindsWithoutAllNodes(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "repoA/a.go::SharedName", Kind: graph.KindFunction, Name: "SharedName", RepoPrefix: "repoA"},
		{ID: "repoB/z.go::SharedName", Kind: graph.KindMethod, Name: "SharedName", RepoPrefix: "repoB"},
		{ID: "repoA/t.go::UsefulType", Kind: graph.KindType, Name: "UsefulType", RepoPrefix: "repoA"},
		{ID: "repoB/i.go::UsefulIface", Kind: graph.KindInterface, Name: "UsefulIface", RepoPrefix: "repoB"},
		{ID: "repoA/f.go::SharedName", Kind: graph.KindField, Name: "SharedName", RepoPrefix: "repoA"},
		{ID: "repoA/a.go::abc", Kind: graph.KindFunction, Name: "abc", RepoPrefix: "repoA"},
	}, nil)

	wantGlobal := SymbolNameIndex(g, "")
	counting := &allNodesCountingStore{Store: g}
	gotGlobal := SymbolNameIndex(counting, "")
	require.Equal(t, wantGlobal, gotGlobal, "constant-query fallback must match the native projection")
	require.Zero(t, counting.allNodesCalls)
	require.Equal(t, []string{
		"repoA/a.go::SharedName",
		"repoB/z.go::SharedName",
	}, gotGlobal["SharedName"], "same-name declarations are deterministically ID-sorted")
	require.NotContains(t, gotGlobal, "abc", "short names retain the scanner floor")

	gotRepoA := SymbolNameIndex(counting, "repoA")
	require.Zero(t, counting.allNodesCalls)
	require.Equal(t, []string{"repoA/a.go::SharedName"}, gotRepoA["SharedName"])
	require.Equal(t, []string{"repoA/t.go::UsefulType"}, gotRepoA["UsefulType"])
	require.NotContains(t, gotRepoA, "UsefulIface")
}
