package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestMathematicaExtractor_Basics(t *testing.T) {
	src := []byte("Needs[\"Utilities`\"]\n" +
		"<< MyPkg`\n" +
		"square[x_] := x^2\n" +
		"cube[x_] = x^3\n" +
		"SetDelayed[quad, Function[{x}, x^4]]\n")
	e := NewMathematicaExtractor()
	require.Equal(t, "mathematica", e.Language())

	res, err := e.Extract("pkg.wl", src)
	require.NoError(t, err)

	var gotSquare, gotCube, gotQuad bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "square":
			gotSquare = n.Kind == graph.KindFunction
		case "cube":
			gotCube = n.Kind == graph.KindFunction
		case "quad":
			gotQuad = n.Kind == graph.KindFunction
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Utilities`" {
			gotImport = true
		}
	}
	assert.True(t, gotSquare)
	assert.True(t, gotCube)
	assert.True(t, gotQuad)
	assert.True(t, gotImport)
}

func TestMathematicaExtractor_EmptyInput(t *testing.T) {
	res, err := NewMathematicaExtractor().Extract("e.wl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
