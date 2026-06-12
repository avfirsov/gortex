package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestTwigExtractor_Basics(t *testing.T) {
	src := []byte(`{% extends "layout.html.twig" %}
{% include "header.html.twig" %}
{% import "forms.html.twig" as forms %}

{% block body %}
  <p>Hi</p>
{% endblock %}

{% macro input(name, value) %}
  <input name="{{ name }}" value="{{ value }}" />
{% endmacro %}
`)
	e := NewTwigExtractor()
	require.Equal(t, "twig", e.Language())

	res, err := e.Extract("page.twig", src)
	require.NoError(t, err)

	var gotBlock, gotMacro bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "body":
			gotBlock = true
		case "input":
			gotMacro = true
		}
	}
	assert.True(t, gotBlock)
	assert.True(t, gotMacro)

	var gotExtends, gotInclude, gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind != graph.EdgeImports {
			continue
		}
		switch ed.To {
		case "unresolved::import::layout.html.twig":
			gotExtends = true
		case "unresolved::import::header.html.twig":
			gotInclude = true
		case "unresolved::import::forms.html.twig":
			gotImport = true
		}
	}
	assert.True(t, gotExtends)
	assert.True(t, gotInclude)
	assert.True(t, gotImport)
}

func TestTwigExtractor_EmptyInput(t *testing.T) {
	res, err := NewTwigExtractor().Extract("e.twig", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
