package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestPowerShellExtractor_FunctionsAndClass(t *testing.T) {
	src := []byte(`Import-Module PSReadLine
. .\helpers.ps1

function Get-Greeting {
    param([string]$Name)
    Write-Host "hello $Name"
}

filter Get-Even {
    if ($_ % 2 -eq 0) { $_ }
}

class Counter {
    [int]$Value = 0
    [void]Increment() { $this.Value++ }
}
`)
	e := NewPowerShellExtractor()
	require.Equal(t, "powershell", e.Language())

	res, err := e.Extract("m.ps1", src)
	require.NoError(t, err)

	var gotGreet, gotEven, gotClass bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Get-Greeting":
			gotGreet = true
		case "Get-Even":
			gotEven = true
		case "Counter":
			gotClass = true
		}
	}
	var gotImport, gotSource bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::PSReadLine" {
			gotImport = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::.\\helpers.ps1" {
			gotSource = true
		}
	}
	assert.True(t, gotGreet)
	assert.True(t, gotEven)
	assert.True(t, gotClass)
	assert.True(t, gotImport)
	assert.True(t, gotSource)
}

func TestPowerShellExtractor_EmptyInput(t *testing.T) {
	res, err := NewPowerShellExtractor().Extract("e.ps1", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
