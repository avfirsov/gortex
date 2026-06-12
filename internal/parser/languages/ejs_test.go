package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestEJSExtractor_Basics(t *testing.T) {
	src := []byte(`<%- include('partials/header') %>
<% include legacy %>

<%
function greet(name) {
  return "hello " + name;
}

const shout = (s) => s.toUpperCase();
%>

<p><%= greet('world') %></p>
`)
	e := NewEJSExtractor()
	require.Equal(t, "ejs", e.Language())

	res, err := e.Extract("page.ejs", src)
	require.NoError(t, err)

	var gotGreet, gotShout bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "greet":
			gotGreet = true
		case "shout":
			gotShout = true
		}
	}
	assert.True(t, gotGreet)
	assert.True(t, gotShout)

	var gotModern, gotLegacy bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			switch ed.To {
			case "unresolved::import::partials/header":
				gotModern = true
			case "unresolved::import::legacy":
				gotLegacy = true
			}
		}
	}
	assert.True(t, gotModern)
	assert.True(t, gotLegacy)
}

func TestEJSExtractor_EmptyInput(t *testing.T) {
	res, err := NewEJSExtractor().Extract("e.ejs", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
