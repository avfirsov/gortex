package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCoffeeScriptExtractor_Basics(t *testing.T) {
	src := []byte(`fs = require 'fs'
_ = require "lodash"

PI = 3.14

square = (x) ->
  x * x

bound = (x) =>
  x + 1

class Animal extends Base
  move: ->
    console.log 'moving'
`)
	e := NewCoffeeScriptExtractor()
	require.Equal(t, "coffeescript", e.Language())

	res, err := e.Extract("app.coffee", src)
	require.NoError(t, err)

	var gotSquare, gotBound, gotAnimal, gotPI bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "square":
			gotSquare = n.Kind == graph.KindFunction
		case "bound":
			gotBound = n.Kind == graph.KindFunction
		case "Animal":
			gotAnimal = n.Kind == graph.KindType
		case "PI":
			gotPI = n.Kind == graph.KindVariable
		}
	}
	var gotFs, gotLodash bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::fs" {
			gotFs = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::lodash" {
			gotLodash = true
		}
	}
	assert.True(t, gotSquare)
	assert.True(t, gotBound)
	assert.True(t, gotAnimal)
	assert.True(t, gotPI)
	assert.True(t, gotFs)
	assert.True(t, gotLodash)
}

func TestCoffeeScriptExtractor_EmptyInput(t *testing.T) {
	res, err := NewCoffeeScriptExtractor().Extract("e.coffee", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
