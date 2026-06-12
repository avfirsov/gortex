package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestReScriptExtractor_Basics(t *testing.T) {
	src := []byte(`open Belt
include Js.Promise

type point = {
  x: float,
  y: float,
}

module Geo = {
  let origin = {x: 0.0, y: 0.0}
}

let pi = 3.14159

let distance = (a: point, b: point): float => {
  let dx = a.x -. b.x
  let dy = a.y -. b.y
  Js.Math.sqrt(dx *. dx +. dy *. dy)
}
`)
	e := NewReScriptExtractor()
	require.Equal(t, "rescript", e.Language())

	res, err := e.Extract("geo.res", src)
	require.NoError(t, err)

	var gotPoint, gotGeo, gotDistance bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "point":
			gotPoint = true
		case "Geo":
			gotGeo = true
		case "distance":
			gotDistance = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Belt" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotGeo)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestReScriptExtractor_EmptyInput(t *testing.T) {
	res, err := NewReScriptExtractor().Extract("e.res", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
