package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestERBExtractor_Basics(t *testing.T) {
	src := []byte(`<h1>Users</h1>

<%= render 'shared/header' %>
<%= render :partial => 'user', :collection => @users %>

<%
  class UserPresenter
    def full_name
      "#{first} #{last}"
    end
  end
%>
`)
	e := NewERBExtractor()
	require.Equal(t, "erb", e.Language())

	res, err := e.Extract("index.html.erb", src)
	require.NoError(t, err)

	var gotClass, gotDef bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "UserPresenter":
			gotClass = true
			assert.Equal(t, graph.KindType, n.Kind)
		case "full_name":
			gotDef = true
			assert.Equal(t, graph.KindFunction, n.Kind)
		}
	}
	assert.True(t, gotClass)
	assert.True(t, gotDef)

	var gotHeader, gotPartial bool
	for _, ed := range res.Edges {
		if ed.Kind != graph.EdgeImports {
			continue
		}
		switch ed.To {
		case "unresolved::import::shared/header":
			gotHeader = true
		case "unresolved::import::user":
			gotPartial = true
		}
	}
	assert.True(t, gotHeader)
	assert.True(t, gotPartial)
}

func TestERBExtractor_EmptyInput(t *testing.T) {
	res, err := NewERBExtractor().Extract("e.erb", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
