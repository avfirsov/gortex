package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestGleamExtractor_Basics(t *testing.T) {
	src := []byte(`import gleam/io
import gleam/list.{map, filter}
import gleam/string as s

pub type Point {
  Point(x: Float, y: Float)
}

pub type Shape {
  Circle(radius: Float)
  Square(side: Float)
}

pub fn distance(a: Point, b: Point) -> Float {
  let dx = a.x -. b.x
  let dy = a.y -. b.y
  sqrt(dx *. dx +. dy *. dy)
}

fn sqrt(x: Float) -> Float {
  x
}
`)
	e := NewGleamExtractor()
	require.Equal(t, "gleam", e.Language())

	res, err := e.Extract("geo.gleam", src)
	require.NoError(t, err)

	var gotPoint, gotShape, gotDistance bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Point":
			gotPoint = true
		case "Shape":
			gotShape = true
		case "distance":
			gotDistance = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::gleam/io" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestGleamExtractor_EmptyInput(t *testing.T) {
	res, err := NewGleamExtractor().Extract("e.gleam", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
