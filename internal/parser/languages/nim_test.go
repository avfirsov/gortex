package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestNimExtractor_ProcsAndTypes(t *testing.T) {
	src := []byte(`import strutils
import sequtils, tables

type
  Point* = object
    x*, y*: int

  Shape = enum
    Circle, Square

proc distance*(a, b: Point): float =
  let dx = float(a.x - b.x)
  let dy = float(a.y - b.y)
  return sqrt(dx * dx + dy * dy)

func double(n: int): int =
  n * 2
`)
	e := NewNimExtractor()
	require.Equal(t, "nim", e.Language())

	res, err := e.Extract("geo.nim", src)
	require.NoError(t, err)

	var gotPoint, gotShape, gotDistance, gotDouble bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Point":
			gotPoint = true
		case "Shape":
			gotShape = true
		case "distance":
			gotDistance = true
		case "double":
			gotDouble = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::strutils" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotDistance)
	assert.True(t, gotDouble)
	assert.True(t, gotImport)
}

func TestNimExtractor_EmptyInput(t *testing.T) {
	res, err := NewNimExtractor().Extract("e.nim", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
