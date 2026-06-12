package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestDExtractor_Basics(t *testing.T) {
	src := []byte(`module app.main;

import std.stdio;
import std.conv : to;

struct Point {
    int x;
    int y;
}

class Greeter {
    string name;

    this(string n) {
        name = n;
    }
}

int add(int a, int b) {
    return a + b;
}

void main() {
    writeln(add(1, 2));
}
`)
	e := NewDExtractor()
	require.Equal(t, "d", e.Language())

	res, err := e.Extract("main.d", src)
	require.NoError(t, err)

	var gotPoint, gotGreeter, gotAdd bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Point":
			gotPoint = true
		case "Greeter":
			gotGreeter = true
		case "add":
			gotAdd = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::std.stdio" {
			gotImport = true
		}
	}
	assert.True(t, gotPoint)
	assert.True(t, gotGreeter)
	assert.True(t, gotAdd)
	assert.True(t, gotImport)
}

func TestDExtractor_EmptyInput(t *testing.T) {
	res, err := NewDExtractor().Extract("e.d", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
