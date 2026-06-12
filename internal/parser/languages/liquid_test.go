package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestLiquidExtractor_Basics(t *testing.T) {
	src := []byte(`{% assign greeting = 'hello' %}
{% include 'header' %}
{% render 'product-card' %}

{% capture banner %}
  <h1>{{ greeting }}</h1>
{% endcapture %}

{{ banner }}
`)
	e := NewLiquidExtractor()
	require.Equal(t, "liquid", e.Language())

	res, err := e.Extract("page.liquid", src)
	require.NoError(t, err)

	var gotGreeting, gotBanner bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "greeting":
			gotGreeting = true
			assert.Equal(t, graph.KindVariable, n.Kind)
		case "banner":
			gotBanner = true
			assert.Equal(t, graph.KindFunction, n.Kind)
		}
	}
	assert.True(t, gotGreeting)
	assert.True(t, gotBanner)

	var gotInclude, gotRender bool
	for _, ed := range res.Edges {
		if ed.Kind != graph.EdgeImports {
			continue
		}
		switch ed.To {
		case "unresolved::import::header":
			gotInclude = true
		case "unresolved::import::product-card":
			gotRender = true
		}
	}
	assert.True(t, gotInclude)
	assert.True(t, gotRender)
}

func TestLiquidExtractor_EmptyInput(t *testing.T) {
	res, err := NewLiquidExtractor().Extract("e.liquid", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
