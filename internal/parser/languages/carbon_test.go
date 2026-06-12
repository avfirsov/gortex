package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCarbonExtractor_Basics(t *testing.T) {
	src := []byte(`package Geo api;

import Math;
import Core library "core";

class Point {
    var x: f64;
    var y: f64;
}

interface Shape {
    fn Area[self: Self]() -> f64;
}

choice Color {
    Red,
    Green,
    Blue,
}

fn Distance(a: Point, b: Point) -> f64 {
    var dx: f64 = a.x - b.x;
    var dy: f64 = a.y - b.y;
    return Math.Sqrt(dx * dx + dy * dy);
}
`)
	e := NewCarbonExtractor()
	require.Equal(t, "carbon", e.Language())

	res, err := e.Extract("geo.carbon", src)
	require.NoError(t, err)

	var gotPoint, gotShape, gotColor, gotDistance bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Point":
			gotPoint = true
		case "Shape":
			gotShape = true
		case "Color":
			gotColor = true
		case "Distance":
			gotDistance = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Math" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotColor)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestCarbonExtractor_EmptyInput(t *testing.T) {
	res, err := NewCarbonExtractor().Extract("e.carbon", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
