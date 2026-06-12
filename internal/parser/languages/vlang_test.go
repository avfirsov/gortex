package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestVlangExtractor_Basics(t *testing.T) {
	src := []byte(`module geo

import math
import json as j

pub struct Point {
    x f64
    y f64
}

pub interface Shape {
    area() f64
}

enum Color {
    red
    green
    blue
}

pub fn distance(a Point, b Point) f64 {
    dx := a.x - b.x
    dy := a.y - b.y
    return math.sqrt(dx * dx + dy * dy)
}
`)
	e := NewVlangExtractor()
	require.Equal(t, "v", e.Language())

	res, err := e.Extract("geo.v", src)
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
		case "distance":
			gotDistance = true
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
	assert.True(t, gotColor)
	assert.True(t, gotDistance)
	assert.True(t, gotImport)
}

func TestVlangExtractor_EmptyInput(t *testing.T) {
	res, err := NewVlangExtractor().Extract("e.v", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
