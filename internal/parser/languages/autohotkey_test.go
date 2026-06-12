package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestAutoHotkeyExtractor_V2Script(t *testing.T) {
	src := []byte(`#Include MyLib.ahk

class Logger {
    Log(msg) {
        FileAppend(msg "` + "`n" + `", "log.txt")
    }
}

^!c::
    MsgBox("Hotkey pressed")
return

::btw::by the way

MyFunc(x, y) {
    result := Add(x, y)
    return result
}

Add(a, b) {
    return a + b
}
`)
	e := NewAutoHotkeyExtractor()
	require.Equal(t, "autohotkey", e.Language())

	res, err := e.Extract("script.ahk", src)
	require.NoError(t, err)

	types, funcs, imports := 0, 0, 0
	hotkey, hotstring := false, false
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindType:
			types++
		case graph.KindFunction:
			funcs++
			if n.Meta != nil {
				if n.Meta["ahk_kind"] == "hotkey" {
					hotkey = true
				}
				if n.Meta["ahk_kind"] == "hotstring" {
					hotstring = true
				}
			}
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}

	assert.GreaterOrEqual(t, types, 1, "Logger class")
	assert.GreaterOrEqual(t, funcs, 3, "Log + MyFunc + Add at minimum")
	assert.Equal(t, 1, imports)
	assert.True(t, hotkey, "hotkey ^!c:: should be detected")
	assert.True(t, hotstring, "hotstring ::btw:: should be detected")
}

func TestAutoHotkeyExtractor_CallEdges(t *testing.T) {
	src := []byte(`Greet(name) {
    MsgBox("hi " name)
}

Main() {
    Greet("Claude")
}
`)
	res, err := NewAutoHotkeyExtractor().Extract("s.ahk", src)
	require.NoError(t, err)

	calls := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls {
			calls++
		}
	}
	assert.GreaterOrEqual(t, calls, 2)
}

func TestAutoHotkeyExtractor_EmptyInput(t *testing.T) {
	res, err := NewAutoHotkeyExtractor().Extract("empty.ahk", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
