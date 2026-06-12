package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestHandlebarsExtractor_Basics(t *testing.T) {
	src := []byte(`{{> header}}

{{#list items}}
  {{> item-card}}
  {{format-date date}}
{{/list}}

{{#each users}}
  <li>{{name}}</li>
{{/each}}
`)
	e := NewHandlebarsExtractor()
	require.Equal(t, "handlebars", e.Language())

	res, err := e.Extract("view.hbs", src)
	require.NoError(t, err)

	var gotList bool
	for _, n := range res.Nodes {
		if n.Name == "list" {
			gotList = true
		}
	}
	assert.True(t, gotList)

	var gotPartial, gotInnerPartial, gotCallEdge bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			switch ed.To {
			case "unresolved::import::header":
				gotPartial = true
			case "unresolved::import::item-card":
				gotInnerPartial = true
			}
		}
		if ed.Kind == graph.EdgeCalls && ed.To == "unresolved::format-date" {
			gotCallEdge = true
		}
	}
	assert.True(t, gotPartial)
	assert.True(t, gotInnerPartial)
	assert.True(t, gotCallEdge)
}

func TestHandlebarsExtractor_EmptyInput(t *testing.T) {
	res, err := NewHandlebarsExtractor().Extract("e.hbs", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
