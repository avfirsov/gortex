package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestJuliaExtractor_Module(t *testing.T) {
	src := []byte(`module Geometry

using LinearAlgebra
import Statistics

struct Circle
    radius::Float64
end

function area(c::Circle)
    pi * c.radius^2
end

diameter(c::Circle) = 2 * c.radius

end # module
`)
	e := NewJuliaExtractor()
	require.Equal(t, "julia", e.Language())

	res, err := e.Extract("geom.jl", src)
	require.NoError(t, err)

	var gotModule, gotStruct, gotArea, gotDiameter bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Geometry":
			gotModule = true
		case "Circle":
			gotStruct = true
		case "area":
			gotArea = true
		case "diameter":
			gotDiameter = true
		}
	}
	var gotUsing, gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::LinearAlgebra" {
			gotUsing = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Statistics" {
			gotImport = true
		}
	}
	assert.True(t, gotModule)
	assert.True(t, gotStruct)
	assert.True(t, gotArea)
	assert.True(t, gotDiameter)
	assert.True(t, gotUsing)
	assert.True(t, gotImport)
}

func TestJuliaExtractor_Include(t *testing.T) {
	src := []byte(`include("helpers.jl")
`)
	res, err := NewJuliaExtractor().Extract("m.jl", src)
	require.NoError(t, err)

	var got bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::helpers.jl" {
			got = true
		}
	}
	assert.True(t, got)
}

func TestJuliaExtractor_EmptyInput(t *testing.T) {
	res, err := NewJuliaExtractor().Extract("e.jl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
