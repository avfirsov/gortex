package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestOdinExtractor_Basics(t *testing.T) {
	src := []byte(`package geo

import "core:fmt"
import math "core:math"

Point :: struct {
    x: f32,
    y: f32,
}

Shape :: enum {
    Circle,
    Square,
}

distance :: proc(a, b: Point) -> f32 {
    dx := a.x - b.x
    dy := a.y - b.y
    return math.sqrt(dx * dx + dy * dy)
}
`)
	e := NewOdinExtractor()
	require.Equal(t, "odin", e.Language())

	res, err := e.Extract("geo.odin", src)
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
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::core:fmt" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestOdinExtractor_EmptyInput(t *testing.T) {
	res, err := NewOdinExtractor().Extract("e.odin", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
