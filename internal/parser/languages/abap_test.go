package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestABAPExtractor_Basics(t *testing.T) {
	src := []byte(`REPORT zdemo_report.

INCLUDE zdemo_top.

CLASS zcl_demo DEFINITION.
  PUBLIC SECTION.
    METHODS: greet.
ENDCLASS.

CLASS zcl_demo IMPLEMENTATION.
  METHOD greet.
    WRITE 'hi'.
  ENDMETHOD.
ENDCLASS.

FORM calculate.
  WRITE 'calc'.
ENDFORM.
`)
	e := NewABAPExtractor()
	require.Equal(t, "abap", e.Language())

	res, err := e.Extract("zdemo.abap", src)
	require.NoError(t, err)

	var gotClass, gotGreet, gotForm, gotReport bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "zcl_demo":
			gotClass = n.Kind == graph.KindType
		case "greet":
			gotGreet = n.Kind == graph.KindMethod
		case "calculate":
			gotForm = n.Kind == graph.KindFunction
		case "zdemo_report":
			gotReport = n.Kind == graph.KindVariable
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::zdemo_top" {
			gotImport = true
		}
	}
	assert.True(t, gotClass)
	assert.True(t, gotGreet)
	assert.True(t, gotForm)
	assert.True(t, gotReport)
	assert.True(t, gotImport)
}

func TestABAPExtractor_EmptyInput(t *testing.T) {
	res, err := NewABAPExtractor().Extract("e.abap", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
