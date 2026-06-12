package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestStataExtractor_Basics(t *testing.T) {
	src := []byte(`use "data/sample.dta"
include "helpers.do"

local n 10
global PATH "/tmp"

program define mysum
    display "summing"
end
`)
	e := NewStataExtractor()
	require.Equal(t, "stata", e.Language())

	res, err := e.Extract("analysis.do", src)
	require.NoError(t, err)

	var gotProg, gotLocal, gotGlobal bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "mysum":
			gotProg = n.Kind == graph.KindFunction
		case "n":
			gotLocal = n.Kind == graph.KindVariable
		case "PATH":
			gotGlobal = n.Kind == graph.KindVariable
		}
	}
	var gotUse, gotInclude bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::data/sample.dta" {
			gotUse = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::helpers.do" {
			gotInclude = true
		}
	}
	assert.True(t, gotProg)
	assert.True(t, gotLocal)
	assert.True(t, gotGlobal)
	assert.True(t, gotUse)
	assert.True(t, gotInclude)
}

func TestStataExtractor_EmptyInput(t *testing.T) {
	res, err := NewStataExtractor().Extract("e.do", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
