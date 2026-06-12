package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestTclExtractor_Procs(t *testing.T) {
	src := []byte(`package require Tcl 8.6
source utils.tcl

namespace eval ::math {
    proc add {a b} {
        return [expr {$a + $b}]
    }
    proc ::math::mul {a b} {
        return [expr {$a * $b}]
    }
}
`)
	e := NewTclExtractor()
	require.Equal(t, "tcl", e.Language())

	res, err := e.Extract("m.tcl", src)
	require.NoError(t, err)

	var gotNS, gotAdd, gotMul bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "::math":
			gotNS = true
		case "add":
			gotAdd = true
		case "::math::mul":
			gotMul = true
		}
	}
	var gotPkg, gotSource bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Tcl" {
			gotPkg = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::utils.tcl" {
			gotSource = true
		}
	}
	assert.True(t, gotNS)
	assert.True(t, gotAdd)
	assert.True(t, gotMul)
	assert.True(t, gotPkg)
	assert.True(t, gotSource)
}

func TestTclExtractor_EmptyInput(t *testing.T) {
	res, err := NewTclExtractor().Extract("e.tcl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
