package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestFSharpExtractor_ModuleAndLets(t *testing.T) {
	src := []byte(`module Math.Shapes

open System
open System.Collections.Generic

type Circle = { Radius : float }

let area (c: Circle) =
    Math.PI * c.Radius * c.Radius

let rec fact n =
    if n <= 1 then 1
    else n * fact (n - 1)
`)
	e := NewFSharpExtractor()
	require.Equal(t, "fsharp", e.Language())

	res, err := e.Extract("shapes.fs", src)
	require.NoError(t, err)

	var gotModule, gotType, gotArea, gotFact, gotOpen bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Math.Shapes":
			gotModule = true
		case "Circle":
			gotType = true
		case "area":
			gotArea = true
		case "fact":
			gotFact = true
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::System" {
			gotOpen = true
		}
	}
	assert.True(t, gotModule)
	assert.True(t, gotType)
	assert.True(t, gotArea)
	assert.True(t, gotFact)
	assert.True(t, gotOpen)
}

func TestFSharpExtractor_Member(t *testing.T) {
	src := []byte(`type Counter() =
    let mutable count = 0
    member this.Increment() = count <- count + 1
    member this.Value = count
`)
	res, err := NewFSharpExtractor().Extract("c.fs", src)
	require.NoError(t, err)

	var gotInc, gotValue bool
	for _, n := range res.Nodes {
		if n.Name == "Increment" && n.Kind == graph.KindMethod {
			gotInc = true
		}
		if n.Name == "Value" && n.Kind == graph.KindMethod {
			gotValue = true
		}
	}
	assert.True(t, gotInc, "member this.Increment should be a method")
	assert.True(t, gotValue, "member this.Value should be a method")
}

func TestFSharpExtractor_EmptyInput(t *testing.T) {
	res, err := NewFSharpExtractor().Extract("empty.fs", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
