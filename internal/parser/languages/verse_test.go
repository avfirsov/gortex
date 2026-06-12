package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestVerseExtractor_FunctionsAndClasses(t *testing.T) {
	src := []byte(`using { /Fortnite.com/Devices }
using { /Verse.org/Simulation }

Greeting : string = "hello"

my_device := class(creative_device):
    OnBegin<override>()<suspends>:void =
        Print("Hello, Verse!")

    Helper(x:int)<transacts>:int =
        return x * 2

player_state := struct:
    Score : int = 0
`)
	e := NewVerseExtractor()
	require.Equal(t, "verse", e.Language())
	require.Equal(t, []string{".verse"}, e.Extensions())

	res, err := e.Extract("example.verse", src)
	require.NoError(t, err)

	var file *graph.Node
	funcs := 0
	types := 0
	vars := 0
	imports := 0
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindFile:
			file = n
		case graph.KindFunction:
			funcs++
		case graph.KindType:
			types++
		case graph.KindVariable:
			vars++
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}

	require.NotNil(t, file)
	assert.Equal(t, "verse", file.Language)
	assert.GreaterOrEqual(t, funcs, 2, "expected at least 2 functions (OnBegin, Helper)")
	assert.GreaterOrEqual(t, types, 2, "expected at least 2 types (my_device, player_state)")
	assert.GreaterOrEqual(t, vars, 1, "expected at least 1 top-level variable (Greeting)")
	assert.Equal(t, 2, imports, "expected two `using` imports")
}

func TestVerseExtractor_CallEdgesInsideFunctions(t *testing.T) {
	src := []byte(`util := class:
    DoWork()<transacts>:void =
        Print("start")
        Helper()
        Print("done")

    Helper()<transacts>:void =
        Log("helping")
`)
	res, err := NewVerseExtractor().Extract("u.verse", src)
	require.NoError(t, err)

	calls := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls {
			calls++
		}
	}
	assert.GreaterOrEqual(t, calls, 3, "expected call edges for Print/Helper/Log")
}

func TestVerseExtractor_EmptyInput(t *testing.T) {
	res, err := NewVerseExtractor().Extract("empty.verse", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
