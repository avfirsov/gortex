package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestPugExtractor_Basics(t *testing.T) {
	src := []byte(`extends layout
include mixins/forms

mixin card(title, body)
  .card
    h2= title
    p= body

block content
  h1 Home
  +card('Hello', 'World')
`)
	e := NewPugExtractor()
	require.Equal(t, "pug", e.Language())

	res, err := e.Extract("page.pug", src)
	require.NoError(t, err)

	var gotCard, gotBlock bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "card":
			gotCard = true
		case "content":
			gotBlock = true
		}
	}
	assert.True(t, gotCard)
	assert.True(t, gotBlock)

	var gotExtends, gotInclude bool
	for _, ed := range res.Edges {
		if ed.Kind != graph.EdgeImports {
			continue
		}
		switch ed.To {
		case "unresolved::import::layout":
			gotExtends = true
		case "unresolved::import::mixins/forms":
			gotInclude = true
		}
	}
	assert.True(t, gotExtends)
	assert.True(t, gotInclude)
}

func TestPugExtractor_EmptyInput(t *testing.T) {
	res, err := NewPugExtractor().Extract("e.pug", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
