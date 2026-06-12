package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestBatchExtractor_Basics(t *testing.T) {
	src := []byte(`@echo off
call helpers.bat
goto start

:start
echo starting
call :setup
goto done

:setup
echo setting up
goto :eof

:done
echo done
`)
	e := NewBatchExtractor()
	require.Equal(t, "batch", e.Language())

	res, err := e.Extract("run.bat", src)
	require.NoError(t, err)

	var gotStart, gotSetup, gotDone bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "start":
			gotStart = true
		case "setup":
			gotSetup = true
		case "done":
			gotDone = true
		}
	}
	var gotImport, gotCallEdge bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::helpers.bat" {
			gotImport = true
		}
		if ed.Kind == graph.EdgeCalls && ed.To == "unresolved::setup" {
			gotCallEdge = true
		}
	}
	assert.True(t, gotStart)
	assert.True(t, gotSetup)
	assert.True(t, gotDone)
	assert.True(t, gotImport)
	assert.True(t, gotCallEdge)
}

func TestBatchExtractor_EmptyInput(t *testing.T) {
	res, err := NewBatchExtractor().Extract("e.bat", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
