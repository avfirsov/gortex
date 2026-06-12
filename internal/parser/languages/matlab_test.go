package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestMatlabExtractor_Basics(t *testing.T) {
	src := []byte(`import matlab.io.*

function [s, p] = stats(x)
    s = sum(x);
    p = prod(x);
end

function y = square(x)
    y = x .^ 2;
end

classdef Point
    properties
        x
        y
    end
end
`)
	e := NewMatlabExtractor()
	require.Equal(t, "matlab", e.Language())

	res, err := e.Extract("stats.mlx", src)
	require.NoError(t, err)

	var gotStats, gotSquare, gotPoint bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "stats":
			gotStats = n.Kind == graph.KindFunction
		case "square":
			gotSquare = n.Kind == graph.KindFunction
		case "Point":
			gotPoint = n.Kind == graph.KindType
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::matlab.io" {
			gotImport = true
		}
	}
	assert.True(t, gotStats)
	assert.True(t, gotSquare)
	assert.True(t, gotPoint)
	assert.True(t, gotImport)
}

func TestMatlabExtractor_EmptyInput(t *testing.T) {
	res, err := NewMatlabExtractor().Extract("e.mlx", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
