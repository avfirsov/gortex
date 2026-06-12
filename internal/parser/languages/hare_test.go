package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestHareExtractor_Basics(t *testing.T) {
	src := []byte(`use fmt;
use math::trig;

type Point = struct {
    x: f64,
    y: f64,
};

type Shape = enum {
    CIRCLE,
    SQUARE,
};

export fn distance(a: Point, b: Point) f64 = {
    const dx = a.x - b.x;
    const dy = a.y - b.y;
    return math::sqrt(dx * dx + dy * dy);
};
`)
	e := NewHareExtractor()
	require.Equal(t, "hare", e.Language())

	res, err := e.Extract("geo.ha", src)
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
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::fmt" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotShape)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestHareExtractor_EmptyInput(t *testing.T) {
	res, err := NewHareExtractor().Extract("e.ha", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
