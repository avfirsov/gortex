package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestMojoExtractor_Basics(t *testing.T) {
	src := []byte(`from math import sqrt
import tensor

struct Point:
    var x: Float64
    var y: Float64

trait Shape:
    fn area(self) -> Float64: ...

fn distance(a: Point, b: Point) -> Float64:
    let dx = a.x - b.x
    let dy = a.y - b.y
    return sqrt(dx * dx + dy * dy)

def greet(name: String):
    print("hello", name)
`)
	e := NewMojoExtractor()
	require.Equal(t, "mojo", e.Language())

	res, err := e.Extract("geo.mojo", src)
	require.NoError(t, err)

	var gotPoint, gotShape, gotDistance, gotGreet bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Point":
			gotPoint = true
		case "Shape":
			gotShape = true
		case "distance":
			gotDistance = true
		case "greet":
			gotGreet = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::math" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotDistance)
	assert.True(t, gotGreet)
	assert.True(t, gotImport)
}

func TestMojoExtractor_EmptyInput(t *testing.T) {
	res, err := NewMojoExtractor().Extract("e.mojo", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
